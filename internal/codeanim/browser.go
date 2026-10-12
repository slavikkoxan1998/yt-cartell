package codeanim

// Finding, starting and sharing the headless browser.
//
// One browser process serves every render of the app (pages are the unit of
// parallelism, limited by Concurrency); it starts on first use and quits
// after browserIdle without users. Each start gets a fresh temporary
// profile, deleted when the browser quits. The child joins sysx's
// kill-on-close job, so it never outlives the app.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"ytcartell/internal/sysx"
)

// ErrNoBrowser: no Chromium-family browser was found (and the headless
// shell is not installed). InstallBrowser fixes it.
var ErrNoBrowser = errors.New("Не знайдено браузера для анімацій (Edge, Chrome або Chromium) — встанови Google Chrome або натисни «Встановити рушій анімацій»")

// BrowserPath, when set, is used instead of the automatic search (a path
// from the settings). Read at every browser start.
var BrowserPath string

// browserIdle: an unused browser quits after this long.
var browserIdle = 45 * time.Second

// Available reports which browser renders animations (absolute path),
// without installing anything; ErrNoBrowser when there is none.
func Available(ctx context.Context) (string, error) {
	if ctx != nil && ctx.Err() != nil {
		return "", ctx.Err()
	}
	if p := findBrowser(); p != "" {
		return p, nil
	}
	return "", ErrNoBrowser
}

// findBrowser: configured path → system Edge/Chrome/Chromium → the headless
// shell the studio installed.
func findBrowser() string {
	if p := strings.Trim(strings.TrimSpace(BrowserPath), `"`); p != "" && isFile(p) {
		return p
	}
	for _, p := range browserCandidates(runtime.GOOS) {
		if isFile(p) {
			return p
		}
	}
	for _, n := range []string{"microsoft-edge", "microsoft-edge-stable", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
			break
		}
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	if p, ok := installedShell(); ok {
		return p
	}
	return ""
}

func browserCandidates(goos string) []string {
	var out []string
	switch goos {
	case "windows":
		pf := []string{os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramFiles"), os.Getenv("ProgramW6432"), os.Getenv("LOCALAPPDATA")}
		rels := []string{
			`Microsoft\Edge\Application\msedge.exe`,
			`Google\Chrome\Application\chrome.exe`,
			`Chromium\Application\chrome.exe`,
		}
		for _, r := range rels {
			for _, base := range pf {
				if base != "" {
					out = append(out, filepath.Join(base, r))
				}
			}
		}
	case "darwin":
		home, _ := os.UserHomeDir()
		apps := []string{
			"Google Chrome.app/Contents/MacOS/Google Chrome",
			"Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"Chromium.app/Contents/MacOS/Chromium",
		}
		for _, a := range apps {
			out = append(out, filepath.Join("/Applications", a))
			if home != "" {
				out = append(out, filepath.Join(home, "Applications", a))
			}
		}
	}
	return out
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// ---- one browser process ---------------------------------------------------------------------

type browser struct {
	path string
	cmd  *exec.Cmd
	cdp  *cdpConn
	dir  string // temp profile
	exit chan struct{}
}

func launchBrowser(ctx context.Context, path string) (*browser, error) {
	dir, err := os.MkdirTemp("", "ytc-codeanim-*")
	if err != nil {
		return nil, err
	}
	args := []string{
		"--headless=new",
		"--remote-debugging-port=0",
		"--user-data-dir=" + dir,
		"--no-first-run", "--no-default-browser-check", "--disable-default-apps",
		"--disable-gpu", "--disable-gpu-compositing", "--disable-software-rasterizer",
		"--disable-extensions", "--disable-component-extensions-with-background-pages",
		"--disable-background-networking", "--disable-component-update", "--disable-sync",
		"--disable-domain-reliability", "--disable-client-side-phishing-detection",
		"--disable-breakpad", "--disable-crash-reporter", "--metrics-recording-only", "--no-pings",
		"--disable-background-timer-throttling", "--disable-renderer-backgrounding", "--disable-backgrounding-occluded-windows",
		"--disable-ipc-flooding-protection", "--disable-hang-monitor", "--disable-popup-blocking", "--disable-prompt-on-repost",
		"--disable-features=Translate,OptimizationHints,MediaRouter,DialMediaRouteProvider,AutofillServerCommunication,CalculateNativeWinOcclusion,InterestFeedContentSuggestions,msEdgeSidebarV2,msShowFirstRunExperience,EdgeCollections,msUndersideButton",
		"--force-color-profile=srgb", "--force-device-scale-factor=1", "--hide-scrollbars", "--mute-audio",
		"--password-store=basic", "--use-mock-keychain",
		"--no-service-autorun", "--edge-skip-compat-layer-relaunch",
		"about:blank",
	}
	cmd := sysx.Command(context.WithoutCancel(ctx), path, args...)
	cmd.Stdout, cmd.Stderr = nil, nil
	if err := sysx.Start(cmd); err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("Браузер для анімацій не запускається (%s): %v", filepath.Base(path), err)
	}
	b := &browser{path: path, cmd: cmd, dir: dir, exit: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(b.exit) }()

	// The browser writes "<port>\n<ws path>" into DevToolsActivePort.
	wsURL := ""
	deadline := time.Now().Add(45 * time.Second)
	for wsURL == "" {
		if raw, err := os.ReadFile(filepath.Join(dir, "DevToolsActivePort")); err == nil {
			lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
			if len(lines) >= 2 {
				if port, err := strconv.Atoi(strings.TrimSpace(lines[0])); err == nil && port > 0 {
					wsURL = fmt.Sprintf("ws://127.0.0.1:%d%s", port, strings.TrimSpace(lines[1]))
					break
				}
			}
		}
		select {
		case <-b.exit:
			b.cleanup()
			return nil, fmt.Errorf("Браузер для анімацій (%s) закрився одразу після запуску", filepath.Base(path))
		case <-ctx.Done():
			b.kill()
			return nil, ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			b.kill()
			return nil, fmt.Errorf("Браузер для анімацій (%s) не відповідає", filepath.Base(path))
		}
	}
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	ws, err := wsDial(dctx, wsURL)
	if err != nil {
		b.kill()
		return nil, fmt.Errorf("Не вдалося під’єднатися до браузера для анімацій: %v", err)
	}
	b.cdp = newCDP(ws)
	return b, nil
}

func (b *browser) alive() bool {
	select {
	case <-b.exit:
		return false
	case <-b.cdp.done:
		return false
	default:
		return true
	}
}

// close asks the browser to quit, then kills it.
func (b *browser) close() {
	if b.cdp != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_ = b.cdp.call(ctx, "", "Browser.close", nil, nil)
		cancel()
	}
	select {
	case <-b.exit:
	case <-time.After(5 * time.Second):
	}
	b.kill()
}

func (b *browser) kill() {
	if b.cdp != nil {
		b.cdp.close()
	}
	if b.cmd.Process != nil {
		_ = b.cmd.Process.Kill()
	}
	select {
	case <-b.exit:
	case <-time.After(5 * time.Second):
	}
	b.cleanup()
}

func (b *browser) cleanup() {
	for i := 0; i < 20; i++ { // the profile's files stay locked a moment after exit
		if err := os.RemoveAll(b.dir); err == nil {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// ---- the shared instance ---------------------------------------------------------------------

var shared struct {
	mu    sync.Mutex
	b     *browser
	users int
	idle  *time.Timer
	slots chan struct{} // page slots (Concurrency)
	n     int
}

// Concurrency is how many pages render at once across the app (frames of
// one clip are spread over the free pages). Default: half the CPUs, 1..6.
func Concurrency() int {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	return slotsLocked()
}

// SetConcurrency changes the number of pages rendering at once (≥1). Takes
// effect for renders that start afterwards.
func SetConcurrency(n int) {
	if n < 1 {
		n = 1
	}
	shared.mu.Lock()
	shared.n = n
	shared.slots = make(chan struct{}, n)
	shared.mu.Unlock()
}

func slotsLocked() int {
	if shared.slots == nil {
		n := runtime.NumCPU() / 2
		n = max(1, min(6, n))
		if shared.n > 0 {
			n = shared.n
		}
		shared.n = n
		shared.slots = make(chan struct{}, n)
	}
	return shared.n
}

func slotChan() chan struct{} {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	slotsLocked()
	return shared.slots
}

// acquireBrowser returns the running browser (starting one when needed);
// release must be called once the caller's pages are closed.
func acquireBrowser(ctx context.Context) (*browser, func(), error) {
	shared.mu.Lock()
	defer shared.mu.Unlock()
	if shared.idle != nil {
		shared.idle.Stop()
		shared.idle = nil
	}
	if shared.b != nil && !shared.b.alive() {
		go shared.b.kill()
		shared.b = nil
	}
	if shared.b == nil {
		path := findBrowser()
		if path == "" {
			return nil, nil, ErrNoBrowser
		}
		b, err := launchBrowser(ctx, path)
		if err != nil {
			return nil, nil, err
		}
		shared.b = b
	}
	b := shared.b
	shared.users++
	var once sync.Once
	return b, func() {
		once.Do(func() {
			shared.mu.Lock()
			defer shared.mu.Unlock()
			shared.users--
			if shared.users == 0 && shared.b == b {
				shared.idle = time.AfterFunc(browserIdle, func() {
					shared.mu.Lock()
					if shared.users != 0 || shared.b != b {
						shared.mu.Unlock()
						return
					}
					shared.b = nil
					shared.mu.Unlock()
					b.close()
				})
			}
		})
	}, nil
}

// Shutdown closes the shared browser now (app exit; tests). Renders still
// running fail.
func Shutdown() {
	shared.mu.Lock()
	b := shared.b
	shared.b = nil
	if shared.idle != nil {
		shared.idle.Stop()
		shared.idle = nil
	}
	shared.mu.Unlock()
	if b != nil {
		b.close()
	}
}

// ---- pages -----------------------------------------------------------------------------------

type page struct {
	b         *browser
	targetID  string
	ctxID     string // its own browser context (own renderer process)
	sid       string
	mu        sync.Mutex
	errs      []string
	warnCount int
}

func newPage(ctx context.Context, b *browser, w, h int) (*page, error) {
	// Each page gets its own browser context: pages of one context (same
	// site) share a renderer process — one main thread — which would
	// serialise the parallel pages.
	var bc struct {
		BrowserContextID string `json:"browserContextId"`
	}
	if err := b.cdp.call(ctx, "", "Target.createBrowserContext", nil, &bc); err != nil {
		return nil, err
	}
	dispose := func() {
		dctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = b.cdp.call(dctx, "", "Target.disposeBrowserContext", map[string]any{"browserContextId": bc.BrowserContextID}, nil)
	}
	var t struct {
		TargetID string `json:"targetId"`
	}
	if err := b.cdp.call(ctx, "", "Target.createTarget", map[string]any{"url": "about:blank", "browserContextId": bc.BrowserContextID}, &t); err != nil {
		dispose()
		return nil, err
	}
	var a struct {
		SessionID string `json:"sessionId"`
	}
	if err := b.cdp.call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": t.TargetID, "flatten": true}, &a); err != nil {
		dispose()
		return nil, err
	}
	p := &page{b: b, targetID: t.TargetID, ctxID: bc.BrowserContextID, sid: a.SessionID}
	b.cdp.setHandler(p.sid, p.event)
	for _, m := range []string{"Runtime.enable", "Page.enable"} {
		if err := b.cdp.call(ctx, p.sid, m, nil, nil); err != nil {
			p.close()
			return nil, err
		}
	}
	if err := b.cdp.call(ctx, p.sid, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": w, "height": h, "deviceScaleFactor": 1, "mobile": false,
	}, nil); err != nil {
		p.close()
		return nil, err
	}
	return p, nil
}

// event collects page errors: uncaught exceptions and console.error (the
// upstream renderer refuses to deliver a clip with either).
func (p *page) event(method string, params json.RawMessage) {
	switch method {
	case "Runtime.exceptionThrown":
		var e struct {
			ExceptionDetails exceptionDetails `json:"exceptionDetails"`
		}
		if json.Unmarshal(params, &e) == nil {
			p.addErr(e.ExceptionDetails.String())
		}
	case "Runtime.consoleAPICalled":
		var e struct {
			Type string         `json:"type"`
			Args []remoteObject `json:"args"`
		}
		if json.Unmarshal(params, &e) != nil {
			return
		}
		var parts []string
		for _, a := range e.Args {
			var s string
			if a.Type == "string" && json.Unmarshal(a.Value, &s) == nil {
				parts = append(parts, s)
			} else if a.Description != "" {
				parts = append(parts, a.Description)
			} else if len(a.Value) > 0 {
				parts = append(parts, string(a.Value))
			}
		}
		msg := strings.Join(parts, " ")
		switch e.Type {
		case "error", "assert":
			p.addErr(msg)
		case "warning":
			p.mu.Lock()
			p.warnCount++
			n := p.warnCount
			p.mu.Unlock()
			if n <= 20 && !strings.Contains(msg, "willReadFrequently") {
				log.Printf("codeanim: page warning: %s", msg)
			}
		}
	}
}

func (p *page) addErr(s string) {
	p.mu.Lock()
	if len(p.errs) < 20 {
		p.errs = append(p.errs, s)
	}
	p.mu.Unlock()
}

// pageErr returns the collected page errors (nil when none).
func (p *page) pageErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.errs) == 0 {
		return nil
	}
	return &PageError{Messages: append([]string(nil), p.errs...)}
}

func (p *page) close() {
	p.b.cdp.setHandler(p.sid, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = p.b.cdp.call(ctx, "", "Target.closeTarget", map[string]any{"targetId": p.targetID}, nil)
	_ = p.b.cdp.call(ctx, "", "Target.disposeBrowserContext", map[string]any{"browserContextId": p.ctxID}, nil)
}

// PageError: the animation's page reported errors (an exception or
// console.error) — the clip is not delivered. Messages are the exact JS
// errors.
type PageError struct{ Messages []string }

func (e *PageError) Error() string {
	return "Помилка в анімації: " + strings.Join(e.Messages, "\n")
}
