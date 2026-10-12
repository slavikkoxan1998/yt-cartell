package montage

// Colour looks: the gallery card, previewed on a real frame of the film
// graded in pure Go off the UI goroutine (the image cache's workers).

import (
	"bufio"
	"fmt"
	"image"
	"os"
	"sync"
	"unicode"
	"unicode/utf8"

	"gioui.org/op/paint"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/media"
	"ytcartell/internal/studio"
)

// ---- decoded bases -------------------------------------------------------------------------------

// baseCache keeps small decoded frames the looks are computed from (the
// ImageCache only hands out GPU images).
var baseCache = struct {
	sync.Mutex
	m     map[string]*image.RGBA
	order []string
}{m: map[string]*image.RGBA{}}

const baseCacheCap = 48

// loadBase decodes an image file shrunk to maxPx (cached).
func loadBase(path string, ver int64, maxPx int) (*image.RGBA, error) {
	key := fmt.Sprintf("%s|%d|%d", path, ver, maxPx)
	baseCache.Lock()
	if img, ok := baseCache.m[key]; ok {
		baseCache.Unlock()
		return img, nil
	}
	baseCache.Unlock()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, _, err := image.Decode(bufio.NewReader(f))
	if err != nil {
		return nil, err
	}
	small := shrink(src, maxPx)
	baseCache.Lock()
	if _, ok := baseCache.m[key]; !ok {
		baseCache.m[key] = small
		baseCache.order = append(baseCache.order, key)
		for len(baseCache.order) > baseCacheCap {
			delete(baseCache.m, baseCache.order[0])
			baseCache.order = baseCache.order[1:]
		}
	}
	baseCache.Unlock()
	return small, nil
}

// lookImage is a scene's picture graded with look id, about px on its
// longest side (the plain picture for "none" or while it computes nothing).
func lookImage(c *core.Ctx, pv *core.ProjectView, sc *studio.Scene, id string, px int) (paint.ImageOp, bool) {
	if id == "" || id == "none" || sc == nil || sc.Frame.Path == "" || c.Img == nil {
		return sceneImage(c, pv, sc, px)
	}
	if _, ok := media.FilterByID(id); !ok {
		return sceneImage(c, pv, sc, px)
	}
	bucket := min(gui.ImgBucket(px), 720)
	abs := pv.Abs(sc.Frame.Path)
	img, _, ok := c.Img.Load("look:"+id+":"+abs, sc.Frame.UpdatedAt, bucket, func() (image.Image, error) {
		base, err := loadBase(abs, sc.Frame.UpdatedAt, bucket)
		if err != nil {
			return nil, err
		}
		return applyLook(base, id), nil
	})
	if !ok {
		// Until the look is ready, the plain picture (no flash of nothing).
		return sceneImage(c, pv, sc, px)
	}
	return img, ok
}

// synLooks caches the looks of the synthetic picture (projects without frames).
var synLooks = struct {
	sync.Mutex
	m map[string]paint.ImageOp
}{m: map[string]paint.ImageOp{}}

func syntheticLook(id string) paint.ImageOp {
	synLooks.Lock()
	defer synLooks.Unlock()
	if op, ok := synLooks.m[id]; ok {
		return op
	}
	op := paint.NewImageOp(applyLook(landscape(240, 135, 0), id))
	synLooks.m[id] = op
	return op
}

// ---- the card --------------------------------------------------------------------------------------

type filtersCard struct {
	tiles []tile
}

func (s *proj) filtersCard(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.flt
	id := pv.ID
	cur := pv.P.Montage.Filter
	if _, ok := media.FilterByID(cur); !ok || cur == "" {
		cur = "none"
	}
	pr := s.pair(c, pv, 240)
	ts := tiles(&st.tiles, len(media.Filters))
	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		sub := "Колір для всього фільму. Перегляд — на кадрі твоєї сцени."
		return gui.VStack(gtx, gui.Sp16,
			func(gtx C) D { return cardHeader(gtx, th, gui.IconPalette, "Фільтри", sub, nil) },
			func(gtx C) D {
				return gui.Grid{MinWidth: galleryMin(gtx), Gap: gui.Sp12}.Layout(gtx, len(media.Filters), func(gtx C, i int) D {
					f := media.Filters[i]
					return ts[i].layout(gtx, th, tileStyle{Label: f.Label, Tip: f.Hint, Selected: f.ID == cur,
						Preview: func(gtx C, size image.Point, sec float64) {
							var img paint.ImageOp
							ok := false
							if pr.real && pr.sceneA != nil {
								img, ok = lookImage(c, pv, pr.sceneA, f.ID, max(size.X, size.Y))
							}
							if !ok {
								img = syntheticLook(f.ID)
							}
							drawImage(gtx, img, size, true)
						}}, func() {
						fid := f.ID
						if fid == "none" {
							fid = ""
						}
						c.Edit(id, func(p *studio.Project) { p.Montage.Filter = fid })
					})
				})
			},
			func(gtx C) D {
				f, _ := media.FilterByID(cur)
				return hint(th, "Зараз: «"+f.Label+"» — "+lower1(f.Hint)+". Перегляд приблизний; точний колір дасть монтаж.")(gtx)
			},
		)
	})
}

// lower1 lower-cases the first letter of s.
func lower1(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToLower(r)) + s[size:]
}
