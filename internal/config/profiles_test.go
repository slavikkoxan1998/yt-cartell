package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// TestMain redirects the user settings folder (config.json, profiles.json)
// into a temp dir: these tests must never touch the real profile.
func TestMain(m *testing.M) {
	tmp, err := os.MkdirTemp("", "ytc-config-test-")
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	os.Setenv("AppData", filepath.Join(tmp, "appdata"))
	os.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, "xdg"))
	if runtime.GOOS == "darwin" {
		os.Setenv("HOME", filepath.Join(tmp, "home"))
	}
	if !strings.HasPrefix(Dir(), tmp) {
		fmt.Println("refusing to run: settings folder is not redirected:", Dir())
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(tmp)
	os.Exit(code)
}

func ids(list []Profile) string {
	var out []string
	for _, p := range list {
		out = append(out, p.ID)
	}
	return strings.Join(out, ",")
}

func TestProfileStoreSeedsOnceAndPersists(t *testing.T) {
	dir := t.TempDir()
	s, err := NewProfileStoreAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	starters := StarterProfiles()
	if got := s.List(); len(got) != 4 || ids(got) != ids(starters) {
		t.Fatalf("seed: %s", ids(got))
	}
	names := map[string]bool{}
	for _, p := range s.List() {
		names[p.Name] = true
		if p.CreatedAt == 0 || p.UpdatedAt == 0 || p.Aspect == "" || p.TargetSeconds == 0 {
			t.Fatalf("starter %+v", p)
		}
	}
	for _, n := range []string{"YouTube історія 16:9", "Shorts / Reels 9:16", "Дитяча казка", "Документальне пояснення"} {
		if !names[n] {
			t.Fatalf("missing starter %q", n)
		}
	}
	if len(s.List()) != len(StarterProfiles()) {
		t.Fatalf("seeded %d profiles", len(s.List()))
	}
	if sh, _ := s.Get("starter-shorts"); sh.Aspect != "9:16" || sh.Subtitles != "burn" {
		t.Fatalf("shorts preset %+v", sh)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles.json")); err != nil {
		t.Fatal("first run must write profiles.json")
	}

	// create + update + delete, then reopen
	mine, err := s.Save(Profile{Name: "  Мій канал  ", Aspect: "4:3", Tempo: "warp", ImageModel: "?", Transition: "wipe",
		Subtitles: "x", MusicVolume: 7, AmbientVolume: -1, TargetSeconds: 5, Language: " EN ", Camera: "drone"})
	if err != nil {
		t.Fatal(err)
	}
	if mine.ID == "" || mine.Name != "Мій канал" || mine.Aspect != "16:9" || mine.Tempo != "balanced" || mine.ImageModel != "NARWHAL" ||
		mine.Transition != "wipe" /* catalog ids are validated by studio.SaveProfile */ || mine.Subtitles != "srt" || mine.MusicVolume != 1 || mine.AmbientVolume != 0 ||
		mine.TargetSeconds != 8 || mine.Language != "en" || mine.Camera != "drone" || mine.CreatedAt == 0 || mine.UpdatedAt < mine.CreatedAt {
		t.Fatalf("normalized %+v", mine)
	}
	mine.Name = "Перейменований"
	mine.CreatedAt = 1 // ignored: kept from the stored profile
	upd, err := s.Save(mine)
	if err != nil || upd.Name != "Перейменований" || upd.CreatedAt == 1 || upd.ID != mine.ID {
		t.Fatalf("update %+v %v", upd, err)
	}
	if _, err := s.Save(Profile{ID: "nope", Name: "x"}); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("update unknown: %v", err)
	}
	if err := s.Delete("starter-kids"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("starter-kids"); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
	s2, err := NewProfileStoreAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(s2.List()); got != "starter-youtube,starter-shorts,starter-explainer,"+mine.ID {
		t.Fatalf("reloaded %s", got)
	}
	if p, ok := s2.Get(mine.ID); !ok || p.Name != "Перейменований" {
		t.Fatalf("reloaded profile %+v", p)
	}
	// Deleting everything does not bring the starters back.
	for _, p := range s2.List() {
		if err := s2.Delete(p.ID); err != nil {
			t.Fatal(err)
		}
	}
	s3, _ := NewProfileStoreAt(dir)
	if len(s3.List()) != 0 {
		t.Fatalf("starters re-seeded: %s", ids(s3.List()))
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 { // profiles.json + profiles-starters.json
		t.Fatalf("temp files left behind: %v", entries)
	}
}

func TestProfileStoreDamagedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	if err := os.WriteFile(path, []byte(`[{"id":"a","name":"A"`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewProfileStoreAt(dir)
	if err == nil || !strings.Contains(err.Error(), "пошкоджений") {
		t.Fatalf("err %v", err)
	}
	if len(s.List()) != len(StarterProfiles()) {
		t.Fatalf("starters expected, got %s", ids(s.List()))
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "profiles.broken-*.json"))
	if len(matches) != 1 {
		t.Fatalf("damaged file must be kept aside: %v", matches)
	}
	if b, _ := os.ReadFile(matches[0]); string(b) != `[{"id":"a","name":"A"` {
		t.Fatalf("backup content %q", b)
	}

	// Duplicate and missing ids in a hand-edited file get fresh unique ids.
	if err := os.WriteFile(path, []byte(`[{"id":"x","name":"A"},{"id":"x","name":"B"},{"name":"C"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err = NewProfileStoreAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	list := s.List()
	seen := map[string]bool{}
	for _, p := range list {
		if p.ID == "" || seen[p.ID] {
			t.Fatalf("ids %s", ids(list))
		}
		seen[p.ID] = true
	}
	if len(list) != 3 || list[0].ID != "x" || list[1].Name != "B" {
		t.Fatalf("list %+v", list)
	}
}

func TestProfileStoreConcurrentSaves(t *testing.T) {
	dir := t.TempDir()
	s, err := NewProfileStoreAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := s.Save(Profile{Name: fmt.Sprint("p", i)}); err != nil {
				t.Error(err)
			}
			_ = s.List()
		}(i)
	}
	wg.Wait()
	if n := len(s.List()); n != 20+len(StarterProfiles()) {
		t.Fatalf("%d profiles, want %d", n, 20+len(StarterProfiles()))
	}
	b, err := os.ReadFile(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []Profile
	if err := json.Unmarshal(b, &onDisk); err != nil || len(onDisk) != 20+len(StarterProfiles()) {
		t.Fatalf("on disk: %d profiles, %v", len(onDisk), err)
	}
}

func TestProfileStoreForMatchesSettingsStore(t *testing.T) {
	mem, err := ProfileStoreFor(NewStoreWith(Default()))
	if err != nil || mem.Path() != "" || len(mem.List()) != len(StarterProfiles()) {
		t.Fatalf("in-memory settings → memory profiles: %q %v", mem.Path(), err)
	}
	if _, err := mem.Save(Profile{Name: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ProfilesPath()); !os.IsNotExist(err) {
		t.Fatal("a memory store wrote profiles.json")
	}
	st, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	disk, err := ProfileStoreFor(st)
	if err != nil || disk.Path() != ProfilesPath() || filepath.Dir(disk.Path()) != Dir() {
		t.Fatalf("persistent settings → profiles.json next to config.json: %q %v", disk.Path(), err)
	}
	if _, err := os.Stat(ProfilesPath()); err != nil {
		t.Fatal("starters not written")
	}
}

// An existing profiles.json is left as it is: no starter is added to it, and
// a profile with any id (also one an older version shipped) stays the user's.
func TestExistingStoreKeptAsIs(t *testing.T) {
	dir := t.TempDir()
	old := []Profile{{ID: "starter-youtube", Name: "YouTube історія 16:9"}, {ID: "p-mine", Name: "Мій канал", Notes: "моє"}}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(dir, "profiles.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		s, err := NewProfileStoreAt(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got := ids(s.List()); got != "starter-youtube,p-mine" {
			t.Fatalf("open %d: %s", i, got)
		}
		if p, _ := s.Get("p-mine"); p.Notes != "моє" {
			t.Fatalf("user profile changed %+v", p)
		}
	}
}

// The map-history building blocks (style, genre, tempo, camera, phrase
// subtitles, code animation with the map grammar, narrator style) survive
// Normalize in a user's profile.
func TestMapHistoryProfileFits(t *testing.T) {
	p := Profile{
		ID: "p-map", Name: "Історія на мапі",
		TargetSeconds: 100, Aspect: "9:16", Style: "history-map-pegs", Genre: "history map", Tempo: "rapid", Camera: "map-pan",
		ImageModel: "NARWHAL", Voiceover: true, NarratorStyle: "male-energetic", Tone: "witty-explainer",
		AudioMode: "voiceover", Content: "mixed", Overlays: "off", Pipeline: "code", CodeGrammar: "map",
		Transition: "none", SubtitlePreset: "phrase", Subtitles: "burn",
		MusicVolume: 0.08, AmbientVolume: 0, Ducking: true,
	}
	n := p
	n.Normalize()
	if n.Tempo != "rapid" || n.Camera != "map-pan" || n.Style != "history-map-pegs" || n.NarratorStyle != "male-energetic" ||
		n.Pipeline != "code" || n.CodeGrammar != "map" || n.Overlays != "off" || n.SubtitlePreset != "phrase" {
		t.Fatalf("normalize changed the profile: %+v", n)
	}
}
