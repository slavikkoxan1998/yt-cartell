package studio

// Editing: port of the PATCH /projects/:id merge in routes/studio.ts.
//
// The UI mutates the live project inside Edit; afterwards the engine restores
// what it owns (ids, assets, job, generated outputs), validates the user fields
// and flags dependent assets as outdated when their source text changed.

import (
	"math"
	"strings"

	"ytcartell/internal/media"
)

// Edit mutates a project under its lock, then applies the studio merge rules:
//
//   - generated assets, the job, montage outputs, music file, ids and
//     timestamps are engine-owned: whatever fn does to them is undone for
//     existing items (new items get empty assets, ids and unique @tags);
//   - edited narration/speaker marks the scene voice stale, edited image prompt
//     the frame, edited motion prompt the clip (kept, but flagged);
//   - deleted characters/locations are removed from every scene (speaker falls
//     back to "narrator");
//   - text is clipped to the web limits, invalid choices keep the old value,
//     the furthest step never goes back.
//
// fn receives the live project: apply the user's change to it (find items by
// id) instead of assigning a stale snapshot, and do not keep references to it.
// fn runs under the project lock, so it must not call engine methods. The save
// is debounced; subscribers are notified.
func (e *Engine) Edit(id string, fn func(p *Project)) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	var dropped []string
	var list bool
	err := func() error {
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.deleted.Load() {
			return ErrNotFound
		}
		before := cloneProject(st.p)
		// cloneProject shares the materials' array: keep an own copy to restore.
		before.Input.Materials = append([]Material(nil), st.p.Input.Materials...)
		fn(st.p)
		reconcile(&before, st.p)
		dropped = orphans(assetPaths(&before), assetPaths(st.p))
		list = e.commitLocked(st)
		return nil
	}()
	if err != nil {
		return err
	}
	e.discardFiles(st, dropped)
	e.emit(st, list)
	return nil
}

func oneOf(v string, allowed ...string) bool {
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

// reconcile applies the merge rules to p after an edit; before is the deep
// copy taken just before the edit.
func reconcile(before, p *Project) {
	// Engine-owned fields.
	p.ID, p.CreatedAt, p.UpdatedAt = before.ID, before.CreatedAt, before.UpdatedAt
	p.Job = before.Job
	p.Montage.Output = before.Montage.Output
	p.Montage.Cover = before.Montage.Cover
	p.Montage.CoverFinal = before.Montage.CoverFinal // rendered by ApplyCoverText
	p.Montage.SRTPath = before.Montage.SRTPath
	p.Montage.MusicPath = before.Montage.MusicPath

	if p.Title != before.Title {
		p.Title = clipRunes(p.Title, 120)
		if p.Title == "" {
			p.Title = "Без назви"
		}
	}
	p.Logline = clipRunes(p.Logline, 1000)
	p.StyleGuide = clipRunes(p.StyleGuide, 2000)

	in, bi := &p.Input, before.Input
	in.Text = clipRunes(in.Text, 200_000)
	if in.Mode != ModeStory && in.Mode != ModeScript && in.Mode != ModeVoice {
		in.Mode = bi.Mode
	}
	// The recording is engine-owned (SetVoiceFile); a film cut to it keeps
	// its voice-over on.
	in.VoiceFile, in.VoiceFileName = bi.VoiceFile, bi.VoiceFileName
	in.TranscriptFile = bi.TranscriptFile // engine-owned too (the transcript API)
	in.Materials = bi.Materials           // AddMaterial / UpdateMaterial / RemoveMaterial
	reconcileModes(in, &bi)
	if in.Mode == ModeVoice {
		in.Voiceover = true
	}
	if in.TargetSeconds != bi.TargetSeconds {
		t := in.TargetSeconds
		if t == 0 {
			t = 60
		}
		in.TargetSeconds = max(8, min(3600, t))
	}
	if !oneOf(in.Aspect, "16:9", "9:16") {
		in.Aspect = bi.Aspect
	}
	in.Style = clipRunes(in.Style, 60)
	in.CustomStyle = clipRunes(in.CustomStyle, 1000)
	in.Genre = clipRunes(in.Genre, 40)
	if !oneOf(in.Tempo, "slow", "balanced", "fast", TempoRapid) {
		in.Tempo = bi.Tempo
	}
	if !knownCamera(in.Camera) { // the Cameras catalogue
		in.Camera = bi.Camera
	}
	if !oneOf(in.ImageModel, "NARWHAL", "GEM_PIX_2", "GEM_PIX") {
		in.ImageModel = bi.ImageModel
	}
	in.NarratorVoiceID = clipRunes(in.NarratorVoiceID, 80)
	if _, ok := NarratorStyleByID(in.NarratorStyle); !ok {
		in.NarratorStyle = ""
	}
	if len(in.ExtraNarrators) > MaxExtraNarrators { // «Диктор 2/3» (multivoice.go)
		in.ExtraNarrators = in.ExtraNarrators[:MaxExtraNarrators]
	}
	for i := range in.ExtraNarrators {
		in.ExtraNarrators[i] = clipRunes(strings.TrimSpace(in.ExtraNarrators[i]), 80)
	}
	in.Notes = clipRunes(in.Notes, 2000)
	in.Language = clipRunes(strings.TrimSpace(in.Language), 16)
	in.Tone = clipRunes(strings.TrimSpace(in.Tone), 40)

	m, bm := &p.Montage, before.Montage
	if _, ok := media.TransitionByID(m.Transition); !ok {
		m.Transition = bm.Transition
	}
	if d := m.TransitionDuration; d != 0 { // 0 = the default
		if math.IsNaN(d) || math.IsInf(d, 0) || d < 0 {
			m.TransitionDuration = bm.TransitionDuration
		} else {
			m.TransitionDuration = media.ClampTransitionDuration(d)
		}
	}
	if _, ok := media.FilterByID(m.Filter); m.Filter != "" && !ok {
		m.Filter = bm.Filter
	}
	if _, ok := media.EffectByID(m.Effect); m.Effect != "" && !ok {
		m.Effect = bm.Effect
	}
	ss, bs := &m.SubtitleStyle, bm.SubtitleStyle
	if _, ok := media.SubtitlePresetByID(ss.Preset); ss.Preset != "" && !ok {
		ss.Preset = bs.Preset
	}
	if ss.Size != 0 {
		ss.Size = max(16, min(160, ss.Size))
	}
	if !oneOf(ss.Position, "", "bottom", "middle", "top") {
		ss.Position = bs.Position
	}
	if ss.Color = strings.TrimSpace(ss.Color); ss.Color != "" && !hexColor(ss.Color) {
		ss.Color = bs.Color
	}
	if !oneOf(m.Subtitles, "none", "srt", "burn") {
		m.Subtitles = bm.Subtitles
	}
	m.MusicName = clipRunes(m.MusicName, 200)
	if m.MusicPath == "" {
		m.MusicName = ""
	}
	m.MusicVolume = clamp01(m.MusicVolume)
	m.AmbientVolume = clamp01(m.AmbientVolume)
	m.CoverPrompt = clipRunes(m.CoverPrompt, 2000)
	// Cover text: kept as written (an empty Stroke = the template's, "none" =
	// no outline), only clipped.
	ct := &m.CoverText
	ct.Title, ct.Subtitle, ct.Badge = clipRunes(ct.Title, 120), clipRunes(ct.Subtitle, 120), clipRunes(ct.Badge, 40)
	for _, f := range []*string{&ct.Template, &ct.Font, &ct.Color, &ct.Stroke, &ct.Accent, &ct.Position} {
		*f = clipRunes(*f, 40)
	}

	step := p.Step
	if step < 1 {
		step = 1
	}
	p.Step = max(before.Step, min(5, step))

	mergeCast(before, p)
	mergeScenes(before, p)
	reconcileCode(before, p)    // «Анімація кодом»: specs, grammar choices (codeanim.go)
	if p.Input.MultiVoiceOn() { // a speaker got another voice: their voiced lines are outdated
		staleChangedVoices(before, p)
	}
}

// mergeCast keeps assets and tags of existing characters/locations, gives new
// ones fresh ids, empty images and unique @tags (mergeCharacters/mergeLocations).
func mergeCast(before, p *Project) {
	oldC := make(map[string]Character, len(before.Characters))
	for _, c := range before.Characters {
		oldC[c.ID] = c
	}
	seen := map[string]bool{}
	if p.Characters == nil {
		p.Characters = []Character{}
	}
	for i := range p.Characters {
		c := &p.Characters[i]
		if old, ok := oldC[c.ID]; ok && !seen[c.ID] {
			c.Image, c.Tag, c.MaterialID = old.Image, old.Tag, old.MaterialID
		} else {
			c.ID = clientID(c.ID, seen)
			c.Image, c.Tag, c.MaterialID = EmptyAsset(), "", "" // only the plan binds materials
		}
		seen[c.ID] = true
		c.Name = clipRunes(c.Name, 80)
		c.Role = clipRunes(c.Role, 200)
		c.Description = clipRunes(c.Description, 2000)
		c.VoiceID = clipRunes(c.VoiceID, 80)
		c.Gender = clipRunes(c.Gender, 20)
		if c.Kind != KindObject {
			c.Kind = ""
		}
		c.Voice = clipRunes(c.Voice, 400)
	}
	p.NarratorVoice = clipRunes(p.NarratorVoice, 400)

	oldL := make(map[string]Location, len(before.Locations))
	for _, l := range before.Locations {
		oldL[l.ID] = l
	}
	seen = map[string]bool{}
	if p.Locations == nil {
		p.Locations = []Location{}
	}
	for i := range p.Locations {
		l := &p.Locations[i]
		if old, ok := oldL[l.ID]; ok && !seen[l.ID] {
			l.Image, l.Tag, l.MaterialID = old.Image, old.Tag, old.MaterialID
		} else {
			l.ID = clientID(l.ID, seen)
			l.Image, l.Tag, l.MaterialID = EmptyAsset(), "", ""
		}
		seen[l.ID] = true
		l.Name = clipRunes(l.Name, 80)
		l.Description = clipRunes(l.Description, 2000)
	}

	// Tags for new characters, unique across the project.
	var fresh []int
	var names, taken []string
	for i, c := range p.Characters {
		if c.Tag == "" {
			fresh = append(fresh, i)
			names = append(names, firstNonBlank(c.Name, "character"))
		} else {
			taken = append(taken, c.Tag)
		}
	}
	for _, l := range p.Locations {
		if l.Tag != "" {
			taken = append(taken, l.Tag)
		}
	}
	for k, t := range uniqueTags(names, taken) {
		p.Characters[fresh[k]].Tag = t
	}

	fresh, names, taken = nil, nil, nil
	for i, l := range p.Locations {
		if l.Tag == "" {
			fresh = append(fresh, i)
			names = append(names, firstNonBlank(l.Name, "location"))
		} else {
			taken = append(taken, l.Tag)
		}
	}
	for _, c := range p.Characters {
		taken = append(taken, c.Tag)
	}
	for k, t := range uniqueTags(names, taken) {
		p.Locations[fresh[k]].Tag = t
	}
}

// mergeScenes keeps assets of existing scenes, drops references to deleted
// characters/locations and flags assets whose source text changed.
func mergeScenes(before, p *Project) {
	oldS := make(map[string]Scene, len(before.Scenes))
	for _, s := range before.Scenes {
		oldS[s.ID] = s
	}
	cids := map[string]bool{}
	for _, c := range p.Characters {
		cids[c.ID] = true
	}
	lids := map[string]bool{}
	for _, l := range p.Locations {
		lids[l.ID] = true
	}
	seen := map[string]bool{}
	if p.Scenes == nil {
		p.Scenes = []Scene{}
	}
	for i := range p.Scenes {
		s := &p.Scenes[i]
		old, existed := oldS[s.ID]
		existed = existed && !seen[s.ID]
		if existed {
			s.Frame, s.Board, s.Video, s.Audio = old.Frame, old.Board, old.Video, old.Audio
			keepSlice(s, &old)
		} else {
			s.ID = clientID(s.ID, seen)
			s.Frame, s.Board, s.Video, s.Audio = EmptyAsset(), EmptyAsset(), EmptyAsset(), EmptyAsset()
			// An inserted scene has no slice of the recording: TTS voices it.
			s.AudioSource, s.VoiceStart, s.VoiceEnd = "", 0, 0
		}
		seen[s.ID] = true

		// Timeline controls (montage editor): cuts in seconds, catalog ids.
		s.TrimIn, s.TrimOut = trimSeconds(s.TrimIn), trimSeconds(s.TrimOut)
		// Voice line placement (montage editor) and the dynamic clip length.
		s.VoiceTrimIn, s.VoiceTrimOut = trimSeconds(s.VoiceTrimIn), trimSeconds(s.VoiceTrimOut)
		if v := s.VoiceShift; math.IsNaN(v) || math.IsInf(v, 0) {
			s.VoiceShift = 0
		} else {
			s.VoiceShift = math.Max(-60, math.Min(60, v))
		}
		if !oneOfInt(s.ClipLen, 0, 4, 6, 8, 10) {
			s.ClipLen = old.ClipLen
		}
		if _, ok := media.TransitionByID(s.Transition); s.Transition != "" && !ok {
			s.Transition = old.Transition
		}
		if _, ok := media.EffectByID(s.Effect); s.Effect != "" && !ok {
			s.Effect = old.Effect
		}

		s.Title = clipRunes(s.Title, 200)
		s.Narration = clipRunes(s.Narration, 4000)
		s.Speaker = clipRunes(s.Speaker, 40)
		if s.Speaker == "" {
			s.Speaker = "narrator"
		}
		s.Shot = clipRunes(s.Shot, 120)
		s.ImagePrompt = clipRunes(s.ImagePrompt, 4000)
		s.MotionPrompt = clipRunes(s.MotionPrompt, 2000)

		// Drop references to deleted characters/locations.
		ids := make([]string, 0, len(s.CharacterIDs))
		for _, id := range s.CharacterIDs {
			if id = clipRunes(id, 24); cids[id] {
				ids = append(ids, id)
			}
		}
		s.CharacterIDs = ids
		if s.LocationID = clipRunes(s.LocationID, 24); s.LocationID != "" && !lids[s.LocationID] {
			s.LocationID = ""
		}
		if s.Speaker != "narrator" && !cids[s.Speaker] {
			s.Speaker = "narrator"
		}
		reconcileLines(s, cids)
		partsChanged := reconcileParts(s, &old, existed, cids) // multi-voice parts (multivoice.go)
		reconcileOnScreen(&s.OnScreen)

		if !existed {
			continue
		}
		// Spoken lines are in the clip (the video model says them).
		if !sameLines(old.Lines, s.Lines) && s.Video.Status == StatusDone {
			s.Video.Stale = true
		}
		// Edited text marks dependent assets as outdated (kept, but flagged).
		// A slice of the user's recording follows its time range and source,
		// not the text (the narration is its subtitle).
		voiceChanged := old.Narration != s.Narration || old.Speaker != s.Speaker || partsChanged
		if s.AudioSource == AudioUpload || old.AudioSource == AudioUpload {
			voiceChanged = old.AudioSource != s.AudioSource || old.VoiceStart != s.VoiceStart || old.VoiceEnd != s.VoiceEnd ||
				(s.AudioSource == "" && voiceChanged)
		}
		if voiceChanged && s.Audio.Status == StatusDone {
			s.Audio.Stale = true
		}
		if old.ImagePrompt != s.ImagePrompt && s.Frame.Status == StatusDone {
			s.Frame.Stale = true
		}
		if old.ImagePrompt != s.ImagePrompt && s.Board.Status == StatusDone {
			s.Board.Stale = true
		}
		if old.MotionPrompt != s.MotionPrompt && s.Video.Status == StatusDone {
			s.Video.Stale = true
		}
	}
}

// reconcileModes validates the sound/content modes of an edited input and
// keeps Voiceover in step with the sound mode: a picked AudioMode decides; an
// older control that only flips Voiceover clears AudioMode (Voiceover then
// decides, like in projects made before the modes).
func reconcileModes(in *Input, bi *Input) {
	if !oneOf(in.AudioMode, "", AudioVoiceover, AudioInVideo, AudioSilent) {
		in.AudioMode = bi.AudioMode
	}
	if !oneOf(in.Content, "", ContentCharacters, ContentNoPeople, ContentMotion, ContentMixed) {
		in.Content = bi.Content
	}
	if !oneOf(in.Overlays, "", "on", "off") {
		in.Overlays = bi.Overlays
	}
	if in.AudioMode == bi.AudioMode && in.Voiceover != bi.Voiceover && in.AudioMode != "" {
		in.AudioMode = ""
	}
	if in.Mode != ModeVoice && in.AudioMode != "" {
		in.Voiceover = in.AudioMode == AudioVoiceover
	}
}

// reconcileLines clips a scene's spoken lines; a line of a removed
// character goes to the off-screen voice.
func reconcileLines(s *Scene, cids map[string]bool) {
	if len(s.Lines) == 0 {
		s.Lines = nil
		return
	}
	if len(s.Lines) > maxLines {
		s.Lines = s.Lines[:maxLines]
	}
	for i := range s.Lines {
		l := &s.Lines[i]
		l.Speaker = clipRunes(strings.TrimSpace(l.Speaker), 40)
		if l.Speaker != narratorID && !cids[l.Speaker] {
			l.Speaker = narratorID
		}
		l.Text = clipRunes(l.Text, 600)
		l.Delivery = clipRunes(l.Delivery, 120)
	}
}

// reconcileOnScreen clips the on-screen text and checks its choices.
func reconcileOnScreen(o *OnScreen) {
	o.Title, o.Value, o.Caption = clipRunes(o.Title, 90), clipRunes(o.Value, 32), clipRunes(o.Caption, 140)
	if !oneOf(o.Anim, "", OverlayFade, OverlaySlide, OverlayPop, OverlayCount, OverlayType) {
		o.Anim = ""
	}
	if !oneOf(o.Position, "", "center", "lower", "top") {
		o.Position = ""
	}
}

// sameLines compares two scenes' lines (what the video model is told).
func sameLines(a, b []Line) bool {
	a, b = cleanLines(a), cleanLines(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Speaker != b[i].Speaker || strings.TrimSpace(a[i].Text) != strings.TrimSpace(b[i].Text) ||
			strings.TrimSpace(a[i].Delivery) != strings.TrimSpace(b[i].Delivery) {
			return false
		}
	}
	return true
}

// keepSlice validates an edited scene's audio source and time range: an
// unknown source or a broken range falls back to the previous values (a
// scene switched to "upload" without a valid range stays on TTS).
func keepSlice(s, old *Scene) {
	if s.AudioSource != "" && s.AudioSource != AudioUpload {
		s.AudioSource = old.AudioSource
	}
	if !finiteNonNeg(s.VoiceStart) || !finiteNonNeg(s.VoiceEnd) || s.VoiceStart > 24*3600 || s.VoiceEnd > 24*3600 {
		s.VoiceStart, s.VoiceEnd = old.VoiceStart, old.VoiceEnd
	}
	if s.AudioSource == AudioUpload && s.VoiceEnd <= s.VoiceStart {
		if old.AudioSource == AudioUpload && old.VoiceEnd > old.VoiceStart {
			s.VoiceStart, s.VoiceEnd = old.VoiceStart, old.VoiceEnd
		} else {
			s.AudioSource = ""
		}
	}
}

func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// trimSeconds keeps a clip cut finite, non-negative and sane (the montage
// still leaves every scene at least half a second).
func trimSeconds(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}
	return math.Min(v, 600)
}

// hexColor reports whether s is a #RRGGBB colour.
func hexColor(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, r := range s[1:] {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}

func oneOfInt(v int, set ...int) bool {
	for _, x := range set {
		if v == x {
			return true
		}
	}
	return false
}
