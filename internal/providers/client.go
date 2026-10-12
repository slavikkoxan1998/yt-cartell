// Package providers is the user's own image / video generation API
// (config.CustomAPI; Image/Video.Provider "custom"): presets for well-known
// APIs — OpenAI Images, fal.ai, Replicate, Runway, Luma, MiniMax, RoyalTechno — plus any
// OpenAI-compatible images endpoint, with polling, progress, cancellation,
// retries with backoff and friendly (Ukrainian) errors. Keys never reach logs
// or messages (redact).
//
//	images  openai · openai-compat · fal · replicate · royaltechno · universal
//	video   fal · replicate · runway · luma · minimax · royaltechno · universal
//
// "universal" speaks any API the user describes in JSON (Spec, universal.go:
// synchronous or async jobs; ready templates in SpecTemplates; the text model
// can draft one from the API's docs — FetchDocs, SpecPrompt, ParseSpecReply).
// Presets (ImagePresets / VideoPresets) carry the defaults and capabilities
// the Settings screen shows; VideoCapsFor / ImageCapsFor give a model's
// (SpecCaps a universal spec's).
package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"ytcartell/internal/config"
	"ytcartell/internal/gen"
	"ytcartell/internal/httpdiag"
)

// Client generates with the configured custom API. Settings are read on
// every call, so changes in the Settings screen apply immediately. It keeps
// no other state: creating one per call is fine.
type Client struct {
	get  func() config.Config
	http *http.Client
}

// New creates a client reading its settings from get. Requests go through
// http.DefaultTransport (shared connection pool).
func New(get func() config.Config) *Client {
	return &Client{get: get, http: &http.Client{}}
}

// protocol is one preset's wire protocol.
type protocol struct {
	name        string // display name in messages
	keyOptional bool   // an OpenAI-compatible local server may need no key
	auth        func(h http.Header, key string)
	image       func(ctx context.Context, ep *endpoint, r gen.ImageRequest) ([]byte, string, error)
	video       func(ctx context.Context, ep *endpoint, r gen.VideoRequest, progress gen.Progress) ([]byte, error)
	// test is a cheap authenticated call (never a paid generation); it returns
	// a short note for the success message.
	test func(ctx context.Context, ep *endpoint) (string, error)
	// untested (universal spec without "test"): the check is this message,
	// with no network call.
	untested string
}

func bearer(h http.Header, key string) { h.Set("Authorization", "Bearer "+key) }

func protocolFor(id string) (*protocol, bool) {
	switch id {
	case PresetOpenAI:
		return &protocol{name: "OpenAI", auth: bearer, image: openaiImage, test: openaiTest}, true
	case PresetOpenAICompat:
		return &protocol{name: "OpenAI-сумісний сервер", keyOptional: true, auth: bearer, image: openaiImage, test: openaiTest}, true
	case PresetFal:
		return &protocol{name: "fal.ai", auth: falAuth, image: falImage, video: falVideo, test: falTest}, true
	case PresetReplicate:
		return &protocol{name: "Replicate", auth: bearer, image: replicateImage, video: replicateVideo, test: replicateTest}, true
	case PresetRunway:
		return &protocol{name: "Runway", auth: runwayAuth, video: runwayVideo, test: runwayTest}, true
	case PresetLuma:
		return &protocol{name: "Luma", auth: bearer, video: lumaVideo, test: lumaTest}, true
	case PresetMiniMax:
		return &protocol{name: "MiniMax", auth: bearer, video: minimaxVideo, test: minimaxTest}, true
	case PresetRoyal:
		return &protocol{name: "RoyalTechno", auth: bearer, image: royalImage, video: royalVideo, test: royalTest}, true
	}
	return nil, false
}

// Image renders one still and returns its bytes and MIME type.
func (c *Client) Image(ctx context.Context, r gen.ImageRequest) ([]byte, string, error) {
	ep, p, err := c.imageEndpoint()
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return nil, "", ep.errf(0, false, "%s: порожній промт", ep.name)
	}
	return p.image(ctx, ep, r)
}

// Video renders one clip and returns the mp4 bytes. r.Seconds (0 = Settings)
// is snapped to what the model renders; r.Aspect to what it accepts.
func (c *Client) Video(ctx context.Context, r gen.VideoRequest, progress gen.Progress) ([]byte, error) {
	if progress == nil {
		progress = gen.NoProgress
	}
	cfg := c.get()
	ep, p, err := c.videoEndpoint()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.Prompt) == "" {
		return nil, ep.errf(0, false, "%s: потрібен опис руху (motion prompt)", ep.name)
	}
	caps := VideoCapsFor(cfg.Custom.VideoPreset, ep.model)
	if strings.TrimSpace(cfg.Custom.VideoPreset) == PresetUniversal {
		caps = SpecCaps(cfg.Custom.VideoSpec).Video
	}
	sec := float64(r.Seconds)
	if sec <= 0 {
		sec = float64(cfg.Video.Length)
	}
	r.Seconds = caps.Snap(sec, caps.Snap(8, 8))
	r.Aspect = caps.Aspect(r.Aspect)
	if r.Mode == gen.ModeComponents && (!caps.Components || len(usableRefs(r.Refs, -1)) == 0) {
		r.Mode = gen.ModeAuto // falls back to the start frame
	}
	if r.Mode == gen.ModeComponents && caps.MaxRefs > 0 {
		r.Refs = usableRefs(r.Refs, caps.MaxRefs)
	}
	// A model without a start-frame field (reference-to-video) still gets the
	// frame — as its only reference (schema.build).
	if r.Start != nil && len(r.Start.Data) == 0 {
		r.Start = nil
	}
	return p.video(ctx, ep, r, progress)
}

// TestImage checks the image API with a cheap authenticated call (nothing is
// generated or billed).
func (c *Client) TestImage(ctx context.Context) (bool, string) {
	ep, p, err := c.imageEndpoint()
	if err != nil {
		return false, err.Error()
	}
	return runTest(ctx, ep, p)
}

// TestVideo checks the video API with a cheap authenticated call (nothing is
// generated or billed).
func (c *Client) TestVideo(ctx context.Context) (bool, string) {
	ep, p, err := c.videoEndpoint()
	if err != nil {
		return false, err.Error()
	}
	return runTest(ctx, ep, p)
}

func runTest(ctx context.Context, ep *endpoint, p *protocol) (bool, string) {
	if p.untested != "" {
		return true, redact(p.untested, ep.key)
	}
	tctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	tctx, trace := httpdiag.WithTrace(tctx) // the failed request goes under the message
	note, err := p.test(tctx, ep)
	if err != nil {
		if ctx.Err() != nil {
			return false, "Перевірку скасовано"
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return false, trace.Append(fmt.Sprintf("%s: сервер не відповів за %s — перевір адресу API та інтернет", ep.name, humanDuration(testTimeout)), ep.key)
		}
		var pe *Error
		if errors.As(err, &pe) {
			return false, trace.Append(pe.Msg, ep.key)
		}
		return false, trace.Append(redact(err.Error(), ep.key), ep.key)
	}
	msg := ep.name + ": ключ працює"
	if ep.key == "" {
		msg = ep.name + ": сервер відповідає"
	}
	if ep.model != "" {
		msg += " · модель " + ep.model
	}
	if note = strings.TrimSpace(note); note != "" {
		msg += " · " + note
	}
	return true, redact(msg, ep.key)
}

func (c *Client) imageEndpoint() (*endpoint, *protocol, error) {
	cu := c.get().Custom
	ep, p, err := c.endpoint(cu.ImagePreset, cu.ImageBaseURL, cu.ImageKey, cu.ImageModel, cu.ImageSpec, imagePresetList(), "кадрів")
	if err == nil && p.image == nil {
		err = &Error{Msg: fmt.Sprintf("Власний API: %s не малює кадри — обери інший сервіс", p.name)}
	}
	return ep, p, err
}

func (c *Client) videoEndpoint() (*endpoint, *protocol, error) {
	cu := c.get().Custom
	ep, p, err := c.endpoint(cu.VideoPreset, cu.VideoBaseURL, cu.VideoKey, cu.VideoModel, cu.VideoSpec, videoPresetList(), "відео")
	if err == nil && p.video == nil {
		err = &Error{Msg: fmt.Sprintf("Власний API: %s не знімає відео — обери інший сервіс", p.name)}
	}
	return ep, p, err
}

// endpoint resolves a preset + the user's overrides into a ready endpoint
// (spec: the universal preset's description of the API).
func (c *Client) endpoint(presetID, base, key, model, spec string, list []Preset, what string) (*endpoint, *protocol, error) {
	presetID = strings.TrimSpace(presetID)
	if presetID == PresetUniversal {
		return c.universalEndpoint(base, key, model, spec, what)
	}
	pr, ok := findPreset(list, presetID)
	p, known := protocolFor(presetID)
	if !ok || !known {
		if presetID == "" {
			return nil, nil, &Error{Msg: fmt.Sprintf("Власний API: обери сервіс для %s у Налаштуваннях → Власний API", what)}
		}
		return nil, nil, &Error{Msg: fmt.Sprintf("Власний API: невідомий сервіс «%s» для %s — обери його знову в Налаштуваннях", clip(presetID, 40), what)}
	}
	base = strings.TrimRight(firstNonEmpty(base, pr.DefaultBaseURL), "/")
	if base == "" {
		return nil, nil, &Error{Msg: fmt.Sprintf("%s: вкажи адресу API (Налаштування → Власний API)", p.name)}
	}
	if u, err := url.Parse(base); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, nil, &Error{Msg: fmt.Sprintf("%s: некоректна адреса API «%s» — потрібно http(s)://…", p.name, clip(redact(base, key), 80))}
	}
	key = strings.TrimSpace(key)
	if key == "" && !p.keyOptional {
		return nil, nil, &Error{Msg: fmt.Sprintf("%s: не задано API-ключ (Налаштування → Власний API). %s", p.name, pr.KeyHint)}
	}
	ep := &endpoint{
		name:  p.name,
		base:  base,
		key:   key,
		model: firstNonEmpty(model, pr.DefaultModel),
		hc:    c.http,
		auth:  p.auth,
	}
	if ep.hc == nil {
		ep.hc = &http.Client{}
	}
	return ep, p, nil
}
