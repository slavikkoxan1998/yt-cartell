package studio

// Steps 3 and 4 (port of production.ts):
//
//	refs   — one reference image per character (full body, neutral background)
//	         and per location (empty establishing shot)
//	frames — the first frame of every scene, generated WITH the reference images
//	         of the characters/location in it (bound by @tag → consistency);
//	         in storyboard mode a 3×3 sheet per scene instead (storyboard.go)
//	videos — each frame animated by Veo (G-Labs or Google); Omni Flash renders
//	         storyboards / chained shots in components mode (storyboard.go)
//	voices — each scene's line voiced by its speaker's voice (ElevenLabs etc.),
//	         or cut out of the user's own recording (ModeVoice, ownvoice.go)

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"ytcartell/internal/gen"
	"ytcartell/internal/stock"
)

const (
	noText         = "No text, no captions, no labels, no watermark, no speech bubbles."
	maxImportImage = 40 << 20
)

// The image() / video() provider dispatch lives in providers.go.

// ---- prompts ------------------------------------------------------------------------------

func characterPrompt(p *Project, c *Character) string {
	if c.IsObject() { // an object / motif: an object shot, never a person
		return strings.Join([]string{
			fmt.Sprintf("Object reference image of %s: %s", c.Name, c.Description),
			"The single object alone, centred, the entire object visible, three-quarter view,",
			"plain light grey seamless background, soft even studio lighting. No people, no hands.",
			"Art style: " + p.StyleGuide,
			noText,
		}, "\n")
	}
	return strings.Join([]string{
		fmt.Sprintf("Character reference image of %s: %s", c.Name, c.Description),
		"Full body, standing, front three-quarter view, neutral relaxed pose, entire figure visible head to toe,",
		"plain light grey seamless background, soft even studio lighting. Single character only.",
		"Art style: " + p.StyleGuide,
		noText,
	}, "\n")
}

func locationPrompt(p *Project, l *Location) string {
	return strings.Join([]string{
		fmt.Sprintf("Establishing shot of %s: %s", l.Name, l.Description),
		"Empty environment, no people, no characters.",
		"Art style: " + p.StyleGuide,
		noText,
	}, "\n")
}

func sceneCast(p *Project, s *Scene) []*Character {
	var out []*Character
	for _, id := range s.CharacterIDs {
		if c := findCharacter(p, id); c != nil {
			out = append(out, c)
		}
	}
	return out
}

func framePrompt(p *Project, s *Scene) string {
	lines := []string{s.ImagePrompt}
	if s.ImagePrompt == "" {
		lines[0] = s.Title
	}
	lines = append(lines, castLinesFor(p, sceneCast(p, s), "")...)
	if l := findLocation(p, s.LocationID); l != nil {
		if textLooks(p, l.Image) {
			lines = append(lines, fmt.Sprintf("Location: %s — %s", l.Name, strings.TrimSpace(l.Description)))
		} else {
			lines = append(lines, fmt.Sprintf("Location: @%s (%s) — match the reference image of this place.", l.Tag, l.Name))
		}
	}
	lines = append(lines, contentLine(p, s))
	lines = append(lines, "Art style: "+p.StyleGuide, frameTextRule(p.Input))
	out := lines[:0]
	for _, l := range lines {
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

// castLinesFor is castLines for a project: in «Без референсів» films a cast
// member without a picture is described in words ("@taras is Тарас: A
// 7-year-old boy in a red jacket…") — the same words in every prompt keep
// the look as steady as text can; members with a picture (the user's own
// material) keep the reference line.
func castLinesFor(p *Project, cast []*Character, every string) []string {
	var refs []*Character
	var people, things []string
	for _, c := range cast {
		if !textLooks(p, c.Image) {
			refs = append(refs, c)
			continue
		}
		desc := strings.TrimSpace(c.Description)
		if c.IsObject() {
			things = append(things, fmt.Sprintf("@%s is %s: %s (exactly the same object%s)", c.Tag, c.Name, desc, every))
		} else {
			people = append(people, fmt.Sprintf("@%s is %s: %s (exactly the same face, hair, body and outfit%s)", c.Tag, c.Name, desc, every))
		}
	}
	out := castLines(refs, every)
	if len(people) > 0 {
		out = append(out, "Characters (no reference image — keep this look): "+strings.Join(people, "; ")+".")
	}
	if len(things) > 0 {
		out = append(out, "Objects (no reference image — keep this look): "+strings.Join(things, "; ")+".")
	}
	return out
}

// castLines names the references of a shot: "Characters: @taras is Тарас —
// identical face…" and "Objects: @lamp is Лампа — identical shape…"; every
// is appended to the likeness ("in every panel" for storyboards).
func castLines(cast []*Character, every string) []string {
	var people, things []string
	for _, c := range cast {
		if c.IsObject() {
			things = append(things, fmt.Sprintf("@%s is %s — identical shape, materials and colours to the @%s reference image%s", c.Tag, c.Name, c.Tag, every))
		} else {
			people = append(people, fmt.Sprintf("@%s is %s — identical face, hair, body and outfit to the @%s reference image%s", c.Tag, c.Name, c.Tag, every))
		}
	}
	var out []string
	if len(people) > 0 {
		out = append(out, "Characters: "+strings.Join(people, "; ")+".")
	}
	if len(things) > 0 {
		out = append(out, "Objects: "+strings.Join(things, "; ")+".")
	}
	return out
}

// contentLine is what the content mode adds to a still: nobody in frame,
// a clean motion-design frame with room for the studio's text.
func contentLine(p *Project, s *Scene) string {
	switch p.Input.ContentMode() {
	case ContentNoPeople:
		return "No people anywhere in the frame: no humans, faces, hands or silhouettes."
	case ContentMotion:
		line := "A clean motion-graphics design frame, no realistic people."
		if p.Input.TextOverlays() && !s.OnScreen.Empty() {
			line += " Leave calm empty space for the on-screen text the studio adds later."
		}
		return line
	case ContentMixed:
		if len(sceneCast(p, s)) == 0 || allObjects(sceneCast(p, s)) {
			return "No people in this shot."
		}
	}
	return ""
}

func allObjects(cast []*Character) bool {
	for _, c := range cast {
		if !c.IsObject() {
			return false
		}
	}
	return true
}

func motionPrompt(s *Scene) string {
	m := s.MotionPrompt
	if m == "" {
		m = "Subtle cinematic camera push-in, natural movement."
	}
	return strings.Join([]string{
		m,
		"Keep every character exactly as in the first frame (same face, hair and outfit).",
		"No dialogue, no speech, no on-screen text, no scene cuts.",
	}, " ")
}

// clipPrompt is the prompt of a clip animated from its frame: the motion,
// and — by the project's modes — the spoken lines first (dialogue first
// gives the fewest burnt-in subtitles), what must stay as in the frame and
// what the clip sounds like. A classic film keeps motionPrompt as it was.
func clipPrompt(in *shotInputs) string {
	if in.sound != AudioInVideo && in.content == ContentCharacters {
		return motionPrompt(&in.scene)
	}
	var parts []string
	if in.talk != "" {
		parts = append(parts, in.talk)
	}
	parts = append(parts, firstNonBlank(strings.TrimSpace(in.scene.MotionPrompt), defaultMotion), keepLine(in), soundLine(in))
	return strings.Join(parts, "\n")
}

// keepLine is what must stay as in the first frame.
func keepLine(in *shotInputs) string {
	switch in.content {
	case ContentNoPeople:
		return "Keep every object and place exactly as in the first frame. No people appear at any moment."
	case ContentMotion:
		return "Smooth motion-design animation of the frame's own elements: keep its colours, shapes and layout. No people, no live action."
	}
	return "Keep every character exactly as in the first frame (same face, hair and outfit)."
}

// soundLine tells the video model what the clip sounds like.
func soundLine(in *shotInputs) string {
	switch {
	case in.sound == AudioInVideo && in.talk != "":
		return "Sound: only the lines above are spoken — no narrator, no other voices; natural ambient sound and effects of the scene; " +
			"no background music. No subtitles, no captions, no on-screen text. No scene cuts."
	case in.sound == AudioInVideo:
		return "Sound: no speech — natural ambient sound and effects of the scene only; no background music. " +
			"No subtitles, no on-screen text. No scene cuts."
	}
	return "No dialogue, no speech, no on-screen text, no scene cuts."
}

// dialogueBlock is the spoken lines of a scene for the video model, Veo's
// way: who (a look cue and the voice) says — how — "what", then the
// no-subtitles constraint right after the lines.
func dialogueBlock(p *Project, s *Scene) string {
	lines := cleanLines(s.Lines)
	if len(lines) == 0 {
		return ""
	}
	out := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		verb := "says"
		if d := strings.TrimSpace(l.Delivery); d != "" {
			verb = "says (" + d + ")"
		}
		out = append(out, fmt.Sprintf(`%s %s: "%s"`, speakerCue(p, l.Speaker), verb, strings.ReplaceAll(strings.TrimSpace(l.Text), `"`, `'`)))
	}
	spoken := "Spoken aloud"
	if lang := languageName(p.Input.Language); lang != "" {
		spoken += " in " + lang
	}
	out = append(out, "("+spoken+", lip-synced by whoever speaks; no subtitles, no captions, no on-screen text.)")
	return strings.Join(out, "\n")
}

// speakerCue names a speaker for the video model: the name, a short look cue
// (the start of the character's description — names mean nothing to it) and
// the voice; the narrator is an off-screen voice.
func speakerCue(p *Project, id string) string {
	c := findCharacter(p, id)
	if c == nil || c.IsObject() {
		v := firstNonBlank(strings.TrimSpace(p.NarratorVoice), "a calm, warm narrator voice")
		return "An off-screen narrator (voice: " + v + "; nobody in frame moves their lips)"
	}
	var cues []string
	if look := lookCue(c.Description); look != "" {
		cues = append(cues, look)
	}
	voice := strings.TrimSpace(c.Voice)
	if voice == "" {
		voice = "a natural voice"
		switch strings.ToLower(c.Gender) {
		case "male":
			voice = "a natural male voice"
		case "female":
			voice = "a natural female voice"
		}
	}
	cues = append(cues, "voice: "+voice)
	return fmt.Sprintf("%s (%s)", firstNonBlank(strings.TrimSpace(c.Name), "The character"), strings.Join(cues, "; "))
}

// lookCue is the first clause of a description, at most 12 words ("A
// 7-year-old boy in a red jacket").
func lookCue(desc string) string {
	desc = strings.TrimSpace(desc)
	if i := strings.IndexAny(desc, ".;:"); i >= 0 {
		desc = desc[:i]
	}
	if i := strings.Index(desc, ","); i >= 0 && len(strings.Fields(desc[:i])) >= 3 {
		desc = desc[:i]
	}
	w := strings.Fields(desc)
	if len(w) > 12 {
		w = w[:12]
	}
	return strings.Join(w, " ")
}

// voiceFor: the speaking character's voice, else the narrator's, else the default.
func voiceFor(p *Project, s *Scene, fallback string) string {
	if c := findCharacter(p, s.Speaker); c != nil && c.VoiceID != "" {
		return c.VoiceID
	}
	if p.Input.NarratorVoiceID != "" {
		return p.Input.NarratorVoiceID
	}
	return fallback
}

// ---- asset state bookkeeping ----------------------------------------------------------

// assetSlot locates one asset of a project (assetRef, boardRef); in returns
// the live asset (valid only under the project lock), nil when it is gone.
type assetSlot interface{ in(p *Project) *Asset }

// product is a finished generation waiting to be stored.
type product struct {
	data      []byte
	sub, name string // <project>/<sub>/<name>
	probe     bool   // read the duration with ffprobe
	// rewrite: the prompt that made it was rewritten after a content
	// refusal (rewrite.go); stored on the asset (nil clears an old one).
	rewrite *PromptRewrite
	// after runs in the same commit as the asset; returns files it dropped.
	after func(p *Project) []string
}

// begin marks the assets as running (all of them in one commit); false when
// any of them is gone.
func (e *Engine) begin(st *proj, refs ...assetSlot) bool {
	ok := false
	e.update(st, func(p *Project) {
		as := make([]*Asset, 0, len(refs))
		for _, ref := range refs {
			a := ref.in(p)
			if a == nil {
				return
			}
			as = append(as, a)
		}
		for _, a := range as {
			a.Status, a.Error, a.Progress = StatusRunning, "", 0
		}
		ok = len(as) > 0
	})
	return ok
}

// fail records err on the assets (all of them in one commit).
func (e *Engine) fail(st *proj, ref assetSlot, err error, more ...assetSlot) {
	e.update(st, func(p *Project) {
		for _, r := range append([]assetSlot{ref}, more...) {
			a := r.in(p)
			switch {
			case a == nil:
			case isCancel(err) && a.Path != "":
				// Stopped (app closing): the previous file is still good.
				a.Status, a.Error = StatusDone, ""
			default:
				a.Status = StatusError
				a.Error = e.failureText(err) // the service's exact error (failure.go)
			}
		}
	})
}

func (e *Engine) finish(st *proj, ref assetSlot, out product) bool {
	rel, err := e.writeAsset(st, out.sub, out.name, out.data)
	if err != nil {
		e.fail(st, ref, err)
		return false
	}
	dur := 0.0
	if out.probe {
		if info, err := e.ffNow().Probe(st.ctx, filepath.Join(st.dir, filepath.FromSlash(rel))); err == nil {
			dur = info.Duration
		}
	}
	var drop []string
	ok := false
	e.update(st, func(p *Project) {
		a := ref.in(p)
		if a == nil { // the item was deleted meanwhile
			drop = append(drop, rel)
			return
		}
		if a.Path != "" && a.Path != rel {
			drop = append(drop, a.Path)
		}
		a.Path, a.Status, a.Error, a.Stale, a.Progress = rel, StatusDone, "", false, 100
		a.Rewrite = out.rewrite
		a.UpdatedAt = nowMs()
		if out.probe {
			a.Duration = dur
		}
		if out.after != nil {
			drop = append(drop, out.after(p)...)
		}
		ok = true
	})
	e.discardFiles(st, drop)
	return ok
}

// produce runs one asset generation with uniform status bookkeeping. Never
// fails loudly: errors land on the asset; false means it did not succeed.
func (e *Engine) produce(st *proj, ref assetSlot, work func() (product, error)) bool {
	if !e.begin(st, ref) {
		return false
	}
	out, err := work()
	if err != nil {
		e.fail(st, ref, err)
		return false
	}
	return e.finish(st, ref, out)
}

// readImage loads a project image as a reference; name binds it to @name.
func readImage(dir, rel, name string) (gen.RefImage, error) {
	abs := filepath.Join(dir, filepath.FromSlash(rel))
	data, err := os.ReadFile(abs)
	if err != nil {
		return gen.RefImage{}, err
	}
	mime := mimeByExt[strings.ToLower(filepath.Ext(abs))]
	if !strings.HasPrefix(mime, "image/") {
		if sniff := http.DetectContentType(data); strings.HasPrefix(sniff, "image/") {
			mime = sniff
		} else {
			mime = "image/png"
		}
	}
	r := gen.RefImage{Data: data, MIME: mime}
	if name != "" {
		r.Name = name + "." + imageExt(mime)
	}
	return r, nil
}

// ---- generators ------------------------------------------------------------------------

func (e *Engine) genCharacter(ctx context.Context, st *proj, id, override string) bool {
	var req gen.ImageRequest
	var tag, photo string
	var prev *PromptRewrite
	if !e.read(st, func(p *Project) bool {
		c := findCharacter(p, id)
		if c == nil {
			return false
		}
		tag, prev = c.Tag, c.Image.Rewrite
		prompt := override
		if strings.TrimSpace(prompt) == "" {
			prompt = characterPrompt(p, c)
		}
		if m := findMaterial(p, c.MaterialID); m != nil && c.MaterialID != "" {
			photo = m.Path // drawn after the user's own picture
			if c.IsObject() {
				prompt += fmt.Sprintf("\nThe reference image @%s shows this object: keep exactly the same shape, materials and colours.", c.Tag)
			} else {
				prompt += fmt.Sprintf("\nThe reference image @%s shows this character: keep exactly the same face, hair, body and outfit.", c.Tag)
			}
		}
		req = gen.ImageRequest{Prompt: prompt, UIModel: p.Input.ImageModel, Aspect: "3:4"}
		return true
	}) {
		return false
	}
	return e.produce(st, assetRef{slotCharacter, id}, func() (product, error) {
		if err := addPhoto(st.dir, photo, tag, &req); err != nil {
			return product{}, err
		}
		data, mime, rw, err := e.imageRewriting(ctx, st, req, prev, maxRewrites)
		if err != nil {
			return product{}, err
		}
		return product{data: data, sub: "images", name: fmt.Sprintf("char-%s-%s.%s", safeName(tag), newID(12), imageExt(mime)), rewrite: rw}, nil
	})
}

func (e *Engine) genLocation(ctx context.Context, st *proj, id, override string) bool {
	var req gen.ImageRequest
	var tag, photo string
	var prev *PromptRewrite
	if !e.read(st, func(p *Project) bool {
		l := findLocation(p, id)
		if l == nil {
			return false
		}
		tag, prev = l.Tag, l.Image.Rewrite
		prompt := override
		if strings.TrimSpace(prompt) == "" {
			prompt = locationPrompt(p, l)
		}
		if m := findMaterial(p, l.MaterialID); m != nil && l.MaterialID != "" {
			photo = m.Path
			prompt += fmt.Sprintf("\nThe reference image @%s shows this place: keep the same architecture, layout and palette.", l.Tag)
		}
		req = gen.ImageRequest{Prompt: prompt, UIModel: p.Input.ImageModel, Aspect: p.Input.Aspect}
		return true
	}) {
		return false
	}
	return e.produce(st, assetRef{slotLocation, id}, func() (product, error) {
		if err := addPhoto(st.dir, photo, tag, &req); err != nil {
			return product{}, err
		}
		data, mime, rw, err := e.imageRewriting(ctx, st, req, prev, maxRewrites)
		if err != nil {
			return product{}, err
		}
		return product{data: data, sub: "images", name: fmt.Sprintf("loc-%s-%s.%s", safeName(tag), newID(12), imageExt(mime)), rewrite: rw}, nil
	})
}

type namedRef struct{ tag, path string }

// frameRefs are the named references of a scene still: its cast and its
// location (those that have an image). G-Labs binds @tag by filename
// substring, first match wins → shortest tags first.
func frameRefs(p *Project, s *Scene) []namedRef {
	var refs []namedRef
	for _, c := range sceneCast(p, s) {
		if c.Image.Path != "" {
			refs = append(refs, namedRef{c.Tag, c.Image.Path})
		}
	}
	if l := findLocation(p, s.LocationID); l != nil && l.Image.Path != "" {
		refs = append(refs, namedRef{l.Tag, l.Image.Path})
	}
	sort.SliceStable(refs, func(a, b int) bool { return len(refs[a].tag) < len(refs[b].tag) })
	return refs
}

// readRefs loads named references for an image request.
func readRefs(dir string, refs []namedRef) ([]gen.RefImage, error) {
	out := make([]gen.RefImage, 0, len(refs))
	for _, r := range refs {
		img, err := readImage(dir, r.path, r.tag)
		if err != nil {
			return nil, fmt.Errorf("не вдалося прочитати референс @%s: %w", r.tag, err)
		}
		out = append(out, img)
	}
	return out, nil
}

// genFrame draws the scene's first frame — or, in storyboard mode, its 3×3
// storyboard sheet with panel 1 as the frame (genBoard).
func (e *Engine) genFrame(ctx context.Context, st *proj, sid string) bool {
	var refs []namedRef
	var req gen.ImageRequest
	var prev *PromptRewrite
	grid := false
	if !e.read(st, func(p *Project) bool {
		s := findScene(p, sid)
		if s == nil {
			return false
		}
		if grid = p.Input.StoryboardMode == StoryboardGrid; grid {
			return true
		}
		refs = frameRefs(p, s)
		req = gen.ImageRequest{Prompt: framePrompt(p, s), UIModel: p.Input.ImageModel, Aspect: p.Input.Aspect}
		prev = s.Frame.Rewrite
		return true
	}) {
		return false
	}
	if grid {
		return e.genBoard(ctx, st, sid)
	}
	return e.produce(st, assetRef{slotFrame, sid}, func() (product, error) {
		imgs, err := readRefs(st.dir, refs)
		if err != nil {
			return product{}, err
		}
		req.Refs = imgs
		data, mime, rw, err := e.imageRewriting(ctx, st, req, prev, maxRewrites)
		if err != nil {
			return product{}, err
		}
		return product{
			data: data, sub: "images", rewrite: rw,
			name: fmt.Sprintf("frame-%s-%s.%s", safeName(sid), newID(12), imageExt(mime)),
			// A new frame invalidates the old clip and a storyboard drawn
			// for the old frame.
			after: func(p *Project) []string {
				s := findScene(p, sid)
				if s == nil {
					return nil
				}
				var drop []string
				if s.Video.Status == StatusDone {
					drop = append(drop, s.Video.Path)
					s.Video = EmptyAsset()
				}
				if s.Board.Path != "" {
					drop = append(drop, s.Board.Path)
					s.Board = EmptyAsset()
				}
				return drop
			},
		}, nil
	})
}

// genVideo renders the scene's clip. The request (start frame, or Omni Flash
// components with the storyboard / previous shot) is built by videoRequest;
// with ChainFrames the new clip's last frame is kept for the next scene.
func (e *Engine) genVideo(ctx context.Context, st *proj, sid string) bool {
	var in shotInputs
	if !e.read(st, func(p *Project) bool {
		var ok bool
		in, ok = shotInputsOf(p, sid)
		return ok
	}) {
		return false
	}
	ref := assetRef{slotVideo, sid}
	c := e.cfg.Get()
	stockMode := c.Stock.Enabled || c.Video.Provider == "stock" || st.p.Input.Pipeline == "stock"
	if in.scene.Frame.Path == "" && !stockMode {
		e.fail(st, ref, errors.New("Спершу потрібен кадр сцени"))
		return false
	}
	// Dynamic lengths / a slice of the recording: the clip is as long as the
	// scene needs; the choice is kept in Scene.ClipLen (videoRequest sends it).
	if l := sceneClipPad(c, &in.scene, in.pad); (c.Video.Dynamic || in.scene.AudioSource == AudioUpload) && l != in.scene.ClipLen {
		in.scene.ClipLen = l
		e.update(st, func(p *Project) {
			if s := findScene(p, sid); s != nil {
				s.ClipLen = l
			}
		})
	}
	ok := e.produce(st, ref, func() (product, error) {
		progress := func(pct int, _ string) {
			e.update(st, func(p *Project) {
				if a := ref.in(p); a != nil && a.Status == StatusRunning {
					a.Progress = max(0, min(100, pct))
				}
			})
		}
		progress(5, "starting")
		// Check if Stock video provider is active or selected for this scene
		if stockMode {
			progress(15, "searching stock videos")
			stockCfg := stock.Config{
				Provider:    c.Stock.Provider,
				PexelsKey:   c.Stock.PexelsKey,
				PixabayKey:  c.Stock.PixabayKey,
				CoverrKey:   c.Stock.CoverrKey,
				Orientation: c.Stock.Orientation,
			}
			prompt := in.scene.ImagePrompt
			if prompt == "" {
				prompt = in.scene.MotionPrompt
			}
			if prompt == "" {
				prompt = in.scene.Title
			}
			if prompt == "" {
				prompt = in.scene.Narration
			}

			// Target duration for this scene
			targetDur := in.scene.Audio.Duration
			if in.scene.AudioSource == AudioUpload && in.scene.VoiceEnd > in.scene.VoiceStart {
				targetDur = in.scene.VoiceEnd - in.scene.VoiceStart
			}
			if targetDur <= 0 && in.scene.Narration != "" {
				targetDur = SpeechSeconds(in.scene.Narration)
			}
			if targetDur <= 0 {
				targetDur = float64(e.ClipSeconds())
			}
			if targetDur <= 0 {
				targetDur = 6.0
			}

			// Collect IDs of stock videos already used in other scenes
			var usedStockIDs []string
			e.read(st, func(p *Project) bool {
				for _, sc := range p.Scenes {
					if sc.ID != sid && sc.Video.Status == StatusDone && sc.Video.Path != "" {
						usedStockIDs = append(usedStockIDs, sc.Video.Path)
					}
				}
				return true
			})

			stockData, stockItem, sErr := stock.SearchAndDownloadTarget(ctx, stockCfg, prompt, c.Stock.Orientation, targetDur, usedStockIDs)
			if sErr == nil && len(stockData) > 0 {
				progress(90, "processing stock clip")
				var frameRel string
				if in.scene.Frame.Path == "" {
					if stockItem != nil && stockItem.Thumbnail != "" {
						if thumbData, tErr := stock.DownloadFile(ctx, stockItem.Thumbnail); tErr == nil && len(thumbData) > 0 {
							frameRel, _ = e.writeAsset(st, "frames", fmt.Sprintf("frame-%s-%d.jpg", safeName(sid), nowMs()), thumbData)
						}
					}
					if frameRel == "" {
						tmpVid := filepath.Join(os.TempDir(), fmt.Sprintf("stock-tmp-%s-%d.mp4", safeName(sid), nowMs()))
						if err := os.WriteFile(tmpVid, stockData, 0644); err == nil {
							tmpJpg := filepath.Join(os.TempDir(), fmt.Sprintf("stock-thumb-%s-%d.jpg", safeName(sid), nowMs()))
							if err := e.ffNow().StillFrame(ctx, tmpVid, 0, 0, 0, tmpJpg); err == nil {
								if thumbBytes, err := os.ReadFile(tmpJpg); err == nil {
									frameRel, _ = e.writeAsset(st, "frames", fmt.Sprintf("frame-%s-%d.jpg", safeName(sid), nowMs()), thumbBytes)
								}
								_ = os.Remove(tmpJpg)
							}
							_ = os.Remove(tmpVid)
						}
					}
				}
				return product{
					data: stockData, sub: "videos", name: fmt.Sprintf("clip-%s-%d.mp4", safeName(sid), nowMs()), probe: true,
					after: func(p *Project) []string {
						if p.Input.ChainFrames {
							markNextClipStale(p, sid)
						}
						if s := findScene(p, sid); s != nil {
							if frameRel != "" && s.Frame.Path == "" {
								s.Frame.Path = frameRel
								s.Frame.Status = StatusDone
								s.Frame.UpdatedAt = nowMs()
							}
							if s.AudioSource == AudioUpload && s.VoiceEnd > s.VoiceStart && s.Video.Duration > 0 {
								s.TrimOut = clipTrim(s.Video.Duration, s.TrimIn, s.VoiceEnd-s.VoiceStart)
							} else if targetDur > 0 && s.Video.Duration > targetDur {
								s.TrimOut = clipTrim(s.Video.Duration, s.TrimIn, targetDur)
							}
						}
						return nil
					},
				}, nil
			}
			log.Printf("stock search fallback to generation: %v", sErr)
			if in.scene.Frame.Path == "" {
				if sErr != nil {
					return product{}, fmt.Errorf("стокове відео не знайдено: %w", sErr)
				}
				return product{}, errors.New("стокове відео не знайдено, а для ШІ-генерації потрібен кадр сцени")
			}
		}
		req, err := e.videoRequest(ctx, st, c, &in)
		if err != nil {
			return product{}, err
		}
		// One safer try after a G-Labs refusal (renderClip), then up to two
		// rewrites of the plain prompt (rewrite.go).
		data, rw, err := e.clipRewriting(ctx, st, &in, req, progress)
		if err != nil {
			return product{}, err
		}
		return product{
			data: data, sub: "videos", name: fmt.Sprintf("clip-%s-%d.mp4", safeName(sid), nowMs()), probe: true, rewrite: rw,
			after: func(p *Project) []string {
				if p.Input.ChainFrames { // the next clip may have continued the old one
					markNextClipStale(p, sid)
				}
				if s := findScene(p, sid); s != nil && s.AudioSource == AudioUpload && s.VoiceEnd > s.VoiceStart && s.Video.Duration > 0 {
					// Own voice-over: the scene ends with its piece of the recording.
					s.TrimOut = clipTrim(s.Video.Duration, s.TrimIn, s.VoiceEnd-s.VoiceStart)
				}
				return nil
			},
		}, nil
	})
	if ok && in.chain && omniFlash(c) {
		e.keepLastFrame(ctx, st, sid)
	}
	return ok
}

// genVoice voices a scene: TTS of its narration, or — for a scene of the
// user's own recording (AudioSource "upload") — its [VoiceStart, VoiceEnd]
// slice cut out of Input.VoiceFile with FFmpeg (no TTS call).
func (e *Engine) genVoice(ctx context.Context, st *proj, sid string) bool {
	fallback := e.cfg.Get().TTS.Voice
	var text, voice, recording string
	var from, to float64
	var parts []voicedPart
	upload := false
	if !e.read(st, func(p *Project) bool {
		s := findScene(p, sid)
		if s == nil {
			return false
		}
		if s.AudioSource == AudioUpload {
			upload, recording, from, to = true, p.Input.VoiceFile, s.VoiceStart, s.VoiceEnd
			return true
		}
		text, voice = strings.TrimSpace(s.Narration), voiceFor(p, s, fallback)
		parts = voicedParts(p, s, fallback) // multi-voice: each part with its speaker's voice (multivoice.go)
		return true
	}) {
		return false
	}
	if len(parts) > 0 {
		return e.genVoiceParts(ctx, st, sid, parts)
	}
	if upload {
		return e.produce(st, assetRef{slotAudio, sid}, func() (product, error) {
			data, err := e.cutVoice(ctx, st, recording, from, to)
			if err != nil {
				return product{}, err
			}
			return product{data: data, sub: "audio", name: fmt.Sprintf("voice-%s-%d.mp3", safeName(sid), nowMs()), probe: true}, nil
		})
	}
	if text == "" {
		var drop []string
		e.update(st, func(p *Project) {
			if s := findScene(p, sid); s != nil {
				drop = append(drop, s.Audio.Path)
				s.Audio = EmptyAsset()
			}
		})
		e.discardFiles(st, drop)
		return true
	}
	return e.produce(st, assetRef{slotAudio, sid}, func() (product, error) {
		data, err := e.speech.SynthesizeWith(ctx, text, voice, e.voiceOptions(st))
		if err != nil {
			return product{}, err
		}
		return product{
			data: data, sub: "audio", name: fmt.Sprintf("voice-%s-%d.mp3", safeName(sid), nowMs()), probe: true,
			after: func(p *Project) []string {
				// A clip shorter than the new line is outdated; the next clip of
				// this scene gets its length chosen again.
				staleShortClip(p, sid)
				if s := findScene(p, sid); s != nil {
					s.ClipLen = 0
				}
				return nil
			},
		}, nil
	})
}

// ---- jobs -------------------------------------------------------------------------------

// runRefs is step 3a: character + location references (skips ones already
// done unless force). A character/location planned from a start material
// gets the material picture instead — never generated here, even with force.
func (e *Engine) runRefs(j *jobCtx, force bool, from, span float64) string {
	snap := j.snapshot()
	type item struct {
		id   string
		char bool
	}
	var items []item
	bound := 0
	skipRefs := snap.Input.SkipRefs()
	for _, c := range snap.Characters {
		if m := findMaterial(&snap, c.MaterialID); m != nil && c.MaterialID != "" {
			if c.Image.Status == StatusDone || e.useMaterialImage(j.st, m, c.ID, true) {
				bound++
				continue
			}
		}
		if skipRefs {
			continue // «Без референсів»: the looks are in the prompts' words
		}
		if force || c.Image.Status != StatusDone && !j.skip(c.Image) {
			items = append(items, item{c.ID, true})
		}
	}
	for _, l := range snap.Locations {
		if m := findMaterial(&snap, l.MaterialID); m != nil && l.MaterialID != "" {
			if l.Image.Status == StatusDone || e.useMaterialImage(j.st, m, l.ID, false) {
				bound++
				continue
			}
		}
		if skipRefs {
			continue
		}
		if force || l.Image.Status != StatusDone && !j.skip(l.Image) {
			items = append(items, item{l.ID, false})
		}
	}
	if len(items) == 0 && bound > 0 {
		return fmt.Sprintf("Референси готові (%d з твоїх матеріалів)", bound)
	}
	if len(items) == 0 && skipRefs {
		return "Референси пропущено — режим «" + PipelineLabel(snap.Input.Pipeline) + "»"
	}
	tick := counter(j, len(items), "Референси персонажів і локацій", from, span)
	var failed atomic.Int64
	_ = mapLimit(items, ImageConcurrency(e.cfg.Get()), j.cancelled, func(it item, _ int) error {
		var ok bool
		if it.char {
			ok = e.genCharacter(j.work, j.st, it.id, "")
		} else {
			ok = e.genLocation(j.work, j.st, it.id, "")
		}
		if !ok {
			failed.Add(1)
		}
		tick()
		return nil
	})
	if f := int(failed.Load()); f > 0 {
		return fmt.Sprintf("Референси: %d/%d, помилок: %d", len(items)-f, len(items), f)
	}
	return fmt.Sprintf("Референси готові (%d)", len(items))
}

// runFrames is step 3b: scene frames (references first if missing).
func (e *Engine) runFrames(j *jobCtx, force bool) string {
	snap := j.snapshot()
	if snap.Input.CodeAnim() { // «Анімація кодом»: voices + specs (codeanim.go)
		return e.runCodePrep(j, force)
	}
	needRefs := false
	for _, c := range snap.Characters {
		needRefs = needRefs || c.Image.Status != StatusDone
	}
	for _, l := range snap.Locations {
		needRefs = needRefs || l.Image.Status != StatusDone
	}
	if snap.Input.SkipRefs() { // only the user's own material pictures get bound
		needRefs = needRefs && len(snap.Input.Materials) > 0
	}
	if needRefs {
		e.runRefs(j, false, 0, 30)
	}
	if j.cancelled() {
		return "Зупинено"
	}
	snap = j.snapshot()
	var ids []string
	for _, s := range snap.Scenes {
		if force || s.Frame.Status != StatusDone && !j.skip(s.Frame) {
			ids = append(ids, s.ID)
		}
	}
	start := 0.0
	if needRefs {
		start = 30
	}
	// Storyboard mode draws a 3×3 sheet per scene (genFrame → genBoard).
	grid := snap.Input.StoryboardMode == StoryboardGrid
	label := "Кадри сцен"
	if grid {
		label = "Розкадровка 3×3"
	}
	tick := counter(j, len(ids), label, start, 100-start)
	var failed atomic.Int64
	_ = mapLimit(ids, ImageConcurrency(e.cfg.Get()), j.cancelled, func(id string, _ int) error {
		if !e.genFrame(j.work, j.st, id) {
			failed.Add(1)
		}
		tick()
		return nil
	})
	e.update(j.st, func(p *Project) { p.Step = max(p.Step, 3) })
	f := int(failed.Load())
	switch {
	case grid && f > 0:
		return fmt.Sprintf("Розкадровка 3×3: %d/%d. Перегенеруй сцени з помилками.", len(ids)-f, len(ids))
	case grid:
		return fmt.Sprintf("Розкадровка 3×3 готова (%d сцен)", len(ids))
	case f > 0:
		return fmt.Sprintf("Кадрів: %d/%d. Перегенеруй сцени з помилками.", len(ids)-f, len(ids))
	}
	return fmt.Sprintf("Розкадровка готова (%d кадрів)", len(ids))
}

// runRender is step 4: videos and voiceover in parallel. With ChainFrames on
// Omni Flash the clips render one after another in scene order (each
// continues from the previous clip's last frame); voices stay parallel.
func (e *Engine) runRender(j *jobCtx, videos, voices, force bool) string {
	snap := j.snapshot()
	if snap.Input.CodeAnim() { // «Анімація кодом»: clips drawn by code (codeanim.go)
		return e.runCodeRender(j, videos, voices, force)
	}
	stills := snap.Input.StillsOnly()
	if stills { // «Тільки картинки»: no clips — the montage animates the frames
		videos = false
	}
	var vids, vcs []string
	cfg := e.cfg.Get()
	stockMode := cfg.Stock.Enabled || cfg.Video.Provider == "stock" || snap.Input.Pipeline == "stock"
	if videos {
		for _, s := range snap.Scenes {
			canVideo := s.Frame.Path != "" || stockMode
			if canVideo && (force || s.Video.Status != StatusDone && !j.skip(s.Video)) {
				vids = append(vids, s.ID)
			}
		}
	}
	if voices && snap.Input.Voiceover {
		for _, s := range snap.Scenes {
			// A slice of the user's recording does not depend on the text.
			spoken := strings.TrimSpace(s.Narration) != "" || s.AudioSource == AudioUpload
			if spoken && (force || s.Audio.Status != StatusDone) {
				vcs = append(vcs, s.ID)
			}
		}
	}
	total := len(vids) + len(vcs)
	if total == 0 {
		if stills {
			return "Відео пропущено — режим «" + PipelineLabel(snap.Input.Pipeline) + "»: сцени змонтуються з кадрів"
		}
		return "Усе вже згенеровано"
	}
	tick := counter(j, total, "Відео й озвучка", 0, 100)
	var failed atomic.Int64
	var wg sync.WaitGroup
	chain := snap.Input.ChainFrames && omniFlash(cfg)
	clipLimit := ClipConcurrency(cfg) // concurrency.go: the setting, clamped per video service
	if chain {
		clipLimit = 1 // mapLimit with one worker keeps the scene order
	}
	number := make(map[string]int, len(snap.Scenes))
	upload := make(map[string]bool, len(snap.Scenes))
	for i, s := range snap.Scenes {
		number[s.ID] = i + 1
		upload[s.ID] = s.AudioSource == AudioUpload && s.VoiceEnd > s.VoiceStart
	}
	// Dynamic clip lengths follow the voice: a clip whose voice is made in
	// this run waits for it (voices go in scene order, so the first clips
	// start soon; everything else stays parallel). A slice of the recording
	// is known in advance.
	voiced := map[string]chan struct{}{}
	if cfg.Video.Dynamic && len(vids) > 0 {
		for _, id := range vcs {
			if !upload[id] {
				voiced[id] = make(chan struct{})
			}
		}
	}
	voicesOver := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = mapLimit(vids, clipLimit, j.cancelled, func(id string, _ int) error {
			if ch := voiced[id]; ch != nil {
				select {
				case <-ch:
				case <-voicesOver: // its voice did not run (stopped)
				}
				if j.cancelled() {
					return nil
				}
			}
			if chain {
				e.setJobNote(j, fmt.Sprintf("Знімаємо сцену %d (ланцюжок)…", number[id]))
			}
			if !e.genVideo(j.work, j.st, id) {
				failed.Add(1)
			}
			tick()
			return nil
		})
	}()
	go func() {
		defer wg.Done()
		defer close(voicesOver)
		_ = mapLimit(vcs, VoiceConcurrency(cfg), j.cancelled, func(id string, _ int) error {
			if !e.genVoice(j.work, j.st, id) {
				failed.Add(1)
			}
			if ch := voiced[id]; ch != nil {
				close(ch)
			}
			tick()
			return nil
		})
	}()
	wg.Wait()
	e.update(j.st, func(p *Project) { p.Step = max(p.Step, 4) })
	note := ""
	if snap.Input.SoundMode() == AudioInVideo && len(vids) > 0 && VideoSpeech(cfg) == SpeechNo {
		note = " · відеомодель знімає без звуку — репліки героїв не прозвучать"
	}
	if f := int(failed.Load()); f > 0 {
		return fmt.Sprintf("Готово %d/%d, помилок: %d", total-f, total, f) + note
	}
	if stills {
		return fmt.Sprintf("Озвучка готова (%d) · відео пропущено — режим «%s»", total, PipelineLabel(snap.Input.Pipeline))
	}
	if snap.Input.SoundMode() == AudioInVideo {
		return fmt.Sprintf("Відео готові (%d) — герої говорять у кадрі", total) + note
	}
	return fmt.Sprintf("Відео й озвучка готові (%d)", total)
}

// runCover draws the YouTube cover using the main characters' references.
func (e *Engine) runCover(j *jobCtx, prompt string) (string, error) {
	j.progress(10, "Малюємо обкладинку")
	snap := j.snapshot()
	var cast []Character
	for _, c := range snap.Characters {
		if c.Image.Path != "" && len(cast) < 3 {
			cast = append(cast, c)
		}
	}
	var refs []gen.RefImage
	var tags []string
	for _, c := range cast {
		img, err := readImage(j.st.dir, c.Image.Path, c.Tag)
		if err != nil {
			return "", fmt.Errorf("не вдалося прочитати референс @%s: %w", c.Tag, err)
		}
		refs = append(refs, img)
		tags = append(tags, "@"+c.Tag)
	}
	prompt = strings.TrimSpace(prompt)
	text := prompt
	if text == "" {
		text = fmt.Sprintf("Eye-catching YouTube thumbnail for the video \"%s\". %s. ", snap.Title, snap.Logline)
		if len(cast) > 0 {
			text += fmt.Sprintf("Feature %s prominently with strong emotion. ", strings.Join(tags, ", "))
		}
		text += "Bold central composition, high contrast, dramatic lighting, clean empty area for a title."
	}
	req := gen.ImageRequest{
		Prompt:  text + "\nArt style: " + snap.StyleGuide + "\n" + noText,
		UIModel: "GEM_PIX_2",
		Aspect:  snap.Input.Aspect,
		Refs:    refs,
	}
	ref := assetRef{slotCover, ""}
	ok := e.produce(j.st, ref, func() (product, error) {
		data, mime, err := e.image(j.work, req)
		if err != nil {
			return product{}, err
		}
		return product{data: data, sub: "images", name: fmt.Sprintf("cover-%s.%s", newID(12), imageExt(mime))}, nil
	})
	coverErr := ""
	e.update(j.st, func(p *Project) {
		if prompt != "" {
			p.Montage.CoverPrompt = prompt
		}
		coverErr = p.Montage.Cover.Error
	})
	if !ok {
		if coverErr == "" {
			coverErr = "обкладинку не збережено: проєкт змінили або видалили, поки вона малювалась"
		}
		return "", errors.New(coverErr)
	}
	// The YouTube-ready cover (the text overlay, or the plain picture when
	// there is no title) follows every new picture; it never fails the job.
	if err := e.ApplyCoverText(j.st.id); err != nil && !errors.Is(err, ErrNoCover) {
		log.Printf("[studio:%s] cover text: %v", j.st.id, err)
	}
	return "Обкладинка готова", nil
}

// ---- single items (run outside the job lock) ------------------------------------------------

// Regen regenerates one item in the background (a character/location
// reference, a scene frame, clip or voice). prompt overrides the reference
// prompt of a character/location. Errors are for unknown items or an item
// that is already being generated; generation failures land on the asset.
func (e *Engine) Regen(id string, kind RegenKind, itemID, prompt string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	var ref assetRef
	var notFound string
	switch kind {
	case RegenCharacter:
		ref, notFound = assetRef{slotCharacter, itemID}, "персонажа не знайдено"
	case RegenLocation:
		ref, notFound = assetRef{slotLocation, itemID}, "локацію не знайдено"
	case RegenFrame:
		ref, notFound = assetRef{slotFrame, itemID}, "сцену не знайдено"
	case RegenVideo:
		ref, notFound = assetRef{slotVideo, itemID}, "сцену не знайдено"
	case RegenVoice:
		ref, notFound = assetRef{slotAudio, itemID}, "сцену не знайдено"
	default:
		return fmt.Errorf("невідомий тип елемента «%s»", kind)
	}
	st.mu.Lock()
	var err error
	a := ref.in(st.p)
	switch {
	case st.deleted.Load():
		err = ErrNotFound
	case e.closing.Load():
		err = ErrClosing
	case a == nil:
		err = errors.New(notFound)
	case kind == RegenVideo && st.p.Input.StillsOnly():
		err = errors.New("Режим «Тільки картинки»: відео не знімається — сцена змонтується з кадру з рухом камери. Щоб зняти кліп, зміни «Режим виробництва» на кроці «Сценарій».")
	case a.Status == StatusRunning || a.Status == StatusQueued:
		err = errors.New("Цей елемент уже генерується — зачекай")
	default:
		a.Status, a.Error = StatusQueued, ""
	}
	list := false
	code := st.p.Input.CodeAnim()
	if err == nil {
		list = e.commitLocked(st)
		st.bg.Add(1)
	}
	st.mu.Unlock()
	if err != nil {
		return err
	}
	e.emit(st, list)

	go func() {
		defer st.bg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[studio:%s] regen panic: %v\n%s", id, r, debug.Stack())
				e.fail(st, ref, fmt.Errorf("внутрішня помилка: %v", r))
			}
		}()
		ctx := st.ctx
		switch kind {
		case RegenCharacter:
			e.genCharacter(ctx, st, itemID, prompt)
		case RegenLocation:
			e.genLocation(ctx, st, itemID, prompt)
		case RegenFrame:
			e.genFrame(ctx, st, itemID)
		case RegenVideo:
			if code { // «Анімація кодом»: the scene's animation (codeanim.go)
				e.genCode(ctx, ctx, st, itemID, true)
			} else {
				e.genVideo(ctx, st, itemID)
			}
		case RegenVoice:
			e.genVoice(ctx, st, itemID)
		}
		// A regen that bailed out early must not stay queued.
		st.mu.Lock()
		list, changed := false, false
		if a := ref.in(st.p); a != nil && a.Status == StatusQueued && !st.deleted.Load() {
			a.Status = StatusEmpty
			if a.Path != "" {
				a.Status = StatusDone
			}
			list, changed = e.commitLocked(st), true
		}
		st.mu.Unlock()
		if changed {
			e.emit(st, list)
		}
		if err := e.writeNow(st); err != nil {
			log.Printf("[studio:%s] %v", id, err)
		}
	}()
	return nil
}

var imageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true}

// ImportImage copies a user image in as a character/location reference or a
// scene frame (step 3 "upload").
func (e *Engine) ImportImage(id string, kind RegenKind, itemID, srcPath string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	var ref assetRef
	var prefix string
	switch kind {
	case RegenCharacter:
		ref = assetRef{slotCharacter, itemID}
	case RegenLocation:
		ref = assetRef{slotLocation, itemID}
	case RegenFrame:
		ref = assetRef{slotFrame, itemID}
	default:
		return errors.New("зображення можна завантажити лише для персонажа, локації або кадру")
	}
	if !e.read(st, func(p *Project) bool {
		switch kind {
		case RegenCharacter:
			if c := findCharacter(p, itemID); c != nil {
				prefix = "char-" + safeName(c.Tag)
			}
		case RegenLocation:
			if l := findLocation(p, itemID); l != nil {
				prefix = "loc-" + safeName(l.Tag)
			}
		default:
			if findScene(p, itemID) != nil {
				prefix = "frame-" + safeName(itemID)
			}
		}
		return prefix != ""
	}) {
		return errors.New("не знайдено")
	}
	info, err := os.Stat(srcPath)
	if err != nil || info.IsDir() {
		return fmt.Errorf("файл не знайдено: %s", srcPath)
	}
	if info.Size() > maxImportImage {
		return errors.New("Файл завеликий (максимум 40 МБ)")
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("не вдалося прочитати файл: %w", err)
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		if m := mimeByExt[strings.ToLower(filepath.Ext(srcPath))]; imageExts[strings.ToLower(filepath.Ext(srcPath))] {
			mime = m
		} else {
			return errors.New("Це не зображення (потрібен PNG, JPG або WEBP)")
		}
	}
	rel, err := e.writeAsset(st, "images", fmt.Sprintf("%s-%s.%s", prefix, newID(12), imageExt(mime)), data)
	if err != nil {
		return err
	}
	var drop []string
	found := false
	e.update(st, func(p *Project) {
		a := ref.in(p)
		if a == nil {
			return
		}
		found = true
		if a.Path != "" && a.Path != rel {
			drop = append(drop, a.Path)
		}
		a.Path, a.Status, a.Error, a.Stale = rel, StatusDone, "", false
		a.UpdatedAt = nowMs()
		// The user's own frame wins over a storyboard drawn for the old one:
		// without a board the clip is animated from this frame.
		if kind == RegenFrame {
			if s := findScene(p, itemID); s != nil && s.Board.Path != "" {
				drop = append(drop, s.Board.Path)
				s.Board = EmptyAsset()
			}
		}
	})
	if !found { // our copy of the user's picture is not needed
		e.removeFiles(st, []string{rel})
	}
	e.discardFiles(st, drop)
	if !found {
		return errors.New("не знайдено")
	}
	return nil
}

var audioExts = map[string]bool{".mp3": true, ".wav": true, ".m4a": true, ".aac": true, ".ogg": true, ".oga": true, ".opus": true, ".flac": true, ".wma": true}

// SetMusic copies a music file into the project (user/) and makes it the
// background track; "" removes the music.
func (e *Engine) SetMusic(id, srcPath string) error {
	st := e.state(id)
	if st == nil {
		return ErrNotFound
	}
	if srcPath == "" {
		var drop []string
		if !e.update(st, func(p *Project) {
			drop = append(drop, p.Montage.MusicPath)
			p.Montage.MusicPath, p.Montage.MusicName = "", ""
		}) {
			return ErrNotFound
		}
		e.removeFiles(st, drop)
		return nil
	}
	ext := strings.ToLower(filepath.Ext(srcPath))
	if !audioExts[ext] {
		return errors.New("Це не аудіофайл (потрібен MP3, WAV, M4A, OGG або FLAC)")
	}
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("не вдалося відкрити файл: %w", err)
	}
	defer src.Close()
	base := filepath.Base(srcPath)
	stem := slugTag(strings.TrimSuffix(base, filepath.Ext(base))) // readable latin, ≤ 24 chars
	name := fmt.Sprintf("music-%s-%s%s", stem, newID(6), ext)
	rel, err := func() (string, error) {
		st.fmu.RLock()
		defer st.fmu.RUnlock()
		if st.deleted.Load() {
			return "", ErrNotFound
		}
		dir := filepath.Join(st.dir, "user")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		dst, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			return "", err
		}
		_, cerr := io.Copy(dst, src)
		if err := dst.Close(); cerr == nil {
			cerr = err
		}
		if cerr != nil {
			_ = os.Remove(filepath.Join(dir, name))
			return "", cerr
		}
		return "user/" + name, nil
	}()
	if err != nil {
		return fmt.Errorf("не вдалося скопіювати музику: %w", err)
	}
	var drop []string
	if !e.update(st, func(p *Project) {
		drop = append(drop, p.Montage.MusicPath)
		p.Montage.MusicPath = rel
		p.Montage.MusicName = clipRunes(base, 200)
	}) {
		return ErrNotFound
	}
	e.removeFiles(st, drop)
	return nil
}
