package settings

// The universal adapter for audio (customspec.go's panel with an audio
// specHost): «Власний сервер» of Озвучка (config.CustomAPI.TTSSpec) and
// «Своє API» of Розпізнавання мови (config.CustomAPI.STTSpec). The address,
// key and model are the card's own fields; the panel adds the JSON
// description, its templates (OpenAI /audio/speech, ElevenLabs, Lumean,
// Whisper, Scribe …) and «Налаштувати з документації (ШІ)».

import (
	"strings"

	"gioui.org/layout"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/gui"
	"ytcartell/internal/providers"
)

// ttsSpecPanel is the description block of «Власний сервер» (Озвучка).
func (p *Page) ttsSpecPanel(c *core.Ctx, flash func()) layout.Widget {
	s := &p.tts
	cfg := p.now
	h := &specHost{kind: providers.KindTTS, sp: &s.spec, check: &s.check, stored: cfg.Custom.TTSSpec,
		base: cfg.TTS.BaseURL, model: cfg.TTS.Model,
		save: func(v, base, model string) {
			p.cfg.set(c, func(cf *config.Config) {
				cf.Custom.TTSSpec = v
				if base != "" {
					cf.TTS.BaseURL = base
				}
				if model != "" {
					cf.TTS.Model = model
				}
			}, flash)
			s.check.reset()
		}}
	sub := "Опиши API в JSON (або візьми шаблон, або налаштуй з документації) — підійде будь-який сервіс озвучки."
	if strings.TrimSpace(cfg.Custom.TTSSpec) == "" {
		sub = "Без опису — простий протокол: POST на адресу {text, voice, model}. З описом — будь-яке API озвучки: синхронне чи із задачами."
	}
	return audioSpecInset(c, h, "Опис API озвучки", sub, p.specBlocks(c, h))
}

// sttSpecPanel is the description block of «Своє API» (Розпізнавання мови).
func (p *Page) sttSpecPanel(c *core.Ctx, flash func()) layout.Widget {
	s := &p.stt
	cfg := p.now
	h := &specHost{kind: providers.KindSTT, sp: &s.spec, check: &s.check, stored: cfg.Custom.STTSpec,
		base: cfg.STT.BaseURL, model: cfg.STT.Model,
		save: func(v, base, model string) {
			p.cfg.set(c, func(cf *config.Config) {
				cf.Custom.STTSpec = v
				if base != "" {
					cf.STT.BaseURL = base
				}
				if model != "" {
					cf.STT.Model = model
				}
			}, flash)
			s.check.reset()
		}}
	sub := "Як надіслати запис і де у відповіді слова з таймкодами. Підійде JSON ElevenLabs, Whisper, Deepgram, AssemblyAI, а також SRT чи VTT."
	return audioSpecInset(c, h, "Опис API розпізнавання", sub, p.specBlocks(c, h))
}

func audioSpecInset(c *core.Ctx, h *specHost, title, sub string, blocks []layout.Widget) layout.Widget {
	th := c.Th
	return func(gtx C) D {
		ws := append([]layout.Widget{func(gtx C) D { return insetHead(gtx, th, gui.IconFileText, title, sub, nil) }}, blocks...)
		return inset(gtx, th, func(gtx C) D { return gui.VStack(gtx, gui.Sp16, ws...) })
	}
}
