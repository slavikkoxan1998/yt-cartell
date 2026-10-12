package config

// TTS (voice-over) settings, kept per provider.
//
// Every provider keeps its own key list, address, model and default voice in
// Providers (keyed by the preset ids of tts.Providers). The flat fields
// (APIKey, Keys, BaseURL, Model, Voice) are the ACTIVE provider's view — the
// rest of the app reads only them — and Normalize writes them back into
// Providers[Provider]. Switch stores the view under the old provider and
// loads the new provider's own settings (empty when it was never set up);
// assigning Provider directly behaves the same (fields edited in the same
// change go to the new provider).
//
// Migration (config.json written before Providers existed, flat fields only):
// the flat fields become the active provider's settings, except keys, a voice
// or a model whose shape surely belongs to another preset — e.g. ElevenLabs
// sk_… keys and voice id left under Cartesia by the old shared list — which
// move to that provider. Nothing is dropped.

import (
	"regexp"
	"strings"
)

// TTS is the voice-over section of the settings.
type TTS struct {
	// Provider is the active preset: "elevenlabs" | "cartesia" | "openai" |
	// "google" | "azure" | "minimax" | "fish" | "custom" | "lumean".
	Provider string `json:"provider"`
	// The active provider's settings (a view of Providers[Provider]).
	APIKey  string `json:"apiKey"`
	BaseURL string `json:"baseUrl"`
	Voice   string `json:"voice"` // default narrator voice id
	Model   string `json:"model"`
	// Keys are extra API keys of the same provider, rotated with APIKey:
	// a key whose monthly quota ran out rests until the 1st of next month,
	// a failing key is skipped.
	Keys []string `json:"keys,omitempty"`
	// Providers holds every provider's own settings (the active one too).
	Providers map[string]TTSProvider `json:"providers,omitempty"`
	// ByID is «Мої голоси за ID» per provider: voices the user pasted by id
	// (a Cartesia or ElevenLabs library voice) after the provider's API found
	// them. The pickers list them next to the account's voices.
	ByID map[string][]VoiceRef `json:"voicesById,omitempty"`

	// view is the provider the flat fields were last normalised for ("" =
	// fresh from JSON or a literal: they belong to Provider).
	view string
}

// VoiceRef is a voice remembered by its id (what the provider said about it).
type VoiceRef struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Language string `json:"language,omitempty"` // locale or language code ("uk", "en-US")
	Gender   string `json:"gender,omitempty"`   // male | female | neutral
	// Library: a Voice Library voice of another account (ElevenLabs: usable
	// through the API only on a paid plan).
	Library bool `json:"library,omitempty"`
}

// maxVoicesByID caps «Мої голоси за ID» per provider.
const maxVoicesByID = 50

// VoicesByID is «Мої голоси за ID» of provider id (a copy, newest first).
func (t TTS) VoicesByID(id string) []VoiceRef {
	return append([]VoiceRef(nil), t.ByID[normTTSProvider(id)]...)
}

// SaveVoiceByID remembers v for provider id (first in the list; an id already
// there is replaced).
func (t *TTS) SaveVoiceByID(id string, v VoiceRef) {
	v = v.clean()
	if v.ID == "" {
		return
	}
	id = normTTSProvider(id)
	list := []VoiceRef{v}
	for _, x := range t.ByID[id] {
		if x.ID != v.ID {
			list = append(list, x)
		}
	}
	m := make(map[string][]VoiceRef, len(t.ByID)+1)
	for k, l := range t.ByID {
		m[k] = l
	}
	m[id] = list
	t.ByID = m
}

// ForgetVoiceByID drops voice from provider id's «Мої голоси за ID».
func (t *TTS) ForgetVoiceByID(id, voice string) {
	id = normTTSProvider(id)
	var list []VoiceRef
	for _, x := range t.ByID[id] {
		if x.ID != strings.TrimSpace(voice) {
			list = append(list, x)
		}
	}
	m := make(map[string][]VoiceRef, len(t.ByID))
	for k, l := range t.ByID {
		m[k] = l
	}
	if len(list) == 0 {
		delete(m, id)
	} else {
		m[id] = list
	}
	t.ByID = m
}

func (v VoiceRef) clean() VoiceRef {
	v.ID = strings.TrimSpace(v.ID)
	v.Name = strings.TrimSpace(v.Name)
	v.Language = strings.TrimSpace(v.Language)
	v.Gender = strings.TrimSpace(v.Gender)
	return v
}

// normalizeByID trims the book: known providers, no empty or repeated ids,
// at most maxVoicesByID each (a fresh map: never mutates a shared one).
func normalizeByID(in map[string][]VoiceRef) map[string][]VoiceRef {
	if len(in) == 0 {
		return nil
	}
	out := map[string][]VoiceRef{}
	for id, list := range in {
		id = strings.ToLower(strings.TrimSpace(id))
		if !validTTSProvider(id) {
			continue
		}
		seen := map[string]bool{}
		var keep []VoiceRef
		for _, v := range append(out[id], list...) {
			if v = v.clean(); v.ID != "" && !seen[v.ID] && len(keep) < maxVoicesByID {
				seen[v.ID] = true
				keep = append(keep, v)
			}
		}
		if len(keep) > 0 {
			out[id] = keep
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// TTSProvider is one provider's own settings.
type TTSProvider struct {
	APIKey  string   `json:"apiKey,omitempty"`
	Keys    []string `json:"keys,omitempty"`
	BaseURL string   `json:"baseUrl,omitempty"`
	Model   string   `json:"model,omitempty"`
	Voice   string   `json:"voice,omitempty"`
}

// TTSProviderIDs are the stored provider ids (never rename them).
var TTSProviderIDs = []string{"elevenlabs", "cartesia", "openai", "google", "azure", "minimax", "fish", "custom", "lumean"}

func validTTSProvider(id string) bool {
	for _, p := range TTSProviderIDs {
		if p == id {
			return true
		}
	}
	return false
}

// normTTSProvider: unknown or empty ids fall back to ElevenLabs.
func normTTSProvider(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "openai-compatible" {
		id = "openai"
	}
	if validTTSProvider(id) {
		return id
	}
	return "elevenlabs"
}

// AllKeys is the rotation list: APIKey, then Keys (trimmed, no repeats).
func (p TTSProvider) AllKeys() []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range append([]string{p.APIKey}, p.Keys...) {
		if k = strings.TrimSpace(k); k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

// Empty: nothing is set.
func (p TTSProvider) Empty() bool {
	return strings.TrimSpace(p.APIKey) == "" && len(p.AllKeys()) == 0 && p.BaseURL == "" && p.Model == "" && p.Voice == ""
}

// clean trims the fields and drops empty and repeated keys (a fresh slice).
func (p TTSProvider) clean() TTSProvider {
	p.APIKey = strings.TrimSpace(p.APIKey)
	p.BaseURL = strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	p.Model = strings.TrimSpace(p.Model)
	p.Voice = strings.TrimSpace(p.Voice)
	var keys []string
	seen := map[string]bool{p.APIKey: p.APIKey != ""}
	for _, k := range p.Keys {
		if k = strings.TrimSpace(k); k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	p.Keys = keys
	return p
}

// withKeys stores a rotation list back (first → APIKey).
func (p TTSProvider) withKeys(keys []string) TTSProvider {
	p.APIKey, p.Keys = "", nil
	all := TTSProvider{Keys: keys}.AllKeys()
	if len(all) > 0 {
		p.APIKey, p.Keys = all[0], all[1:]
	}
	if len(p.Keys) == 0 {
		p.Keys = nil
	}
	return p
}

// Active is the active provider's settings (the flat fields).
func (t TTS) Active() TTSProvider {
	return TTSProvider{APIKey: t.APIKey, Keys: append([]string(nil), t.Keys...), BaseURL: t.BaseURL, Model: t.Model, Voice: t.Voice}
}

func (t *TTS) setActive(p TTSProvider) {
	t.APIKey, t.Keys, t.BaseURL, t.Model, t.Voice = p.APIKey, append([]string(nil), p.Keys...), p.BaseURL, p.Model, p.Voice
	if len(t.Keys) == 0 {
		t.Keys = nil
	}
}

// Of is the stored settings of provider id (the flat fields for the active one).
func (t TTS) Of(id string) TTSProvider {
	id = normTTSProvider(id)
	if id == normTTSProvider(t.Provider) {
		return t.Active().clean()
	}
	p := t.Providers[id]
	p.Keys = append([]string(nil), p.Keys...)
	return p
}

// SetKeys replaces the active provider's key list (first → APIKey; trimmed,
// empty and repeated keys dropped).
func (t *TTS) SetKeys(keys []string) {
	t.setActive(t.Active().withKeys(keys))
}

// Switch makes id the active provider: the current view is stored under the
// old provider, id's own settings (empty when never set) become the view.
func (t *TTS) Switch(id string) {
	t.normalize()
	id = normTTSProvider(id)
	if id == t.Provider {
		return
	}
	t.Provider = id
	t.setActive(t.Providers[id])
	t.view = id
}

// clone is a deep copy (the map and every key list).
func (t TTS) clone() TTS {
	t.Keys = append([]string(nil), t.Keys...)
	if len(t.Keys) == 0 {
		t.Keys = nil
	}
	if t.Providers != nil {
		m := make(map[string]TTSProvider, len(t.Providers))
		for id, p := range t.Providers {
			p.Keys = append([]string(nil), p.Keys...)
			if len(p.Keys) == 0 {
				p.Keys = nil
			}
			m[id] = p
		}
		t.Providers = m
	}
	if t.ByID != nil {
		m := make(map[string][]VoiceRef, len(t.ByID))
		for id, l := range t.ByID {
			m[id] = append([]VoiceRef(nil), l...)
		}
		t.ByID = m
	}
	return t
}

// normalize cleans the section, follows a provider assigned directly,
// migrates a legacy (flat) section and stores the view under Provider.
// It never mutates a map it may share with another copy.
func (t *TTS) normalize() {
	t.Provider = normTTSProvider(t.Provider)
	legacy := t.Providers == nil && t.view == ""
	m := make(map[string]TTSProvider, len(t.Providers)+1)
	for id, p := range t.Providers {
		if id = strings.ToLower(strings.TrimSpace(id)); validTTSProvider(id) {
			m[id] = p.clean()
		}
	}
	cur := t.Active().clean()
	if t.view != "" && t.view != t.Provider {
		// Provider was assigned directly: what is unchanged since the last
		// normalisation is the old provider's; edited fields are the new one's.
		old, next := m[t.view], m[t.Provider]
		if cur.APIKey != old.APIKey {
			next.APIKey = cur.APIKey
		}
		if !sameKeys(cur.Keys, old.Keys) {
			next.Keys = cur.Keys
		}
		if cur.BaseURL != old.BaseURL {
			next.BaseURL = cur.BaseURL
		}
		if cur.Model != old.Model {
			next.Model = cur.Model
		}
		if cur.Voice != old.Voice {
			next.Voice = cur.Voice
		}
		cur = next.clean()
	}
	if legacy {
		cur = migrateTTS(t.Provider, cur, m)
	}
	t.setActive(cur)
	if cur.Empty() {
		delete(m, t.Provider)
	} else {
		m[t.Provider] = cur
	}
	t.Providers = m
	t.ByID = normalizeByID(t.ByID)
	t.view = t.Provider
}

func sameKeys(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// migrateTTS moves the keys, voice and model of a legacy section that surely
// belong to another provider into m and returns what stays with provider.
// Own servers (custom, an OpenAI-compatible gateway) take any key: nothing
// moves away from them.
func migrateTTS(provider string, cur TTSProvider, m map[string]TTSProvider) TTSProvider {
	if provider == "custom" || (provider == "openai" && cur.BaseURL != "") {
		return cur
	}
	var keep []string
	moved := map[string][]string{}
	for _, k := range cur.AllKeys() {
		if owner := TTSKeyOwner(k); owner != "" && owner != provider {
			moved[owner] = append(moved[owner], k)
			continue
		}
		keep = append(keep, k)
	}
	for owner, keys := range moved {
		p := m[owner]
		m[owner] = p.withKeys(append(p.AllKeys(), keys...)).clean()
	}
	cur = cur.withKeys(keep)
	if owner := TTSVoiceOwner(cur.Voice); owner != "" && owner != provider && !ambiguousVoice(provider, owner) {
		if p := m[owner]; p.Voice == "" {
			p.Voice = cur.Voice
			m[owner] = p
		}
		cur.Voice = ""
	}
	if owner := ttsModelOwner(cur.Model); owner != "" && owner != provider {
		if p := m[owner]; p.Model == "" {
			p.Model = cur.Model
			m[owner] = p
		}
		cur.Model = ""
	}
	return cur.clean()
}

// ---- shapes of keys, voices and models -----------------------------------------------------

var (
	reElevenKey   = regexp.MustCompile(`^sk_[0-9a-f]{40,64}(_residency_[a-z0-9]+)?$`)
	reCartesiaKey = regexp.MustCompile(`^sk_car_[A-Za-z0-9_\-]{12,}$`)
	// Google AI Studio: the new auth keys "AQ.…" (issued since spring 2026)
	// and the legacy standard keys "AIza" + 35 characters.
	reGoogleKey = regexp.MustCompile(`^(AIza[0-9A-Za-z_\-]{35}|AQ\.[A-Za-z0-9_\-]{16,}(\.[A-Za-z0-9_\-]+)*)$`)
	reOpenAIKey = regexp.MustCompile(`^sk-(proj|svcacct|admin)-[A-Za-z0-9_\-]{20,}$`)

	reUUID        = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	reElevenVoice = regexp.MustCompile(`^[A-Za-z0-9]{20}$`)
	reHex32       = regexp.MustCompile(`^[0-9a-f]{32}$`)
	reAzureVoice  = regexp.MustCompile(`^[a-z]{2,3}-[A-Z]{2,4}-[A-Za-z0-9:]*Neural(HD)?[A-Za-z0-9]*$`)
	reGoogleVoice = regexp.MustCompile(`^([a-z]{2,3}-[A-Z]{2}-)?(Chirp3-HD|Chirp-HD|Neural2|Wavenet|Standard|Studio|Polyglot|News|Casual)-[A-Za-z0-9]+$`)
)

// openAIVoiceNames are the built-in voices of the OpenAI Speech API.
var openAIVoiceNames = map[string]bool{
	"alloy": true, "ash": true, "ballad": true, "cedar": true, "coral": true, "echo": true, "fable": true,
	"marin": true, "nova": true, "onyx": true, "sage": true, "shimmer": true, "verse": true,
}

// TTSKeyOwner is the provider whose key format k surely has ("" = cannot
// tell: Azure, Fish Audio, MiniMax and gateway keys have no distinctive shape).
func TTSKeyOwner(k string) string {
	k = strings.TrimSpace(k)
	switch {
	case reCartesiaKey.MatchString(k):
		return "cartesia"
	case reElevenKey.MatchString(k):
		return "elevenlabs"
	case reGoogleKey.MatchString(k):
		return "google"
	case reOpenAIKey.MatchString(k):
		return "openai"
	}
	return ""
}

// TTSVoiceOwner is the provider whose voice-id format id surely has ("" =
// cannot tell). ElevenLabs: 20 letters and digits in both cases; Cartesia: a
// UUID; Fish Audio: 32 hex digits; Azure: "uk-UA-OstapNeural"; Google:
// "uk-UA-Chirp3-HD-Charon" / "Chirp3-HD-Charon"; OpenAI: its built-in names.
func TTSVoiceOwner(id string) string {
	id = strings.TrimSpace(id)
	switch {
	case id == "":
		return ""
	case reUUID.MatchString(id):
		return "cartesia"
	case reHex32.MatchString(id):
		return "fish"
	case reElevenVoice.MatchString(id) && strings.ContainsAny(id, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") && strings.ContainsAny(id, "abcdefghijklmnopqrstuvwxyz"):
		return "elevenlabs"
	case reAzureVoice.MatchString(id):
		return "azure"
	case reGoogleVoice.MatchString(id):
		return "google"
	case openAIVoiceNames[strings.ToLower(id)]:
		return "openai"
	}
	return ""
}

// ambiguousVoice: provider's own ids may look like owner's (MiniMax voice ids
// of cloned/designed voices are free-form letters, digits, "-" and "_").
// Lumean voices ElevenLabs Voice Library voices: their ids are its own.
func ambiguousVoice(provider, owner string) bool {
	return (provider == "minimax" && (owner == "elevenlabs" || owner == "fish" || owner == "cartesia")) ||
		(provider == "lumean" && owner == "elevenlabs")
}

// ForeignTTSVoice reports the provider a voice id surely belongs to when that
// is not provider ("" = the id may be provider's own). Own servers and
// OpenAI-compatible gateways (gateway = a base URL is set) take any id.
func ForeignTTSVoice(provider, id string, gateway bool) string {
	provider = normTTSProvider(provider)
	if provider == "custom" || (provider == "openai" && gateway) {
		return ""
	}
	owner := TTSVoiceOwner(id)
	if owner == "" || owner == provider || ambiguousVoice(provider, owner) {
		return ""
	}
	return owner
}

// ttsModelOwner is the provider of a model id ("" = cannot tell).
func ttsModelOwner(model string) string {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.HasPrefix(m, "eleven_"):
		return "elevenlabs"
	case strings.HasPrefix(m, "sonic"):
		return "cartesia"
	case strings.HasPrefix(m, "gpt-4o-mini-tts"), m == "tts-1", m == "tts-1-hd":
		return "openai"
	case strings.HasPrefix(m, "speech-"):
		return "minimax"
	}
	return ""
}
