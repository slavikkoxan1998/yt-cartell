package config

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
)

// Synthetic Google keys (never real ones).
var (
	gKey1 = "AIza" + strings.Repeat("A1b2C3d4E5", 3) + "f6g7h"
	gKey2 = "AIza" + strings.Repeat("Z9y8X7w6V5", 3) + "u4t3s"
)

func readDisk(t *testing.T) Config {
	t.Helper()
	b, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	c := Default()
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	c.Normalize()
	return c
}

// Two studio windows on one settings file: what one saved survives the other's
// next change (it is applied on top of the file, not over it with an old
// snapshot), and Refresh shows it to the other window.
func TestStoreKeepsOtherProcessChanges(t *testing.T) {
	t.Cleanup(func() { os.Remove(Path()) })
	os.Remove(Path())
	a, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Update(func(c *Config) { c.Image.Provider = "glabs" }); err != nil {
		t.Fatal(err)
	}
	b, err := NewStore() // the second window
	if err != nil {
		t.Fatal(err)
	}
	var seen []Config
	a.Subscribe(func(c Config) { seen = append(seen, c) })

	// Window B adds Google keys; window A, still holding its old copy, changes
	// the frames provider: the keys stay.
	if err := b.Update(func(c *Config) { c.Google.Keys = []string{gKey1, gKey2} }); err != nil {
		t.Fatal(err)
	}
	if err := a.Update(func(c *Config) { c.Image.Provider = "google" }); err != nil {
		t.Fatal(err)
	}
	d := readDisk(t)
	if len(d.Google.Keys) != 2 || d.Image.Provider != "google" {
		t.Fatalf("disk: keys %d, image %q", len(d.Google.Keys), d.Image.Provider)
	}
	if got := a.Get(); len(got.Google.Keys) != 2 {
		t.Fatalf("window A lost the keys: %v", got.Google.Keys)
	}
	// B picks up A's change on Refresh (and tells its subscribers once).
	var bSeen int
	b.Subscribe(func(Config) { bSeen++ })
	if !b.Refresh() || b.Get().Image.Provider != "google" || bSeen != 1 {
		t.Fatalf("refresh: image %q, notified %d", b.Get().Image.Provider, bSeen)
	}
	if b.Refresh() {
		t.Fatal("refresh without a change")
	}
	if len(seen) == 0 || seen[len(seen)-1].Image.Provider != "google" {
		t.Fatal("subscribers did not get the merged settings")
	}
	// A broken file is ignored (ours stay and are written back).
	if err := os.WriteFile(Path(), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.Update(func(c *Config) { c.Concurrency = 3 }); err != nil {
		t.Fatal(err)
	}
	if d := readDisk(t); d.Concurrency != 3 || len(d.Google.Keys) != 2 {
		t.Fatalf("after a broken file: %+v", d.Google.Keys)
	}
}

// Concurrent changes in one process are saved in order: the file ends with
// every change (a later save never carries an older snapshot).
func TestStoreSavesInOrder(t *testing.T) {
	t.Cleanup(func() { os.Remove(Path()) })
	os.Remove(Path())
	s, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Update(func(c *Config) { c.Google.Keys = append(c.Google.Keys, []string{gKey1, gKey2}[i%2]) })
			_ = s.Update(func(c *Config) { c.Concurrency++ })
		}()
	}
	wg.Wait()
	if d, m := readDisk(t), s.Get(); d.Concurrency != m.Concurrency || strings.Join(d.Google.Keys, ",") != strings.Join(m.Google.Keys, ",") {
		t.Fatalf("disk %d %v, memory %d %v", d.Concurrency, d.Google.Keys, m.Concurrency, m.Google.Keys)
	}
}

// A change that panics leaves the store usable.
func TestStorePanicReleasesLocks(t *testing.T) {
	t.Cleanup(func() { os.Remove(Path()) })
	s, err := NewStore()
	if err != nil {
		t.Fatal(err)
	}
	func() {
		defer func() { _ = recover() }()
		_ = s.Update(func(c *Config) { panic("boom") })
	}()
	if err := s.Update(func(c *Config) { c.Concurrency = 4 }); err != nil || s.Get().Concurrency != 4 {
		t.Fatalf("store stuck after a panic: %v", err)
	}
}

// «Мої голоси за ID»: per provider, newest first, cleaned on Normalize and
// deep-copied by Get.
func TestVoicesByID(t *testing.T) {
	var tt TTS
	tt.SaveVoiceByID("cartesia", VoiceRef{ID: " " + carVoce + " ", Name: "Олена", Language: "uk"})
	tt.SaveVoiceByID("cartesia", VoiceRef{ID: "b7d50908-b17c-442d-ad8d-810c63997ed9", Name: "Тарас"})
	tt.SaveVoiceByID("cartesia", VoiceRef{ID: carVoce, Name: "Олена (нова назва)"})
	tt.SaveVoiceByID("elevenlabs", VoiceRef{ID: elVoice, Name: "George"})
	got := tt.VoicesByID("cartesia")
	if len(got) != 2 || got[0].ID != carVoce || got[0].Name != "Олена (нова назва)" || got[1].Name != "Тарас" {
		t.Fatalf("cartesia: %+v", got)
	}
	tt.ByID["nope"] = []VoiceRef{{ID: "x"}}
	tt.ByID["fish"] = []VoiceRef{{ID: ""}}
	tt.normalize()
	if _, ok := tt.ByID["nope"]; ok || len(tt.ByID) != 2 {
		t.Fatalf("normalize kept %v", tt.ByID)
	}
	st := NewStoreWith(Config{TTS: tt})
	c := st.Get()
	c.TTS.ByID["cartesia"][0].Name = "змінено"
	if st.Get().TTS.VoicesByID("cartesia")[0].Name == "змінено" {
		t.Fatal("Get shares the book")
	}
	tt.ForgetVoiceByID("elevenlabs", elVoice)
	if len(tt.VoicesByID("elevenlabs")) != 0 || len(tt.ByID) != 1 {
		t.Fatalf("forget: %v", tt.ByID)
	}
	b, _ := json.Marshal(tt)
	if !strings.Contains(string(b), `"voicesById":{"cartesia":[{"id":"`+carVoce) {
		t.Fatalf("json: %s", b)
	}
}
