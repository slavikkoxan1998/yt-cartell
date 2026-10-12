package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Every key here is a fake; files go to t.TempDir() and the settings folder
// is redirected by TestMain (profiles_test.go).

const pw = "кіт на даху 2026"

func sampleConfig(t *testing.T) Config {
	t.Helper()
	c := Default()
	c.Text = Text{Provider: "openai", BaseURL: "https://gw.example.test/v1", APIKey: "text-key-0001", Model: "gpt-test"}
	c.Glabs.APIKey = "glabs-key-0001"
	c.Google.Keys = []string{"AIzaFAKE-0001", "AIzaFAKE-0002", "AIzaFAKE-0003"}
	c.TTS.Switch("elevenlabs")
	c.TTS.SetKeys([]string{"sk_" + strings.Repeat("ab", 20)})
	c.TTS.Voice = "Xb7hH8MSUJpSbSDYk0k2"
	c.TTS.Switch("cartesia")
	var car []string
	for i := 0; i < 52; i++ {
		car = append(car, "sk_car_FAKEFAKEFAKE"+strings.Repeat("x", i%3)+string(rune('A'+i%26))+string(rune('a'+i/26)))
	}
	c.TTS.SetKeys(car)
	c.STT = STT{Provider: "assemblyai", AssemblyKey: "aai-key-0001", LocalBin: `C:\old\whisper-cli.exe`, LocalModel: `C:\old\ggml-small.bin`}
	c.Dictation = Dictation{Provider: "deepgram", DeepgramKey: "dg-key-0001", Device: "Microphone (Old USB)", Language: "uk"}
	c.Telegram = Telegram{Enabled: true, BotToken: "123456:FAKE-token", ChatID: "42", OnDone: true}
	c.Custom = CustomAPI{VideoPreset: "royaltechno", VideoKey: "royal-key-0001", VideoModel: "veo-3.1",
		ImagePreset: "universal", ImageKey: "uni-key-0001", ImageSpec: `{"submit":{"path":"/gen"}}`}
	c.Video.Provider = "custom"
	c.Concurrency = 6
	c.UI.Theme = "dark"
	c.UI.ReducedMotion = true
	c.UI.UpdateChecked, c.UI.UpdateSeen = 12345, "9.9.9"
	c.Autopilot = Autopilot{DefaultProfileID: "p-mine", MakeCover: false, Retries: 2}
	c.DataDir = filepath.Join(t.TempDir(), "missing", "old-data")
	c.FFmpegPath = `C:\nowhere\ffmpeg.exe`
	c.FFprobePath = `C:\nowhere\ffprobe.exe`
	c.Normalize()
	return c
}

func sampleProfiles() []Profile {
	ps := StarterProfiles()[:4] // the four starters
	ps = append(ps, Profile{ID: "p-mine", Name: "Мій канал", Aspect: "9:16", Style: "anime", TargetSeconds: 60})
	for i := range ps {
		ps[i].Normalize()
	}
	return ps
}

var allSections = ExportOptions{Connections: true, Profiles: true, Defaults: true, KeyStates: true, AppVersion: "2.0.0"}

const sampleStates = `{"version":1,"keys":{"aaaa":{"provider":"cartesia","mask":"sk_ca…AAa","state":"exhausted"},"bbbb":{"provider":"cartesia","mask":"sk_ca…BBa","state":"ok","usedChars":120}}}`

func withPassword(o ExportOptions, p string) ExportOptions { o.Password = p; return o }

// roundTrip exports and opens again.
func roundTrip(t *testing.T, c Config, opt ExportOptions) (*Bundle, []byte) {
	t.Helper()
	data, err := Export(c, sampleProfiles(), []byte(sampleStates), opt)
	if err != nil {
		t.Fatal(err)
	}
	f, err := ReadExport(data)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.Open(opt.Password)
	if err != nil {
		t.Fatal(err)
	}
	return b, data
}

func TestExportEncryptedRoundTrip(t *testing.T) {
	src := sampleConfig(t)
	opt := withPassword(allSections, pw)
	data, err := Export(src, sampleProfiles(), []byte(sampleStates), opt)
	if err != nil {
		t.Fatal(err)
	}
	// No secret and no settings value is readable in the file.
	for _, secret := range []string{"text-key-0001", "AIzaFAKE-0002", "sk_car_FAKE", "royal-key-0001", "123456:FAKE-token", "Мій канал", "old-data"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("the encrypted file shows %q", secret)
		}
	}
	f, err := ReadExport(data)
	if err != nil {
		t.Fatal(err)
	}
	h := f.Header
	if !f.Encrypted() || h.KDF.Name != "pbkdf2-sha256" || h.KDF.Iterations < 600_000 || len(h.KDF.Salt) != 32 ||
		h.Cipher.Name != "aes-256-gcm" || len(h.Cipher.Nonce) != 12 || h.Schema != ExportSchema || h.AppVersion != "2.0.0" || h.OS == "" {
		t.Fatalf("header: %+v kdf %+v cipher %+v", h, h.KDF, h.Cipher)
	}
	if _, err := f.Open(""); !errors.Is(err, ErrNeedPassword) {
		t.Fatalf("no password: %v", err)
	}
	if _, err := f.Open("кіт на даху 2025"); !errors.Is(err, ErrWrongPassword) {
		t.Fatalf("wrong password: %v", err)
	}
	b, err := f.Open(pw)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Warnings) != 0 || len(b.Skipped) != 0 {
		t.Fatalf("warnings on a file of this version: %v %v", b.Warnings, b.Skipped)
	}
	// Everything arrives on a fresh computer.
	dst := Default()
	dst.Normalize()
	cur := dst.DataDir
	ApplyImport(&dst, b, b.DefaultPlan(), nil)
	dst.Normalize()
	for name, pair := range map[string][2]any{
		"text":      {src.Text, dst.Text},
		"glabs":     {src.Glabs, dst.Glabs},
		"google":    {src.Google, dst.Google},
		"telegram":  {src.Telegram, dst.Telegram},
		"custom":    {src.Custom, dst.Custom},
		"video":     {src.Video, dst.Video},
		"ui.theme":  {src.UI.Theme, dst.UI.Theme},
		"motion":    {src.UI.ReducedMotion, dst.UI.ReducedMotion},
		"conc":      {src.Concurrency, dst.Concurrency},
		"autopilot": {src.Autopilot, dst.Autopilot},
		"tts":       {src.TTS.Providers, dst.TTS.Providers},
		"tts.prov":  {src.TTS.Provider, dst.TTS.Provider},
		"stt.aai":   {src.STT.AssemblyKey, dst.STT.AssemblyKey},
		"dict.key":  {src.Dictation.DeepgramKey, dst.Dictation.DeepgramKey},
	} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("%s: got %+v, want %+v", name, pair[1], pair[0])
		}
	}
	if n := len(dst.TTS.Of("cartesia").AllKeys()); n != 52 {
		t.Errorf("cartesia keys: %d", n)
	}
	// Never taken: update bookkeeping, machine paths.
	if dst.UI.UpdateChecked != 0 || dst.UI.UpdateSeen != "" {
		t.Errorf("update state taken: %+v", dst.UI)
	}
	if dst.DataDir != cur || dst.FFmpegPath != "" || dst.STT.LocalBin != "" || dst.Dictation.Device != "" {
		t.Errorf("machine paths applied: %q %q %q %q", dst.DataDir, dst.FFmpegPath, dst.STT.LocalBin, dst.Dictation.Device)
	}
	// The hints travel for the dialog.
	if b.Hints.DataDir != src.DataDir || b.Hints.FFmpeg != src.FFmpegPath || b.Hints.WhisperBin != src.STT.LocalBin || b.Hints.Microphone != src.Dictation.Device {
		t.Errorf("hints: %+v", b.Hints)
	}
	if len(b.Profiles) != 5 || !b.HasProfiles {
		t.Errorf("profiles: %d", len(b.Profiles))
	}
	if r, ex := b.KeyStateCounts(); r != 2 || ex != 1 {
		t.Errorf("key states %d/%d", r, ex)
	}
	if got := b.Connections.TTSKeys(); len(got) != 2 || got[0].ID != "cartesia" || got[0].Keys != 52 || !got[0].Active || got[1].Keys != 1 {
		t.Errorf("tts summary: %+v", got)
	}
}

func TestExportPlainRoundTrip(t *testing.T) {
	src := sampleConfig(t)
	b, data := roundTrip(t, src, allSections)
	if !bytes.Contains(data, []byte("text-key-0001")) {
		t.Fatal("a plain file should carry the payload in the clear")
	}
	f, _ := ReadExport(data)
	if f.Encrypted() || f.Header.SHA256 == "" {
		t.Fatalf("plain header: %+v", f.Header)
	}
	if b.Connections == nil || b.Connections.Text.APIKey != "text-key-0001" || b.Defaults == nil || *b.Defaults.Concurrency != 6 {
		t.Fatalf("bundle: %+v", b)
	}
	// A changed value breaks the checksum.
	bad := bytes.Replace(data, []byte("text-key-0001"), []byte("text-key-0002"), 1)
	if _, err := ReadExport(bad); !errors.Is(err, ErrDamaged) {
		t.Fatalf("tampered plain file: %v", err)
	}
	// Only the chosen sections go in.
	data, err := Export(src, sampleProfiles(), nil, ExportOptions{Profiles: true})
	if err != nil {
		t.Fatal(err)
	}
	f, _ = ReadExport(data)
	b, err = f.Open("")
	if err != nil || b.Connections != nil || b.Defaults != nil || len(b.KeyStates) != 0 || len(b.Profiles) != 5 {
		t.Fatalf("profiles only: %+v %v", b, err)
	}
	if _, err := Export(src, nil, nil, ExportOptions{}); err == nil {
		t.Fatal("an empty export was written")
	}
}

func TestExportTampered(t *testing.T) {
	src := sampleConfig(t)
	data, err := Export(src, sampleProfiles(), nil, withPassword(allSections, pw))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	// A flipped ciphertext byte: the password is right, GCM refuses.
	var ct []byte
	_ = json.Unmarshal(doc["data"], &ct)
	ct[len(ct)/2] ^= 0x01
	doc["data"], _ = json.Marshal(ct)
	b1, _ := json.Marshal(doc)
	f, err := ReadExport(b1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Open(pw); !errors.Is(err, ErrDamaged) {
		t.Fatalf("flipped ciphertext: %v", err)
	}
	// An edited header (it is the additional data): damaged, not a wrong password.
	b2 := bytes.Replace(data, []byte(`"appVersion": "2.0.0"`), []byte(`"appVersion": "2.0.1"`), 1)
	if bytes.Equal(b2, data) {
		t.Fatal("header not found in the file")
	}
	f, err = ReadExport(b2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Open(pw); !errors.Is(err, ErrDamaged) {
		t.Fatalf("edited header: %v", err)
	}
	// Whitespace does not matter (the header is compacted before use).
	var any map[string]any
	_ = json.Unmarshal(data, &any)
	b3, _ := json.Marshal(any)
	f, err = ReadExport(b3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Open(pw); err != nil {
		t.Fatalf("re-formatted file: %v", err)
	}
	// Garbage and foreign JSON.
	for _, junk := range []string{"", "hello", `{"a":1}`, `[1,2]`, `{"ytcs":1,"header":{"app":"Other"}}`} {
		if _, err := ReadExport([]byte(junk)); !errors.Is(err, ErrNotExport) {
			t.Errorf("%q: %v", junk, err)
		}
	}
	// A hostile work factor is refused before any derivation.
	b4 := bytes.Replace(data, []byte(`"iterations": 600000`), []byte(`"iterations": 2000000000`), 1)
	if _, err := ReadExport(b4); !errors.Is(err, ErrDamaged) {
		t.Fatalf("huge iterations: %v", err)
	}
}

// A newer studio's file: the container is the same, the payload has a newer
// schema with unknown sections and fields — what is understood is imported,
// the rest is listed.
func TestExportFutureVersion(t *testing.T) {
	payload := `{
		"connections": {
			"text": {"provider":"openai","baseUrl":"https://new.test/v1","apiKey":"k-new","model":"m","temperature":0.3},
			"tts": {"provider":"cartesia","apiKey":"sk_car_NEWNEWNEWNEW1","providers":{"cartesia":{"apiKey":"sk_car_NEWNEWNEWNEW1","emotion":"calm"}}},
			"glabs": "this used to be an object",
			"hologram": {"key": "x"}
		},
		"profiles": [{"id":"p-x","name":"Новий","aspect":"16:9","hdr":true}, 42],
		"defaults": {"concurrency": 3, "autopilot": {"retries": 2, "smartMode": true}},
		"music": {"library": []}
	}`
	var compact bytes.Buffer
	_ = json.Compact(&compact, []byte(payload))
	file := buildPlain(t, ExportHeader{App: exportApp, AppVersion: "3.1.0", Schema: 2, CreatedAt: time.Now(), OS: "darwin"}, compact.Bytes())
	f, err := ReadExport(file)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.Open("")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"connections.hologram", "connections.text.temperature", "connections.tts.providers.cartesia.emotion",
		"defaults.autopilot.smartMode", "music", "profiles.hdr"}
	if !reflect.DeepEqual(b.Skipped, want) {
		t.Fatalf("skipped %v\nwant    %v", b.Skipped, want)
	}
	if len(b.Warnings) != 2 || !strings.Contains(b.Warnings[0], "3.1.0") || !strings.Contains(b.Warnings[1], "connections.glabs") {
		t.Fatalf("warnings: %q", b.Warnings)
	}
	if b.Connections.Glabs != nil || b.Connections.Text == nil || b.Connections.Text.APIKey != "k-new" || len(b.Profiles) != 1 {
		t.Fatalf("bundle: %+v", b.Connections)
	}
	dst := Default()
	dst.Glabs.APIKey = "keep-me"
	dst.Normalize()
	ApplyImport(&dst, b, b.DefaultPlan(), nil)
	dst.Normalize()
	if dst.Text.BaseURL != "https://new.test/v1" || dst.TTS.Provider != "cartesia" || dst.TTS.APIKey != "sk_car_NEWNEWNEWNEW1" ||
		dst.Glabs.APIKey != "keep-me" || dst.Concurrency != 3 || dst.Autopilot.Retries != 2 {
		t.Fatalf("applied: text %+v tts %s/%s glabs %q conc %d", dst.Text, dst.TTS.Provider, dst.TTS.APIKey, dst.Glabs.APIKey, dst.Concurrency)
	}
	// A newer container (crypto) format is refused with the version named.
	newer := bytes.Replace(file, []byte(`"ytcs": 1`), []byte(`"ytcs": 2`), 1)
	var nf *NewerFormatError
	if _, err := ReadExport(newer); !errors.As(err, &nf) || nf.AppVersion != "3.1.0" {
		t.Fatalf("newer container: %v", err)
	}
	enc, _ := Export(sampleConfig(t), nil, nil, withPassword(ExportOptions{Defaults: true}, pw))
	enc = bytes.Replace(enc, []byte(`"name": "pbkdf2-sha256"`), []byte(`"name": "argon2id"`), 1)
	if _, err := ReadExport(enc); !errors.As(err, &nf) {
		t.Fatalf("unknown KDF: %v", err)
	}
}

func buildPlain(t *testing.T, h ExportHeader, payload []byte) []byte {
	t.Helper()
	h.SHA256 = sha256Hex(payload)
	hb, _ := json.Marshal(h)
	one := 1
	out, err := json.MarshalIndent(exportDoc{Format: &one, Header: hb, Payload: payload}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestImportMergeVsReplace(t *testing.T) {
	src := sampleConfig(t)
	b, _ := roundTrip(t, src, allSections)

	here := Default()
	here.Text = Text{Provider: "openai", BaseURL: "https://mine.test/v1", APIKey: "my-text-key", Model: "mine"}
	here.Google.Keys = []string{"AIzaFAKE-0002", "AIzaMINE-0001"}
	here.TTS.Switch("cartesia")
	here.TTS.SetKeys([]string{"sk_car_MINEMINEMINE1", "sk_car_FAKEFAKEFAKEAa"})
	here.TTS.Voice = "11111111-2222-3333-4444-555555555555"
	here.TTS.Switch("openai")
	here.TTS.SetKeys([]string{"sk-proj-MINEMINEMINEMINEMINEMINE"})
	here.Telegram = Telegram{}
	here.Normalize()

	merged := here
	merged.TTS = here.TTS.clone()
	merged.Google.Keys = append([]string(nil), here.Google.Keys...)
	plan := b.DefaultPlan()
	plan.Connections.Mode = ModeMerge
	ApplyImport(&merged, b, plan, nil)
	merged.Normalize()
	if merged.Text.APIKey != "my-text-key" || merged.Text.BaseURL != "https://mine.test/v1" {
		t.Errorf("merge replaced a set-up service: %+v", merged.Text)
	}
	if got := strings.Join(merged.Google.Keys, ","); got != "AIzaFAKE-0002,AIzaMINE-0001,AIzaFAKE-0001,AIzaFAKE-0003" {
		t.Errorf("google keys: %s", got)
	}
	if merged.TTS.Provider != "openai" {
		t.Errorf("merge changed the active voice-over service to %s", merged.TTS.Provider)
	}
	car := merged.TTS.Of("cartesia")
	if n := len(car.AllKeys()); n != 53 || car.AllKeys()[0] != "sk_car_MINEMINEMINE1" || car.Voice != "11111111-2222-3333-4444-555555555555" {
		t.Errorf("cartesia after merge: %d keys, first %q, voice %q", n, car.AllKeys()[0], car.Voice)
	}
	if el := merged.TTS.Of("elevenlabs"); len(el.AllKeys()) != 1 || el.Voice != "Xb7hH8MSUJpSbSDYk0k2" {
		t.Errorf("elevenlabs not added: %+v", el)
	}
	if merged.Telegram.BotToken != "123456:FAKE-token" {
		t.Errorf("a service not set up here was not taken: %+v", merged.Telegram)
	}
	if merged.Video.Provider != here.Video.Provider {
		t.Errorf("merge changed the video choice")
	}

	replaced := here
	replaced.TTS = here.TTS.clone()
	ApplyImport(&replaced, b, b.DefaultPlan(), nil)
	replaced.Normalize()
	if replaced.Text != src.Text || strings.Join(replaced.Google.Keys, ",") != strings.Join(src.Google.Keys, ",") ||
		replaced.TTS.Provider != "cartesia" || len(replaced.TTS.Of("openai").AllKeys()) != 0 || len(replaced.TTS.Of("cartesia").AllKeys()) != 52 {
		t.Errorf("replace: text %+v, google %v, tts %s openai %d cartesia %d", replaced.Text, replaced.Google.Keys, replaced.TTS.Provider,
			len(replaced.TTS.Of("openai").AllKeys()), len(replaced.TTS.Of("cartesia").AllKeys()))
	}

	// Sections that are off change nothing.
	off := here
	off.TTS = here.TTS.clone()
	ApplyImport(&off, b, ImportPlan{}, nil)
	off.Normalize()
	if off.Text != here.Text || off.UI != here.UI || off.Concurrency != here.Concurrency {
		t.Error("an empty plan changed the settings")
	}
}

func TestImportProfiles(t *testing.T) {
	cur := sampleProfiles()[:2] // the YouTube and Shorts starters
	cur = append(cur, Profile{ID: "p-mine", Name: "Інший", Aspect: "16:9"})
	for i := range cur {
		cur[i].Normalize()
	}
	in := sampleProfiles()   // 4 starters + "Мій канал" (id p-mine, taken here)
	in[1].TargetSeconds = 30 // "Shorts / Reels 9:16" with other settings
	in = append(in, in[0])   // a repeat inside the file
	out, ids, added, same := MergeProfiles(cur, in)
	if same != 2 || added != 4 {
		t.Fatalf("added %d same %d", added, same)
	}
	names := map[string]bool{}
	for _, p := range out {
		if names[p.ID] {
			t.Fatalf("duplicate id %s", p.ID)
		}
		names[p.ID] = true
	}
	var shorts, mine Profile
	for _, p := range out {
		switch p.Name {
		case "Shorts / Reels 9:16 (імпорт)":
			shorts = p
		case "Мій канал":
			mine = p
		}
	}
	if shorts.ID == "" || shorts.TargetSeconds != 30 || shorts.ID == "starter-shorts" {
		t.Fatalf("conflicting name: %+v", shorts)
	}
	if mine.ID == "" || mine.ID == "p-mine" || ids["p-mine"] != mine.ID {
		t.Fatalf("taken id: %+v, map %v", mine, ids)
	}
	if ids["starter-youtube"] != "starter-youtube" {
		t.Fatalf("identical profile not mapped: %v", ids)
	}
	// Importing the same file again adds nothing.
	_, _, added, same = MergeProfiles(out, in)
	if added != 0 || same != len(in) {
		t.Fatalf("second merge: added %d same %d", added, same)
	}
	// Replace keeps the file's ids.
	rep, rids := ReplaceProfiles(sampleProfiles())
	if len(rep) != 5 || rep[4].ID != "p-mine" || rids["p-mine"] != "p-mine" {
		t.Fatalf("replace: %+v", rids)
	}

	// The default autopilot profile follows its id (or stays when unknown).
	src := sampleConfig(t)
	b, _ := roundTrip(t, src, ExportOptions{Defaults: true})
	dst := Default()
	dst.Autopilot.DefaultProfileID = "starter-kids"
	ApplyImport(&dst, b, b.DefaultPlan(), func(id string) string { return ids[id] })
	if dst.Autopilot.DefaultProfileID != mine.ID {
		t.Fatalf("default profile %q, want %q", dst.Autopilot.DefaultProfileID, mine.ID)
	}
	dst.Autopilot.DefaultProfileID = "starter-kids"
	ApplyImport(&dst, b, b.DefaultPlan(), func(string) string { return "" })
	if dst.Autopilot.DefaultProfileID != "starter-kids" {
		t.Fatalf("unknown profile replaced the choice: %q", dst.Autopilot.DefaultProfileID)
	}

	// ReplaceAll persists atomically.
	dir := t.TempDir()
	s, err := NewProfileStoreAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ReplaceAll(rep); err != nil {
		t.Fatal(err)
	}
	again, err := NewProfileStoreAt(dir)
	if err != nil || len(again.List()) != 5 || again.List()[4].Name != "Мій канал" {
		t.Fatalf("reloaded: %v %v", again.List(), err)
	}
}

func TestImportHints(t *testing.T) {
	src := sampleConfig(t)
	b, _ := roundTrip(t, src, allSections)

	here := Default()
	here.STT.LocalBin = `D:\here\whisper-cli.exe`
	here.Dictation.Device = "Mic (here)"
	here.Normalize()
	keep := here.DataDir

	// Asked, but nothing exists here: nothing changes.
	a := here
	plan := b.DefaultPlan()
	plan.Hints = HintPlan{DataDir: true, FFmpeg: true, Whisper: true}
	ApplyImport(&a, b, plan, nil)
	if a.DataDir != keep || a.FFmpegPath != "" || a.STT.LocalBin != `D:\here\whisper-cli.exe` || a.Dictation.Device != "Mic (here)" {
		t.Fatalf("missing paths applied: %q %q %q %q", a.DataDir, a.FFmpegPath, a.STT.LocalBin, a.Dictation.Device)
	}
	if b.Hints.Here() != (HintPlan{}) {
		t.Fatalf("Here() found missing paths: %+v", b.Hints.Here())
	}

	// They exist here, but were not asked for: nothing changes either.
	dir := t.TempDir()
	data := filepath.Join(dir, "data")
	ff := filepath.Join(dir, "ffmpeg.exe")
	fp := filepath.Join(dir, "ffprobe.exe")
	_ = os.MkdirAll(data, 0o755)
	_ = os.WriteFile(ff, []byte("x"), 0o600)
	_ = os.WriteFile(fp, []byte("x"), 0o600)
	b.Hints.DataDir, b.Hints.FFmpeg, b.Hints.FFprobe = data, ff, fp
	c := here
	ApplyImport(&c, b, b.DefaultPlan(), nil)
	if c.DataDir != keep || c.FFmpegPath != "" {
		t.Fatalf("hints applied without asking: %q %q", c.DataDir, c.FFmpegPath)
	}
	if h := b.Hints.Here(); !h.DataDir || !h.FFmpeg || h.Whisper {
		t.Fatalf("Here(): %+v", h)
	}
	// Asked and present: taken.
	c = here
	plan.Hints = HintPlan{DataDir: true, FFmpeg: true, Microphone: true}
	ApplyImport(&c, b, plan, nil)
	if c.DataDir != data || c.FFmpegPath != ff || c.FFprobePath != fp || c.Dictation.Device != "Microphone (Old USB)" {
		t.Fatalf("hints not applied: %q %q %q %q", c.DataDir, c.FFmpegPath, c.FFprobePath, c.Dictation.Device)
	}
}

func TestSettingsBackup(t *testing.T) {
	// A persisting store in the redirected settings folder (TestMain).
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	src := sampleConfig(t)
	if err := Save(src); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Remove(Path())
		for _, n := range backupNames(Dir()) {
			_ = os.Remove(filepath.Join(Dir(), n))
		}
		m, _ := filepath.Glob(filepath.Join(Dir(), backupProfiles+"*"))
		for _, p := range m {
			_ = os.Remove(p)
		}
	})
	s, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 2, 15, 30, 12, 0, time.Local)
	bk, err := s.SaveBackup(sampleProfiles(), at)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(bk.Path) != "config.json.bak-2026-10-02-153012" {
		t.Fatalf("backup path %s", bk.Path)
	}
	if _, err := os.Stat(filepath.Join(Dir(), "profiles.json.bak-2026-10-02-153012")); err != nil {
		t.Fatalf("profiles backup: %v", err)
	}
	// A second one in the same second does not overwrite the first.
	bk2, err := s.SaveBackup(nil, at)
	if err != nil || bk2.Path == bk.Path {
		t.Fatalf("second backup %s: %v", bk2.Path, err)
	}
	list := s.Backups()
	if len(list) != 2 || list[1].Path != bk.Path || len(list[1].Profiles) != 5 || !list[1].Time.Equal(at) || list[0].Profiles != nil {
		t.Fatalf("backups: %d", len(list))
	}
	if list[1].Config.Text.APIKey != "text-key-0001" {
		t.Fatalf("backup content: %+v", list[1].Config.Text)
	}
	// Only the newest copies stay.
	for i := 0; i < 8; i++ {
		if _, err := s.SaveBackup(nil, at.Add(time.Duration(i+1)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(s.Backups()); n != keepBackups {
		t.Fatalf("%d backups kept", n)
	}
	if m, _ := filepath.Glob(filepath.Join(Dir(), backupProfiles+"*")); len(m) != 0 {
		t.Fatalf("pruned profiles backups left: %v", m)
	}
	// Restoring keeps the update bookkeeping of now.
	c := Default()
	c.UI.UpdateSeen = "NOW"
	ApplyBackup(&c, list[1])
	c.Normalize()
	if c.Text != src.Text || c.UI.UpdateSeen != "NOW" || c.DataDir != src.DataDir || len(c.TTS.Of("cartesia").AllKeys()) != 52 {
		t.Fatalf("restored: %+v %+v", c.Text, c.UI)
	}

	// An in-memory store keeps its backups in memory and writes nothing.
	m := NewStoreWith(Default())
	before := backupNames(Dir())
	if _, err := m.SaveBackup(sampleProfiles(), at); err != nil {
		t.Fatal(err)
	}
	if got := m.Backups(); len(got) != 1 || got[0].Path != "" || len(got[0].Profiles) != 5 {
		t.Fatalf("memory backups: %+v", got)
	}
	if after := backupNames(Dir()); len(after) != len(before) {
		t.Fatal("an in-memory store wrote a backup")
	}
}

func TestExportFileNameAndWrite(t *testing.T) {
	if n := ExportFileName(time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)); n != "YT-Cartell-Studio-settings-2026-10-02.ytcs" {
		t.Fatal(n)
	}
	data, err := Export(sampleConfig(t), nil, nil, withPassword(ExportOptions{Connections: true}, pw))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sub", ExportFileName(time.Now()))
	if err := WriteExportFile(path, data); err != nil {
		t.Fatal(err)
	}
	f, err := ReadExportFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Open(pw); err != nil {
		t.Fatal(err)
	}
	// The salt and nonce are fresh for every file.
	data2, _ := Export(sampleConfig(t), nil, nil, withPassword(ExportOptions{Connections: true}, pw))
	f2, _ := ReadExport(data2)
	if bytes.Equal(f.Header.KDF.Salt, f2.Header.KDF.Salt) || bytes.Equal(f.Header.Cipher.Nonce, f2.Header.Cipher.Nonce) {
		t.Fatal("salt or nonce reused")
	}
}

func TestPasswordNormalisation(t *testing.T) {
	// "й" precomposed vs decomposed (и + combining breve) is the same password.
	data, err := Export(Default(), nil, nil, withPassword(ExportOptions{Defaults: true}, "мій пароль"))
	if err != nil {
		t.Fatal(err)
	}
	f, _ := ReadExport(data)
	if _, err := f.Open("мі\u0438\u0306 пароль"); err != nil {
		t.Fatalf("decomposed password: %v", err)
	}
}

// Remote control through the bot moves with the token and the pairing, but
// stays off on a computer that did not run it with that bot (one bot cannot
// take commands on two computers at once).
func TestImportTelegramRemote(t *testing.T) {
	src := sampleConfig(t)
	src.Telegram.Remote, src.Telegram.OwnerID = true, 42
	src.Telegram.Allowed = []TelegramUser{{ID: 7, Name: "Петро"}}
	src.Normalize()
	b, _ := roundTrip(t, src, ExportOptions{Connections: true})

	fresh := Default()
	ApplyImport(&fresh, b, b.DefaultPlan(), nil)
	fresh.Normalize()
	if fresh.Telegram.Remote || fresh.Telegram.OwnerID != 42 || len(fresh.Telegram.Allowed) != 1 || fresh.Telegram.BotToken != src.Telegram.BotToken {
		t.Fatalf("new computer: %+v", fresh.Telegram)
	}
	fresh.Telegram.Allowed[0].Name = "змінено"
	if b.Connections.Telegram.Allowed[0].Name != "Петро" {
		t.Fatal("the imported list is shared with the file")
	}

	same := Default()
	same.Telegram = src.Telegram.clone()
	ApplyImport(&same, b, b.DefaultPlan(), nil)
	if !same.Telegram.Remote {
		t.Fatal("remote control turned off where it already ran with the same bot")
	}
}

// Files of older versions carried a top-level "unlocks" section (since
// removed): it is ignored, never reported as unknown.
func TestImportIgnoresLegacyUnlocks(t *testing.T) {
	b, err := decodePayload(ExportHeader{Schema: ExportSchema}, []byte(`{"profiles":[],"unlocks":{"x":true}}`))
	if err != nil || len(b.Skipped) != 0 || len(b.Warnings) != 0 || !b.HasProfiles {
		t.Fatalf("bundle %+v %v", b, err)
	}
}

// An old config.json with the removed sign-in session and unlock flags
// still loads; the next save drops them.
func TestLoadIgnoresLegacyFields(t *testing.T) {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		t.Fatal(err)
	}
	old, _ := os.ReadFile(Path())
	defer func() {
		if old == nil {
			_ = os.Remove(Path())
		} else {
			_ = os.WriteFile(Path(), old, 0o600)
		}
	}()
	legacy := `{"concurrency":3,"auth":{"session":"demo-session","user":{"id":"1","name":"x"},"lastVerified":5,"missing":["chat"]},"unlocks":{"x":true}}`
	if err := os.WriteFile(Path(), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load()
	if err != nil || c.Concurrency != 3 {
		t.Fatalf("load: %v %d", err, c.Concurrency)
	}
	if err := Save(c); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path())
	if bytes.Contains(b, []byte(`"auth"`)) || bytes.Contains(b, []byte("demo-session")) || bytes.Contains(b, []byte(`"unlocks"`)) {
		t.Fatalf("legacy fields kept: %s", b)
	}
}
