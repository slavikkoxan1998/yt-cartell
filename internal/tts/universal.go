package tts

// «Власний сервер» described by a spec (config.CustomAPI.TTSSpec): the
// universal adapter of package providers (KindTTS — {{text}} {{voice}}
// {{model}} {{language}} {{language_name}} {{speed}} {{format}}; the audio is
// the reply, a URL, a data: URI or base64, synchronous or an async job). The
// address, key(s) and model are the «Власний сервер» fields of Озвучка; the
// keys rotate through the pool like any provider's. Without a spec the plain
// own-server protocol of openai.go stays.

import (
	"context"
	"errors"
	"strings"

	universal "ytcartell/internal/providers"
)

// ttsSpec is the spec of the own server ("" = the plain protocol).
func (c *Client) ttsSpec() string { return strings.TrimSpace(c.get().Custom.TTSSpec) }

func (c *Client) specAPI(s settings, spec, key string) universal.AudioAPI {
	return universal.AudioAPI{Kind: universal.KindTTS, BaseURL: s.base, Key: key, Model: s.model, Spec: spec, HTTP: c.http}
}

func (c *Client) customSpec(ctx context.Context, s settings, spec, text, voice string, opt SynthOptions) ([]byte, error) {
	var out []byte
	err := c.run(ctx, s, voice, runes(text), false, func(key string) (int, error) {
		api := c.specAPI(s, spec, key)
		if m := strings.TrimSpace(opt.Model); m != "" {
			api.Model = m
		}
		data, _, err := api.Speech(ctx, universal.SpeechRequest{Text: text, Voice: voice, Language: langCode(opt.LanguageCode), Speed: opt.Speed})
		if err != nil {
			return 0, specErr(err)
		}
		out = data
		return 0, nil
	})
	return out, err
}

// specErr maps the adapter's error to the key pool's kinds.
func specErr(err error) error {
	var pe *universal.Error
	if !errors.As(err, &pe) {
		return err
	}
	switch {
	case pe.Status == 401:
		return &apiErr{kind: failAuth, msg: pe.Msg}
	case pe.Status == 402:
		return &apiErr{kind: failQuota, msg: pe.Msg}
	case pe.Status == 429:
		return &apiErr{kind: failRate, msg: pe.Msg}
	case pe.Retry:
		return &apiErr{kind: failTransient, msg: pe.Msg}
	}
	return fatal(pe.Msg)
}

// customSpecProbe is the spec's "test" call (nothing is synthesised).
func (c *Client) customSpecProbe(ctx context.Context, s settings, spec, key string) probeResult {
	out := probeResult{used: -1}
	ok, msg := c.specAPI(s, spec, key).Test(ctx)
	if !ok {
		kind := failFatal
		switch {
		case strings.Contains(msg, "(401)"):
			kind = failAuth
		case strings.Contains(msg, "закінчились кошти"):
			kind = failQuota
		}
		out.err = &apiErr{kind: kind, msg: msg}
		return out
	}
	out.info = msg
	return out
}
