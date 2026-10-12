package studio

// Regression tests of the production-reliability fixes (Oct 2026): scenes
// that failed are never dropped or replaced silently (gaps.go), the exact
// service errors with hints (failure.go), clip lengths that cover the line
// in the real montage (storyboard.go), the storyboard → single-frame
// fallback, the Telegram failure lists (notify.go).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
)

// The owner's «Історія про голодомор» (NVbZGo9Olo): 6 of 23 scenes had a
// refused storyboard (no frame, no clip) and vanished from the film with
// their narration; 4 more showed a still — the job said only "4 сцен без
// кліпу". Every such scene is a gap with its exact error now.
func TestMontageGapsListEveryFailedScene(t *testing.T) {
	dir := t.TempDir()
	img := writeAsset(t, dir, "images/f.jpg", []byte("x"))
	vid := writeAsset(t, dir, "videos/a.mp4", []byte("x"))
	aud := writeAsset(t, dir, "audio/v.mp3", []byte("x"))
	p := Project{Input: DefaultInput(), Montage: DefaultMontage()}
	p.Input.Voiceover = true
	refused := "G-Labs відхилив запит: змінити промпт або референси · G-Labs [400]: INVALID_ARGUMENT (flag=[3])"
	p.Scenes = []Scene{
		{ID: "ok", Narration: "Раз.", Frame: doneAsset(img, 0), Video: doneAsset(vid, 8), Audio: doneAsset(aud, 2)},
		{ID: "clip", Narration: "Два.", Frame: doneAsset(img, 0), Video: Asset{Status: StatusError, Error: "G-Labs: PROMINENT_PERSON"}, Audio: doneAsset(aud, 2)},
		{ID: "board", Narration: "Три.", Frame: Asset{Status: StatusError, Error: refused}, Board: Asset{Status: StatusError, Error: refused}, Audio: doneAsset(aud, 2)},
		{ID: "voice", Narration: "Чотири.", Frame: doneAsset(img, 0), Video: doneAsset(vid, 8), Audio: Asset{Status: StatusError, Error: "ElevenLabs [401]: invalid_api_key"}},
		{ID: "gone", Narration: "", Frame: doneAsset(img, 0), Video: doneAsset("videos/deleted.mp4", 8)},
		{ID: "silent", Frame: doneAsset(img, 0), Video: doneAsset(vid, 8)}, // nothing to say: no voice needed
	}
	gaps := MontageGaps(&p, dir)
	if len(gaps) != 4 {
		t.Fatalf("gaps %+v", gaps)
	}
	for i, want := range []struct {
		n                        int
		noClip, noFrame, noVoice bool
		reason                   string
	}{
		{2, true, false, false, "кліп: G-Labs: PROMINENT_PERSON"},
		{3, true, true, false, "кадр: " + refused},
		{4, false, false, true, "озвучка: ElevenLabs [401]: invalid_api_key"},
		{5, true, false, false, "файл кліпу зник з папки проєкту (videos/deleted.mp4)"},
	} {
		g := gaps[i]
		if g.Number != want.n || g.NoClip != want.noClip || g.NoFrame != want.noFrame || g.NoVoice != want.noVoice || g.Reason != want.reason {
			t.Errorf("gap %d: %+v, want %+v", i, g, want)
		}
	}
	// Statuses only (the UI): the deleted file is not seen, the rest is.
	if g := MontageGaps(&p, ""); len(g) != 3 {
		t.Fatalf("status-only gaps %+v", g)
	}
	// A film with sound in the clips or without a voice-over needs no voices.
	p.Input.AudioMode = AudioInVideo
	if g := MontageGaps(&p, dir); len(g) != 3 {
		t.Fatalf("invideo gaps %+v", g)
	}
	p.Input.AudioMode = ""

	// No choice → refused, with every scene and its exact error.
	err := errMissing(gaps, MissingAsk)
	m, ok := IsMissingScenes(err)
	if !ok || len(m.Gaps) != 4 || !strings.Contains(err.Error(), "Сцена 2 — кліп: G-Labs: PROMINENT_PERSON") ||
		!strings.Contains(err.Error(), "Сцена 3 — кадр: "+refused) || !strings.Contains(err.Error(), "Обери на кроці «Монтаж»") {
		t.Fatalf("refusal: %v", err)
	}
	if err := errMissing(gaps, MissingStop); err == nil || !strings.Contains(err.Error(), "обрано «Зупинитись»") {
		t.Fatalf("stop: %v", err)
	}
	if errMissing(gaps, MissingSkip) != nil || errMissing(gaps, MissingStill) != nil || errMissing(nil, MissingAsk) != nil {
		t.Fatal("a choice (or nothing missing) lets the montage run")
	}

	// «З кадром замість відео»: the frame stands in, a scene without a frame
	// is left out, a missing line leaves the scene silent — all recorded.
	o := montageOptions(&p, dir)
	done := applyGaps(&o, gaps, MissingStill)
	if o.Segments[1].Video != "" || o.Segments[1].Frame == "" || o.Segments[2].Frame != "" || o.Segments[2].Voice != "" ||
		o.Segments[3].Voice != "" || o.Segments[3].Video == "" || o.Segments[0].Video == "" || o.Segments[4].Frame == "" {
		t.Fatalf("still segments %+v", o.Segments)
	}
	if done[0].Action != GapStill || done[1].Action != GapSkipped || done[2].Action != GapSilent || done[3].Action != GapStill {
		t.Fatalf("still actions %+v", done)
	}
	sum := gapsSummary(done)
	for _, want := range []string{"кадр замість відео: сцена 2 (кліп: G-Labs: PROMINENT_PERSON), сцена 5", "пропущено: сцена 3 (кадр: G-Labs відхилив запит", "без голосу: сцена 4 (озвучка: ElevenLabs [401]"} {
		if !strings.Contains(sum, want) {
			t.Fatalf("summary %q lacks %q", sum, want)
		}
	}
	// «Змонтувати без них»: every gap leaves the film, its voice too.
	o = montageOptions(&p, dir)
	done = applyGaps(&o, gaps, MissingSkip)
	for _, k := range []int{1, 2, 3, 4} {
		if s := o.Segments[k]; s.Video != "" || s.Frame != "" || s.Voice != "" || done[0].Action != GapSkipped {
			t.Fatalf("skip segment %d: %+v", k, s)
		}
	}
	if o.Segments[0].Video == "" || o.Segments[5].Video == "" {
		t.Fatal("ready scenes stay")
	}
}

// Run(StageMontage) without a choice never starts a render that would fill
// in or drop scenes; RunWith with a choice does, and the job is marked.
func TestMontageRefusedUntilChosen(t *testing.T) {
	t.Parallel()
	env := newEnv(t, nil)
	e := env.e
	p, err := e.Create("Без кадрів")
	must(t, err)
	must(t, e.Edit(p.ID, func(p *Project) {
		p.Input.Voiceover = false
		p.Scenes = []Scene{{ID: "a", Title: "A"}, {ID: "b", Title: "B"}}
	}))
	err = e.Run(p.ID, StageMontage, false, "")
	if m, ok := IsMissingScenes(err); !ok || len(m.Gaps) != 2 || !strings.Contains(err.Error(), "Сцена 1 — кадр ще не намальовано") {
		t.Fatalf("run without a choice: %v", err)
	}
	if q, _ := e.Get(p.ID); q.Job != nil {
		t.Fatalf("a job started: %+v", q.Job)
	}
	// Leaving both out leaves nothing: an exact error, not an empty film.
	must(t, e.RunWith(p.ID, StageMontage, RunOptions{Missing: MissingSkip}))
	q := waitJob(t, e, p.ID)
	if q.Job.Status != JobError || !strings.HasPrefix(q.Job.Error, "Немає жодної готової сцени для монтажу: ⚠ пропущено: сцена 1 (кадр ще не намальовано), сцена 2") {
		t.Fatalf("job %+v", q.Job)
	}
}

// Errors are stored exactly as the services said them, keys masked; hints
// explain the codes the owner saw.
func TestFailureTextAndHints(t *testing.T) {
	env := newEnv(t, func(c *config.Config) {
		c.Custom.VideoKey = "rt-SECRETSECRETSECRET42"
		c.Google.Keys = []string{"AIzaSyDUMMYDUMMYDUMMYDUMMYDUMMYDUMMY12"}
	})
	e := env.e
	msg := e.failureText(fmt.Errorf("RoyalTechno [401]: key rt-SECRETSECRETSECRET42 rejected; url https://x/y?key=abcdef123456&z=1; Bearer abcdefghijkl; AIzaSyDUMMYDUMMYDUMMYDUMMYDUMMYDUMMY12"))
	for _, leak := range []string{"SECRETSECRETSECRET", "abcdef123456", "abcdefghijkl", "DUMMYDUMMYDUMMYDUMMY"} {
		if strings.Contains(msg, leak) {
			t.Fatalf("secret %q in %q", leak, msg)
		}
	}
	if !strings.HasPrefix(msg, "RoyalTechno [401]: key rt-SE") {
		t.Fatalf("message changed: %q", msg)
	}
	for in, want := range map[string]string{
		"G-Labs: PROMINENT_PERSON": "реальну людину",
		"G-Labs відхилив запит: змінити промпт або референси — фільтр безпеки Google (REPUTATIONAL)": "репутаційно чутливою",
		"G-Labs: DANGEROUS": "небезпечні дії",
		"G-Labs [400]: INVALID_ARGUMENT (flag=[3]) — запит відхилено (можливо, промт порушує політику)": "ще до генерації",
		"Upload failed for image 1: UPLOAD_ERROR|500: INTERNAL (flag=[13])":                             "тимчасовий збій",
		"G-Labs [429]: quota — вичерпано ліміт/кредити акаунта, спробуй пізніше":                        "",
		"Готово": "",
	} {
		if h := FailureHint(in); want == "" && h != "" || !strings.Contains(h, want) {
			t.Errorf("hint of %q: %q", in, h)
		}
	}
}

// KTXzWcfeA_ (dissolve 0.3 s): a 7.36 s line got an 8 s clip — the montage
// needs 8.54 s between the two crossfades, so the clip froze for 0.5 s.
// With dynamic lengths the clip now covers the overlaps too; projects with
// the default dip keep the old lengths.
func TestDynamicClipCoversTransitions(t *testing.T) {
	c := config.Default()
	c.Video = config.Video{Provider: "glabs", GlabsModel: "omni_flash", Length: 10, Dynamic: true}
	p := Project{Montage: DefaultMontage()}
	for i := 0; i < 3; i++ {
		p.Scenes = append(p.Scenes, Scene{ID: fmt.Sprintf("s%d", i), Narration: "Рядок.",
			Audio: Asset{Status: StatusDone, Path: "audio/v.mp3", Duration: 7.36}})
	}
	p.Montage.Transition = ""
	if pad := montagePad(&p, "s1"); pad != 0 {
		t.Fatalf("default dip pad %v", pad)
	}
	if got := sceneClipPad(c, &p.Scenes[1], montagePad(&p, "s1")); got != 8 {
		t.Fatalf("dip: %d s", got)
	}
	p.Montage.Transition, p.Montage.TransitionDuration = "dissolve", 0.3
	pad := montagePad(&p, "s1")
	if pad < 0.58 || pad > 0.59 {
		t.Fatalf("crossfade pad %v", pad)
	}
	if got := sceneClipPad(c, &p.Scenes[1], pad); got != 10 {
		t.Fatalf("crossfades: %d s, want 10", got)
	}
	// The last scene has one crossfade and the film's closing dip only.
	if last := montagePad(&p, "s2"); last >= pad {
		t.Fatalf("last scene pad %v", last)
	}
	// A slice of the user's recording carries its own lead/tail: no pad.
	own := Scene{AudioSource: AudioUpload, VoiceStart: 0, VoiceEnd: 7.36}
	if got := sceneClipPad(c, &own, pad); got != 8 {
		t.Fatalf("own voice: %d", got)
	}
}

// Q0QICaVG9X (invideo, 4 s clips): "О ні! Радіоактивний пончик кусає?! Д'ов!"
// is 6 words (2.4 s at the assumed 2.5 w/s) but 14 syllables; the clip
// ended while Homer was still talking. The clip length now allows slow
// speech.
func TestDialogueClipAllowsSlowSpeech(t *testing.T) {
	c := config.Default()
	c.Video = config.Video{Provider: "glabs", GlabsModel: "omni_flash", Length: 10, Dynamic: true}
	s := &Scene{Lines: []Line{{Speaker: "homer", Text: "О ні! Радіоактивний пончик кусає?! Д'ов!", Delivery: "shrieking in comic panic"}}}
	if n := syllables(s.Lines[0].Text); n != 14 {
		t.Fatalf("syllables %d", n)
	}
	if got := sceneClip(c, s); got != 6 {
		t.Fatalf("clip %d s, want 6", got)
	}
	if got := sceneClip(c, &Scene{Lines: []Line{{Speaker: "a", Text: "Так!"}}}); got != 4 {
		t.Fatalf("short line %d s", got)
	}
}

// The safer form of a refused components clip is the frame alone with the
// plain prompt — never the storyboard again.
func TestSaferRequestIsTheFrameAlone(t *testing.T) {
	start := &gen.RefImage{Data: []byte("x"), MIME: "image/jpeg"}
	in := &shotInputs{plain: gen.VideoRequest{Prompt: "Motion only.", Start: start, Seconds: 6}}
	req := gen.VideoRequest{Prompt: "Shot: …", Mode: gen.ModeComponents, Refs: []gen.RefImage{{Name: "storyboard.jpg"}, {Name: "previous_shot.jpg"}}}
	safer, _, ok := saferRequest(in, req)
	if !ok || safer.Mode != "" || len(safer.Refs) != 0 || safer.Start != start || safer.Prompt != "Motion only." || safer.Seconds != 6 {
		t.Fatalf("safer %+v", safer)
	}
	if _, _, ok := saferRequest(in, gen.VideoRequest{Start: start}); ok {
		t.Fatal("a frame-only request has no safer form")
	}
}

// imageRejectProxy refuses G-Labs image requests whose prompt matches.
type imageRejectProxy struct {
	mu    sync.Mutex
	next  http.Handler
	match string
	n     int
	seen  []string
}

func (x *imageRejectProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost && r.URL.Path == "/api/image/generate" {
		b, _ := io.ReadAll(r.Body)
		r.Body = io.NopCloser(bytes.NewReader(b))
		var body struct{ Prompt string }
		_ = json.Unmarshal(b, &body)
		x.mu.Lock()
		board := strings.Contains(body.Prompt, x.match)
		x.seen = append(x.seen, fmt.Sprintf("board=%v", board))
		if board {
			x.n++
			id := fmt.Sprintf("rejimg%d", x.n)
			x.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			fmt.Fprintf(w, `{"task_id":%q}`, id)
			return
		}
		x.mu.Unlock()
	}
	if strings.HasPrefix(r.URL.Path, "/api/status/rejimg") {
		_, _ = w.Write([]byte(`{"status":"failed","error_code":400,"error":"400: INVALID_ARGUMENT (flag=[3])","error_detail":"400: INVALID_ARGUMENT (flag=[3])"}`))
		return
	}
	x.next.ServeHTTP(w, r)
}

// JFa0416DaP / NVbZGo9Olo / saPKXuRJwd: a refused 3×3 storyboard left the
// scene with no frame — no clip — and the scene vanished from the film. A
// sheet refused for its content now gets one single-frame try; the board
// keeps the refusal so the step shows it.
func TestRefusedBoardFallsBackToFrame(t *testing.T) {
	needMedia(t)
	t.Parallel()
	env, gl := newSBEnv(t, nil)
	e := env.e
	p := castProject(t, e, func(in *Input) { in.StoryboardMode = StoryboardGrid }, []int{0})
	px := &imageRejectProxy{next: gl, match: "3×3 storyboard sheet"}
	srv := httptest.NewServer(px)
	allow(srv)
	t.Cleanup(srv.Close)
	_ = env.store.Update(func(c *config.Config) { c.Glabs.BaseURL = srv.URL })

	must(t, e.Run(p.ID, StageFrames, false, ""))
	p = waitJob(t, e, p.ID)
	s := p.Scenes[0]
	if s.Frame.Status != StatusDone || s.Frame.Path == "" || s.Board.Status != StatusError || s.Board.Path != "" ||
		!strings.HasPrefix(s.Board.Error, "Розкадровку 3×3 відхилено — намальовано один кадр") || !strings.Contains(s.Board.Error, "INVALID_ARGUMENT (flag=[3])") {
		t.Fatalf("scene after a refused board: frame %+v board %+v", s.Frame, s.Board)
	}
	if strings.Join(px.seen, ",") != "board=true,board=false" {
		t.Fatalf("image requests %v", px.seen)
	}
	if _, err := os.Stat(filepath.Join(e.Dir(p.ID), filepath.FromSlash(s.Frame.Path))); err != nil {
		t.Fatal(err)
	}
	if gaps := MontageGaps(&p, e.Dir(p.ID)); len(gaps) != 1 || gaps[0].NoFrame {
		t.Fatalf("the scene still has its frame: %+v", gaps)
	}

	// Both refused: the frame keeps both exact errors.
	px.mu.Lock()
	px.match = "Wide shot"
	px.mu.Unlock()
	must(t, e.Regen(p.ID, RegenFrame, s.ID, ""))
	waitIdle(t, e)
	p, _ = e.Get(p.ID)
	if f := p.Scenes[0].Frame; f.Status != StatusError || !strings.Contains(f.Error, "(Спершу розкадровка 3×3: G-Labs відхилив запит") || !rejected(f) {
		t.Fatalf("frame after two refusals %+v", f)
	}
}

// Telegram lists every failed item with its exact error and a hint.
func TestTelegramFailureLines(t *testing.T) {
	p := Project{Scenes: []Scene{
		{Video: Asset{Status: StatusError, Error: "G-Labs: PROMINENT_PERSON"}},
		{Video: Asset{Status: StatusDone}, Audio: Asset{Status: StatusError, Error: "ElevenLabs [429]: quota_exceeded"}},
	}}
	lines := failureLines(&p, JobRender)
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "Сцена 1 · кліп: G-Labs: PROMINENT_PERSON (фільтр Google побачив упізнавану реальну людину") ||
		!strings.HasPrefix(lines[1], "Сцена 2 · озвучка: ElevenLabs [429]: quota_exceeded") {
		t.Fatalf("lines %q", lines)
	}
	if b := failedBlock(lines); !strings.Contains(b, "❌ Не вдалося:") || !strings.Contains(b, "• Сцена 1 · кліп") {
		t.Fatalf("block %q", b)
	}
	if failedBlock(nil) != "" || len(failureLines(&p, JobMontage)) != 0 {
		t.Fatal("a montage job lists no generation failures")
	}
}
