package config

// Settings transfer (Settings → Дані → «Експортувати налаштування…» /
// «Імпортувати…»): one .ytcs file carries the keys, connections, profiles
// and defaults of this computer to another one.
//
// File (JSON; container version 1, see export_crypto.go for the crypto):
//
//	{
//	  "ytcs": 1,
//	  "note": "…",                   // for a person who opens the file; ignored
//	  "header": {                   // authenticated: GCM additional data (canonical JSON)
//	    "app": "YT Cartell Studio", "appVersion": "2.0.0", "schema": 1,
//	    "createdAt": "2026-10-02T15:30:00+03:00", "os": "windows", "arch": "amd64",
//	    "encrypted": true,
//	    "kdf": {"name": "pbkdf2-sha256", "iterations": 600000, "salt": "<base64, 32 B>"},
//	    "cipher": {"name": "aes-256-gcm", "nonce": "<base64, 12 B>"},
//	    "check": "<base64, 16 B>"
//	  },
//	  "data": "<base64: AES-256-GCM(payload) + tag>"
//	}
//
// Without a password: "encrypted": false, header "sha256" = SHA-256 (hex) of
// the compact payload, and "payload" holds the JSON in the clear.
//
// Payload (schema 1) — a section the user left out is absent:
//
//	connections  text, image, video, glabs, google, tts (every provider and key),
//	             stt, dictation, remake, telegram, custom (incl. the universal specs)
//	profiles     profiles.json
//	defaults     concurrency, autopilot, ui (theme, motion, tips, tour, auto-update)
//	keyStates    tts-keys.json (key ids and masks only — never a key)
//	hints        data folder, FFmpeg / ffprobe, whisper.cpp program and model,
//	             microphone: machine-specific, applied only when the user asks
//	             and the path exists on the importing computer
//
// Never exported: update bookkeeping, the legacy OnboardingDone. An import is one Store.Update (ApplyImport) plus
// ProfileStore.ReplaceAll, after a backup (SaveBackup) the user can restore.
// A file of a newer schema imports what this version understands and lists
// the rest (Bundle.Skipped, Bundle.Warnings); a newer container (crypto)
// format is refused with NewerFormatError.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// ExportExt is the extension of settings files.
	ExportExt = ".ytcs"
	// ExportSchema is the payload schema this version writes and understands.
	ExportSchema = 1

	exportContainer = 1
	exportApp       = "YT Cartell Studio"
	exportNote      = "Налаштування YT Cartell Studio: ключі API, підключення, профілі. Не пересилай цей файл у чати й не викладай у хмару."
	maxExportSize   = 32 << 20
)

// Errors of reading a settings file (plain Ukrainian, shown as they are).
var (
	ErrNotExport     = errors.New("Це не файл налаштувань YT Cartell Studio.")
	ErrNeedPassword  = errors.New("Файл захищено паролем — введи його.")
	ErrWrongPassword = errors.New("Пароль не підходить. Перевір розкладку клавіатури й Caps Lock.")
	ErrDamaged       = errors.New("Файл пошкоджено або змінено після експорту — його не можна відкрити.")
)

// NewerFormatError: the file's container (crypto) format comes from a newer
// studio than this one can read.
type NewerFormatError struct{ AppVersion string }

func (e *NewerFormatError) Error() string {
	v := ""
	if e.AppVersion != "" {
		v = " (" + e.AppVersion + ")"
	}
	return "Файл зроблено новішою версією студії" + v + " у форматі, який ця версія ще не читає. Онови студію й спробуй знову."
}

// ExportFileName is the suggested file name: YT-Cartell-Studio-settings-2026-10-02.ytcs.
func ExportFileName(t time.Time) string {
	return "YT-Cartell-Studio-settings-" + t.Format("2006-01-02") + ExportExt
}

// ---- the sections ------------------------------------------------------------------------

// Connections are the services and every key (a nil part = not in the file).
type Connections struct {
	Text      *Text      `json:"text,omitempty"`
	Image     *Image     `json:"image,omitempty"`
	Video     *Video     `json:"video,omitempty"`
	Glabs     *Glabs     `json:"glabs,omitempty"`
	Google    *Google    `json:"google,omitempty"`
	TTS       *TTS       `json:"tts,omitempty"`
	STT       *STT       `json:"stt,omitempty"`
	Dictation *Dictation `json:"dictation,omitempty"`
	Remake    *Remake    `json:"remake,omitempty"`
	Telegram  *Telegram  `json:"telegram,omitempty"`
	Custom    *CustomAPI `json:"custom,omitempty"`
}

// Defaults are the studio-wide choices that are not connections.
type Defaults struct {
	Concurrency *int       `json:"concurrency,omitempty"`
	Autopilot   *Autopilot `json:"autopilot,omitempty"`
	UI          *UI        `json:"ui,omitempty"`
}

// Hints are the machine-specific paths of the exporting computer.
type Hints struct {
	DataDir      string `json:"dataDir,omitempty"`
	FFmpeg       string `json:"ffmpeg,omitempty"`
	FFprobe      string `json:"ffprobe,omitempty"`
	WhisperBin   string `json:"whisperBin,omitempty"`
	WhisperModel string `json:"whisperModel,omitempty"`
	Microphone   string `json:"microphone,omitempty"` // Dictation.Device (FFmpeg's device name)
}

// Empty: no hint at all.
func (h Hints) Empty() bool { return h == Hints{} }

// Here reports which hints point at something that exists on this computer
// (Microphone is never checked here: the UI asks FFmpeg for the devices).
func (h Hints) Here() HintPlan {
	return HintPlan{
		DataDir: h.DataDir != "" && isDirPath(h.DataDir),
		FFmpeg:  h.FFmpeg != "" && isFilePath(h.FFmpeg),
		Whisper: h.WhisperBin != "" && isFilePath(h.WhisperBin),
	}
}

type exportPayload struct {
	Connections *Connections    `json:"connections,omitempty"`
	Profiles    *[]Profile      `json:"profiles,omitempty"`
	Defaults    *Defaults       `json:"defaults,omitempty"`
	KeyStates   json.RawMessage `json:"keyStates,omitempty"`
	Hints       *Hints          `json:"hints,omitempty"`
}

// ExportHeader is the readable (and authenticated) head of a settings file.
type ExportHeader struct {
	App        string        `json:"app"`
	AppVersion string        `json:"appVersion,omitempty"`
	Schema     int           `json:"schema"`
	CreatedAt  time.Time     `json:"createdAt"`
	OS         string        `json:"os,omitempty"`
	Arch       string        `json:"arch,omitempty"`
	Encrypted  bool          `json:"encrypted"`
	KDF        *ExportKDF    `json:"kdf,omitempty"`
	Cipher     *ExportCipher `json:"cipher,omitempty"`
	Check      []byte        `json:"check,omitempty"`
	SHA256     string        `json:"sha256,omitempty"`
}

// ExportKDF are the password-stretching parameters.
type ExportKDF struct {
	Name       string `json:"name"`
	Iterations int    `json:"iterations"`
	Salt       []byte `json:"salt"`
}

// ExportCipher are the encryption parameters.
type ExportCipher struct {
	Name  string `json:"name"`
	Nonce []byte `json:"nonce"`
}

// OSLabel is the exporting system in plain words ("Windows", "macOS").
func (h ExportHeader) OSLabel() string {
	switch h.OS {
	case "windows":
		return "Windows"
	case "darwin":
		return "macOS"
	case "linux":
		return "Linux"
	case "":
		return ""
	}
	return h.OS
}

type exportDoc struct {
	Format  *int            `json:"ytcs"`
	Note    string          `json:"note,omitempty"`
	Header  json.RawMessage `json:"header"`
	Data    []byte          `json:"data,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// ---- export ----------------------------------------------------------------------------------

// ExportOptions say what goes into the file and how it is protected.
type ExportOptions struct {
	Connections, Profiles, Defaults, KeyStates bool
	// Password encrypts the file; "" writes it in the clear (the UI warns).
	Password   string
	AppVersion string
	Now        time.Time // zero = time.Now()
}

func (o ExportOptions) any() bool { return o.Connections || o.Profiles || o.Defaults || o.KeyStates }

// NewBundle is what an export of these settings would carry (the export
// dialog summarises it; Export writes it).
func NewBundle(c Config, profiles []Profile, keyStates []byte, opt ExportOptions) *Bundle {
	b := &Bundle{Hints: hintsOf(c)}
	if opt.Connections {
		b.Connections = connectionsOf(c)
	}
	if opt.Profiles {
		b.Profiles, b.HasProfiles = append([]Profile{}, profiles...), true
	}
	if opt.Defaults {
		n := c.Concurrency
		a := c.Autopilot
		ui := c.UI
		ui.UpdateChecked, ui.UpdateSeen = 0, ""
		b.Defaults = &Defaults{Concurrency: &n, Autopilot: &a, UI: &ui}
	}
	if opt.KeyStates && len(bytes.TrimSpace(keyStates)) > 0 && json.Valid(keyStates) {
		b.KeyStates = append(json.RawMessage(nil), keyStates...)
	}
	return b
}

func connectionsOf(c Config) *Connections {
	text, image, video, glabs := c.Text, c.Image, c.Video, c.Glabs
	google := c.Google
	google.Keys = append([]string(nil), c.Google.Keys...)
	tts := c.TTS.clone()
	stt := c.STT
	stt.LocalBin, stt.LocalModel = "", "" // hints
	dict := c.Dictation
	dict.Device = "" // a hint
	remake, tg, custom := c.Remake, c.Telegram.clone(), c.Custom
	return &Connections{Text: &text, Image: &image, Video: &video, Glabs: &glabs, Google: &google, TTS: &tts,
		STT: &stt, Dictation: &dict, Remake: &remake, Telegram: &tg, Custom: &custom}
}

func hintsOf(c Config) Hints {
	return Hints{DataDir: c.DataDir, FFmpeg: c.FFmpegPath, FFprobe: c.FFprobePath,
		WhisperBin: c.STT.LocalBin, WhisperModel: c.STT.LocalModel, Microphone: c.Dictation.Device}
}

func (b *Bundle) payload() exportPayload {
	p := exportPayload{Connections: b.Connections, Defaults: b.Defaults, KeyStates: b.KeyStates}
	if b.HasProfiles {
		list := append([]Profile{}, b.Profiles...)
		p.Profiles = &list
	}
	if !b.Hints.Empty() {
		h := b.Hints
		p.Hints = &h
	}
	return p
}

// Export writes the settings file: what opt selects of c, the profiles and
// the key-state document (tts.Client.KeyStateJSON), encrypted with
// opt.Password unless it is "".
func Export(c Config, profiles []Profile, keyStates []byte, opt ExportOptions) ([]byte, error) {
	if !opt.any() {
		return nil, errors.New("Вибери, що експортувати.")
	}
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	plain, err := json.Marshal(NewBundle(c, profiles, keyStates, opt).payload())
	if err != nil {
		return nil, err
	}
	h := ExportHeader{App: exportApp, AppVersion: opt.AppVersion, Schema: ExportSchema,
		CreatedAt: now.Truncate(time.Second), OS: runtime.GOOS, Arch: runtime.GOARCH}
	format := exportContainer
	doc := exportDoc{Format: &format, Note: exportNote}
	if opt.Password == "" {
		sum := sha256.Sum256(plain)
		h.SHA256 = hex.EncodeToString(sum[:])
		if doc.Header, err = json.Marshal(h); err != nil {
			return nil, err
		}
		doc.Payload = plain
	} else {
		s, err := newSeal(opt.Password, KDFIterations)
		if err != nil {
			return nil, err
		}
		h.Encrypted = true
		h.KDF = &ExportKDF{Name: kdfName, Iterations: KDFIterations, Salt: s.salt}
		h.Cipher = &ExportCipher{Name: cipherName, Nonce: s.nonce}
		h.Check = s.check
		if doc.Header, err = json.Marshal(h); err != nil {
			return nil, err
		}
		aad, err := canonicalJSON(doc.Header)
		if err != nil {
			return nil, err
		}
		if doc.Data, err = s.seal(plain, aad); err != nil {
			return nil, err
		}
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

// WriteExportFile stores a settings file (owner-only permissions, atomic).
func WriteExportFile(path string, data []byte) error {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("не вдалося створити папку: %w", err)
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("не вдалося записати файл: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return fmt.Errorf("не вдалося записати файл: %w", err)
		}
	}
	return nil
}

// ---- reading -----------------------------------------------------------------------------------

// ExportFile is a settings file whose head is read; Open decrypts it.
type ExportFile struct {
	Header  ExportHeader
	aad     []byte // the compact header: GCM additional data
	data    []byte // ciphertext (encrypted)
	payload []byte // compact payload (plain)
}

// ReadExportFile reads and checks a settings file from disk.
func ReadExportFile(path string) (*ExportFile, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("не вдалося відкрити файл: %w", err)
	}
	if st.Size() > maxExportSize {
		return nil, ErrNotExport
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("не вдалося прочитати файл: %w", err)
	}
	return ReadExport(b)
}

// ReadExport checks a settings file's container and head. Errors:
// ErrNotExport, ErrDamaged, *NewerFormatError.
func ReadExport(b []byte) (*ExportFile, error) {
	if len(b) > maxExportSize {
		return nil, ErrNotExport
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	var doc exportDoc
	if err := json.Unmarshal(b, &doc); err != nil || doc.Format == nil || len(doc.Header) == 0 {
		return nil, ErrNotExport
	}
	var h ExportHeader
	if err := json.Unmarshal(doc.Header, &h); err != nil {
		return nil, ErrDamaged
	}
	if *doc.Format > exportContainer {
		return nil, &NewerFormatError{AppVersion: h.AppVersion}
	}
	if *doc.Format < 1 || h.App != exportApp {
		return nil, ErrNotExport
	}
	aad, err := canonicalJSON(doc.Header)
	if err != nil {
		return nil, ErrDamaged
	}
	f := &ExportFile{Header: h, aad: aad}
	if !h.Encrypted {
		var p bytes.Buffer
		if len(doc.Payload) == 0 || json.Compact(&p, doc.Payload) != nil {
			return nil, ErrDamaged
		}
		sum := sha256.Sum256(p.Bytes())
		if !strings.EqualFold(h.SHA256, hex.EncodeToString(sum[:])) {
			return nil, ErrDamaged
		}
		f.payload = p.Bytes()
		return f, nil
	}
	if h.KDF == nil || h.Cipher == nil {
		return nil, ErrDamaged
	}
	if h.KDF.Name != kdfName || h.Cipher.Name != cipherName {
		return nil, &NewerFormatError{AppVersion: h.AppVersion}
	}
	if h.KDF.Iterations < minKDFIterations || h.KDF.Iterations > maxKDFIterations ||
		len(h.KDF.Salt) < 16 || len(h.Cipher.Nonce) != nonceSize || len(h.Check) != checkSize || len(doc.Data) < 16 {
		return nil, ErrDamaged
	}
	f.data = doc.Data
	return f, nil
}

// Encrypted reports whether Open needs a password.
func (f *ExportFile) Encrypted() bool { return f.Header.Encrypted }

// Open decrypts (password is ignored for a plain file) and decodes the file.
// Errors: ErrNeedPassword, ErrWrongPassword, ErrDamaged — nothing is applied
// anywhere, the caller only gets a Bundle on success.
func (f *ExportFile) Open(password string) (*Bundle, error) {
	plain := f.payload
	if f.Header.Encrypted {
		if password == "" {
			return nil, ErrNeedPassword
		}
		var err error
		if plain, err = openSealed(password, f.Header, f.data, f.aad); err != nil {
			return nil, err
		}
	}
	return decodePayload(f.Header, plain)
}

// Bundle is the decoded content of a settings file (or of an export to be).
type Bundle struct {
	Header      ExportHeader
	Connections *Connections
	Profiles    []Profile
	HasProfiles bool // the profiles section is there (it may be empty)
	Defaults    *Defaults
	KeyStates   json.RawMessage
	Hints       Hints
	// Skipped are the parts of a newer file this version does not know
	// (dotted JSON paths, sorted); Warnings say it in plain Ukrainian.
	Skipped  []string
	Warnings []string
}

var payloadSections = map[string]bool{"connections": true, "profiles": true, "defaults": true, "keystates": true, "hints": true,
	"unlocks": true} // "unlocks": written by 2.0.6, ignored

func decodePayload(h ExportHeader, plain []byte) (*Bundle, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(plain, &top); err != nil {
		return nil, ErrDamaged
	}
	b := &Bundle{Header: h}
	unknown := map[string]bool{}
	bad := map[string]bool{}
	for k := range top {
		if !payloadSections[strings.ToLower(k)] {
			unknown[k] = true
		}
	}
	if raw, ok := lookupFold(top, "connections"); ok && !isNull(raw) {
		var c Connections
		if decodeLenient(raw, &c, "connections.", bad) == nil {
			b.Connections = &c
			unknownKeys(raw, reflect.TypeOf(c), "connections.", unknown, 0)
		} else {
			bad["connections"] = true
		}
	}
	if raw, ok := lookupFold(top, "profiles"); ok && !isNull(raw) {
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) == nil {
			b.HasProfiles = true
			b.Profiles = []Profile{}
			for i, r := range list {
				var p Profile
				if err := json.Unmarshal(r, &p); err != nil {
					bad[fmt.Sprintf("profiles[%d]", i)] = true
					continue
				}
				b.Profiles = append(b.Profiles, p)
				unknownKeys(r, reflect.TypeOf(p), "profiles.", unknown, 0)
			}
		} else {
			bad["profiles"] = true
		}
	}
	if raw, ok := lookupFold(top, "defaults"); ok && !isNull(raw) {
		var d Defaults
		if decodeLenient(raw, &d, "defaults.", bad) == nil {
			b.Defaults = &d
			unknownKeys(raw, reflect.TypeOf(d), "defaults.", unknown, 0)
		} else {
			bad["defaults"] = true
		}
	}
	if raw, ok := lookupFold(top, "keyStates"); ok && !isNull(raw) {
		if t := bytes.TrimSpace(raw); len(t) > 0 && t[0] == '{' && json.Valid(t) {
			b.KeyStates = append(json.RawMessage(nil), t...)
		} else {
			bad["keyStates"] = true
		}
	}
	if raw, ok := lookupFold(top, "hints"); ok && !isNull(raw) {
		if json.Unmarshal(raw, &b.Hints) != nil {
			bad["hints"] = true
		}
		unknownKeys(raw, reflect.TypeOf(b.Hints), "hints.", unknown, 0)
	}
	b.Skipped = sortedKeys(unknown)
	newer := h.Schema > ExportSchema
	switch {
	case len(b.Skipped) > 0:
		from := "Файл зроблено новішою версією студії"
		if h.AppVersion != "" {
			from += " (" + h.AppVersion + ")"
		}
		if !newer {
			from = "У файлі є щось незнайоме цій версії"
		}
		b.Warnings = append(b.Warnings, fmt.Sprintf("%s: %s пропустимо — решту перенесемо. Онови студію, щоб перенести все.",
			from, countUK(len(b.Skipped), "параметр", "параметри", "параметрів")))
	case newer:
		v := ""
		if h.AppVersion != "" {
			v = " (" + h.AppVersion + ")"
		}
		b.Warnings = append(b.Warnings, "Файл зроблено новішою версією студії"+v+". Усе, що ця версія знає, перенесемо.")
	}
	if len(bad) > 0 {
		b.Warnings = append(b.Warnings, fmt.Sprintf("Частину файлу не вдалося прочитати (%s) — її пропустимо.", strings.Join(sortedKeys(bad), ", ")))
	}
	if b.Connections == nil && !b.HasProfiles && b.Defaults == nil && len(b.KeyStates) == 0 {
		if len(bad) > 0 {
			return nil, ErrDamaged
		}
	}
	return b, nil
}

// canonicalJSON is raw with sorted object keys, no spaces and the numbers
// exactly as written: the header's authenticated form, the same however a
// tool re-indented or re-ordered the file.
func canonicalJSON(raw []byte) ([]byte, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

func isNull(raw json.RawMessage) bool { return string(bytes.TrimSpace(raw)) == "null" }

// lookupFold finds key in m like encoding/json does (exact, then any case).
func lookupFold(m map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	for k, v := range m {
		if strings.EqualFold(k, key) {
			return v, true
		}
	}
	return nil, false
}

func jsonName(f reflect.StructField) string {
	if !f.IsExported() {
		return ""
	}
	tag, ok := f.Tag.Lookup("json")
	if !ok {
		return f.Name
	}
	n, _, _ := strings.Cut(tag, ",")
	switch n {
	case "-":
		return ""
	case "":
		return f.Name
	}
	return n
}

// decodeLenient decodes the JSON object raw into the struct v points to one
// field at a time: a field a newer version changed the shape of is skipped
// and named in bad (the rest still decodes).
func decodeLenient(raw json.RawMessage, v any, path string, bad map[string]bool) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return err
	}
	rv := reflect.ValueOf(v).Elem()
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		name := jsonName(t.Field(i))
		if name == "" {
			continue
		}
		r, ok := lookupFold(m, name)
		if !ok || isNull(r) {
			continue
		}
		ptr := reflect.New(t.Field(i).Type)
		if err := json.Unmarshal(r, ptr.Interface()); err != nil {
			bad[path+name] = true
			continue
		}
		rv.Field(i).Set(ptr.Elem())
	}
	return nil
}

// unknownKeys adds the JSON object keys of raw that type t has no field for
// (dotted paths), looking into nested objects, lists and maps.
func unknownKeys(raw json.RawMessage, t reflect.Type, path string, out map[string]bool, depth int) {
	if depth > 8 {
		return
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		if t == reflect.TypeOf(time.Time{}) {
			return
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			if n := jsonName(t.Field(i)); n != "" {
				fields[strings.ToLower(n)] = t.Field(i).Type
			}
		}
		for k, v := range m {
			ft, ok := fields[strings.ToLower(k)]
			if !ok {
				out[path+k] = true
				continue
			}
			unknownKeys(v, ft, path+k+".", out, depth+1)
		}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return
		}
		var list []json.RawMessage
		if json.Unmarshal(raw, &list) != nil {
			return
		}
		for _, v := range list {
			unknownKeys(v, t.Elem(), path, out, depth+1)
		}
	case reflect.Map:
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			return
		}
		for k, v := range m {
			unknownKeys(v, t.Elem(), path+k+".", out, depth+1)
		}
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// countUK is "3 параметри" (Ukrainian plural forms).
func countUK(n int, one, few, many string) string {
	w := many
	switch m10, m100 := n%10, n%100; {
	case m10 == 1 && m100 != 11:
		w = one
	case m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14):
		w = few
	}
	return fmt.Sprintf("%d %s", n, w)
}

// ---- what is inside (summaries for the dialogs) -----------------------------------------------

// ProviderKeys is one voice-over provider of a file with its key count.
type ProviderKeys struct {
	ID     string
	Keys   int
	Active bool
}

// TTSKeys lists the voice-over providers that have settings, the active one
// first, then in TTSProviderIDs order.
func (c *Connections) TTSKeys() []ProviderKeys {
	if c == nil || c.TTS == nil {
		return nil
	}
	active := normTTSProvider(c.TTS.Provider)
	var out []ProviderKeys
	for _, id := range TTSProviderIDs {
		p := c.TTS.Of(id)
		if p.Empty() {
			continue
		}
		pk := ProviderKeys{ID: id, Keys: len(p.AllKeys()), Active: id == active}
		if pk.Active {
			out = append([]ProviderKeys{pk}, out...)
		} else {
			out = append(out, pk)
		}
	}
	return out
}

// secrets lists every key and token of the section (trimmed, no repeats).
func (c *Connections) secrets() []string {
	if c == nil {
		return nil
	}
	var all []string
	add := func(v ...string) { all = append(all, v...) }
	if c.Text != nil {
		add(c.Text.APIKey)
	}
	if c.Glabs != nil {
		add(c.Glabs.APIKey)
	}
	if c.Google != nil {
		add(c.Google.Keys...)
	}
	if c.TTS != nil {
		for _, id := range TTSProviderIDs {
			add(c.TTS.Of(id).AllKeys()...)
		}
	}
	if c.STT != nil {
		add(c.STT.APIKey, c.STT.AssemblyKey)
	}
	if d := c.Dictation; d != nil {
		add(d.ElevenKey, d.OpenAIKey, d.ChunksKey, d.DeepgramKey, d.AssemblyKey, d.GoogleKey)
	}
	if c.Telegram != nil {
		add(c.Telegram.BotToken)
	}
	if c.Custom != nil {
		add(c.Custom.ImageKey, c.Custom.VideoKey)
	}
	return unionKeys(nil, all)
}

// KeyCount is the number of different keys and tokens in the section.
func (c *Connections) KeyCount() int { return len(c.secrets()) }

// KeyStateCounts: how many key records the file carries and how many of them
// are exhausted (a monthly quota that ran out).
func (b *Bundle) KeyStateCounts() (records, exhausted int) {
	if b == nil || len(b.KeyStates) == 0 {
		return 0, 0
	}
	var doc struct {
		Keys map[string]struct {
			State string `json:"state"`
		} `json:"keys"`
	}
	if json.Unmarshal(b.KeyStates, &doc) != nil {
		return 0, 0
	}
	for _, r := range doc.Keys {
		records++
		if r.State == "exhausted" {
			exhausted++
		}
	}
	return records, exhausted
}

// ---- import --------------------------------------------------------------------------------------

// ImportMode is how a section meets the settings already here.
type ImportMode string

const (
	// ModeReplace: the section becomes what the file has.
	ModeReplace ImportMode = "replace"
	// ModeMerge: what is set up here stays; keys the file adds are joined
	// without repeats, services not set up here are taken from the file,
	// profiles are added by name.
	ModeMerge ImportMode = "merge"
)

// SectionPlan is one section of an import.
type SectionPlan struct {
	On   bool
	Mode ImportMode // "" = ModeReplace
}

// Merge reports the merge mode.
func (s SectionPlan) Merge() bool { return s.Mode == ModeMerge }

// HintPlan: which machine-specific hints the user asked to take. ApplyImport
// still takes a path only when it exists here; Microphone is trusted (the UI
// checks FFmpeg's device list).
type HintPlan struct {
	DataDir, FFmpeg, Whisper, Microphone bool
}

// ImportPlan is what the user chose in the import dialog.
type ImportPlan struct {
	Connections, Profiles, Defaults, KeyStates SectionPlan
	Hints                                      HintPlan
}

// Any reports whether the plan changes anything.
func (p ImportPlan) Any() bool {
	h := p.Hints
	return p.Connections.On || p.Profiles.On || p.Defaults.On || p.KeyStates.On || h.DataDir || h.FFmpeg || h.Whisper || h.Microphone
}

// DefaultPlan takes every section the file has, replacing (key states:
// merged — this computer's own records are fresher), and no hint.
func (b *Bundle) DefaultPlan() ImportPlan {
	return ImportPlan{
		Connections: SectionPlan{On: b.Connections != nil, Mode: ModeReplace},
		Profiles:    SectionPlan{On: b.HasProfiles, Mode: ModeReplace},
		Defaults:    SectionPlan{On: b.Defaults != nil, Mode: ModeReplace},
		KeyStates:   SectionPlan{On: len(b.KeyStates) > 0, Mode: ModeMerge},
	}
}

// ImportResult is what an import did (studio.Engine.ImportSettings).
type ImportResult struct {
	Backup                      Backup
	ProfilesAdded, ProfilesSame int // merge: added, and skipped as identical
	ProfilesTotal               int // profiles after the import
	KeyStates                   int // key records taken
	Warnings                    []string
}

// ApplyImport changes c the way plan says. Run it inside Store.Update, so
// the whole import is one atomic settings change. profileID maps the file's
// Autopilot.DefaultProfileID to the id that profile has here ("" = no such
// profile here: the current choice stays); nil keeps the id as it is.
// Machine-specific values (paths, the microphone) are never taken from the
// sections — only through accepted hints.
func ApplyImport(c *Config, b *Bundle, plan ImportPlan, profileID func(fileID string) string) {
	if c == nil || b == nil {
		return
	}
	old := *c
	if plan.Connections.On && b.Connections != nil {
		if plan.Connections.Merge() {
			mergeConnections(c, b.Connections)
		} else {
			replaceConnections(c, b.Connections)
		}
	}
	if plan.Defaults.On && b.Defaults != nil {
		applyDefaults(c, b.Defaults, old, profileID)
	}
	applyHints(c, b.Hints, plan.Hints)
}

func replaceConnections(c *Config, in *Connections) {
	if x := in.Text; x != nil {
		c.Text = *x
	}
	if x := in.Image; x != nil {
		c.Image = *x
	}
	if x := in.Video; x != nil {
		c.Video = *x
	}
	if x := in.Glabs; x != nil {
		c.Glabs = *x
	}
	if x := in.Google; x != nil {
		g := *x
		g.Keys = append([]string(nil), x.Keys...)
		c.Google = g
	}
	if x := in.TTS; x != nil {
		t := x.clone()
		t.view = "" // the flat fields are the file's active provider
		c.TTS = t
	}
	if x := in.STT; x != nil {
		s := *x
		s.LocalBin, s.LocalModel = c.STT.LocalBin, c.STT.LocalModel // this computer's (hints)
		c.STT = s
	}
	if x := in.Dictation; x != nil {
		d := *x
		d.Device = c.Dictation.Device // this computer's microphone (a hint)
		c.Dictation = d
	}
	if x := in.Remake; x != nil {
		c.Remake = *x
	}
	if x := in.Telegram; x != nil {
		c.Telegram = importTelegram(*x, c.Telegram)
	}
	if x := in.Custom; x != nil {
		c.Custom = *x
	}
}

// importTelegram is the file's Telegram section for this computer. Remote
// control stays on only when it already ran here with the same bot: one bot
// cannot take commands on two computers at once (Telegram ends one getUpdates
// with the other), so a moved studio switches it on itself when the old one
// no longer uses the bot.
func importTelegram(x, cur Telegram) Telegram {
	t := x.clone()
	t.Remote = x.Remote && cur.Remote && cur.BotToken != "" && cur.BotToken == x.BotToken
	return t
}

// mergeConnections keeps what is set up here: key lists are joined, a
// service that is not set up here is taken whole, choices (providers,
// models, lengths, who watches remakes) stay.
func mergeConnections(c *Config, in *Connections) {
	if x := in.Text; x != nil && !textSetUp(c.Text) {
		c.Text = *x
	}
	if x := in.Glabs; x != nil && strings.TrimSpace(c.Glabs.APIKey) == "" {
		c.Glabs = *x
	}
	if x := in.Google; x != nil {
		c.Google.Keys = unionKeys(c.Google.Keys, x.Keys)
	}
	if x := in.TTS; x != nil {
		mergeTTS(&c.TTS, *x)
	}
	if x := in.STT; x != nil && !sttSetUp(c.STT) {
		s := *x
		s.LocalBin, s.LocalModel = c.STT.LocalBin, c.STT.LocalModel
		c.STT = s
	}
	if x := in.Dictation; x != nil {
		mergeDictation(&c.Dictation, *x)
	}
	if x := in.Telegram; x != nil && strings.TrimSpace(c.Telegram.BotToken) == "" {
		c.Telegram = importTelegram(*x, c.Telegram)
	}
	if x := in.Custom; x != nil {
		a := &c.Custom
		if a.ImagePreset == "" && x.ImagePreset != "" {
			a.ImagePreset, a.ImageBaseURL, a.ImageKey, a.ImageModel, a.ImageSpec = x.ImagePreset, x.ImageBaseURL, x.ImageKey, x.ImageModel, x.ImageSpec
		}
		if a.VideoPreset == "" && x.VideoPreset != "" {
			a.VideoPreset, a.VideoBaseURL, a.VideoKey, a.VideoModel, a.VideoSpec = x.VideoPreset, x.VideoBaseURL, x.VideoKey, x.VideoModel, x.VideoSpec
		}
		if a.TTSSpec == "" {
			a.TTSSpec = x.TTSSpec
		}
		if a.STTSpec == "" {
			a.STTSpec = x.STTSpec
		}
	}
}

func textSetUp(t Text) bool {
	return strings.TrimSpace(t.APIKey) != "" || t.Provider == "google"
}

func sttSetUp(s STT) bool {
	return s.APIKey != "" || s.AssemblyKey != "" || s.BaseURL != "" || s.Model != "" || (s.Provider != "" && s.Provider != "elevenlabs")
}

func mergeDictation(d *Dictation, x Dictation) {
	fill := func(dst *string, src string) {
		if strings.TrimSpace(*dst) == "" {
			*dst = src
		}
	}
	fill(&d.ElevenKey, x.ElevenKey)
	fill(&d.OpenAIKey, x.OpenAIKey)
	fill(&d.OpenAIModel, x.OpenAIModel)
	fill(&d.DeepgramKey, x.DeepgramKey)
	fill(&d.AssemblyKey, x.AssemblyKey)
	fill(&d.GoogleKey, x.GoogleKey)
	fill(&d.GeminiModel, x.GeminiModel)
	if d.ChunksURL == "" && d.ChunksKey == "" {
		d.ChunksURL, d.ChunksKey, d.ChunksModel = x.ChunksURL, x.ChunksKey, x.ChunksModel
	}
}

// mergeTTS joins every provider's keys (this computer's first) and fills an
// address, model or voice that is empty here. The active provider stays.
func mergeTTS(t *TTS, in TTS) {
	t.normalize()
	in = in.clone()
	in.view = ""
	in.normalize()
	for _, id := range TTSProviderIDs {
		q, ok := in.Providers[id]
		if !ok {
			continue
		}
		p := t.Of(id)
		m := p.withKeys(append(p.AllKeys(), q.AllKeys()...))
		if m.BaseURL == "" {
			m.BaseURL = q.BaseURL
		}
		if m.Model == "" {
			m.Model = q.Model
		}
		if m.Voice == "" {
			m.Voice = q.Voice
		}
		m = m.clean()
		if id == t.Provider {
			t.setActive(m)
			continue
		}
		next := make(map[string]TTSProvider, len(t.Providers)+1)
		for k, v := range t.Providers {
			next[k] = v
		}
		next[id] = m
		t.Providers = next
	}
	t.normalize()
}

// unionKeys is a then b, trimmed, without empty or repeated keys.
func unionKeys(a, b []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, k := range append(append([]string(nil), a...), b...) {
		if k = strings.TrimSpace(k); k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	return out
}

func applyDefaults(c *Config, d *Defaults, old Config, profileID func(string) string) {
	if d.Concurrency != nil {
		c.Concurrency = *d.Concurrency
	}
	if d.Autopilot != nil {
		a := *d.Autopilot
		if a.DefaultProfileID != "" && profileID != nil {
			if id := profileID(a.DefaultProfileID); id != "" {
				a.DefaultProfileID = id
			} else {
				a.DefaultProfileID = old.Autopilot.DefaultProfileID
			}
		}
		c.Autopilot = a
	}
	if d.UI != nil {
		u := *d.UI
		u.UpdateChecked, u.UpdateSeen = old.UI.UpdateChecked, old.UI.UpdateSeen
		c.UI = u
	}
}

func applyHints(c *Config, h Hints, plan HintPlan) {
	if plan.DataDir && h.DataDir != "" && isDirPath(h.DataDir) {
		c.DataDir = h.DataDir
	}
	if plan.FFmpeg && h.FFmpeg != "" && isFilePath(h.FFmpeg) {
		c.FFmpegPath = h.FFmpeg
		if h.FFprobe != "" && isFilePath(h.FFprobe) {
			c.FFprobePath = h.FFprobe
		}
	}
	if plan.Whisper && h.WhisperBin != "" && isFilePath(h.WhisperBin) {
		c.STT.LocalBin = h.WhisperBin
		if h.WhisperModel != "" && isFilePath(h.WhisperModel) {
			c.STT.LocalModel = h.WhisperModel
		}
	}
	if plan.Microphone && strings.TrimSpace(h.Microphone) != "" {
		c.Dictation.Device = h.Microphone
	}
}

func isFilePath(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

// ---- profiles ---------------------------------------------------------------------------------

// ReplaceProfiles is the file's profiles as they will be stored (normalized,
// unique ids — the file's ids are kept, so Autopilot.DefaultProfileID still
// points at the right one); ids maps the file's ids to the stored ones.
func ReplaceProfiles(in []Profile) (out []Profile, ids map[string]string) {
	ids = map[string]string{}
	now := time.Now().UnixMilli()
	for _, p := range in {
		orig := strings.TrimSpace(p.ID)
		p.ID = orig
		if p.ID == "" || indexOf(out, p.ID) >= 0 {
			p.ID = newProfileID(out)
		}
		stamp(&p, now)
		p.Normalize()
		out = append(out, p)
		if _, ok := ids[orig]; orig != "" && !ok {
			ids[orig] = p.ID
		}
	}
	return out, ids
}

// MergeProfiles adds the file's profiles to cur: one with the same name and
// the same settings as a profile here is skipped (same), one whose name is
// taken here by other settings is added as "<name> (імпорт)"; an id taken
// here gets a new one. ids maps the file's ids to the ids after the merge.
func MergeProfiles(cur, in []Profile) (out []Profile, ids map[string]string, added, same int) {
	out = append([]Profile{}, cur...)
	ids = map[string]string{}
	now := time.Now().UnixMilli()
	for _, p := range in {
		orig := strings.TrimSpace(p.ID)
		p.Normalize()
		if i := sameProfile(out, p); i >= 0 {
			if orig != "" {
				ids[orig] = out[i].ID
			}
			same++
			continue
		}
		if nameTaken(out, p.Name) {
			p.Name = importName(out, p.Name)
		}
		p.ID = orig
		if p.ID == "" || indexOf(out, p.ID) >= 0 {
			p.ID = newProfileID(out)
		}
		stamp(&p, now)
		p.Normalize()
		out = append(out, p)
		if orig != "" {
			ids[orig] = p.ID
		}
		added++
	}
	return out, ids, added, same
}

func stamp(p *Profile, now int64) {
	if p.CreatedAt <= 0 {
		p.CreatedAt = now
	}
	if p.UpdatedAt <= 0 {
		p.UpdatedAt = now
	}
}

// settingsOf is a profile without its identity (id, name, timestamps).
func settingsOf(p Profile) Profile {
	p.ID, p.Name, p.CreatedAt, p.UpdatedAt = "", "", 0, 0
	return p
}

// sameProfile finds a profile with p's settings under p's name (or the name
// an earlier import gave it).
func sameProfile(list []Profile, p Profile) int {
	want := settingsOf(p)
	for i, q := range list {
		if settingsOf(q) != want {
			continue
		}
		if strings.EqualFold(q.Name, p.Name) || strings.HasPrefix(q.Name, p.Name+" (імпорт") {
			return i
		}
	}
	return -1
}

func nameTaken(list []Profile, name string) bool {
	for _, q := range list {
		if strings.EqualFold(q.Name, name) {
			return true
		}
	}
	return false
}

func importName(list []Profile, name string) string {
	name = clipText(name, 66)
	cand := name + " (імпорт)"
	for n := 2; nameTaken(list, cand); n++ {
		cand = fmt.Sprintf("%s (імпорт %d)", name, n)
	}
	return cand
}

// ReplaceAll makes list the store's profiles (normalized, unique ids) in one
// atomic write; nothing changes when it cannot be written.
func (s *ProfileStore) ReplaceAll(list []Profile) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	next := clean(append([]Profile(nil), list...))
	if err := s.write(next); err != nil {
		return err
	}
	s.mu.Lock()
	s.list = next
	s.mu.Unlock()
	return nil
}

// ---- backups ---------------------------------------------------------------------------------

// Backup is a copy of the settings and profiles kept before an import.
type Backup struct {
	Time     time.Time
	Config   Config
	Profiles []Profile // nil = not kept
	// Path is the config copy on disk ("" = kept in memory by a store that
	// never writes).
	Path string
}

const (
	backupConfig   = "config.json.bak-"
	backupProfiles = "profiles.json.bak-"
	backupStamp    = "2006-01-02-150405"
	keepBackups    = 5
)

// memBackups are the backups of in-memory stores (tests, demo renders).
var memBackups = struct {
	sync.Mutex
	m map[*Store][]Backup
}{m: map[*Store][]Backup{}}

// SaveBackup keeps a copy of the current settings and profiles before an
// import: config.json.bak-<date> and profiles.json.bak-<date> next to
// config.json (the newest five are kept). A store that never writes keeps
// it in memory.
func (s *Store) SaveBackup(profiles []Profile, now time.Time) (Backup, error) {
	b := Backup{Time: now, Config: s.Get()}
	if profiles != nil {
		b.Profiles = append([]Profile{}, profiles...)
	}
	if !s.persist {
		memBackups.Lock()
		list := append([]Backup{b}, memBackups.m[s]...)
		if len(list) > keepBackups {
			list = list[:keepBackups]
		}
		memBackups.m[s] = list
		memBackups.Unlock()
		return b, nil
	}
	return WriteBackup(Dir(), b)
}

// Backups are the kept copies, newest first.
func (s *Store) Backups() []Backup {
	if !s.persist {
		memBackups.Lock()
		defer memBackups.Unlock()
		return append([]Backup(nil), memBackups.m[s]...)
	}
	return ReadBackups(Dir())
}

// WriteBackup writes b into dir (config.json.bak-<date>, profiles.json.bak-<date>)
// and keeps only the newest copies there. It returns b with its Path.
func WriteBackup(dir string, b Backup) (Backup, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return b, fmt.Errorf("не вдалося зберегти резервну копію: %w", err)
	}
	stamp := b.Time.Format(backupStamp)
	suffix := stamp
	for n := 2; ; n++ {
		if _, err := os.Stat(filepath.Join(dir, backupConfig+suffix)); errors.Is(err, os.ErrNotExist) {
			break
		}
		suffix = fmt.Sprintf("%s-%d", stamp, n)
	}
	cb, err := json.MarshalIndent(b.Config, "", "  ")
	if err != nil {
		return b, err
	}
	path := filepath.Join(dir, backupConfig+suffix)
	if err := os.WriteFile(path, cb, 0o600); err != nil {
		return b, fmt.Errorf("не вдалося зберегти резервну копію: %w", err)
	}
	if b.Profiles != nil {
		pb, err := json.MarshalIndent(b.Profiles, "", "  ")
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, backupProfiles+suffix), pb, 0o600)
		}
		if err != nil {
			_ = os.Remove(path)
			return b, fmt.Errorf("не вдалося зберегти резервну копію профілів: %w", err)
		}
	}
	b.Path = path
	pruneBackups(dir)
	return b, nil
}

// backupNames are the config backups of dir, newest first.
func backupNames(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if n := e.Name(); !e.IsDir() && strings.HasPrefix(n, backupConfig) && !strings.HasSuffix(n, ".tmp") {
			names = append(names, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	return names
}

func pruneBackups(dir string) {
	names := backupNames(dir)
	for i := keepBackups; i < len(names); i++ {
		suffix := strings.TrimPrefix(names[i], backupConfig)
		_ = os.Remove(filepath.Join(dir, names[i]))
		_ = os.Remove(filepath.Join(dir, backupProfiles+suffix))
	}
}

// ReadBackups loads the backups of dir, newest first (unreadable ones are
// left out).
func ReadBackups(dir string) []Backup {
	var out []Backup
	for _, n := range backupNames(dir) {
		path := filepath.Join(dir, n)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		c := Default()
		if json.Unmarshal(raw, &c) != nil {
			continue
		}
		c.Normalize()
		suffix := strings.TrimPrefix(n, backupConfig)
		b := Backup{Config: c, Path: path}
		stampPart := suffix
		if len(stampPart) > len(backupStamp) {
			stampPart = stampPart[:len(backupStamp)]
		}
		if t, err := time.ParseInLocation(backupStamp, stampPart, time.Local); err == nil {
			b.Time = t
		} else if st, err := os.Stat(path); err == nil {
			b.Time = st.ModTime()
		}
		if pb, err := os.ReadFile(filepath.Join(dir, backupProfiles+suffix)); err == nil {
			var list []Profile
			if json.Unmarshal(pb, &list) == nil {
				b.Profiles = list
			}
		}
		out = append(out, b)
	}
	return out
}

// ApplyBackup puts b's settings back into c (inside Store.Update). The
// update bookkeeping stays as it is now.
func ApplyBackup(c *Config, b Backup) {
	checked, seen := c.UI.UpdateChecked, c.UI.UpdateSeen
	n := b.Config
	n.Google.Keys = append([]string(nil), b.Config.Google.Keys...)
	n.TTS = b.Config.TTS.clone()
	n.TTS.view = ""
	n.Telegram = b.Config.Telegram.clone()
	*c = n
	c.UI.UpdateChecked, c.UI.UpdateSeen = checked, seen
}
