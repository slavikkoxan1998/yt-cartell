package pickers

import (
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/tts"
)

// DefaultVoice is the label of the "" narrator voice.
const DefaultVoice = "Як у налаштуваннях"

// VoiceMeta describes a voice in plain Ukrainian: "жіночий · британський ·
// стандартний" (+ why the account cannot use it: "потрібен платний тариф
// ElevenLabs").
func VoiceMeta(v tts.Voice) string {
	var parts []string
	switch strings.ToLower(v.Gender) {
	case "male":
		parts = append(parts, "чоловічий")
	case "female":
		parts = append(parts, "жіночий")
	case "neutral", "non-binary", "nonbinary":
		parts = append(parts, "нейтральний")
	}
	if a := accentUK(v.Accent); a != "" {
		parts = append(parts, a)
	}
	switch strings.ToLower(strings.ReplaceAll(v.Age, "_", " ")) {
	case "young":
		parts = append(parts, "молодий")
	case "middle aged", "middle-aged":
		parts = append(parts, "середнього віку")
	case "old":
		parts = append(parts, "літній")
	}
	switch strings.ToLower(v.Category) {
	case "premade":
		parts = append(parts, "стандартний")
	case "cloned":
		parts = append(parts, "клонований")
	case "professional":
		parts = append(parts, "професійний")
	case "generated":
		parts = append(parts, "згенерований")
	case "library":
		parts = append(parts, "з бібліотеки")
	case CategoryByID:
		parts = append(parts, "за ID")
	}
	if !v.Usable {
		reason := v.Locked
		if reason == "" {
			reason = "потрібен платний тариф"
		}
		parts = append(parts, reason)
	} else if v.Note != "" {
		parts = append(parts, v.Note) // "доступний до 31 грудня 2026"
	}
	return strings.Join(parts, " · ")
}

var accents = map[string]string{
	"american": "американський", "british": "британський", "australian": "австралійський",
	"irish": "ірландський", "indian": "індійський", "african": "африканський", "scottish": "шотландський",
	"canadian": "канадський", "swedish": "шведський", "italian": "італійський", "german": "німецький",
	"spanish": "іспанський", "french": "французький", "polish": "польський", "ukrainian": "український",
	"russian": "російський", "transatlantic": "трансатлантичний", "english": "англійський",
}

func accentUK(a string) string {
	a = strings.TrimSpace(strings.ToLower(a))
	if a == "" {
		return ""
	}
	if s, ok := accents[a]; ok {
		return s
	}
	return a
}

// UnknownVoice names a voice id the account list does not have (another
// account, a removed voice) — never the raw id.
const UnknownVoice = "Інший голос"

// ForeignVoice names a voice id of another TTS provider (a project planned
// before the provider changed): it cannot sound until a voice is picked again.
const ForeignVoice = "Голос іншого сервісу"

// VoiceTitle is how a voice id reads for the current provider: its name,
// ForeignVoice for another provider's id, UnknownVoice otherwise ("" for "").
func VoiceTitle(c *core.Ctx, id string) string {
	if id == "" {
		return ""
	}
	vl, _, _ := c.Voices()
	if v, ok := findVoice(withSaved(c, vl.Voices, nil), id); ok && v.Name != "" {
		return v.Name
	}
	if foreignOwner(c, id) != "" {
		return ForeignVoice
	}
	return UnknownVoice
}

// foreignOwner is the label of the provider id surely belongs to when that is
// not the current one ("" = it may be the current provider's).
func foreignOwner(c *core.Ctx, id string) string {
	if c.E == nil || id == "" {
		return ""
	}
	owner := tts.ForeignVoiceOf(c.E.Config().Get().TTS, id)
	if owner == "" {
		return ""
	}
	if p, ok := tts.ProviderByID(owner); ok {
		return p.Label
	}
	return owner
}

// currentProviderLabel is the label of the configured TTS provider.
func currentProviderLabel(c *core.Ctx) string {
	if c.E == nil {
		return ""
	}
	p, _ := tts.ProviderByID(c.E.Config().Get().TTS.Provider)
	return p.Label
}

// VoiceName is how a voice id reads: its name when the list knows it,
// UnknownVoice otherwise ("" for "").
func VoiceName(list []tts.Voice, id string) string {
	if id == "" {
		return ""
	}
	for _, v := range list {
		if v.ID == id && v.Name != "" {
			return v.Name
		}
	}
	return UnknownVoice
}

// Voice is the narrator voice field (value = voice id, "" = the default from Settings).
type Voice struct {
	pick[string]
	Look
	// Default is the label of "" (zero = DefaultVoice; characters use "Як у
	// оповідача").
	Default string

	btn      gui.Clickable
	play     gui.Clickable
	chev     gui.Anim
	fx       flatFX
	pop      list
	plays    []gui.Clickable // preview buttons of the rows
	opts     []option
	voices   []tts.Voice
	reload   gui.Clickable
	settings gui.Clickable
	// «Вставити ID голосу» (voicebyid.go): the popover's button, the dialog.
	pasteID gui.Clickable
	byID    voiceByID
}

// IsOpen reports whether the voice list (or the «Голос за ID» dialog) is open.
func (s *Voice) IsOpen() bool { return s.pop.open || s.byID.dlg.IsOpen() }

func findVoice(list []tts.Voice, id string) (tts.Voice, bool) {
	for _, v := range list {
		if v.ID == id {
			return v, true
		}
	}
	return tts.Voice{}, false
}

// Layout draws the field.
func (s *Voice) Layout(gtx core.C, c *core.Ctx, label, value string) core.D {
	th := c.Th
	value = s.shown(gtx, value)
	vl, loading, err := c.Voices()
	// The account's voices and «Мої голоси за ID» (the one just added until
	// the settings have it).
	provider, saved := savedVoices(c)
	if a := s.byID.added; a != nil {
		for _, v := range saved {
			if v.ID == a.ID {
				s.byID.added = nil
			}
		}
	}
	s.voices = withSaved(c, vl.Voices, s.byID.added)
	byID := tts.CanLookupVoice(provider) && c.E != nil
	def := s.Default
	if def == "" {
		def = DefaultVoice
	}
	settingsVoice := ""
	if c.E != nil {
		settingsVoice = c.E.Config().Get().TTS.Voice
	}

	// Options: "" first, then the account voices (usable first, as listed).
	s.opts = s.opts[:0]
	defCaption := "голос, вибраний у Налаштуваннях"
	if s.Default != "" {
		defCaption = "без окремого голосу"
	}
	if v, ok := findVoice(s.voices, settingsVoice); ok && s.Default == "" {
		defCaption = "зараз: " + v.Name
	}
	s.opts = append(s.opts, option{Value: "", Label: def, Caption: defCaption})
	for _, v := range s.voices {
		name := v.Name
		if name == "" {
			name = v.ID
		}
		s.opts = append(s.opts, option{Value: v.ID, Label: name, Caption: VoiceMeta(v), Disabled: !v.Usable,
			Search: v.Accent + " " + v.Gender})
	}
	known := value == ""
	for _, o := range s.opts {
		known = known || o.Value == value
	}
	foreign := ""
	if !known {
		foreign = foreignOwner(c, value)
		if foreign != "" {
			s.opts = append(s.opts, option{Value: value, Label: ForeignVoice, Caption: "з " + foreign + " — тут не звучить, вибери інший"})
		} else {
			s.opts = append(s.opts, option{Value: value, Label: UnknownVoice, Caption: "немає у списку голосів акаунта"})
		}
	}
	if len(s.plays) < len(s.opts) {
		s.plays = append(s.plays, make([]gui.Clickable, len(s.opts)-len(s.plays))...)
	}
	for i, o := range s.opts {
		if s.plays[i].Clicked(gtx) {
			togglePreview(c, s.previewOf(c, o.Value))
		}
	}

	st := listStyle{Options: s.opts, Value: value, Hint: "Пошук голосу", MinWidth: 400, RowH: voiceRowH, TwoLine: true,
		Row: func(gtx C, i int, hi, selected bool) D { return s.row(gtx, c, i, selected) }}
	if len(s.voices) == 0 {
		// Nothing to choose from yet: the popover explains why.
		st.Options = nil
		st.Empty = func(gtx C) D { return s.empty(gtx, c, loading, err) }
		st.EmptyH = 176
	}
	if s.reload.Clicked(gtx) {
		c.ReloadVoices()
	}
	if s.settings.Clicked(gtx) {
		s.pop.hide(gtx, &s.btn)
		c.Navigate(core.Route{Page: core.PageSettings, Section: "tts"})
	}
	if byID {
		st.Header = func(gtx C) D { return s.byIDHeader(gtx, c) }
		if s.pasteID.Clicked(gtx) {
			s.pop.hide(gtx, &s.btn)
			s.byID.open(provider)
		}
	}
	if v, ok := s.pop.update(gtx, &s.btn, st); ok {
		s.set(gtx, v)
		value = v
		st.Value = v
	}
	if s.btn.Clicked(gtx) && !s.Disabled {
		if s.pop.open {
			s.pop.hide(gtx, &s.btn)
		} else {
			s.pop.openAt(gtx, &s.btn, st)
		}
	}

	// The field.
	name, caption, errc := def, "", false
	preview := ""
	switch {
	case value == "":
		if v, ok := findVoice(s.voices, settingsVoice); ok && s.Default == "" {
			caption, preview = v.Name+" · з Налаштувань", v.PreviewURL
		} else {
			caption = defCaption
		}
	default:
		if v, ok := findVoice(s.voices, value); ok {
			name, caption, preview = v.Name, VoiceMeta(v), v.PreviewURL
			if name == "" {
				name = v.ID
			}
		} else if foreign != "" {
			// Another provider's voice: say so and ask for a new one.
			name, caption, errc = ForeignVoice, "з "+foreign+" — вибери голос "+currentProviderLabel(c), true
		} else {
			name = UnknownVoice
			if !loading {
				caption = "немає у списку голосів акаунта"
			}
		}
	}
	switch {
	case foreign != "":
	case loading && len(s.voices) == 0:
		caption = "Завантажуємо голоси…"
	case err != nil && len(s.voices) == 0:
		caption, errc = "Не вдалося завантажити голоси", true
	}
	if s.Note != "" {
		caption, errc = s.Note, false
	}
	if s.play.Clicked(gtx) {
		togglePreview(c, preview)
	}
	var vis layout.Widget
	if preview != "" {
		playing := c.Audio != nil && c.Audio.Playing(preview)
		vis = func(gtx C) D {
			b := gui.Button{Kind: gui.Tinted, Icon: gui.IconPlay, Round: true, Height: visualSize, Tip: "Прослухати голос"}
			if s.Flat {
				b.Kind = gui.Secondary // a white disc reads on the warm flat field
			}
			if playing {
				b.Kind, b.Icon, b.Tip = gui.Primary, gui.IconSquare, "Зупинити"
			}
			return b.Layout(gtx, th, &s.play)
		}
	} else if _, ok := findVoice(s.voices, value); ok && value != "" {
		vis = func(gtx C) D { return gui.Avatar{Name: name, Size: visualSize}.Layout(gtx, th) }
	} else {
		vis = visIcon(th, gui.IconMic, s.Flat)
	}
	t := tile{Label: label, Value: name, Caption: caption, Error: errc, Visual: vis, Flat: s.Flat, Loading: loading && len(s.voices) == 0 && value != "",
		Open: s.pop.open, Disabled: s.Disabled, Tip: s.Tip, Anchor: s.pop.key()}
	d := fieldWithPopover(gtx, th, t, &s.btn, &s.chev, &s.fx, func(gtx C, field image.Point) {
		s.pop.layout(gtx, th, field, st)
	})
	s.byID.layout(gtx, c, func(id string) { s.set(gtx, id) })
	return d
}

// byIDHeader is the popover's first line: «Вставити ID голосу».
func (s *Voice) byIDHeader(gtx C, c *core.Ctx) D {
	th := c.Th
	return layout.Inset{Left: gui.Sp4, Top: 2, Bottom: 2}.Layout(gtx, func(gtx C) D {
		gtx.Constraints.Min.X = 0
		return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconHash, Text: "Вставити ID голосу",
			Tip: "Голос із бібліотеки сервісу за його ID — якщо його немає в списку акаунта"}.Layout(gtx, th, &s.pasteID)
	})
}

// row draws one voice: a round preview button (orange while it plays), the
// name and the description, a check on the current voice.
func (s *Voice) row(gtx C, c *core.Ctx, i int, selected bool) D {
	th := c.Th
	o := s.opts[i]
	w, h := gtx.Constraints.Max.X, gtx.Dp(voiceRowH)
	pad := gtx.Dp(popPad) + gtx.Dp(8)
	x := pad
	preview := s.previewOf(c, o.Value)
	fg, sub := th.Pal.Text, th.Pal.Text2
	if o.Disabled {
		fg, sub = th.Pal.Text3, th.Pal.Text3
	}
	right := w - pad
	if selected {
		ic := gtx.Dp(18)
		t := op.Offset(image.Pt(right-ic, (h-ic)/2)).Push(gtx.Ops)
		gui.IconCheck.LayoutStroke(gtx, 18, 2, th.Pal.ActionText)
		t.Pop()
		right -= ic + gtx.Dp(10)
	}
	as := gtx.Dp(36)
	t := op.Offset(image.Pt(x, (h-as)/2)).Push(gtx.Ops)
	switch {
	case preview != "":
		// The button plays the preview; the rest of the row picks the voice.
		playing := c.Audio != nil && c.Audio.Playing(preview)
		b := gui.Button{Kind: gui.Tinted, Icon: gui.IconPlay, Round: true, Height: 36, Tip: "Прослухати"}
		if playing {
			b.Kind, b.Icon, b.Tip = gui.Primary, gui.IconSquare, "Зупинити"
		}
		b.Layout(gtx, th, &s.plays[i])
	case o.Value == "":
		gui.FillRRect(gtx, image.Rect(0, 0, as, as), 10, th.Pal.Surface)
		ip := gtx.Dp(18)
		t2 := op.Offset(image.Pt((as-ip)/2, (as-ip)/2)).Push(gtx.Ops)
		gui.IconSettings2.Layout(gtx, 18, th.Pal.Text)
		t2.Pop()
	default:
		gui.Avatar{Name: o.Label, Size: 36}.Layout(gtx, th)
	}
	t.Pop()
	x += as + gtx.Dp(12)
	lg := gtx
	lg.Constraints = layout.Constraints{Max: image.Pt(max(0, right-x), h)}
	m := op.Record(gtx.Ops)
	d := layout.Flex{Axis: layout.Vertical}.Layout(lg,
		layout.Rigid(gui.Text{Style: th.Body.Weighted(gui.FontMedium).Lined(1.3), Color: fg, MaxLines: 1}.Widget(th, o.Label)),
		layout.Rigid(func(gtx C) D {
			if o.Caption == "" {
				return D{}
			}
			return gui.Text{Style: th.Small, Color: sub, MaxLines: 1}.Layout(gtx, th, o.Caption)
		}),
	)
	call := m.Stop()
	t = op.Offset(image.Pt(x, (h-d.Size.Y)/2)).Push(gtx.Ops)
	call.Add(gtx.Ops)
	t.Pop()
	return D{Size: image.Pt(w, h)}
}

// previewOf is the preview URL of an option ("" = the Settings voice's).
func (s *Voice) previewOf(c *core.Ctx, value string) string {
	if v, ok := findVoice(s.voices, value); ok {
		return v.PreviewURL
	}
	if value == "" && s.Default == "" && c.E != nil {
		if v, ok := findVoice(s.voices, c.E.Config().Get().TTS.Voice); ok {
			return v.PreviewURL
		}
	}
	return ""
}

// togglePreview plays src (or stops it when it plays).
func togglePreview(c *core.Ctx, src string) {
	if src == "" || c.Audio == nil {
		return
	}
	if c.Audio.Playing(src) {
		c.Audio.Stop()
		return
	}
	if err := c.Audio.Play(src, c.Invalidate); err != nil {
		c.Error(err)
	}
}

// empty is the popover body while there is no voice list: loading
// skeletons, an error with "Оновити", or how to get voices.
func (s *Voice) empty(gtx C, c *core.Ctx, loading bool, err error) D {
	th := c.Th
	in := layout.UniformInset(gui.Sp12)
	switch {
	case loading:
		return in.Layout(gtx, func(gtx C) D {
			var ch []layout.FlexChild
			for i := 0; i < 3; i++ {
				if i > 0 {
					ch = append(ch, layout.Rigid(layout.Spacer{Height: gui.Sp12}.Layout))
				}
				ch = append(ch, layout.Rigid(func(gtx C) D {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(gui.Skeleton{Width: 36, Height: 36, Radius: 18}.Widget(th)),
						layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
						layout.Flexed(1, func(gtx C) D { return gui.SkeletonLines(gtx, th, 2) }),
					)
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, ch...)
		})
	case err != nil:
		return in.Layout(gtx, func(gtx C) D {
			return gui.VStack(gtx, gui.Sp8,
				gui.Text{Style: th.BodyStrong}.Widget(th, "Не вдалося завантажити голоси"),
				gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 3}.Widget(th, core.ErrorText(err)),
				func(gtx C) D {
					return layout.Inset{Top: gui.Sp4}.Layout(gtx, func(gtx C) D {
						return gui.HStack(gtx, gui.Sp8,
							func(gtx C) D {
								return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconRefreshCw, Text: "Оновити"}.Layout(gtx, th, &s.reload)
							},
							func(gtx C) D {
								return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Text: "Налаштування озвучки"}.Layout(gtx, th, &s.settings)
							},
						)
					})
				},
			)
		})
	}
	service := "сервісу озвучки"
	if l := currentProviderLabel(c); l != "" {
		service = l
	}
	return in.Layout(gtx, func(gtx C) D {
		return gui.VStack(gtx, gui.Sp8,
			gui.Text{Style: th.BodyStrong}.Widget(th, "Голосів поки немає"),
			gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 3}.Widget(th,
				"Додай ключ "+service+" в Налаштуваннях — і тут з'являться голоси акаунта з прослуховуванням."),
			func(gtx C) D {
				return layout.Inset{Top: gui.Sp4}.Layout(gtx, func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Size: gui.Sm, Icon: gui.IconSettings, Text: "Відкрити налаштування"}.Layout(gtx, th, &s.settings)
				})
			},
		)
	})
}

// voiceRowH is the height of a voice row (dp).
const voiceRowH unit.Dp = 56

// Open opens the list (as if the field was pressed).
func (s *Voice) Open() { s.pop.openNow() }

// Close closes the list.
func (s *Voice) Close() { s.pop.open = false }
