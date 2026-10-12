package pickers

// «Вставити ID голосу» (Cartesia, ElevenLabs): a voice the account list does
// not show — a Cartesia library voice, an ElevenLabs Voice Library voice — is
// pasted by its id, looked up with the provider's API (Engine.LookupVoice: a
// free read), shown (name, language, gender) and, on «Використати», remembered
// in «Мої голоси за ID» of that provider (config.TTS.ByID) and picked. The
// pickers list remembered voices next to the account's, in every place
// (Settings, the project, the sheets).

import (
	"context"
	"errors"
	"image"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/tts"
)

// CategoryByID marks a voice remembered by its id (tts.Voice.Category).
const CategoryByID = "byid"

// voiceByID is the dialog's state.
type voiceByID struct {
	dlg      gui.Dialog
	field    gui.TextField
	find     gui.Clickable
	use      gui.Clickable
	cancel   gui.Clickable
	forget   []gui.Clickable
	provider string
	busy     bool
	seq      int
	found    *tts.Voice // what the provider said about the id in the field
	foundFor string     // the id it was found for
	err      string
	// added is the voice just picked, listed until the settings have it.
	added *tts.Voice
}

// savedVoices are «Мої голоси за ID» of the configured provider as voices.
func savedVoices(c *core.Ctx) (provider string, out []tts.Voice) {
	if c.E == nil {
		return "", nil
	}
	t := c.E.Config().Get().TTS
	for _, r := range t.VoicesByID(t.Provider) {
		out = append(out, voiceOfRef(r))
	}
	return t.Provider, out
}

func voiceOfRef(r config.VoiceRef) tts.Voice {
	v := tts.Voice{ID: r.ID, Name: r.Name, Gender: r.Gender, Language: r.Language, Category: CategoryByID, Usable: true}
	if v.Name == "" {
		v.Name = r.ID
	}
	return v
}

func refOfVoice(v tts.Voice) config.VoiceRef {
	return config.VoiceRef{ID: v.ID, Name: v.Name, Language: v.Language, Gender: v.Gender, Library: v.Category == "library"}
}

// withSaved is the account's voices plus the remembered ones it does not list
// (and the one just added).
func withSaved(c *core.Ctx, list []tts.Voice, added *tts.Voice) []tts.Voice {
	_, saved := savedVoices(c)
	if added != nil {
		saved = append([]tts.Voice{*added}, saved...)
	}
	if len(saved) == 0 {
		return list
	}
	have := make(map[string]bool, len(list))
	for _, v := range list {
		have[v.ID] = true
	}
	out := append([]tts.Voice(nil), list...)
	for _, v := range saved {
		if !have[v.ID] {
			have[v.ID] = true
			out = append(out, v)
		}
	}
	return out
}

// languageUK names a locale in Ukrainian where it is a common one.
func languageUK(loc string) string {
	code := strings.ToLower(strings.SplitN(strings.ReplaceAll(loc, "_", "-"), "-", 2)[0])
	names := map[string]string{"uk": "українська", "en": "англійська", "pl": "польська", "de": "німецька", "fr": "французька",
		"es": "іспанська", "it": "італійська", "pt": "португальська", "ru": "російська", "ja": "японська", "zh": "китайська",
		"ko": "корейська", "hi": "гінді", "tr": "турецька", "nl": "нідерландська", "sv": "шведська", "cs": "чеська", "ar": "арабська"}
	if n, ok := names[code]; ok {
		return n
	}
	return loc
}

// voiceFacts: «українська · жіночий · з бібліотеки».
func voiceFacts(v tts.Voice) string {
	var parts []string
	if v.Language != "" {
		parts = append(parts, languageUK(v.Language))
	}
	if m := VoiceMeta(v); m != "" {
		parts = append(parts, m)
	}
	return strings.Join(parts, " · ")
}

// open shows the dialog for provider.
func (b *voiceByID) open(provider string) {
	if b.provider != provider {
		b.field.SetText("")
		b.found, b.err = nil, ""
	}
	b.provider = provider
	b.dlg.Open()
}

// lookup checks the id in the field (in the background).
func (b *voiceByID) lookup(c *core.Ctx) {
	id := tts.CleanVoiceID(b.field.Editor.Text())
	b.found, b.err = nil, ""
	if msg := tts.VoiceIDProblem(b.provider, id); msg != "" {
		b.err = msg
		return
	}
	if c.Offline || c.E == nil {
		b.err = "Перевірка недоступна в демо-режимі."
		return
	}
	b.busy = true
	b.seq++
	seq := b.seq
	c.Background(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		v, err := c.E.LookupVoice(ctx, id)
		c.Post(func() {
			if seq != b.seq {
				return
			}
			b.busy = false
			if err != nil {
				b.err = strings.TrimPrefix(core.ErrorText(err), tts.ErrVoiceNotFound.Error()+": ")
				if errors.Is(err, tts.ErrVoiceNotFound) {
					b.err = "Голос не знайдено. " + b.err
				}
				return
			}
			b.found, b.foundFor = &v, id
		})
	})
}

// layout draws the dialog (when open); pick runs with the chosen voice id.
func (b *voiceByID) layout(gtx C, c *core.Ctx, pick func(id string)) {
	th := c.Th
	if !b.dlg.IsOpen() && !b.busy {
		b.dlg.Layout(gtx, th, gui.DialogStyle{})
		return
	}
	prov, _ := tts.ProviderByID(b.provider)
	label := firstNonEmpty(prov.Label, b.provider)
	saved := c.E.Config().Get().TTS.VoicesByID(b.provider)
	if len(b.forget) < len(saved) {
		b.forget = append(b.forget, make([]gui.Clickable, len(saved)-len(b.forget))...)
	}
	for i, r := range saved {
		if b.forget[i].Clicked(gtx) {
			id, p := r.ID, b.provider
			c.Do(func() error {
				return c.E.Config().Update(func(cf *config.Config) { cf.TTS.ForgetVoiceByID(p, id) })
			}, nil)
		}
	}
	if _, ok := b.field.Changed(gtx); ok && b.found != nil && tts.CleanVoiceID(b.field.Editor.Text()) != b.foundFor {
		b.found = nil // the id changed: look it up again
	}
	if b.field.Submitted(gtx) || b.find.Clicked(gtx) {
		b.lookup(c)
	}
	if b.cancel.Clicked(gtx) || b.dlg.Dismissed() {
		b.dlg.Close()
		b.seq++
		b.busy = false
	}
	if b.use.Clicked(gtx) && b.found != nil && b.found.Usable {
		v, p := *b.found, b.provider
		ref := refOfVoice(v)
		vv := voiceOfRef(ref)
		b.added = &vv
		c.Do(func() error {
			return c.E.Config().Update(func(cf *config.Config) { cf.TTS.SaveVoiceByID(p, ref) })
		}, nil)
		pick(v.ID)
		b.dlg.Close()
		c.Toast(gui.ToneSuccess, "Голос «"+firstNonEmpty(v.Name, v.ID)+"» додано в «Мої голоси за ID»")
		b.field.SetText("")
		b.found = nil
	}
	where := map[string]string{
		"cartesia":   "з бібліотеки голосів Cartesia (play.cartesia.ai → Voices → меню голосу → Copy ID) або з посилання на голос",
		"elevenlabs": "з бібліотеки голосів ElevenLabs (Voice Library → меню голосу → Copy voice ID); голоси бібліотеки працюють через API на платному тарифі",
	}[b.provider]
	body := func(gtx C) D {
		ws := []layout.Widget{
			func(gtx C) D {
				return b.field.Layout(gtx, th, gui.FieldStyle{Label: "ID голосу " + label, Placeholder: tts.VoiceIDExample(b.provider),
					Hint: "Можна вставити й посилання — студія візьме з нього ID.", Submit: true})
			},
		}
		switch {
		case b.busy:
			ws = append(ws, func(gtx C) D { return resultBox(gtx, th, true, false, "Шукаємо голос у "+label+"…", "") })
		case b.found != nil:
			v := *b.found
			msg := voiceFacts(v)
			if v.ID != "" {
				msg += "\nID " + v.ID
			}
			ws = append(ws, func(gtx C) D {
				return resultBox(gtx, th, false, true, "Знайдено: "+firstNonEmpty(v.Name, v.ID), msg)
			})
		case b.err != "":
			ws = append(ws, func(gtx C) D { return resultBox(gtx, th, false, false, "Не вдалося", b.err) })
		}
		if len(saved) > 0 {
			ws = append(ws, func(gtx C) D {
				rows := []layout.Widget{gui.Text{Style: th.Label}.Widget(th, "Мої голоси за ID · "+label)}
				for i, r := range saved {
					i, r := i, r
					rows = append(rows, func(gtx C) D {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Rigid(gui.IconHash.Widget(16, th.Pal.Text2)),
							layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
							layout.Flexed(1, func(gtx C) D {
								return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
									layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium), MaxLines: 1}.Widget(th, firstNonEmpty(r.Name, r.ID))),
									layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 1}.Widget(th, voiceFacts(voiceOfRef(r)))),
								)
							}),
							layout.Rigid(func(gtx C) D {
								return gui.IconButton{Icon: gui.IconX, Size: 32, Tip: "Забути цей голос"}.Layout(gtx, th, &b.forget[i])
							}),
						)
					})
				}
				return gui.VStack(gtx, gui.Sp8, rows...)
			})
		}
		return gui.VStack(gtx, gui.Sp16, ws...)
	}
	acts := []layout.Widget{
		func(gtx C) D {
			return gui.Button{Kind: gui.Ghost, Text: "Скасувати"}.Layout(gtx, th, &b.cancel)
		},
	}
	if b.found != nil {
		usable := b.found.Usable // an ElevenLabs library voice on a free plan cannot sound
		acts = append(acts, func(gtx C) D {
			return gui.Button{Kind: gui.Primary, Icon: gui.IconCheck, Text: "Використати голос", Disabled: !usable}.Layout(gtx, th, &b.use)
		})
	} else {
		acts = append(acts, func(gtx C) D {
			return gui.Button{Kind: gui.Primary, Icon: gui.IconSearch, Text: "Знайти голос", Busy: b.busy,
				Disabled: b.field.Editor.Len() == 0}.Layout(gtx, th, &b.find)
		})
	}
	b.dlg.Layout(gtx, th, gui.DialogStyle{
		Title: "Голос за ID", Icon: gui.IconHash, Width: 520,
		Text:    "Встав ID голосу " + label + " — " + where + ". Студія перевірить його й запам'ятає в «Мої голоси за ID».",
		Body:    body,
		Actions: acts,
	})
}

// resultBox is the outcome line of a lookup: busy, found (green) or an error (red).
func resultBox(gtx C, th *gui.Theme, busy, ok bool, title, msg string) D {
	bg, ink := th.Pal.Surface, th.Pal.Text2
	icon := gui.IconSearch
	switch {
	case busy:
	case ok:
		bg, ink, icon = th.Pal.SuccessSoft, th.Pal.SuccessInk, gui.IconCheckCircle
	default:
		bg, ink, icon = th.Pal.ErrorSoft, th.Pal.ErrorInk, gui.IconCircleAlert
	}
	pad := gtx.Dp(gui.Sp12)
	w := gtx.Constraints.Max.X
	cg := gtx
	cg.Constraints = layout.Constraints{Max: image.Pt(max(0, w-2*pad-gtx.Dp(30)), gtx.Constraints.Max.Y)}
	m := op.Record(gtx.Ops)
	d := layout.Flex{Axis: layout.Vertical}.Layout(cg,
		layout.Rigid(gui.Text{Style: th.Label.Weighted(gui.FontSemiBold), Color: ink}.Widget(th, title)),
		layout.Rigid(func(gtx C) D {
			if msg == "" {
				return D{}
			}
			return layout.Inset{Top: 2}.Layout(gtx, gui.Text{Style: th.Small, Color: th.Pal.Text}.Widget(th, msg))
		}),
	)
	call := m.Stop()
	h := d.Size.Y + 2*pad
	gui.FillRRect(gtx, image.Rect(0, 0, w, h), gui.RadiusControl, bg)
	o := op.Offset(image.Pt(pad, pad)).Push(gtx.Ops)
	if busy {
		gui.Spinner{Size: 18, Color: th.Pal.Text2}.Layout(gtx, th)
	} else {
		icon.Layout(gtx, 20, ink)
	}
	o.Pop()
	o = op.Offset(image.Pt(pad+gtx.Dp(30), pad)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	o.Pop()
	return D{Size: image.Pt(w, h)}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}
