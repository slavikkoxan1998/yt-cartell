package stock

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExtractKeywords(t *testing.T) {
	prompt := "Cinematic 4K hyperrealistic shot of drone flying over foggy mountains at sunrise"
	kw := ExtractKeywords(prompt)
	if kw == "" {
		t.Fatalf("expected extracted keywords, got empty string")
	}
	t.Logf("Extracted keywords: %s", kw)
}

func TestPickBestFile(t *testing.T) {
	item := VideoItem{
		Files: []VideoFile{
			{Width: 640, Height: 360, Link: "http://example.com/sd.mp4"},
			{Width: 1920, Height: 1080, Link: "http://example.com/hd.mp4"},
			{Width: 1280, Height: 720, Link: "http://example.com/720.mp4"},
		},
	}
	best := PickBestFile(item)
	if best == nil || best.Width != 1920 {
		t.Fatalf("expected 1920x1080, got %v", best)
	}
}

func TestMockDownloadFile(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.Write([]byte("fake-mp4-data"))
	}))
	defer ts.Close()

	data, err := DownloadFile(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("DownloadFile failed: %v", err)
	}
	if string(data) != "fake-mp4-data" {
		t.Fatalf("unexpected content: %s", string(data))
	}
}
