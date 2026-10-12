package gui

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"time"

	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// SnapshotEpoch is the fixed clock of the first headless frame, so renders
// (and every animation in them) are deterministic.
var SnapshotEpoch = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// SnapOption configures Snapshot.
type SnapOption func(*snapConfig)

type snapConfig struct {
	frames int
	settle int
	step   time.Duration
	before func(frame int, r *input.Router)
}

// Frames lays the UI out at least n times before the picture is taken (async
// images, measured popovers, first-frame animations need a few frames).
func Frames(n int) SnapOption { return func(c *snapConfig) { c.frames = max(1, n) } }

// Settle keeps laying out frames until no animation asks for another frame
// (or maxFrames more frames have run; ≤ 0 means 300 ≈ 5 s of animation), so
// the picture shows the UI "after animations settle". Endless animations
// (spinners, shimmer) stop at the cap, deterministically.
func Settle(maxFrames int) SnapOption {
	return func(c *snapConfig) {
		if maxFrames <= 0 {
			maxFrames = 300
		}
		c.settle = maxFrames
	}
}

// Step sets how far the clock advances per frame (default 16 ms).
func Step(d time.Duration) SnapOption { return func(c *snapConfig) { c.step = d } }

// BeforeFrame runs fn ahead of every frame, e.g. to queue pointer or key
// events on the router or to wait for image decodes.
func BeforeFrame(fn func(frame int, r *input.Router)) SnapOption {
	return func(c *snapConfig) { c.before = fn }
}

// Snapshot renders draw off-screen (gioui.org/gpu/headless) into a w×h pixel
// image with metricScale pixels per dp. Frames run through a real input
// router with a fixed clock starting at SnapshotEpoch; only the last frame is
// rasterised.
func Snapshot(w, h int, metricScale float32, draw func(gtx layout.Context) layout.Dimensions, opts ...SnapOption) (*image.RGBA, error) {
	cfg := snapConfig{frames: 1, step: 16 * time.Millisecond}
	for _, o := range opts {
		o(&cfg)
	}
	if metricScale <= 0 {
		metricScale = 1
	}
	var router input.Router
	var ops op.Ops
	for i := 0; ; i++ {
		if cfg.before != nil {
			cfg.before(i, &router)
		}
		ops.Reset()
		gtx := layout.Context{
			Ops:         &ops,
			Now:         SnapshotEpoch.Add(time.Duration(i) * cfg.step),
			Metric:      unit.Metric{PxPerDp: metricScale, PxPerSp: metricScale},
			Constraints: layout.Exact(image.Pt(w, h)),
			Source:      router.Source(),
		}
		draw(gtx)
		router.Frame(&ops)
		_, wake := router.WakeupTime()
		n := i + 1
		if n < cfg.frames {
			continue
		}
		if cfg.settle == 0 || !wake || n >= cfg.frames+cfg.settle {
			break
		}
	}
	win, err := headless.NewWindow(w, h)
	if err != nil {
		return nil, fmt.Errorf("gui: headless window: %w", err)
	}
	defer win.Release()
	if err := win.Frame(&ops); err != nil {
		return nil, fmt.Errorf("gui: headless frame: %w", err)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	if err := win.Screenshot(img); err != nil {
		return nil, fmt.Errorf("gui: headless screenshot: %w", err)
	}
	return img, nil
}

// SavePNG writes img to path, creating the folder.
func SavePNG(img image.Image, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
