package studio

// Translate: the same film in another language, reusing every picture.
//
// Translate makes a copy of the project (new id, SourceID = the source) that
// shares the source's references, frames, 3×3 boards, clips, last frames,
// music and cover (hard links, see duplicate.go). Voices, the final render,
// its SRT and the cover with text are not taken over. A JobTranslate job on
// the copy then does:
//
//	text     the text model translates the audience-facing texts — title,
//	         logline, character names/roles and cover texts first (that fixes
//	         the names), then scene titles + narration in batches. Every line
//	         is fitted to its source line's speaking time (±10%, the planner's
//	         words-per-second budget); lines estimated >20% too long are asked
//	         once more.
//	voices   every scene voiced in the target language (genVoice: the TTS
//	         language/tone options follow Input.Language)
//	montage  when asked and the source had a finished film
//
// @tags, ids and image/motion prompts are never touched, so the reused frames
// and clips stay valid.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"ytcartell/internal/llm"
	"ytcartell/internal/tts"
)

// JobTranslate is the background stage of a translated copy (Translate).
const JobTranslate JobKind = "translate"

func init() {
	jobLabels[JobTranslate] = "Переклад"
	jobRestarts[JobTranslate] = (*Engine).resumeTranslation // «Повторити» in the queue
	jobAssets[JobTranslate] = []RegenKind{RegenVoice}
}

const translateBatch = 8

// TranslateOptions of Translate.
type TranslateOptions struct {
	// Language of the translation: an ISO 639-1 code from Languages (ISO
	// 639-3 where the catalogue has no two-letter code).
	Language string
	// Voice is the narrator voice of the translation ("" = keep the source's
	// voices). Character voices are kept either way.
	Voice string
	// Montage also renders the final video of the translation (only when the
	// source has a finished one).
	Montage bool
}

// Translate creates the translated copy of a project and starts its
// JobTranslate job; the new project's id is returned at once (also when
// starting the job failed after the copy was made). The source is not
// changed.
func (e *Engine) Translate(id string, opt TranslateOptions) (newID string, err error) {
	lang, ok := LanguageByCode(opt.Language)
	if !ok {
		return "", fmt.Errorf("Невідома мова перекладу «%s»", strings.TrimSpace(opt.Language))
	}
	src := e.state(id)
	if src == nil {
		return "", ErrNotFound
	}
	if e.closing.Load() {
		return "", ErrClosing
	}
	snap, ok := e.Get(id)
	if !ok {
		return "", ErrNotFound
	}
	if len(snap.Scenes) == 0 {
		return "", errors.New("Спершу створи план — перекладати ще нічого")
	}
	from := sourceLanguage(src.dir, &snap)
	if from == lang.Code {
		return "", fmt.Errorf("Цей проєкт уже мовою «%s» — вибери іншу мову", lang.Label)
	}

	p := cloneProject(&snap)
	p.Title = clipRunes(strings.TrimSpace(snap.Title)+" · "+lang.Native, 120)
	p.SourceID = snap.ID
	in := &p.Input
	in.Language = lang.Code
	if in.Mode == ModeVoice { // the recording is in the source language: TTS from now on
		in.Mode, in.VoiceFile, in.VoiceFileName = ModeScript, "", ""
		in.AudioMode, in.Voiceover = AudioVoiceover, true // the recording was the voice-over: TTS takes over
	}
	if v := strings.TrimSpace(opt.Voice); v != "" {
		in.NarratorVoiceID = clipRunes(v, 80)
	}
	for i := range p.Scenes {
		s := &p.Scenes[i]
		s.Audio = EmptyAsset()
		s.AudioSource, s.VoiceStart, s.VoiceEnd = "", 0, 0
	}
	p.Montage.Output, p.Montage.CoverFinal, p.Montage.SRTPath = EmptyAsset(), EmptyAsset(), ""
	p.Step = min(p.Step, 4)

	job := translateJob{
		from:    from,
		to:      lang,
		source:  snap,
		montage: opt.Montage && snap.Montage.Output.Status == StatusDone && snap.Montage.Output.Path != "",
		cover:   snap.Montage.CoverFinal.Status == StatusDone && snap.Montage.Cover.Path != "",
	}
	st, err := e.createCopy(src, &p, translateFiles(src.dir, &snap))
	if err != nil {
		return "", err
	}
	return st.id, e.startJobKind(st, JobTranslate, nil, func(j *jobCtx) (string, error) { return e.runTranslate(j, job) })
}

// sourceLanguage is the language the project speaks: the chosen one, else
// the one detected in its recording, else one its writing system gives away
// ("" = unknown).
func sourceLanguage(dir string, p *Project) string {
	if l := tts.NormalizeLanguage(p.Input.Language); l != "" {
		return l
	}
	if p.Input.Mode == ModeVoice && p.Input.VoiceFile != "" {
		if tr, ok := loadTranscript(dir, p.Input.VoiceFile); ok && tr.Language != "" {
			return tts.NormalizeLanguage(tr.Language)
		}
	}
	var sample strings.Builder
	for _, s := range p.Scenes {
		if sample.Len() > 4000 {
			break
		}
		sample.WriteString(spokenText(&s))
		sample.WriteByte(' ')
	}
	if l := scriptLanguage(sample.String()); l != "" {
		return l
	}
	return unspacedLanguage(sample.String())
}

// translateFiles are the source files a translation reuses: references,
// frames, boards, clips, last frames, music and the cover picture.
func translateFiles(dir string, p *Project) []string {
	var out []string
	add := func(rel string) {
		if rel != "" {
			out = append(out, rel)
		}
	}
	for _, c := range p.Characters {
		add(c.Image.Path)
	}
	for _, l := range p.Locations {
		add(l.Image.Path)
	}
	for _, s := range p.Scenes {
		add(s.Frame.Path)
		add(s.Board.Path)
		add(s.Video.Path)
		if rel := LastFrameRel(s.ID); isFile(filepath.Join(dir, filepath.FromSlash(rel))) {
			add(rel)
		}
	}
	add(p.Montage.MusicPath)
	add(p.Montage.Cover.Path)
	for _, m := range p.Input.Materials { // the user's start materials (a new plan binds them again)
		add(m.Path)
	}
	return out
}

// resumeTranslation starts the translate job of a translated copy again
// («Повторити» after it failed or was stopped): scenes whose text is still
// the source's are translated, missing voices are made; no montage.
func (e *Engine) resumeTranslation(id string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	p, ok := e.Get(id)
	if !ok {
		return ErrNotFound
	}
	if p.SourceID == "" {
		return errors.New("Це не переклад — перекладай з вихідного проєкту")
	}
	lang, ok := LanguageByCode(p.Input.Language)
	if !ok {
		return fmt.Errorf("Невідома мова перекладу «%s»", p.Input.Language)
	}
	src, ok := e.Get(p.SourceID)
	if !ok {
		return errors.New("Вихідний проєкт видалено — переклад не можна продовжити")
	}
	job := translateJob{from: sourceLanguage(e.Dir(src.ID), &src), to: lang, source: src, resume: true}
	return e.startJobKind(st, JobTranslate, nil, func(j *jobCtx) (string, error) { return e.runTranslate(j, job) })
}

// startJobKind starts run as a background job of kind on st — the Run
// machinery (one job per project, closing/busy/check, runJob bookkeeping)
// for jobs that are not Run stages (translate, transcribe).
func (e *Engine) startJobKind(st *proj, kind JobKind, check func(p *Project) error, run func(j *jobCtx) (string, error)) error {
	st.mu.Lock()
	if st.deleted.Load() {
		st.mu.Unlock()
		return ErrNotFound
	}
	if e.closing.Load() {
		st.mu.Unlock()
		return ErrClosing
	}
	if j := st.p.Job; st.job != nil || (j != nil && j.Status == JobRunning) {
		k := kind
		if j != nil {
			k = j.Kind
		}
		st.mu.Unlock()
		return &busyError{k}
	}
	if check != nil {
		if err := check(st.p); err != nil {
			st.mu.Unlock()
			return err
		}
	}
	jc := &jobCtx{e: e, st: st, work: st.ctx}
	jc.ctx, jc.stop = context.WithCancel(st.ctx)
	st.job = jc
	st.p.Job = &Job{Kind: kind, Status: JobRunning, Message: "Запуск…", StartedAt: nowMs()}
	list := e.commitLocked(st)
	st.bg.Add(1)
	st.mu.Unlock()
	e.emit(st, list)
	go e.runJob(st, jc, kind, run)
	return nil
}

// ---- the job ------------------------------------------------------------------------------

type translateJob struct {
	from    string   // source language code ("" = unknown)
	to      Language // target
	source  Project  // the source as it was when the translation started
	montage bool     // render the final video at the end
	cover   bool     // put the translated text on the cover (the source had one)
	resume  bool     // a retry: keep what is translated and voiced already
}

// translated reports that the copy's scene c no longer has its source
// scene's text.
func translated(c, s *Scene) bool {
	switch {
	case strings.TrimSpace(s.Narration) != "":
		return c.Narration != s.Narration
	case len(cleanLines(s.Lines)) > 0:
		return LinesText(c.Lines) != LinesText(s.Lines)
	}
	return c.Title != s.Title
}

// Progress of the translate job (percent).
const (
	trHeaderAt   = 2
	trScenesFrom = 5
	trScenesSpan = 35
	trRetryAt    = 42
	trVoicesFrom = 45
	trMontageAt  = 88
)

// lineResult is a scene's translation: its title, narration, the words of
// its spoken lines (AudioInVideo, in order) and its on-screen text.
type lineResult struct {
	title, narration string
	lines            []string
	parts            []string // a multi-voice scene's parts, in order (narration = them joined)
	onScreen         *OnScreen
}

func (e *Engine) runTranslate(j *jobCtx, t translateJob) (string, error) {
	const stopped = "Зупинено"
	src := &t.source
	cur := j.snapshot()
	var todo []int // source scenes to translate
	for i := range src.Scenes {
		s := &src.Scenes[i]
		if strings.TrimSpace(s.Title) == "" && strings.TrimSpace(s.Narration) == "" && len(cleanLines(s.Lines)) == 0 && s.OnScreen.Empty() {
			continue
		}
		if c := findScene(&cur, s.ID); t.resume && (c == nil || translated(c, s)) {
			continue // gone, or done in the first run
		}
		todo = append(todo, i)
	}
	total := len(todo)
	j.progress(trHeaderAt, fmt.Sprintf("Переклад тексту · 0/%d", total))

	// 1. Title, logline, names, cover: the names then stay fixed.
	names, err := e.translateHeader(j, t)
	if err != nil {
		if j.cancelled() && isCancel(err) {
			return stopped, nil
		}
		return "", err
	}
	if j.cancelled() {
		return stopped, nil
	}
	if t.cover {
		if err := e.ApplyCoverText(j.st.id); err != nil {
			log.Printf("[studio:%s] translated cover text: %v", j.st.id, err)
		}
	}

	// 2. Scene titles and narration, in batches.
	var batches [][]int
	for i := 0; i < len(todo); i += translateBatch {
		batches = append(batches, todo[i:min(len(todo), i+translateBatch)])
	}
	var (
		mu      sync.Mutex
		done    int
		errs    []error
		results = map[string]lineResult{}
	)
	_ = mapLimit(batches, 3, j.cancelled, func(batch []int, _ int) error {
		got, err := e.translateLines(j.ctx, t, names, batch, nil)
		mu.Lock()
		if err != nil {
			errs = append(errs, err)
		}
		for id, r := range got {
			results[id] = r
		}
		done += len(batch)
		n := done
		mu.Unlock()
		e.applyLines(j.st, got)
		j.progress(trScenesFrom+trScenesSpan*float64(n)/float64(max(1, len(todo))), fmt.Sprintf("Переклад тексту · %d/%d", n, total))
		return nil
	})
	if j.cancelled() {
		return stopped, nil
	}
	if len(errs) == len(batches) && len(batches) > 0 {
		return "", errs[0] // nothing came back at all (key, network…)
	}

	// 3. Once more for lines that are missing or clearly too long.
	var again []int
	for _, i := range todo {
		s := &src.Scenes[i]
		r, ok := results[s.ID]
		if !ok || (strings.TrimSpace(s.Narration) != "" && strings.TrimSpace(r.narration) == "") || tooLong(r.narration, s, t) {
			again = append(again, i)
		}
	}
	if len(again) > 0 {
		j.progress(trRetryAt, fmt.Sprintf("Уточнюємо %d %s", len(again), ukPlural(len(again), "рядок", "рядки", "рядків")))
		var rb [][]int
		for i := 0; i < len(again); i += translateBatch {
			rb = append(rb, again[i:min(len(again), i+translateBatch)])
		}
		first := make(map[string]lineResult, len(results)) // read by every retry batch
		for id, r := range results {
			first[id] = r
		}
		_ = mapLimit(rb, 3, j.cancelled, func(batch []int, _ int) error {
			got, err := e.translateLines(j.ctx, t, names, batch, first)
			if err != nil {
				log.Printf("[studio:%s] translation retry: %v", j.st.id, err)
			}
			mu.Lock()
			for id, r := range got {
				old := results[id]
				if r.title == "" {
					r.title = old.title
				}
				if len(old.parts) > 0 && len(r.parts) == 0 { // multi-voice: the first parts stay
					r.narration, r.parts = old.narration, old.parts
				}
				results[id] = r
			}
			mu.Unlock()
			e.applyLines(j.st, got)
			return nil
		})
		if j.cancelled() {
			return stopped, nil
		}
	}
	untranslated := map[string]bool{}
	for _, i := range todo {
		s := &src.Scenes[i]
		r := results[s.ID]
		if strings.TrimSpace(s.Narration) != "" && strings.TrimSpace(r.narration) == "" || len(cleanLines(s.Lines)) > 0 && len(r.lines) == 0 {
			untranslated[s.ID] = true
		}
	}

	// 4. Voices in the target language.
	snap := j.snapshot()
	var voices []string
	if snap.Input.Voiceover {
		for _, s := range snap.Scenes {
			if strings.TrimSpace(s.Narration) == "" || untranslated[s.ID] {
				continue
			}
			if t.resume && s.Audio.Status == StatusDone && !s.Audio.Stale {
				continue
			}
			voices = append(voices, s.ID)
		}
	}
	span := float64(100 - trVoicesFrom)
	if t.montage {
		span = trMontageAt - trVoicesFrom
	}
	tick := counter(j, len(voices), "Озвучка", trVoicesFrom, span)
	var failed atomic.Int64
	_ = mapLimit(voices, VoiceConcurrency(e.cfg.Get()), j.cancelled, func(id string, _ int) error {
		if !e.genVoice(j.work, j.st, id) {
			failed.Add(1)
		}
		tick()
		return nil
	})
	if len(voices) > 0 {
		e.update(j.st, func(p *Project) { p.Step = max(p.Step, 4) })
	}
	if j.cancelled() {
		return stopped, nil
	}

	var notes []string
	if n := len(untranslated); n > 0 {
		notes = append(notes, fmt.Sprintf("%d %s не перекладено", n, ukPlural(n, "сцену", "сцени", "сцен")))
	}
	if n := int(failed.Load()); n > 0 {
		notes = append(notes, fmt.Sprintf("%d %s з помилкою", n, ukPlural(n, "озвучка", "озвучки", "озвучок")))
	}
	if snap.Input.SoundMode() == AudioInVideo {
		// The clips speak the source language: their lines changed.
		n := 0
		for _, s := range snap.Scenes {
			if len(cleanLines(s.Lines)) > 0 && s.Video.Status == StatusDone && s.Video.Stale {
				n++
			}
		}
		if n > 0 {
			notes = append(notes, fmt.Sprintf("%d %s говорять мовою оригіналу — перезніми їх, щоб герої заговорили новою мовою",
				n, ukPlural(n, "кліп", "кліпи", "кліпів")))
		}
	}
	msg := "Переклад готовий"
	if len(notes) > 0 {
		msg += ", " + strings.Join(notes, ", ")
	}

	// 5. The final video.
	if !t.montage {
		return msg, nil
	}
	if len(notes) > 0 {
		return msg + " — монтаж пропущено", nil
	}
	j.progress(trMontageAt, "Монтаж…")
	j.within(trMontageAt, 100-trMontageAt, "Монтаж · ")
	res, err := e.runMontage(j)
	j.within(0, 0, "")
	switch {
	case err != nil && j.cancelled():
		return msg + " · монтаж зупинено", nil
	case err != nil:
		return msg + " · монтаж не вдався: " + clipRunes(e.redact(errText(err)), 1000), nil
	}
	return msg + " · " + res, nil
}

// tooLong: the translated line is estimated >20% longer than its source line
// (and at least half a second — one word — over).
func tooLong(line string, s *Scene, t translateJob) bool {
	if strings.TrimSpace(line) == "" {
		return false
	}
	have := sourceSeconds(s, t.from)
	est := spokenSeconds(line, t.to.Code)
	return est > have*1.2 && est-have >= 0.5
}

// applyLines stores translated scene texts on the live project.
func (e *Engine) applyLines(st *proj, got map[string]lineResult) {
	if len(got) == 0 {
		return
	}
	e.update(st, func(p *Project) {
		for id, r := range got {
			s := findScene(p, id)
			if s == nil {
				continue
			}
			if t := strings.TrimSpace(r.title); t != "" {
				s.Title = clipRunes(t, 200)
			}
			if n := strings.TrimSpace(r.narration); n != "" {
				s.Narration = clipRunes(n, 4000)
			}
			applyParts(s, r) // multi-voice: the same speakers say the translated parts (multivoice.go)
			if len(r.lines) > 0 {
				changed := false
				k := 0
				for li := range s.Lines {
					if strings.TrimSpace(s.Lines[li].Text) == "" || k >= len(r.lines) {
						continue
					}
					if t := strings.TrimSpace(r.lines[k]); t != "" && t != s.Lines[li].Text {
						s.Lines[li].Text, changed = clipRunes(t, 600), true
					}
					k++
				}
				// The clip says the old words (the video model spoke them).
				if changed && s.Video.Status == StatusDone {
					s.Video.Stale = true
				}
			}
			if o := r.onScreen; o != nil {
				for _, f := range []struct {
					dst *string
					v   string
				}{{&s.OnScreen.Title, o.Title}, {&s.OnScreen.Value, o.Value}, {&s.OnScreen.Caption, o.Caption}} {
					if v := strings.TrimSpace(f.v); v != "" && strings.TrimSpace(*f.dst) != "" {
						*f.dst = v
					}
				}
				reconcileOnScreen(&s.OnScreen)
			}
		}
	})
}

// ---- the text model -------------------------------------------------------------------------

func (t translateJob) fromName() string {
	if n := languageName(t.from); n != "" {
		return n
	}
	return "its original language"
}

// translateHeader translates the title, logline, character names/roles and
// cover texts, stores them and returns the name glossary (source → target).
func (e *Engine) translateHeader(j *jobCtx, t translateJob) (map[string]string, error) {
	src := &t.source
	type char struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Role string `json:"role"`
	}
	type coverText struct {
		Title    string `json:"title"`
		Subtitle string `json:"subtitle"`
		Badge    string `json:"badge"`
	}
	input := struct {
		Title      string    `json:"title"`
		Logline    string    `json:"logline"`
		Characters []char    `json:"characters"`
		Cover      coverText `json:"cover"`
	}{Title: src.Title, Logline: src.Logline, Characters: []char{},
		Cover: coverText{src.Montage.CoverText.Title, src.Montage.CoverText.Subtitle, src.Montage.CoverText.Badge}}
	for _, c := range src.Characters {
		if hasLetters(c.Name) || hasLetters(c.Role) {
			input.Characters = append(input.Characters, char{c.ID, c.Name, c.Role})
		}
	}
	if !hasLetters(input.Title+input.Logline+input.Cover.Title+input.Cover.Subtitle+input.Cover.Badge) && len(input.Characters) == 0 {
		return map[string]string{}, nil // nothing to translate here
	}
	to := t.to.English
	system := strings.Join([]string{
		fmt.Sprintf("You are a professional film translator localising a finished video for %s speakers. Translate the audience-facing texts below from %s into %s.", to, t.fromName(), to),
		"Rules:",
		fmt.Sprintf("- Natural, idiomatic %s with the same meaning, tone and register. No additions, no omissions, no notes.", to),
		`- "title" is the film's title: short and catchy.`,
		fmt.Sprintf(`- Character "name": a personal name keeps its identity — use its established %s form if there is one, else a natural transliteration; never translate what a personal name means. A name that is an ordinary word or nickname (e.g. "the Fox", "Old Miller") is translated. "role" is translated.`, to),
		`- "cover" holds the thumbnail texts: keep them as short and punchy as the originals; "badge" is one or two words (upper case if the original is).`,
		"- Never change @tags, ids, numbers or URLs. Keep an empty field empty.",
		`Return ONLY a JSON object of the same shape: {"title", "logline", "characters": [{"id", "name", "role"}], "cover": {"title", "subtitle", "badge"}}.`,
	}, "\n")
	prompt, _ := json.MarshalIndent(input, "", "  ")
	var raw json.RawMessage
	if err := e.text.JSON(j.ctx, llm.Request{System: system, Prompt: string(prompt), Temperature: 0.3, Timeout: 180 * time.Second}, &raw); err != nil {
		return nil, err
	}
	reply, _ := parseJSON(raw)
	str := func(v any) string { return strings.TrimSpace(jsString(v)) }
	chars := map[string]char{}
	for k, v := range asArray(get(reply, "characters")) {
		c := char{ID: str(get(v, "id")), Name: str(get(v, "name")), Role: str(get(v, "role"))}
		if c.ID == "" && k < len(input.Characters) {
			c.ID = input.Characters[k].ID
		}
		chars[c.ID] = c
	}
	cv := get(reply, "cover")
	names := map[string]string{}
	e.update(j.st, func(p *Project) {
		if v := str(get(reply, "title")); v != "" && src.Title != "" {
			p.Title = clipRunes(v+" · "+t.to.Native, 120)
		}
		if v := str(get(reply, "logline")); v != "" && src.Logline != "" {
			p.Logline = clipRunes(v, 1000)
		}
		for i := range p.Characters {
			c := &p.Characters[i]
			tr, ok := chars[c.ID]
			if !ok {
				continue
			}
			if tr.Name != "" && strings.TrimSpace(c.Name) != "" {
				if tr.Name != c.Name {
					names[c.Name] = tr.Name
				}
				c.Name = clipRunes(tr.Name, 80)
			}
			if tr.Role != "" && strings.TrimSpace(c.Role) != "" {
				c.Role = clipRunes(tr.Role, 200)
			}
		}
		ct := &p.Montage.CoverText
		for _, f := range []struct {
			dst  *string
			key  string
			size int
		}{{&ct.Title, "title", 120}, {&ct.Subtitle, "subtitle", 120}, {&ct.Badge, "badge", 40}} {
			if v := str(get(cv, f.key)); v != "" && strings.TrimSpace(*f.dst) != "" {
				*f.dst = clipRunes(v, f.size)
			}
		}
	})
	return names, nil
}

// translateLines translates the scenes at idx (indexes into the source).
// With prev set it is the second attempt for lines that came back missing or
// too long: a stricter prompt that also shows the previous try.
func (e *Engine) translateLines(ctx context.Context, t translateJob, names map[string]string, idx []int, prev map[string]lineResult) (map[string]lineResult, error) {
	src := &t.source
	to := t.to.English
	_, unit := lineBudget(1, t.to.Code)
	type spoken struct {
		Speaker string `json:"speaker"`
		Text    string `json:"text"`
	}
	type screen struct {
		Title   string `json:"title,omitempty"`
		Value   string `json:"value,omitempty"`
		Caption string `json:"caption,omitempty"`
	}
	type line struct {
		ID        string   `json:"id"`
		Title     string   `json:"title,omitempty"`
		Narration string   `json:"narration,omitempty"`
		Lines     []spoken `json:"lines,omitempty"`
		Parts     []spoken `json:"parts,omitempty"`
		OnScreen  *screen  `json:"onScreen,omitempty"`
		Original  string   `json:"original,omitempty"`
		Previous  string   `json:"previous,omitempty"`
		Seconds   float64  `json:"seconds"`
		Words     int      `json:"words,omitempty"`
		Chars     int      `json:"characters,omitempty"`
	}
	lines := make([]line, 0, len(idx))
	for _, i := range idx {
		s := &src.Scenes[i]
		secs := sourceSeconds(s, t.from)
		n, _ := lineBudget(secs, t.to.Code)
		l := line{ID: s.ID, Seconds: math.Round(secs*10) / 10}
		if unit == "characters" {
			l.Chars = n
		} else {
			l.Words = n
		}
		if parts := cleanParts(s.Parts); len(parts) > 0 {
			// Multi-voice: the parts are translated one by one, speakers kept
			// (on a retry too); "narration" is their words joined.
			for _, sp := range parts {
				l.Parts = append(l.Parts, spoken{Speaker: SpeakerLabel(src, sp.Speaker), Text: sp.Text})
			}
			if prev == nil {
				l.Title = s.Title
			} else {
				l.Previous = prev[s.ID].narration
			}
		} else if prev == nil {
			l.Title, l.Narration = s.Title, s.Narration
			for _, sl := range cleanLines(s.Lines) {
				l.Lines = append(l.Lines, spoken{Speaker: speakerName(src, sl.Speaker), Text: sl.Text})
			}
			if !s.OnScreen.Empty() {
				l.OnScreen = &screen{Title: s.OnScreen.Title, Value: s.OnScreen.Value, Caption: s.OnScreen.Caption}
			}
		} else {
			l.Original, l.Previous = s.Narration, prev[s.ID].narration
		}
		lines = append(lines, l)
	}
	var glossary []string
	for from, to := range names {
		glossary = append(glossary, fmt.Sprintf("%s → %s", from, to))
	}
	nameRule := ""
	if len(glossary) > 0 {
		nameRule = "- Names — use exactly these forms: " + strings.Join(glossary, "; ") + "."
	}
	var system string
	if prev == nil {
		system = strings.Join(dropEmpty(
			fmt.Sprintf(`You are a professional dubbing translator. Translate each scene's "title" and "narration" (the voice-over line) from %s into %s for the dubbed version of a finished video.`, t.fromName(), to),
			fmt.Sprintf(`- The pictures are final: every "narration" must be speakable in the scene's original time. "seconds" is how long the original line takes; "%s" is the length to aim for in %s (±10%%). Say the same thing naturally within it: rephrase shorter when a literal translation runs long, expand a little when it is much shorter. Keep every name and fact and the tone.`, unit, to),
			nameRule,
			toneHint(src.Input.Tone),
			`- "lines" (when a scene has them) are said ON CAMERA by the characters: translate every "text" — the same number of lines, `+
				`in order, "speaker" unchanged — so that all of them together fit the scene's "seconds" like a narration would.`,
			`- "parts" (when a scene has them instead of "narration") are its voice-over split between speakers: translate every "text" — the same `+
				`number of parts, in order, "speaker" unchanged — so that all of them together fit the scene's "seconds" like a narration would.`,
			`- "onScreen" (when a scene has it) is short text animated over the picture: translate "title" and "caption" as short and punchy as the originals; `+
				`in "value" keep the digits and translate only the words or units around them.`,
			"- Never change @tags or ids. Keep an empty field empty.",
			`Return ONLY a JSON array with one object per scene, in the same order: [{"id", "title", "narration", "lines": [{"speaker", "text"}], "parts": [{"speaker", "text"}], "onScreen": {"title", "value", "caption"}}] `+
				`("lines", "parts" and "onScreen" only for scenes that have them).`,
		), "\n")
	} else {
		system = strings.Join(dropEmpty(
			fmt.Sprintf(`You are a professional dubbing translator. The %s voice-over lines below are too long for their scenes, or missing ("previous" is the last try). For each scene translate the "original" line from %s into %s again so that it fits: at most "%s" %s %s — a hard limit — for a line spoken in "seconds" seconds. Keep the meaning, every name and the tone; stay natural.`, to, t.fromName(), to, unit, to, unit),
			nameRule,
			`- A scene with "parts" instead of "original" is a voice-over split between speakers: translate every part's "text" again — the same `+
				`number of parts, in order, "speaker" unchanged — all of them together within the limit; "previous" is the last try joined.`,
			"- Never change @tags or ids.",
			`Return ONLY a JSON array with one object per scene, in the same order: [{"id", "narration"}] ([{"id", "parts": [{"speaker", "text"}]}] for a scene with parts).`,
		), "\n")
	}
	prompt, _ := json.MarshalIndent(lines, "", "  ")
	var raw json.RawMessage
	if err := e.text.JSON(ctx, llm.Request{System: system, Prompt: string(prompt), Temperature: 0.3, Timeout: 180 * time.Second}, &raw); err != nil {
		return nil, err
	}
	reply, _ := parseJSON(raw)
	out := map[string]lineResult{}
	for k, v := range asArray(reply) {
		id := strings.TrimSpace(jsString(get(v, "id")))
		known := false
		for _, l := range lines {
			known = known || l.ID == id
		}
		if !known { // no or unknown id: by position
			if k >= len(lines) {
				continue
			}
			id = lines[k].ID
		}
		r := lineResult{title: strings.TrimSpace(jsString(get(v, "title"))), narration: strings.TrimSpace(jsString(get(v, "narration")))}
		for _, l := range asArray(get(v, "lines")) {
			r.lines = append(r.lines, strings.TrimSpace(jsString(get(l, "text"))))
		}
		for _, l := range asArray(get(v, "parts")) {
			r.parts = append(r.parts, strings.TrimSpace(jsString(get(l, "text"))))
		}
		if s := findScene(src, id); s != nil && hasParts(s) {
			r.narration = "" // a scene in parts says only its parts
			if len(r.parts) == len(cleanParts(s.Parts)) && !slices.Contains(r.parts, "") {
				r.narration = strings.Join(r.parts, " ")
			} else {
				r.parts = nil
			}
		}
		if o := get(v, "onScreen"); o != nil {
			str := func(k string) string { return strings.TrimSpace(jsString(get(o, k))) }
			r.onScreen = &OnScreen{Title: str("title"), Value: str("value"), Caption: str("caption")}
		}
		out[id] = r
	}
	return out, nil
}

// ---- speaking time -----------------------------------------------------------------------------

// charRates: characters spoken per second in languages written without
// spaces (a word budget means little there); everything else uses the
// planner's words per second.
var charRates = map[string]float64{"zh": 4.5, "yue": 4.5, "ja": 5.5, "th": 11, "lo": 11, "my": 9}

// lineBudget is how much text in lang fits into seconds of speech: words, or
// characters for languages written without spaces.
func lineBudget(seconds float64, lang string) (n int, unit string) {
	if cps, ok := charRates[lang]; ok {
		return max(1, int(math.Round(seconds*cps))), "characters"
	}
	return max(1, int(math.Round(seconds*wps))), "words"
}

// spokenSeconds estimates how long text takes to say in lang ("" = guess
// from the writing system).
func spokenSeconds(text, lang string) float64 {
	if lang == "" {
		lang = unspacedLanguage(text)
	}
	if cps, ok := charRates[lang]; ok {
		n := 0
		for _, r := range text {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				n++
			}
		}
		return float64(n) / cps
	}
	return float64(countWords(text)) / wps
}

// sourceSeconds is how long the source line of a scene takes: its voice
// when there is one, its slice of the recording, else the estimate.
func sourceSeconds(s *Scene, lang string) float64 {
	switch {
	case s.Audio.Status == StatusDone && !s.Audio.Stale && s.Audio.Duration > 0:
		return s.Audio.Duration
	case s.AudioSource == AudioUpload && s.VoiceEnd > s.VoiceStart:
		return s.VoiceEnd - s.VoiceStart
	case strings.TrimSpace(s.Narration) == "" && DialogueSeconds(s.Lines) > 0:
		return DialogueSeconds(s.Lines) * talkWPS / wps // the lines' words at the narration rate (lineBudget counts words at wps)
	}
	return spokenSeconds(s.Narration, lang)
}

// unspacedLanguage guesses a language written without spaces from the
// characters of text ("" when most letters are not of such a script).
func unspacedLanguage(text string) string {
	var letters, han, kana, thai, lao, myanmar int
	for _, r := range text {
		if !unicode.IsLetter(r) {
			continue
		}
		letters++
		switch {
		case unicode.In(r, unicode.Hiragana, unicode.Katakana):
			kana++
		case unicode.Is(unicode.Han, r):
			han++
		case unicode.Is(unicode.Thai, r):
			thai++
		case unicode.Is(unicode.Lao, r):
			lao++
		case unicode.Is(unicode.Myanmar, r):
			myanmar++
		}
	}
	switch {
	case letters == 0:
		return ""
	case (kana+han)*2 > letters && kana > 0:
		return "ja"
	case han*2 > letters:
		return "zh"
	case thai*2 > letters:
		return "th"
	case lao*2 > letters:
		return "lo"
	case myanmar*2 > letters:
		return "my"
	}
	return ""
}

func hasLetters(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// ukPlural picks the Ukrainian plural form for n (1 сцена, 2 сцени, 5 сцен).
func ukPlural(n int, one, few, many string) string {
	n %= 100
	if n < 0 {
		n = -n
	}
	if n >= 11 && n <= 14 {
		return many
	}
	switch n % 10 {
	case 1:
		return one
	case 2, 3, 4:
		return few
	}
	return many
}
