package home

// The story card's «Звук» and «Що в кадрі» chips: the sound and content
// modes of the next project (studio.Input.AudioMode, Content) — the picked
// ones, else the profile's.

import (
	"ytcartell/internal/appui/pickers"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// heroModes are the modes of the story card.
type heroModes struct {
	pickedSound, pickedContent string // "" = the profile's
	sound, content             string // what the chips show (update)
}

// update works out what the chips show for the profile (has = a profile is used).
func (m *heroModes) update(pr config.Profile, has bool) {
	in := studio.DefaultInput()
	if has {
		in.AudioMode, in.Voiceover, in.Content = pr.AudioMode, pr.Voiceover, pr.Content
	}
	m.sound, m.content = in.SoundMode(), in.ContentMode()
	if m.pickedSound != "" {
		m.sound = m.pickedSound
	}
	if m.pickedContent != "" {
		m.content = m.pickedContent
	}
}

// pick records a choice from a chip's list ("sound" or "content").
func (m *heroModes) pick(id, v string) {
	if id == "sound" {
		m.pickedSound, m.sound = v, v
	} else {
		m.pickedContent, m.content = v, v
	}
}

func (m *heroModes) reset() { m.pickedSound, m.pickedContent = "", "" }

// apply puts the picked modes into the new project's options.
func (m *heroModes) apply(opt *studio.NewProjectOptions) {
	opt.AudioMode, opt.Content = m.pickedSound, m.pickedContent
}

// modeItems are the rows of a mode chip's list.
func modeItems(list []pickers.ModeInfo) []popItem {
	items := make([]popItem, 0, len(list))
	for _, m := range list {
		items = append(items, popItem{Value: m.Value, Label: m.Label, Hint: modeHints[m.Value], Icon: m.Icon})
	}
	return items
}

// modeHints are the short lines of the chip lists (the full ones live on step 1).
var modeHints = map[string]string{
	studio.AudioVoiceover:    "диктор читає текст",
	studio.AudioInVideo:      "герої говорять у кадрі",
	studio.AudioSilent:       "музика й звуки",
	studio.ContentCharacters: "герої історії",
	studio.ContentNoPeople:   "місця й предмети",
	studio.ContentMotion:     "графіка й текст",
	studio.ContentMixed:      "герої й b-roll",
}

// modeChips are the two chips (their lists open under them).
func (h *hero) modeChips(gtx C, th *gui.Theme) D {
	s := pickers.ModeByValue(pickers.SoundModes, h.modes.sound)
	c := pickers.ModeByValue(pickers.ContentModes, h.modes.content)
	return gui.Flow{Gap: gui.Sp8}.Layout(gtx,
		func(gtx C) D {
			return gui.Anchor(gtx, th, "home.sound", func(gtx C) D {
				return gui.Chip{Text: s.Label, Icon: s.Icon, Tip: "Звук: " + s.Caption}.Layout(gtx, th, &h.sound)
			})
		},
		func(gtx C) D {
			return gui.Anchor(gtx, th, "home.content", func(gtx C) D {
				return gui.Chip{Text: c.Label, Icon: c.Icon, Tip: "Що в кадрі: " + c.Caption}.Layout(gtx, th, &h.content)
			})
		},
	)
}
