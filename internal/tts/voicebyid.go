package tts

// Voices by id («Вставити ID голосу» in the voice pickers): a voice the
// account list does not show — a Cartesia library voice, an ElevenLabs Voice
// Library voice — found by its id with a free read (never a synthesis).
// Checked against the docs 2026-10-02:
//
//	Cartesia   GET /voices/{id}?expand[]=preview_file_url (Authorization: Bearer
//	           sk_car_…, Cartesia-Version 2026-08-14) → {id, name, description,
//	           gender masculine|feminine|gender_neutral, language, accents[{accent,
//	           locale, is_native}], is_owner, status active|archived, access
//	           public|private, preview_file_url}; 404 = no such voice.
//	           (docs.cartesia.ai/api-reference/voices/get)
//	ElevenLabs GET /v1/voices/{voice_id} (xi-api-key) → the account's and the
//	           default voices {voice_id, name, category, labels{gender, accent,
//	           age, language}, preview_url}; not found (400 voice_does_not_exist,
//	           404 voice_not_found, 422) → the Voice Library: GET
//	           /v1/shared-voices?search={id}&page_size=30 {voices[{voice_id, name,
//	           gender, age, accent, language, locale, preview_url}]} matched on
//	           voice_id. Library voices work through the API on paid plans only.
//	           (elevenlabs.io/docs/api-reference/voices/get, …/voice-library/get-shared)

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"ytcartell/internal/config"
)

// VoiceByIDProviders are the providers whose voices can be added by id.
var VoiceByIDProviders = []string{"cartesia", "elevenlabs"}

// CanLookupVoice reports whether provider id finds voices by id.
func CanLookupVoice(provider string) bool {
	provider = normProvider(provider)
	for _, p := range VoiceByIDProviders {
		if p == provider {
			return true
		}
	}
	return false
}

// ErrVoiceNotFound: the provider has no voice with that id.
var ErrVoiceNotFound = errors.New("голос не знайдено")

// VoiceIDExample is how a voice id of provider looks ("" = no fixed shape).
func VoiceIDExample(provider string) string {
	switch normProvider(provider) {
	case "cartesia":
		return "694f9389-aac1-45b6-b726-9d9369183238"
	case "elevenlabs":
		return "JBFqnCBsd6RMkjVDRZzb"
	}
	return ""
}

// VoiceIDProblem says why id cannot be a voice id of provider ("" = it can):
// empty, another provider's id (by its shape), or the wrong shape.
func VoiceIDProblem(provider, id string) string {
	provider = normProvider(provider)
	id = CleanVoiceID(id)
	p, _ := ProviderByID(provider)
	label := firstNonEmpty(p.Label, provider)
	switch {
	case id == "":
		return "Встав ID голосу"
	case strings.ContainsAny(id, " /?#"):
		return "ID голосу — це один рядок без пробілів, напр. " + VoiceIDExample(provider)
	}
	if owner := config.TTSVoiceOwner(id); owner != "" && owner != provider {
		o, _ := ProviderByID(owner)
		return fmt.Sprintf("Це ID голосу %s, а озвучка зараз — %s. Встав ID голосу %s.", firstNonEmpty(o.Label, owner), label, label)
	}
	switch provider {
	case "cartesia":
		if config.TTSVoiceOwner(id) != "cartesia" {
			return "ID голосу Cartesia має вигляд " + VoiceIDExample(provider) + " (36 символів з дефісами)"
		}
	case "elevenlabs":
		if config.TTSVoiceOwner(id) != "elevenlabs" {
			return "ID голосу ElevenLabs — 20 латинських літер і цифр, напр. " + VoiceIDExample(provider)
		}
	}
	return ""
}

// CleanVoiceID trims a pasted id: spaces, quotes, and a link's last part
// ("…/voices/694f9389-…" → the id).
func CleanVoiceID(id string) string {
	id = strings.Trim(strings.TrimSpace(id), "\"'`«»“”<>")
	if i := strings.LastIndex(id, "/"); i >= 0 && strings.Contains(id, "://") {
		id = id[i+1:]
	}
	if i := strings.IndexAny(id, "?#"); i >= 0 && strings.Contains(id, "=") {
		id = id[:i]
	}
	return strings.TrimSpace(id)
}

// LookupVoice finds a voice of the configured provider by its id (Cartesia,
// ElevenLabs): ErrVoiceNotFound when the provider has none, a friendly error
// for a wrong shape, a missing key or a refused one.
func (c *Client) LookupVoice(ctx context.Context, id string) (Voice, error) {
	s := c.settings()
	id = CleanVoiceID(id)
	if !CanLookupVoice(s.provider) {
		return Voice{}, fmt.Errorf("%s не шукає голоси за ID — вибери голос зі списку", firstNonEmpty(s.info.Label, s.provider))
	}
	if msg := VoiceIDProblem(s.provider, id); msg != "" {
		return Voice{}, errors.New(msg)
	}
	if len(s.keys) == 0 {
		return Voice{}, errNoKey(s)
	}
	var v Voice
	var found bool
	var err error
	switch s.provider {
	case "cartesia":
		v, found, err = c.cartesiaVoice(ctx, s, id)
	default:
		v, found, err = c.elevenVoice(ctx, s, id)
	}
	if err != nil {
		return Voice{}, err
	}
	if !found {
		return Voice{}, fmt.Errorf("%w: у %s немає голосу з ID %s — перевір, чи скопійовано весь ID", ErrVoiceNotFound, s.info.Label, id)
	}
	return v, nil
}

// cartesiaVoice: GET /voices/{id}.
func (c *Client) cartesiaVoice(ctx context.Context, s settings, id string) (Voice, bool, error) {
	var raw cartesiaVoice
	var status string
	found := false
	err := c.run(ctx, s, "", 0, true, func(key string) (int, error) {
		found = false
		res, err := c.do(ctx, http.MethodGet, cartesiaBase+"/voices/"+url.PathEscape(id)+"?expand%5B%5D=preview_file_url", cartesiaHeaders(key), nil, listTimeout, 1<<20)
		if err != nil {
			return 0, netErr(ctx, "Cartesia", err)
		}
		if res.status == http.StatusNotFound || (res.status == http.StatusBadRequest && strings.Contains(strings.ToLower(string(res.body)), "not found")) {
			return 0, nil // no such voice: not this key's fault
		}
		if !res.ok() {
			return 0, cartesiaErr(res, "")
		}
		var j struct {
			cartesiaVoice
			Status string `json:"status"`
		}
		if err := json.Unmarshal(res.body, &j); err != nil || j.ID == "" {
			return 0, fatal("Cartesia: незрозуміла відповідь про голос: " + clip(string(res.body), 200))
		}
		raw, status, found = j.cartesiaVoice, j.Status, true
		return 0, nil
	})
	if err != nil || !found {
		return Voice{}, found, err
	}
	if status == "archived" {
		return Voice{}, false, fmt.Errorf("Cartesia: голос %s архівовано — його вже не можна озвучувати", firstNonEmpty(raw.Name, id))
	}
	return cartesiaVoiceOf(raw), true, nil
}

// cartesiaVoiceOf maps a Cartesia voice onto Voice.
func cartesiaVoiceOf(v cartesiaVoice) Voice {
	loc, accent := v.Language, ""
	for _, a := range v.Accents {
		if loc == "" || a.IsNative {
			loc, accent = firstNonEmpty(a.Locale, loc), a.Accent
			if a.IsNative {
				break
			}
		}
	}
	g := ""
	if v.Gender != nil {
		g = gender(*v.Gender)
	}
	cat := "premade"
	if v.IsOwner {
		cat = "cloned"
	}
	prev := ""
	if v.PreviewURL != nil {
		prev = *v.PreviewURL
	}
	return Voice{ID: v.ID, Name: firstNonEmpty(v.Name, v.ID), Category: cat, Gender: g,
		Accent: firstNonEmpty(strings.ToLower(accent), accentOf(loc)), PreviewURL: prev, Language: loc, Usable: true}
}

// elevenVoice: GET /v1/voices/{id}, else the Voice Library.
func (c *Client) elevenVoice(ctx context.Context, s settings, id string) (Voice, bool, error) {
	var v Voice
	found := false
	err := c.run(ctx, s, "", 0, true, func(key string) (int, error) {
		found = false
		hdr := map[string]string{"xi-api-key": key}
		res, err := c.do(ctx, http.MethodGet, elevenBase+"/v1/voices/"+url.PathEscape(id), hdr, nil, listTimeout, 1<<20)
		if err != nil {
			return 0, netErr(ctx, "ElevenLabs", err)
		}
		if res.ok() {
			var ev elVoice
			if err := json.Unmarshal(res.body, &ev); err != nil || ev.VoiceID == "" {
				return 0, fatal("ElevenLabs: незрозуміла відповідь про голос: " + clip(string(res.body), 200))
			}
			list := elevenVoiceList([]elVoice{ev}, c.elevenFree(s))
			v, found = list[0], true
			if l := str(ev.Labels["language"]); l != "" {
				v.Language = l
			}
			return 0, nil
		}
		code, _ := elevenDetail(res.body)
		missing := res.status == http.StatusNotFound || res.status == http.StatusUnprocessableEntity ||
			code == "voice_not_found" || code == "voice_does_not_exist" || code == "invalid_uid"
		if !missing {
			return 0, elevenErr(res)
		}
		// Not in the account: the Voice Library.
		q := url.Values{"search": {id}, "page_size": {"30"}}
		res, err = c.do(ctx, http.MethodGet, elevenBase+"/v1/shared-voices?"+q.Encode(), hdr, nil, listTimeout, 4<<20)
		if err != nil {
			return 0, netErr(ctx, "ElevenLabs", err)
		}
		if !res.ok() {
			if res.status == http.StatusNotFound || res.status == http.StatusUnprocessableEntity {
				return 0, nil
			}
			return 0, elevenErr(res)
		}
		var j struct {
			Voices []struct {
				VoiceID    string `json:"voice_id"`
				Name       string `json:"name"`
				Gender     string `json:"gender"`
				Age        string `json:"age"`
				Accent     string `json:"accent"`
				Language   string `json:"language"`
				Locale     string `json:"locale"`
				PreviewURL string `json:"preview_url"`
			} `json:"voices"`
		}
		_ = json.Unmarshal(res.body, &j)
		for _, sv := range j.Voices {
			if sv.VoiceID != id {
				continue
			}
			v = Voice{ID: sv.VoiceID, Name: firstNonEmpty(strings.TrimSpace(strings.SplitN(sv.Name, " - ", 2)[0]), sv.VoiceID),
				Category: "library", Gender: gender(sv.Gender), Age: sv.Age, Accent: sv.Accent,
				Language: firstNonEmpty(sv.Locale, sv.Language), PreviewURL: sv.PreviewURL, Usable: true}
			if c.elevenFree(s) {
				v.Usable, v.Locked = false, LockedPaidEleven
			}
			found = true
			break
		}
		return 0, nil
	})
	return v, found, err
}

// elevenFree: every key with a known plan is on the free plan (library voices
// do not work through the API then).
func (c *Client) elevenFree(s settings) bool {
	plans := c.pool.plans(s)
	if len(plans) == 0 {
		return false
	}
	for _, p := range plans {
		if p != "free" {
			return false
		}
	}
	return true
}
