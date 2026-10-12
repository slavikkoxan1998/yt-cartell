// Command guikit renders the component gallery of the new UI kit
// (internal/gui) — every component and state, light and dark ("Вечір"), at
// 1920×1080 and 1440×900 — to PNG files, headlessly:
//
//	go run ./cmd/guikit -out guikit-out
//	go run ./cmd/guikit -out guikit-out -pages home,controls -sizes 1440x900 -themes light
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gioui.org/io/input"

	"ytcartell/internal/gui"
)

// page is one gallery screen. setup (optional) runs once before the first
// frame (open dialogs, post toasts); before (optional) runs ahead of every
// frame (queue synthetic input).
type page struct {
	name   string
	draw   func(g *gallery, gtx C) D
	setup  func(g *gallery)
	before func(g *gallery, frame int, r *input.Router)
	// frames > 0 renders exactly that many frames instead of settling (pages
	// with toasts, which would time out while settling).
	frames int
}

var pages = []page{
	{name: "home", draw: (*gallery).home},
	{name: "foundations", draw: (*gallery).foundations},
	{name: "icons", draw: (*gallery).icons},
	{name: "controls", draw: (*gallery).controls},
	{name: "inputs", draw: (*gallery).inputs},
	{name: "cards", draw: (*gallery).cards},
	{name: "dialog", draw: (*gallery).dialogPage, setup: (*gallery).dialogSetup, before: (*gallery).tooltipInput, frames: 50},
	{name: "drawer", draw: (*gallery).drawerPage, setup: (*gallery).drawerSetup},
	{name: "picker", draw: (*gallery).pickerPage, setup: (*gallery).pickerSetup},
	{name: "tour", draw: (*gallery).tourPage, setup: (*gallery).tourSetup},
}

func main() {
	out := flag.String("out", "guikit-out", "output folder")
	only := flag.String("pages", "", "comma-separated page names (default: all)")
	sizes := flag.String("sizes", "1920x1080,1440x900", "comma-separated WxH sizes")
	themes := flag.String("themes", "light,dark", "comma-separated themes: light, dark")
	scale := flag.Float64("scale", 1, "pixels per dp")
	flag.Parse()

	want := map[string]bool{}
	for _, p := range strings.Split(*only, ",") {
		if p = strings.TrimSpace(p); p != "" {
			want[p] = true
		}
	}
	type size struct{ w, h int }
	var szs []size
	for _, s := range strings.Split(*sizes, ",") {
		w, h, ok := strings.Cut(strings.TrimSpace(s), "x")
		wi, err1 := strconv.Atoi(w)
		hi, err2 := strconv.Atoi(h)
		if !ok || err1 != nil || err2 != nil {
			log.Fatalf("bad size %q", s)
		}
		szs = append(szs, size{wi, hi})
	}
	start := time.Now()
	n := 0
	for _, p := range pages {
		if len(want) > 0 && !want[p.name] {
			continue
		}
		for _, theme := range strings.Split(*themes, ",") {
			dark := strings.TrimSpace(theme) == "dark"
			for _, sz := range szs {
				if err := render(p, dark, sz.w, sz.h, float32(*scale), *out); err != nil {
					log.Fatalf("%s: %v", p.name, err)
				}
				n++
			}
		}
	}
	fmt.Printf("%d images in %s\n", n, time.Since(start).Round(time.Millisecond))
	_ = os.Stdout.Sync()
}

func render(p page, dark bool, w, h int, scale float32, out string) error {
	th := gui.MustTheme()
	th.SetPaletteNow(dark)
	g := newGallery(th)
	if p.setup != nil {
		p.setup(g)
	}
	opts := []gui.SnapOption{gui.Frames(3), gui.Settle(0)}
	if p.frames > 0 {
		opts = []gui.SnapOption{gui.Frames(p.frames)}
	}
	if p.before != nil {
		opts = append(opts, gui.BeforeFrame(func(frame int, r *input.Router) { p.before(g, frame, r) }))
	}
	img, err := gui.Snapshot(w, h, scale, func(gtx C) D {
		g.frame++
		return th.Root(gtx, func(gtx C) D { return p.draw(g, gtx) })
	}, opts...)
	if err != nil {
		return err
	}
	theme := "light"
	if dark {
		theme = "dark"
	}
	path := filepath.Join(out, fmt.Sprintf("%s-%s-%dx%d.png", p.name, theme, w, h))
	if err := gui.SavePNG(img, path); err != nil {
		return err
	}
	fmt.Println(path)
	return nil
}
