package stt

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ytcartell/internal/config"
	"ytcartell/internal/providers"
)

// TestCustomSpecSTT: «Своє API» with the Whisper template — a multipart
// upload, verbose_json back, words with timings; the check uses "test".
func TestCustomSpecSTT(t *testing.T) {
	var gotFile, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		if r.URL.Path == "/v1/models" {
			if auth != "Bearer stt-key-0001" {
				w.WriteHeader(401)
				_, _ = w.Write([]byte(`{"error":{"message":"bad key"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err == nil {
			if f := r.MultipartForm.File["file"]; len(f) > 0 {
				gotFile = f[0].Filename
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"Привіт світ","language":"ukrainian","duration":1.4,"words":[{"word":"Привіт","start":0.1,"end":0.6},{"word":"світ","start":0.7,"end":1.2}]}`))
	}))
	defer srv.Close()
	tp, _ := providers.FindSpecTemplateFor(providers.KindSTT, "openai-whisper")
	cfg := config.Config{STT: config.STT{Provider: "custom", BaseURL: srv.URL + "/v1", APIKey: "stt-key-0001", Model: "whisper-1"}}
	cfg.Custom.STTSpec = tp.Spec
	c := New(func() config.Config { return cfg })
	path := filepath.Join(t.TempDir(), "voice.mp3")
	if err := os.WriteFile(path, []byte("ID3fake-mp3-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr, err := c.Transcribe(context.Background(), path, "uk")
	if err != nil || len(tr.Words) != 2 || tr.Words[1].Text != "світ" || tr.Words[1].End != 1.2 {
		t.Fatalf("transcript %+v %v", tr, err)
	}
	if gotFile != "audio-1.mp3" || auth != "Bearer stt-key-0001" {
		t.Fatalf("file %q auth %q", gotFile, auth)
	}
	if ok, msg := c.Test(context.Background()); !ok {
		t.Fatalf("test %q", msg)
	}
	cfg.STT.APIKey = "wrong-stt-key-99"
	if ok, msg := c.Test(context.Background()); ok || !strings.Contains(msg, "GET "+srv.URL+"/v1/models → 401") || strings.Contains(msg, "wrong-stt-key-99") {
		t.Fatalf("bad key %v %q", ok, msg)
	}
	cfg.Custom.STTSpec = ""
	if ok, msg := c.Test(context.Background()); ok || !strings.Contains(msg, "опису API") {
		t.Fatalf("no spec %v %q", ok, msg)
	}
}
