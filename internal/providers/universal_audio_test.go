package providers

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

var mp3Bytes = append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), make([]byte, 64)...)

func TestAudioTemplatesValid(t *testing.T) {
	for _, kind := range []SpecKind{KindTTS, KindSTT} {
		ids := map[string]bool{}
		for _, tp := range SpecTemplatesFor(kind) {
			if ids[tp.ID] || tp.Label == "" || tp.Hint == "" {
				t.Errorf("%s template %q: duplicate or missing label/hint", kind, tp.ID)
			}
			ids[tp.ID] = true
			sum, warns, err := CheckSpecFor(tp.Spec, kind)
			if err != nil || sum == "" {
				t.Errorf("%s template %s: %v", kind, tp.ID, err)
			}
			t.Logf("%s/%s: %s %v", kind, tp.ID, sum, warns)
			if got, ok := FindSpecTemplateFor(kind, tp.ID); !ok || got.Spec != tp.Spec {
				t.Errorf("FindSpecTemplateFor(%s, %s)", kind, tp.ID)
			}
		}
	}
	// The image templates still check as images; an image spec is not a TTS one.
	if _, _, err := CheckSpecFor(tplOpenAIImages, KindImage); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSpecFor(tplOpenAIImages, KindTTS); err == nil || !strings.Contains(err.Error(), "{{text}}") {
		t.Fatalf("image spec as TTS: %v", err)
	}
	if _, err := ParseSpecFor(`{"submit":{"path":"/tts","body":{"text":"{{prompt}}"}}}`, KindTTS); err == nil || !strings.Contains(err.Error(), "{{text}}") {
		t.Fatalf("prompt in TTS: %v", err)
	}
	if _, err := ParseSpecFor(`{"submit":{"path":"/stt","body":{"x":"{{text}}"}}}`, KindSTT); err == nil || !strings.Contains(err.Error(), "{{audio}}") {
		t.Fatalf("STT without audio: %v", err)
	}
	if p := SpecPromptFor(KindTTS); !strings.Contains(p, "{{voice}}") || strings.Contains(p, "{{prompt}}") {
		t.Fatal("TTS prompt")
	}
	if p := SpecPromptFor(KindSTT); !strings.Contains(p, "{{audio}}") {
		t.Fatal("STT prompt")
	}
}

// TestSpeechOpenAITemplate: the reply is the audio itself; the body carries
// the text, voice and speed; the key goes as Bearer.
func TestSpeechOpenAITemplate(t *testing.T) {
	fast(t)
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+uniKey {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/v1/audio/speech":
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write(mp3Bytes)
		case "/v1/models":
			_, _ = w.Write([]byte(`{"data":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	tp, _ := FindSpecTemplateFor(KindTTS, "openai-speech")
	api := AudioAPI{Kind: KindTTS, BaseURL: srv.URL + "/v1", Key: uniKey, Model: "tts-1", Spec: tp.Spec}
	data, ctype, err := api.Speech(context.Background(), SpeechRequest{Text: "Привіт", Voice: "nova", Speed: 1.1, Language: "uk"})
	if err != nil || string(data) != string(mp3Bytes) || ctype != "audio/mpeg" {
		t.Fatalf("speech %q %v", ctype, err)
	}
	if body["input"] != "Привіт" || body["voice"] != "nova" || body["model"] != "tts-1" || body["speed"].(float64) != 1.1 {
		t.Fatalf("body %v", body)
	}
	if ok, msg := api.Test(context.Background()); !ok {
		t.Fatalf("test: %s", msg)
	}
	api.Key = "wrong-key-0000000"
	if ok, msg := api.Test(context.Background()); ok || !strings.Contains(msg, "401") || strings.Contains(msg, "wrong-key-0000000") {
		t.Fatalf("bad key test: %v %s", ok, msg)
	}
}

// TestSpeechElevenTemplate: the voice in the path, xi-api-key, no voice → its key dropped.
func TestSpeechElevenTemplate(t *testing.T) {
	fast(t)
	var path, key, accept string
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key, accept = r.URL.RequestURI(), r.Header.Get("xi-api-key"), r.Header.Get("Accept")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(mp3Bytes)
	}))
	defer srv.Close()
	tp, _ := FindSpecTemplateFor(KindTTS, "elevenlabs")
	api := AudioAPI{BaseURL: srv.URL, Key: uniKey, Model: "eleven_v3", Spec: tp.Spec}
	if _, _, err := api.Speech(context.Background(), SpeechRequest{Text: "Hi", Voice: "JBFqnCBsd6RMkjVDRZzb", Language: "uk"}); err != nil {
		t.Fatal(err)
	}
	if path != "/v1/text-to-speech/JBFqnCBsd6RMkjVDRZzb?output_format=mp3_44100_128" || key != uniKey || accept != "audio/mpeg" ||
		body["language_code"] != "uk" || body["model_id"] != "eleven_v3" {
		t.Fatalf("path %s key %q accept %q body %v", path, key, accept, body)
	}
	if vs := body["voice_settings"].(map[string]any); vs["speed"] != nil || vs["stability"].(float64) != 0.5 {
		t.Fatalf("voice_settings %v", vs)
	}
}

// TestSpeechLumeanTemplate: an order, its status, the storage link, the file.
func TestSpeechLumeanTemplate(t *testing.T) {
	fast(t)
	var polls atomic.Int32
	var order map[string]any
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/dl/a.mp3" && r.Header.Get("X-API-KEY") != uniKey {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"success":false,"message":"auth.api_key.invalid"}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/public/orders":
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &order)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"success":true,"message":"Created","data":{"id":"ord-1","status":"created"}}`))
		case r.URL.Path == "/api/public/orders/ord-1":
			if polls.Add(1) < 2 {
				_, _ = w.Write([]byte(`{"success":true,"message":"OK","data":{"id":"ord-1","status":"in_progress","progress_percent":40}}`))
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"message":"OK","data":{"id":"ord-1","status":"completed","result":{"files":["orders/ord-1/a.mp3"]}}}`))
		case r.Method == "POST" && r.URL.Path == "/api/public/storage/url":
			b, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(b), `"orders/ord-1/a.mp3"`) {
				w.WriteHeader(422)
				return
			}
			_, _ = w.Write([]byte(`{"success":true,"data":{"url":"` + srv.URL + `/dl/a.mp3"}}`))
		case r.URL.Path == "/dl/a.mp3":
			w.Header().Set("Content-Type", "audio/mpeg")
			_, _ = w.Write(mp3Bytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	tp, _ := FindSpecTemplateFor(KindTTS, "lumean")
	api := AudioAPI{BaseURL: srv.URL + "/api/public", Key: uniKey, Model: "tpl-uuid", Spec: tp.Spec}
	data, _, err := api.Speech(context.Background(), SpeechRequest{Text: "Текст", Voice: "voice-1", Language: "uk"})
	if err != nil || string(data) != string(mp3Bytes) {
		t.Fatalf("lumean: %v", err)
	}
	ov := order["config_override"].(map[string]any)["tts_settings"].(map[string]any)
	if order["template_id"] != "tpl-uuid" || order["input_text"] != "Текст" || ov["voice_id"] != "voice-1" || ov["language_code"] != "uk" {
		t.Fatalf("order %v", order)
	}
}

// TestTranscribeWhisperTemplate: a multipart upload with an audio file part;
// the verbose_json reply comes back as is.
func TestTranscribeWhisperTemplate(t *testing.T) {
	fast(t)
	var fileName, fileType, model, granular string
	var fileLen int
	reply := `{"text":"привіт світ","language":"ukrainian","duration":1.2,"words":[{"word":"привіт","start":0,"end":0.5},{"word":"світ","start":0.6,"end":1.1}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mt, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if mt != "multipart/form-data" {
			w.WriteHeader(400)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			b, _ := io.ReadAll(p)
			switch p.FormName() {
			case "file":
				fileName, fileType, fileLen = p.FileName(), p.Header.Get("Content-Type"), len(b)
			case "model":
				model = string(b)
			case "timestamp_granularities[]":
				granular += string(b) + ","
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()
	tp, _ := FindSpecTemplateFor(KindSTT, "openai-whisper")
	api := AudioAPI{BaseURL: srv.URL + "/v1", Key: uniKey, Model: "whisper-1", Spec: tp.Spec}
	out, err := api.Transcribe(context.Background(), TranscribeRequest{Audio: mp3Bytes, Name: "voice.mp3", Language: "uk"})
	if err != nil || string(out) != reply {
		t.Fatalf("transcribe %s %v", out, err)
	}
	if fileName != "audio-1.mp3" || fileType != "audio/mpeg" || fileLen != len(mp3Bytes) || model != "whisper-1" || granular != "word,segment," {
		t.Fatalf("file %q %q %d model %q gran %q", fileName, fileType, fileLen, model, granular)
	}
}

// TestTranscribeAsyncResult: a job whose transcript sits under "result"; an
// SRT reply passes through untouched.
func TestTranscribeAsyncResult(t *testing.T) {
	fast(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jobs":
			_, _ = w.Write([]byte(`{"job":{"id":"j1"}}`))
		case "/jobs/j1":
			_, _ = w.Write([]byte(`{"state":"done","output":{"transcript":{"words":[{"text":"a","start":0,"end":1}]}}}`))
		case "/srt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("1\n00:00:00,000 --> 00:00:01,000\nпривіт\n"))
		}
	}))
	defer srv.Close()
	spec := `{"auth":"none","submit":{"path":"/jobs","body":{"audio":"{{audio_b64}}","name":"{{audio_name}}"}},
	  "id":"job.id","poll":{"path":"/jobs/{id}"},"state":"state","done":["done"],"result":"output.transcript"}`
	out, err := AudioAPI{Kind: KindSTT, BaseURL: srv.URL, Spec: spec}.Transcribe(context.Background(), TranscribeRequest{Audio: mp3Bytes, Name: "v.mp3"})
	if err != nil || string(out) != `{"words":[{"end":1,"start":0,"text":"a"}]}` {
		t.Fatalf("async %s %v", out, err)
	}
	srt := `{"auth":"none","submit":{"path":"/srt","encoding":"multipart","body":{"f":"{{audio}}"}}}`
	out, err = AudioAPI{BaseURL: srv.URL, Spec: srt}.Transcribe(context.Background(), TranscribeRequest{Audio: mp3Bytes})
	if err != nil || !strings.Contains(string(out), "привіт") {
		t.Fatalf("srt %s %v", out, err)
	}
}
