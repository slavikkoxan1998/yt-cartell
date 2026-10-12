package studio

// On-screen text in the montage (Scene.OnScreen, Input.TextOverlays): the
// cover package designs and animates it (cover.OverlayClip), this file turns
// a scene's text into the transparent PNG frames media.Montage composites
// over the picture — every distinct picture written once, the still stretches
// as one long frame of the concat list.

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"

	"ytcartell/internal/cover"
	"ytcartell/internal/media"
)

const (
	overlayFPS = 24
	// overlayLead / overlayTail: the text enters this long after the
	// incoming transition and has left this long before the outgoing one.
	overlayLead, overlayTail = 0.25, 0.15
	// overlayMin is the shortest time on screen worth a text (s).
	overlayMin = 1.2
)

// overlayOf is the cover design of a scene's on-screen text in project p.
func overlayOf(p *Project, o OnScreen) cover.Overlay {
	pos := o.Position
	if pos == "" {
		pos = "lower"
		if p.Input.ContentMode() == ContentMotion {
			pos = "center"
		}
	}
	return cover.Overlay{
		Title: strings.TrimSpace(o.Title), Value: strings.TrimSpace(o.Value), Caption: strings.TrimSpace(o.Caption),
		Anim: o.Anim, Position: pos, Accent: overlayAccent(p),
	}
}

// overlayAccent is a bright colour of the project's style palette for the
// text accents ("" = the default warm yellow).
func overlayAccent(p *Project) string {
	st, ok := StyleByID(p.Input.Style)
	if !ok || strings.TrimSpace(p.Input.CustomStyle) != "" {
		return ""
	}
	best, bestScore := "", 0.0
	for _, hex := range st.Palette {
		r, g, b, ok := hexRGB(hex)
		if !ok {
			continue
		}
		hi, lo := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
		luma := 0.2126*r + 0.7152*g + 0.0722*b
		sat := 0.0
		if hi > 0 {
			sat = (hi - lo) / hi
		}
		// Bright and saturated reads on footage; pale or dark colours do not.
		if luma < 0.45 || sat < 0.45 {
			continue
		}
		if score := luma * sat; score > bestScore {
			best, bestScore = hex, score
		}
	}
	return best
}

func hexRGB(s string) (r, g, b float64, ok bool) {
	if !hexColor(s) {
		return 0, 0, 0, false
	}
	var v [3]float64
	for i := range v {
		var x int
		if _, err := fmt.Sscanf(s[1+2*i:3+2*i], "%02x", &x); err != nil {
			return 0, 0, 0, false
		}
		v[i] = float64(x) / 255
	}
	return v[0], v[1], v[2], true
}

// overlayFunc is the montage's on-screen text renderer for p (nil when the
// texts are off or no scene has one). Segment i of the montage is scene i.
func overlayFunc(p *Project) media.OverlayFunc {
	if !p.Input.TextOverlays() {
		return nil
	}
	designs := map[int]cover.Overlay{}
	for i, s := range p.Scenes {
		if !s.OnScreen.Empty() {
			designs[i] = overlayOf(p, s.OnScreen)
		}
	}
	if len(designs) == 0 {
		return nil
	}
	return func(ctx context.Context, seg int, span media.OverlaySpan, size image.Point, dir string) (media.Overlay, bool, error) {
		o, ok := designs[seg]
		if !ok {
			return media.Overlay{}, false, nil
		}
		return writeOverlay(ctx, o, span, size, dir)
	}
}

// writeOverlay renders the frames of o for a scene of span into dir and the
// concat list that plays them for exactly the scene's length.
func writeOverlay(ctx context.Context, o cover.Overlay, span media.OverlaySpan, size image.Point, dir string) (media.Overlay, bool, error) {
	show := span.In + overlayLead
	hide := span.Duration - span.Out - overlayTail
	if hide-show < overlayMin {
		return media.Overlay{}, false, nil // too short to read
	}
	clip, err := cover.NewOverlayClip(o, size, show, hide)
	if err != nil {
		return media.Overlay{}, false, err
	}
	box := clip.Bounds()
	if box.Empty() {
		return media.Overlay{}, false, nil
	}
	frames := int(math.Round(span.Duration * overlayFPS))
	type run struct {
		file string
		n    int
	}
	var runs []run
	files := map[int]string{} // still id → its file
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	write := func(name string, img image.Image) (string, error) {
		path := filepath.Join(dir, name)
		f, err := os.Create(path)
		if err != nil {
			return "", err
		}
		if err := enc.Encode(f, img); err != nil {
			f.Close()
			return "", err
		}
		return path, f.Close()
	}
	lastStill := -2
	for i := 0; i < frames; i++ {
		if i%12 == 0 {
			if err := ctx.Err(); err != nil {
				return media.Overlay{}, false, err
			}
		}
		t := float64(i) / overlayFPS
		still, id := clip.Still(t)
		if still && id == lastStill && len(runs) > 0 {
			runs[len(runs)-1].n++
			continue
		}
		var file string
		if still {
			if f, ok := files[id]; ok {
				file = f
			} else {
				f, err := write(fmt.Sprintf("still%d.png", id), clip.Frame(t))
				if err != nil {
					return media.Overlay{}, false, err
				}
				files[id], file = f, f
			}
			lastStill = id
		} else {
			f, err := write(fmt.Sprintf("f%05d.png", i), clip.Frame(t))
			if err != nil {
				return media.Overlay{}, false, err
			}
			file, lastStill = f, -2
		}
		runs = append(runs, run{file: file, n: 1})
	}
	if len(runs) == 0 {
		return media.Overlay{}, false, nil
	}
	var b strings.Builder
	b.WriteString("ffconcat version 1.0\n")
	for _, r := range runs {
		fmt.Fprintf(&b, "file '%s'\nduration %.6f\n", concatPath(r.file), float64(r.n)/overlayFPS)
	}
	// The concat demuxer ignores the last duration unless the file follows once more.
	fmt.Fprintf(&b, "file '%s'\n", concatPath(runs[len(runs)-1].file))
	list := filepath.Join(dir, "overlay.txt")
	if err := os.WriteFile(list, []byte(b.String()), 0o644); err != nil {
		return media.Overlay{}, false, err
	}
	return media.Overlay{List: list, X: box.Min.X, Y: box.Min.Y}, true, nil
}

// concatPath quotes a path for an FFmpeg concat list.
func concatPath(p string) string {
	return strings.ReplaceAll(filepath.ToSlash(p), "'", `'\''`)
}
