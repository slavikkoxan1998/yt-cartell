package dictate

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/websocket"
)

type fakeRec struct {
	fn  func(ctx context.Context, audio <-chan []int16, out sink) error
	fin time.Duration
}

func (f *fakeRec) run(ctx context.Context, audio <-chan []int16, out sink) error {
	return f.fn(ctx, audio, out)
}
func (f *fakeRec) finishTime() time.Duration { return f.fin }

// useRec makes every session use fn as its recogniser.
func useRec(t *testing.T, fn func(ctx context.Context, audio <-chan []int16, out sink) error) {
	t.Helper()
	old := newRecognizer
	newRecognizer = func(Plan) (recognizer, error) { return &fakeRec{fn: fn, fin: 2 * time.Second}, nil }
	t.Cleanup(func() { newRecognizer = old })
}

// echoRec types "слово N" for every phrase the detector finds; interim
// "сл…" while one goes on.
func echoRec(ctx context.Context, audio <-chan []int16, out sink) error {
	var v vad
	n, in := 0, false
	for f := range audio {
		sp := v.push(frameDB(f))
		switch {
		case sp && !in:
			in = true
			out.interim("сл")
		case !sp && in:
			in = false
			n++
			out.final("слово " + strconv.Itoa(n))
			out.interim("")
		}
	}
	if in {
		out.final("останнє")
	}
	return nil
}

func readyPlan() Plan {
	return Plan{Provider: ElevenLabs, Label: "test", Key: "k", Ready: true, Silence: 2}
}

func TestSessionSilenceStop(t *testing.T) {
	ff, _ := fakeMic(t, cat(hush(0.3), tone(1), hush(0.6), tone(0.6), hush(6)), false)
	useRec(t, echoRec)
	var notes atomic.Int32
	s := Start(Options{Plan: readyPlan(), FFmpeg: ff, Notify: func() { notes.Add(1) }})
	st := waitDone(t, s, 10*time.Second)
	if st.Reason != StopSilence || st.Err != nil {
		t.Fatalf("stopped: %+v", st)
	}
	// 0.3 + 1 + 0.6 + 0.6 s, then 2 s of silence (+ the detector's hangover).
	if st.Elapsed < 4400*time.Millisecond || st.Elapsed > 5000*time.Millisecond || !st.Heard {
		t.Fatalf("elapsed %v heard %v", st.Elapsed, st.Heard)
	}
	if st.Device != "Microphone (High Definition Audio Device)" || st.Label != "test" {
		t.Fatalf("device %q label %q", st.Device, st.Label)
	}
	finals, interim := takeAll(s)
	if strings.Join(finals, "|") != "слово 1|слово 2" || interim != "" {
		t.Fatalf("text %q %q", finals, interim)
	}
	if notes.Load() == 0 {
		t.Fatal("never notified")
	}
	if _, _, changed := s.Take(); changed {
		t.Fatal("Take reports a change twice")
	}
}

func TestSessionStopGoneAndMute(t *testing.T) {
	// A live microphone (never ends) that delivers digital zeros.
	ff, _ := fakeMic(t, make([]int16, SampleRate), true)
	useRec(t, echoRec)
	p := readyPlan()
	p.Silence = 0
	s := Start(Options{Plan: p, FFmpeg: ff})
	deadline := time.Now().Add(5 * time.Second)
	for !s.State().Mute && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !s.State().Mute {
		t.Fatalf("digital silence not noticed: %+v", s.State())
	}
	s.Stop()
	if st := waitDone(t, s, 5*time.Second); st.Reason != StopUser || st.Err != nil {
		t.Fatalf("user stop: %+v", st)
	}

	// The story box goes away: no heartbeat.
	old := beatTimeout
	beatTimeout = 150 * time.Millisecond
	defer func() { beatTimeout = old }()
	s = Start(Options{Plan: p, FFmpeg: ff})
	s.Heartbeat()
	if st := waitDone(t, s, 5*time.Second); st.Reason != StopGone {
		t.Fatalf("gone: %+v", st)
	}
}

func TestSessionErrors(t *testing.T) {
	ff, _ := fakeMic(t, tone(1), false)
	useRec(t, echoRec)
	// Not ready: the plan's problem.
	p := Plan{Provider: Deepgram, Problem: "Немає ключа Deepgram"}
	if st := waitDone(t, Start(Options{Plan: p, FFmpeg: ff}), 3*time.Second); KindOf(st.Err) != KindNoKey || st.Reason != StopError {
		t.Fatalf("no key: %+v", st)
	}
	if st := waitDone(t, Start(Options{Plan: readyPlan()}), 3*time.Second); KindOf(st.Err) != KindNoFFmpeg {
		t.Fatalf("no ffmpeg: %+v", st)
	}
	// The microphone cannot be opened.
	t.Setenv(fakeFailEnv, `[dshow @ 1] Could not find audio only device with name [x]`)
	if st := waitDone(t, Start(Options{Plan: readyPlan(), FFmpeg: ff}), 5*time.Second); KindOf(st.Err) != KindNoMic {
		t.Fatalf("no mic: %+v", st)
	}
	t.Setenv(fakeFailEnv, "")
	// No microphones at all.
	t.Setenv(fakeListEnv, "")
	if st := waitDone(t, Start(Options{Plan: readyPlan(), FFmpeg: ff}), 5*time.Second); KindOf(st.Err) != KindNoMic {
		t.Fatalf("no devices: %+v", st)
	}
	t.Setenv(fakeListEnv, dshowList)
	// The service fails mid-way: the session ends with its error, the
	// words already typed stay.
	useRec(t, func(ctx context.Context, audio <-chan []int16, out sink) error {
		<-audio
		out.final("встигло")
		return newErr(KindAuth, nil, "ключ відхилено")
	})
	ff, _ = fakeMic(t, cat(tone(1), hush(1)), true)
	s := Start(Options{Plan: readyPlan(), FFmpeg: ff})
	st := waitDone(t, s, 5*time.Second)
	if KindOf(st.Err) != KindAuth || !NeedsSettings(st.Err) {
		t.Fatalf("service error: %+v", st)
	}
	if f, _ := takeAll(s); len(f) != 1 || f[0] != "встигло" {
		t.Fatalf("text %q", f)
	}
	// A recogniser that never finishes is cut after its finish time; its
	// interim text is kept as typed text.
	useRec(t, func(ctx context.Context, audio <-chan []int16, out sink) error {
		for range audio {
		}
		out.interim("незавершене")
		<-ctx.Done()
		return ctx.Err()
	})
	old := newRecognizer
	newRecognizer = func(Plan) (recognizer, error) {
		r, _ := old(Plan{})
		r.(*fakeRec).fin = 200 * time.Millisecond
		return r, nil
	}
	t.Setenv(fakeHoldEnv, "")
	ff, _ = fakeMic(t, tone(0.5), false)
	s = Start(Options{Plan: readyPlan(), FFmpeg: ff})
	st = waitDone(t, s, 5*time.Second)
	if st.Err != nil || st.Reason != StopEnded {
		t.Fatalf("slow finish: %+v", st)
	}
	if f, i := takeAll(s); len(f) != 1 || f[0] != "незавершене" || i != "" {
		t.Fatalf("kept %q %q", f, i)
	}
}

func TestSessionElevenLabsEndToEnd(t *testing.T) {
	srv := wsServer(t, func(ws *websocket.Conn) {
		bytes := 0
		said := false
		for {
			var msg string
			if err := websocket.Message.Receive(ws, &msg); err != nil {
				return
			}
			var m struct {
				Audio  string `json:"audio_base_64"`
				Commit bool   `json:"commit"`
			}
			_ = json.Unmarshal([]byte(msg), &m)
			bytes += len(m.Audio) * 3 / 4
			if !said && bytes > 20000 {
				said = true
				websocket.Message.Send(ws, `{"message_type":"partial_transcript","text":"жив собі"}`)
			}
			if m.Commit {
				websocket.Message.Send(ws, `{"message_type":"committed_transcript","text":"Жив собі кіт."}`)
			}
		}
	})
	ff, _ := fakeMic(t, cat(tone(1.5), hush(0.5)), false)
	p := readyPlan()
	p.BaseURL, p.Model = wsURL(srv, "/v1/speech-to-text/realtime"), ElevenModel
	s := Start(Options{Plan: p, FFmpeg: ff})
	st := waitDone(t, s, 10*time.Second)
	if st.Err != nil || st.Reason != StopEnded {
		t.Fatalf("ended: %+v", st)
	}
	f, _ := takeAll(s)
	if strings.Join(f, "|") != "Жив собі кіт." {
		t.Fatalf("text %q", f)
	}
}

func TestErrorKinds(t *testing.T) {
	e := newErr(KindQuota, errors.New("cause"), "Кредити %d", 0)
	if e.Error() != "Кредити 0" || !errors.Is(e, e.Err) || KindOf(e) != KindQuota || NeedsSettings(e) {
		t.Fatalf("%v", e)
	}
	if KindOf(errors.New("x")) != KindOther {
		t.Fatal("foreign error")
	}
}
