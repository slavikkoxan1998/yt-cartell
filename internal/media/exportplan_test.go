package media

// The plan accessors, the subtitles and the motion moves editor exports use
// (studio/export.go), and the detailed probe's parsing — no files needed.

import (
	"math"
	"testing"
)

func TestParseStreams(t *testing.T) {
	video := `{"streams":[
		{"codec_type":"video","codec_name":"h264","width":1920,"height":1080,"r_frame_rate":"30000/1001","avg_frame_rate":"30000/1001","nb_frames":"240","duration":"8.008"},
		{"codec_type":"audio","codec_name":"aac","sample_rate":"48000","channels":2}],
		"format":{"duration":"8.008","format_name":"mov,mp4,m4a,3gp,3g2,mj2"}}`
	info, err := parseStreams([]byte(video))
	if err != nil {
		t.Fatal(err)
	}
	if info.FPSNum != 30000 || info.FPSDen != 1001 || info.Frames != 240 || info.SampleRate != 48000 || info.Channels != 2 ||
		!info.HasAudio || info.Image || info.Width != 1920 || math.Abs(info.Duration-8.008) > 1e-9 {
		t.Fatalf("video %+v", info)
	}
	// A timebase in r_frame_rate (some MP4s): the average rate wins.
	odd := `{"streams":[{"codec_type":"video","width":640,"height":360,"r_frame_rate":"90000/1","avg_frame_rate":"24/1"}],"format":{"duration":"2"}}`
	if info, _ := parseStreams([]byte(odd)); info.FPS() != 24 {
		t.Fatalf("odd rate %+v", info)
	}
	still := `{"streams":[{"codec_type":"video","codec_name":"png","width":1280,"height":720,"r_frame_rate":"25/1"}],"format":{"duration":"0.04","format_name":"png_pipe"}}`
	if info, _ := parseStreams([]byte(still)); !info.Image || info.FPS() != 0 || info.Duration != 0 {
		t.Fatalf("still %+v", info)
	}
	if _, err := parseStreams([]byte(`{"streams":[]}`)); err == nil {
		t.Fatal("no streams accepted")
	}
}

func TestEffectMove(t *testing.T) {
	if _, ok := EffectMove("none", false, 1280, 720); ok {
		t.Fatal("none moves")
	}
	mv, ok := EffectMove("panright", false, 1280, 720)
	if !ok {
		t.Fatal("panright")
	}
	// The window travels from −0.9 to +0.9 of its free room (W/2)(1−1/z):
	// the picture moves the other way by x·(W/2)(z−1).
	if math.Abs(mv.Scale0-1.12) > 1e-9 || math.Abs(mv.X0-0.9*640*0.12) > 1e-6 || math.Abs(mv.X1+0.9*640*0.12) > 1e-6 || mv.Y0 != 0 {
		t.Fatalf("panright %+v", mv)
	}
	half, _ := EffectMove("zoomin", true, 1280, 720)
	if math.Abs(half.Scale1-1.06) > 1e-9 {
		t.Fatalf("a clip's zoom runs at half strength: %+v", half)
	}
	rot, _ := EffectMove("rotate", false, 1280, 720)
	if rot.Rot0 <= 0 || rot.Rot1 >= 0 {
		t.Fatalf("the picture turns against the window: %+v", rot)
	}
	if s, x, _, _ := mv.At(0.5); math.Abs(s-1.12) > 1e-9 || math.Abs(x) > 1e-6 {
		t.Fatalf("middle %v %v", s, x)
	}
	push, _ := EffectMove("pushin", false, 1280, 720)
	if s, _, _, _ := push.At(0.25); s >= 1+0.3*0.25 {
		t.Fatalf("eased move should be slower at the start: %v", s)
	}
}

func TestPlanAccessors(t *testing.T) {
	o := MontageOptions{
		Transition: "fade", TransitionDuration: 0.5,
		Segments: []Segment{
			{Video: "a.mp4", VideoDur: 4, Narration: "Перша сцена.", Transition: "dissolve"},
			{Video: "b.mp4", VideoDur: 3, Narration: "Друга."},
			{Frame: "c.jpg", Narration: "Третя."},
		},
	}
	slots, total := Layout(o)
	if len(slots) != 3 || total <= 0 {
		t.Fatalf("%d slots, %v s", len(slots), total)
	}
	// The film fades in from black; the dissolve overlaps 12 frames; the dip
	// fades 0.3 s each side.
	if in, _, col, _ := slots[0].Fades(); in != 0.3 || col != "black" {
		t.Fatalf("opening fade %v %s", in, col)
	}
	if slots[0].TransitionKind() != KindXFade || slots[0].OutFrames() != 12 || slots[1].InFrames() != 12 {
		t.Fatalf("dissolve %v %d %d", slots[0].TransitionKind(), slots[0].OutFrames(), slots[1].InFrames())
	}
	if _, out, _, col := slots[1].Fades(); out != 0.3 || col != "black" || slots[1].TransitionKind() != KindDip {
		t.Fatalf("dip %v %s", out, col)
	}
	if slots[2].TransitionKind() != KindCut {
		t.Fatal("the last scene has no transition")
	}
	if got := slots[1].StartFrame(); got != slots[0].StartFrame()+slots[0].Frames()-12 {
		t.Fatalf("second scene at %d", got)
	}
	if slots[0].ClipFrames() != 96 {
		t.Fatalf("clip frames %d", slots[0].ClipFrames())
	}
	// The cues are the montage's own .srt.
	var segs []srtSeg
	for _, sl := range slots {
		segs = append(segs, srtSeg{narration: o.Segments[sl.Segment].Narration, start: sl.cueStart, span: sl.cueSpan})
	}
	if got, want := SRT(SubtitleCues(o)), buildSRT(segs, false); got != want || got == "" {
		t.Fatalf("srt:\n%s\nwant:\n%s", got, want)
	}
}
