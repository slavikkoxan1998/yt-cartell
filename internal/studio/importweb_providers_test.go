package studio

import (
	"testing"

	"ytcartell/internal/config"
)

// «Імпорт з веб-версії» keeps an OpenAI-compatible text endpoint "openai" and
// never switches a service to Google while there is no Google key (the studio
// would stop at «ключів Google не задано»); with keys, a web «google» is taken.
func TestImportSettingsProviders(t *testing.T) {
	cases := []struct {
		name, env, keys         string
		wantText, wantImg, want string
	}{
		{name: "openai endpoint", env: "TEXT_PROVIDER=openai\nTEXT_BASE_URL=http://127.0.0.1:8317/v1\nTEXT_MODEL=gemini-3.8-flash-high\n",
			keys:     `{"providers":{"text":{"provider":"openai","baseUrl":"http://127.0.0.1:8317/v1","model":"gemini-3.8-flash-high"},"image":{"provider":"glabs"},"video":{"provider":"glabs"}}}`,
			wantText: "openai", wantImg: "glabs", want: "glabs"},
		{name: "google without keys", env: "TEXT_PROVIDER=google\nIMAGE_PROVIDER=google\nVIDEO_PROVIDER=google\n",
			keys:     `{"googleKeys":[],"providers":{"text":{"provider":"google"},"image":{"provider":"google"},"video":{"provider":"google"}}}`,
			wantText: "openai", wantImg: "glabs", want: "glabs"},
		{name: "google with keys", env: "TEXT_PROVIDER=google\nIMAGE_PROVIDER=google\nGEMINI_API_KEY=AIza-test-key\n",
			keys:     `{"providers":{"text":{"provider":"google"},"image":{"provider":"google"}}}`,
			wantText: "google", wantImg: "google", want: "glabs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newEnv(t, func(c *config.Config) { c.Google.Keys = nil })
			root := t.TempDir()
			writeTree(t, root, map[string][]byte{".env": []byte(tc.env), "data/runtime-keys.json": []byte(tc.keys)})
			if _, err := env.e.ImportFromWeb(root); err != nil {
				t.Fatal(err)
			}
			c := env.store.Get()
			if c.Text.Provider != tc.wantText || c.Image.Provider != tc.wantImg || c.Video.Provider != tc.want {
				t.Fatalf("text %q image %q video %q (keys %d)", c.Text.Provider, c.Image.Provider, c.Video.Provider, len(c.Google.Keys))
			}
		})
	}
}
