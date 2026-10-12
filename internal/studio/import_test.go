package studio

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"ytcartell/internal/config"
)

// treeState fingerprints everything under root: files by size and mtime,
// folders by presence (NTFS updates folder times lazily).
func treeState(t *testing.T, root string) map[string]string {
	t.Helper()
	m := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			m[p] = "dir"
			return nil
		}
		if info, err := os.Stat(p); err == nil {
			m[p] = fmt.Sprintf("%d %d", info.Size(), info.ModTime().UnixNano())
		}
		return nil
	})
	return m
}

func writeTree(t *testing.T, root string, files map[string][]byte) {
	t.Helper()
	for rel, data := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		must(t, os.MkdirAll(filepath.Dir(p), 0o755))
		must(t, os.WriteFile(p, data, 0o644))
	}
}

const webProjectJSON = `{
 "id": "abcDEF1234",
 "title": "Стежка додому",
 "logline": "Хлопчик шукає дорогу.",
 "styleGuide": "Quirky minimalist.",
 "createdAt": 1790766000000,
 "updatedAt": 1790769220720,
 "step": 5,
 "input": {"text": "Маленький хлопчик Тарас…", "mode": "story", "targetSeconds": 24, "aspect": "16:9",
   "style": "quirky-minimalist", "customStyle": "", "genre": "", "tempo": "balanced", "camera": "cinematic",
   "imageModel": "NARWHAL", "voiceover": true, "narratorVoiceId": "pNInz6obpgDQGcFmaJgB", "notes": ""},
 "characters": [
   {"id": "0lHVf5TV", "name": "Тарас", "tag": "taras", "role": "lost boy", "gender": "male", "description": "boy",
    "voiceId": "v1", "image": {"status": "done", "progress": 100, "url": "http://localhost:3000/uploads/images/studio/abcDEF1234/char-taras-X.jpg", "updatedAt": 1790766609272}},
   {"id": "Qx3CGl88", "name": "Лисиця", "tag": "lysytsia", "role": "fox", "description": "fox",
    "image": {"status": "done", "url": "http://localhost:3000/uploads/images/studio/abcDEF1234/missing.jpg"}}
 ],
 "locations": [
   {"id": "4EEBo-fI", "name": "Осінній ліс", "tag": "osinnii_lis", "description": "forest",
    "image": {"status": "done", "url": "https://cdn.example.com/storage/v1/object/public/media/loc.jpg"}}
 ],
 "scenes": [
   {"id": "js7nUgjm", "title": "Зустріч", "speaker": "Qx3CGl88", "narration": "Не бійся.", "characterIds": ["0lHVf5TV", "Qx3CGl88"],
    "locationId": "4EEBo-fI", "shot": "medium", "imagePrompt": "img", "motionPrompt": "mot",
    "frame": {"status": "done", "url": "/uploads/images/studio/abcDEF1234/frame-js7nUgjm-A.jpg", "updatedAt": 1},
    "video": {"status": "done", "url": "http://localhost:3000/uploads/videos/studio/abcDEF1234/clip-js7nUgjm-1-glabs.mp4", "duration": 8, "updatedAt": 2},
    "audio": {"status": "queued", "url": "http://localhost:3000/uploads/studio/abcDEF1234/voice%20js7nUgjm.mp3", "duration": 2.1}},
   {"id": "sc2", "title": "Друга", "speaker": "narrator", "narration": "Кінець.", "characterIds": [],
    "shot": "", "imagePrompt": "", "motionPrompt": "",
    "frame": {"status": "error", "error": "G-Labs [429]: ліміт"}, "video": {"status": "running"}, "audio": {"status": "empty"}}
 ],
 "montage": {"transition": "fade", "subtitles": "burn", "musicVolume": 0.3, "ducking": true, "ambientVolume": 0.25,
   "output": {"status": "done", "url": "http://localhost:3000/uploads/studio/abcDEF1234/montage/final-1.mp4", "duration": 24, "updatedAt": 3},
   "cover": {"status": "empty"},
   "srtUrl": "http://localhost:3000/uploads/studio/abcDEF1234/montage/subtitles-1.srt",
   "musicUrl": "/uploads/user/u_de85.mp3", "musicName": "sad-music.mp3"},
 "job": {"kind": "montage", "status": "running", "progress": 40, "message": "Монтаж сцен · 1/2", "startedAt": 1}
}`

func TestImportFromWebSynthetic(t *testing.T) {
	needMedia(t)
	env := newEnv(t, nil)
	e := env.e
	root := t.TempDir()
	files := map[string][]byte{
		".env": []byte("# web studio\nPORT=3000\nUPLOADS_DIR=./uploads\nTEXT_BASE_URL=\"http://127.0.0.1:9/v1\"\nTEXT_MODEL=env-model # inline comment\n" +
			"GLABS_API_KEY=env-glabs\nGEMINI_API_KEYS=k1, k2\nGEMINI_API_KEY_3=k3\nVIDEO_LENGTH=6\nexport TTS_VOICE=env-voice\nVEO_MODEL=veo-x\nTEXT_API_KEY=\n"),
		"data/runtime-keys.json": []byte(`{"googleKeys":["k2","k4"],"tts":{"provider":"elevenlabs","apiKey":"rk-tts","baseUrl":"","voice":"","model":""},
			"providers":{"text":{"provider":"openai","model":"rk-model","apiKey":""},"image":{"provider":"glabs"},
			"video":{"provider":"glabs","length":4,"resolution":"1080p"},"glabs":{"apiKey":"rk-glabs"},"concurrency":3}}`),
		"data/studio/abcDEF1234.json":                                []byte(webProjectJSON),
		"data/studio/notes.txt":                                      []byte("not a project"),
		"data/studio/broken.json":                                    []byte("{nope"),
		"uploads/images/studio/abcDEF1234/char-taras-X.jpg":          testImage,
		"uploads/images/studio/abcDEF1234/frame-js7nUgjm-A.jpg":      testImage,
		"uploads/videos/studio/abcDEF1234/clip-js7nUgjm-1-glabs.mp4": testClip,
		"uploads/studio/abcDEF1234/voice js7nUgjm.mp3":               testVoice,
		"uploads/studio/abcDEF1234/montage/final-1.mp4":              testClip,
		"uploads/studio/abcDEF1234/montage/subtitles-1.srt":          []byte("1\n00:00:00,250 --> 00:00:01,000\nНе бійся.\n"),
		"uploads/user/u_de85.mp3":                                    testVoice,
	}
	writeTree(t, root, files)
	before := treeState(t, root)

	n, err := e.ImportFromWeb(root)
	if n != 1 || err == nil || !strings.Contains(err.Error(), "broken.json") {
		t.Fatalf("import: %d %v", n, err)
	}
	if after := treeState(t, root); !reflect.DeepEqual(before, after) {
		for k, v := range after {
			if before[k] != v {
				t.Errorf("changed: %s: %q → %q", k, before[k], v)
			}
		}
		for k := range before {
			if _, ok := after[k]; !ok {
				t.Errorf("removed: %s", k)
			}
		}
		t.Fatal("the web root must not be modified")
	}

	// Settings: .env first, runtime-keys.json on top, only non-empty values.
	c := env.store.Get()
	if c.Text.Model != "rk-model" || c.Text.BaseURL != "http://127.0.0.1:9/v1" || c.Text.APIKey != "txt-test" {
		t.Fatalf("text %+v", c.Text)
	}
	if c.Glabs.APIKey != "rk-glabs" || c.Video.Length != 4 || c.Video.Resolution != "1080p" || c.Concurrency != 3 {
		t.Fatalf("glabs/video %+v %+v %d", c.Glabs, c.Video, c.Concurrency)
	}
	if strings.Join(c.Google.Keys, ",") != "k2,k4,k1,k3" || c.Google.VeoModel != "veo-x" {
		t.Fatalf("google %+v", c.Google)
	}
	if c.TTS.APIKey != "rk-tts" || c.TTS.Voice != "env-voice" || c.TTS.Provider != "elevenlabs" {
		t.Fatalf("tts %+v", c.TTS)
	}
	if c.DataDir == root {
		t.Fatal("data folder must not change")
	}

	p, ok := e.Get("abcDEF1234")
	if !ok {
		t.Fatal("project not imported")
	}
	if p.Title != "Стежка додому" || p.Step != 5 || p.CreatedAt != 1790766000000 || p.Input.Style != "quirky-minimalist" || p.Characters[0].VoiceID != "v1" {
		t.Fatalf("fields: %+v", p)
	}
	check := func(what string, a Asset, prefix string, data []byte) {
		t.Helper()
		if a.Status != StatusDone || !strings.HasPrefix(a.Path, prefix) {
			t.Fatalf("%s: %+v", what, a)
		}
		got, err := os.ReadFile(e.Abs(p.ID, a.Path))
		if err != nil || !bytes.Equal(got, data) {
			t.Fatalf("%s: file %v", what, err)
		}
	}
	check("character", p.Characters[0].Image, "images/char-taras-X", testImage)
	check("frame", p.Scenes[0].Frame, "images/frame-js7nUgjm-A", testImage)
	check("video", p.Scenes[0].Video, "videos/clip-js7nUgjm-1-glabs", testClip)
	check("audio (queued with file → done)", p.Scenes[0].Audio, "audio/voice_js7nUgjm", testVoice)
	check("output", p.Montage.Output, "montage/final-1", testClip)
	if p.Scenes[0].Video.Duration != 8 || p.Montage.Output.Duration != 24 || p.Scenes[0].Video.UpdatedAt != 2 {
		t.Fatal("durations/updatedAt must be kept")
	}
	if p.Characters[1].Image.Status != StatusEmpty || p.Characters[1].Image.Path != "" {
		t.Fatalf("missing file → empty: %+v", p.Characters[1].Image)
	}
	if p.Locations[0].Image.Status != StatusEmpty {
		t.Fatalf("foreign URL → empty: %+v", p.Locations[0].Image)
	}
	if f := p.Scenes[1].Frame; f.Status != StatusError || f.Error != "G-Labs [429]: ліміт" {
		t.Fatalf("failed asset kept: %+v", f)
	}
	if v := p.Scenes[1].Video; v.Status != StatusError {
		t.Fatalf("running without file → error: %+v", v)
	}
	if p.Job == nil || p.Job.Status != JobError {
		t.Fatalf("running job → error: %+v", p.Job)
	}
	if p.Montage.SRTPath != "montage/subtitles-1.srt" || p.Montage.MusicPath != "user/u_de85.mp3" || p.Montage.MusicName != "sad-music.mp3" || p.Montage.Subtitles != "burn" {
		t.Fatalf("montage: %+v", p.Montage)
	}
	fileExists(t, e.Abs(p.ID, p.Montage.SRTPath))
	fileExists(t, e.Abs(p.ID, p.Montage.MusicPath))
	fileExists(t, filepath.Join(e.Dir(p.ID), projectFile))

	// Existing ids are skipped.
	if n, _ := e.ImportFromWeb(root); n != 0 {
		t.Fatalf("re-import imported %d", n)
	}
	// Not a web studio folder.
	if _, err := e.ImportFromWeb(t.TempDir()); err == nil {
		t.Fatal("empty folder must be rejected")
	}
	// The imported project works with the engine (edit + reload).
	must(t, e.Edit(p.ID, func(p *Project) { p.Scenes[0].Narration = "Не бійся, друже." }))
	p, _ = e.Get(p.ID)
	if !p.Scenes[0].Audio.Stale {
		t.Fatal("imported audio must go stale on edit")
	}
}

// TestImportFromWebReal imports the real Node studio of this repository (if
// present) into a TEMP data folder. The repository is only read.
func TestImportFromWebReal(t *testing.T) {
	root, _ := filepath.Abs(filepath.Join("..", "..", ".."))
	if !isDir(filepath.Join(root, "data", "studio")) {
		t.Skip("no web studio data at " + root)
	}
	env := newEnv(t, nil)
	e := env.e
	// The import stores the real keys via Store.Update, which persists to the
	// (temp-redirected) settings file: remove it as soon as the test ends.
	t.Cleanup(func() { _ = os.Remove(config.Path()) })
	n, err := e.ImportFromWeb(root)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if n < 1 {
		t.Fatalf("imported %d projects", n)
	}
	dataDir := env.store.Get().DataDir
	if strings.HasPrefix(dataDir, root) {
		t.Fatal("data folder must stay in TEMP")
	}
	files := 0
	for _, s := range e.List() {
		p, _ := e.Get(s.ID)
		if !strings.HasPrefix(e.Dir(p.ID), dataDir) {
			t.Fatalf("project outside the temp data folder: %s", e.Dir(p.ID))
		}
		var assets []Asset
		for _, c := range p.Characters {
			assets = append(assets, c.Image)
		}
		for _, l := range p.Locations {
			assets = append(assets, l.Image)
		}
		for _, sc := range p.Scenes {
			assets = append(assets, sc.Frame, sc.Video, sc.Audio)
		}
		assets = append(assets, p.Montage.Output, p.Montage.Cover)
		for _, a := range assets {
			if a.Path == "" {
				continue
			}
			fileExists(t, e.Abs(p.ID, a.Path))
			files++
		}
		if p.Job != nil && p.Job.Status == JobRunning {
			t.Fatalf("%s: running job after import", p.ID)
		}
		t.Logf("imported %s «%s»: step %d, %d scenes", p.ID, p.Title, p.Step, len(p.Scenes))
	}
	if p, ok := e.Get("Z6DAPmBjrG"); ok {
		if len(p.Scenes) != 3 || p.Montage.Output.Path == "" || p.Characters[0].Tag != "taras" {
			t.Fatalf("demo project: %d scenes, output %q", len(p.Scenes), p.Montage.Output.Path)
		}
	}
	t.Logf("%d projects, %d files copied", n, files)
}
