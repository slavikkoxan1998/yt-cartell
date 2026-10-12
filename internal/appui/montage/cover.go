package montage

// Обкладинка — the cover studio: the picture (drawn by the cover stage), a
// text overlay editor (templates, title/subtitle/badge with *highlight*,
// fonts, colours, position, size, "Підказати заголовок"), a live preview
// rendered off the UI goroutine (debounced, the previous picture stays
// until the next is ready), YouTube-like phone previews, and apply / open /
// reveal / save as.

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/cover"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

const (
	tabText = iota
	tabTemplates
	tabStyle
	tabPicture
)

type coverStudio struct {
	tabs gui.PillTabs
	tab  int

	title, subtitle, badge gui.TextField
	prompt                 gui.TextField
	suggest                gui.Clickable
	suggesting             bool

	gen, gen2          gui.Clickable
	ring, bar          gui.Anim
	apply              gui.Clickable
	open, reveal, save gui.Clickable
	toPicture          gui.Clickable

	tplTiles     []tile
	fontTiles    []tile
	textColors   swatchRow
	strokeColors swatchRow
	accentColors swatchRow
	posChips     []gui.Clickable
	badgeChips   []gui.Clickable
	autoSize     gui.Toggle
	size         valSlider

	// Live preview (debounced; the cache keeps the previous version on screen).
	prevWant, prevKey int64
	prevVer           int64
	prevAt            time.Time
	prevW             int
	fade              gui.Anim
	measured          int64 // design signature the metrics belong to
	small             bool  // the title is too small for a phone feed
	titlePx           int   // measured title size (px of the full cover)
	lastImg           paint.ImageOp
	lastOK            bool
	ideas             []gui.Clickable
	rawThumb          gui.ImageView
	openRaw           gui.Clickable

	// A ready picture as the cover: a scene frame or the user's own image.
	ownPic      gui.Clickable
	moreFrames  gui.Clickable
	frameTiles  []tile
	allFrames   bool
	setting     bool   // SetCoverImage is copying
	settingFrom string // scene id, or "own"

	// Template previews (all share one debounced version).
	tplWant, tplKey int64
	tplVer          int64
	tplAt           time.Time

	appliedSig int64 // design stored in CoverFinal (0 = unknown)
	appliedAt  int64 // CoverFinal.UpdatedAt the signature belongs to
	applying   bool
	finalSize  fileSize
}

// coverSig is the design signature of the text overlay on a picture.
func coverSig(p *studio.Project) int64 {
	ct := p.Montage.CoverText
	return hashOf(ct.Template, ct.Title, ct.Subtitle, ct.Badge, ct.Font, ct.Color, ct.Stroke, ct.Accent, ct.Position,
		fmt.Sprint(ct.Size), p.Montage.Cover.Path, fmt.Sprint(p.Montage.Cover.UpdatedAt), p.Input.Aspect, firstFrame(p))
}

// firstFrame is the frame PreviewCover falls back to without a cover picture.
func firstFrame(p *studio.Project) string {
	for _, s := range p.Scenes {
		if s.Frame.Path != "" {
			return s.Frame.Path + fmt.Sprint(s.Frame.UpdatedAt)
		}
	}
	return ""
}

// coverFile is the file to open / save: the finished cover, else the picture.
func coverFile(p *studio.Project) (studio.Asset, bool) {
	if f := p.Montage.CoverFinal; f.Path != "" && f.Status == studio.StatusDone {
		return f, true
	}
	if c := p.Montage.Cover; c.Path != "" && c.Status == studio.StatusDone {
		return c, false
	}
	return studio.Asset{}, false
}

// ---- preview pipeline ------------------------------------------------------------------------------

// previewImage returns the live preview (w px wide), scheduling renders.
func (s *proj) previewImage(gtx C, c *core.Ctx, pv *core.ProjectView, w int) (paint.ImageOp, bool) {
	st := &s.cov
	bucket := gui.ImgBucket(w)
	want := coverSig(&pv.P) ^ int64(bucket)<<40
	if want != st.prevWant {
		st.prevWant = want
		st.prevAt = gtx.Now.Add(150 * time.Millisecond)
		if st.prevVer == 0 {
			st.prevAt = gtx.Now
		}
	}
	if st.prevKey != st.prevWant {
		if wait := st.prevAt.Sub(gtx.Now); wait > 0 {
			gtx.Execute(op.InvalidateCmd{At: st.prevAt})
		} else {
			st.prevKey = st.prevWant
			st.prevVer = nextVersion()
		}
	}
	if st.prevVer == 0 || c.Img == nil {
		return paint.ImageOp{}, false
	}
	ct := pv.P.Montage.CoverText
	id, aspect := pv.ID, pv.P.Input.Aspect
	ver := st.prevVer
	maxW := bucket
	if aspect == "9:16" {
		maxW = min(bucket, 540)
	}
	img, _, ok := c.Img.Load("coverprev:"+id, ver, -1, func() (image.Image, error) {
		return c.E.PreviewCover(id, ct, maxW)
	})
	s.measureTitle(c, ct, aspect, st.prevKey)
	st.lastImg, st.lastOK = img, ok
	return img, ok
}

// measureTitle learns (in the background, once per design) how big the
// title ends up on the real cover and whether a phone can read it.
func (s *proj) measureTitle(c *core.Ctx, ct studio.CoverText, aspect string, sig int64) {
	st := &s.cov
	if sig == st.measured {
		return
	}
	st.measured = sig
	c.Background(func() {
		m, err := cover.Measure(studio.CoverSpec(ct, aspect), cover.SizeFor(aspect))
		if err != nil {
			return
		}
		c.Post(func() {
			if st.measured == sig {
				st.small, st.titlePx = m.Small, int(math.Round(m.TitleSize))
			}
		})
	})
}

// versions numbers cache entries across every view (so two views of the
// same project never mix their pictures).
var versions atomic.Int64

func nextVersion() int64 { return versions.Add(1) }

// templateImage is the preview of one template with the user's texts.
func (s *proj) templateImage(gtx C, c *core.Ctx, pv *core.ProjectView, tpl string) (paint.ImageOp, bool) {
	st := &s.cov
	p := &pv.P
	ct := p.Montage.CoverText
	want := hashOf(ct.Title, ct.Subtitle, ct.Badge, p.Montage.Cover.Path, fmt.Sprint(p.Montage.Cover.UpdatedAt), p.Input.Aspect, firstFrame(p), p.Title)
	if want != st.tplWant {
		st.tplWant = want
		st.tplAt = gtx.Now.Add(450 * time.Millisecond)
		if st.tplVer == 0 {
			st.tplAt = gtx.Now
		}
	}
	if st.tplKey != st.tplWant {
		if wait := st.tplAt.Sub(gtx.Now); wait > 0 {
			gtx.Execute(op.InvalidateCmd{At: st.tplAt})
		} else {
			st.tplKey = st.tplWant
			st.tplVer = nextVersion()
		}
	}
	if st.tplVer == 0 || c.Img == nil {
		return paint.ImageOp{}, false
	}
	sample := studio.CoverText{Template: tpl, Title: ct.Title, Subtitle: ct.Subtitle, Badge: ct.Badge}
	if strings.TrimSpace(sample.Title) == "" {
		sample.Title = coverTitleSample(p)
	}
	id := pv.ID
	maxW := 320
	if p.Input.Aspect == "9:16" {
		maxW = 240
	}
	img, _, ok := c.Img.Load("covertpl:"+id+":"+tpl, st.tplVer, -1, func() (image.Image, error) {
		return c.E.PreviewCover(id, sample, maxW)
	})
	return img, ok
}

// coverTitleSample stands in for an empty title in the template gallery.
func coverTitleSample(p *studio.Project) string {
	t := strings.TrimSpace(p.Title)
	if t == "" || t == "Новий проєкт" || t == "Без назви" {
		return "Твій *заголовок* тут"
	}
	words := strings.Fields(t)
	if len(words) > 5 {
		words = words[:5]
	}
	return strings.Join(words, " ")
}

// fontSample renders a cover font's name in the face itself (theme colours).
func fontSample(c *core.Ctx, f cover.FontDef) (paint.ImageOp, bool) {
	if c.Img == nil {
		return paint.ImageOp{}, false
	}
	bg, fg := c.Th.Pal.Surface, c.Th.Pal.Text
	key := fmt.Sprintf("coverfont:%s:%02x%02x%02x:%02x%02x%02x", f.ID, bg.R, bg.G, bg.B, fg.R, fg.G, fg.B)
	img, _, ok := c.Img.Load(key, 1, -1, func() (image.Image, error) {
		canvas := image.NewRGBA(image.Rect(0, 0, 420, 150))
		draw.Draw(canvas, canvas.Rect, &image.Uniform{C: color.RGBA{bg.R, bg.G, bg.B, 255}}, image.Point{}, draw.Src)
		spec := cover.ResolveSpec(cover.Text{Template: "minimal", Title: "Заголовок", Font: f.ID,
			Color: fmt.Sprintf("#%02X%02X%02X", fg.R, fg.G, fg.B), Position: "center"})
		spec.Look.Scrim, spec.Look.ShadowOpacity, spec.Look.Vignette = "none", 0, 0
		spec.Look.Align, spec.Look.MaxSize, spec.Look.Uppercase = "center", 0.42, false
		if err := cover.Draw(canvas, spec); err != nil {
			return nil, err
		}
		return canvas, nil
	})
	return img, ok
}

// ---- the card ----------------------------------------------------------------------------------------

func (s *proj) coverCard(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.cov
	p := &pv.P
	m := p.Montage
	id := pv.ID
	s.coverEvents(gtx, c, pv)

	// What was applied: learn it from a finished cover we did not render.
	if f := m.CoverFinal; f.Status == studio.StatusDone && f.UpdatedAt != st.appliedAt {
		st.appliedAt = f.UpdatedAt
		if f.UpdatedAt >= m.Cover.UpdatedAt || st.appliedSig == 0 {
			st.appliedSig = coverSig(p)
		}
	}
	sig := coverSig(p)
	outdated := studio.CoverFinalOutdated(*p)
	dirty := m.Cover.Path != "" && (m.CoverFinal.Path == "" || outdated || (st.appliedSig != 0 && sig != st.appliedSig))
	coverRunning := s.running(pv) == studio.JobCover || m.Cover.Status == studio.StatusRunning

	status := func(gtx C) D {
		switch {
		case coverRunning:
			return gui.StatusPill{State: gui.StateRunning, Percent: jobPct(s.job(pv), studio.JobCover), Text: "Малюємо картинку"}.Layout(gtx, th)
		case m.CoverFinal.Status == studio.StatusRunning || st.applying:
			return gui.StatusPill{State: gui.StateRunning, Percent: -1, Text: "Застосовуємо текст"}.Layout(gtx, th)
		case m.Cover.Path == "":
			return gui.StatusPill{State: gui.StateEmpty, Text: "Картинки ще немає"}.Layout(gtx, th)
		case dirty:
			return gui.StatusPill{State: gui.StateStale, Text: "Є незастосовані зміни"}.Layout(gtx, th)
		case m.CoverFinal.Status == studio.StatusError:
			return gui.StatusPill{State: gui.StateError}.Layout(gtx, th)
		}
		return gui.StatusPill{State: gui.StateDone, Text: "Готова"}.Layout(gtx, th)
	}
	_ = id
	return gui.Card{}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		inner := gtx.Constraints.Max.X
		wide := inner >= gtx.Dp(900)
		header := func(gtx C) D {
			if inner < gtx.Dp(520) {
				return gui.VStack(gtx, gui.Sp8,
					func(gtx C) D {
						return cardHeader(gtx, th, gui.IconImage, "Обкладинка", "Картинка й текст для YouTube.", nil)
					},
					func(gtx C) D { return layout.W.Layout(gtx, status) })
			}
			return cardHeader(gtx, th, gui.IconImage, "Обкладинка", "Картинка й текст для YouTube — із живим переглядом, як її побачать глядачі.", status)
		}
		previewCol := func(gtx C) D {
			return gui.VStack(gtx, gui.Sp16,
				func(gtx C) D { return s.coverPreview(gtx, c, pv, coverRunning) },
				func(gtx C) D { return s.youtubeMocks(gtx, c, pv) },
				func(gtx C) D { return s.coverActions(gtx, c, pv, dirty, outdated) },
			)
		}
		editor := func(gtx C) D { return s.coverEditor(gtx, c, pv, coverRunning) }
		if wide {
			span := float32(5)
			if p.Input.Aspect == "9:16" {
				span = 4
			}
			return gui.VStack(gtx, gui.Sp20, header, func(gtx C) D {
				return gui.Columns{Gap: gui.Sp32}.Layout(gtx, gui.Col{Span: span, W: previewCol}, gui.Col{Span: 12 - span, W: editor})
			})
		}
		return gui.VStack(gtx, gui.Sp20, header, previewCol, editor)
	})
}

// jobPct is the percent of a running job of kind k (−1 otherwise).
func jobPct(j *studio.Job, k studio.JobKind) int {
	if j != nil && j.Status == studio.JobRunning && j.Kind == k {
		return j.Progress
	}
	return -1
}

// coverEvents handles the studio's buttons and fields.
func (s *proj) coverEvents(gtx C, c *core.Ctx, pv *core.ProjectView) {
	st := &s.cov
	p := &pv.P
	id := pv.ID
	editCT := func(fn func(ct *studio.CoverText)) {
		c.Edit(id, func(p *studio.Project) { fn(&p.Montage.CoverText) })
	}
	if v, ok := st.title.Changed(gtx); ok {
		editCT(func(ct *studio.CoverText) { ct.Title = v })
	}
	if v, ok := st.subtitle.Changed(gtx); ok {
		editCT(func(ct *studio.CoverText) { ct.Subtitle = v })
	}
	if v, ok := st.badge.Changed(gtx); ok {
		editCT(func(ct *studio.CoverText) { ct.Badge = v })
	}
	if v, ok := st.prompt.Changed(gtx); ok {
		c.Edit(id, func(p *studio.Project) { p.Montage.CoverPrompt = v })
	}
	st.title.Sync(gtx, p.Montage.CoverText.Title)
	st.subtitle.Sync(gtx, p.Montage.CoverText.Subtitle)
	st.badge.Sync(gtx, p.Montage.CoverText.Badge)
	st.prompt.Sync(gtx, p.Montage.CoverPrompt)
	if i, ok := st.tabs.Changed(gtx); ok {
		st.tab = i
	}
	if st.toPicture.Clicked(gtx) {
		st.tab = tabPicture
	}
	if v, ok := st.textColors.Changed(); ok {
		editCT(func(ct *studio.CoverText) { ct.Color = v })
	}
	if v, ok := st.strokeColors.Changed(); ok {
		editCT(func(ct *studio.CoverText) { ct.Stroke = v })
	}
	if v, ok := st.accentColors.Changed(); ok {
		editCT(func(ct *studio.CoverText) { ct.Accent = v })
	}
	if v, ok := st.size.Commit(gtx); ok {
		size := int(math.Round(float64(v)))
		editCT(func(ct *studio.CoverText) { ct.Size = size })
	}
	if v, ok := st.autoSize.Changed(gtx); ok {
		size := 0
		if !v {
			size = max(40, st.titlePx)
			if size <= 40 {
				size = 110
			}
		}
		editCT(func(ct *studio.CoverText) { ct.Size = size })
	}
	if st.suggest.Clicked(gtx) && !st.suggesting && !c.Offline {
		st.suggesting = true
		c.Background(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			title, err := c.E.SuggestCoverTitle(ctx, id)
			c.Post(func() {
				st.suggesting = false
				if err != nil {
					c.Error(err)
					return
				}
				st.title.SetText(title)
				c.Edit(id, func(p *studio.Project) { p.Montage.CoverText.Title = title })
			})
		})
	}
	if st.gen.Clicked(gtx) || st.gen2.Clicked(gtx) {
		prompt := strings.TrimSpace(st.prompt.Text())
		c.Run(id, studio.StageCover, false, prompt)
	}
	if st.apply.Clicked(gtx) && !st.applying && p.Montage.Cover.Path != "" {
		st.applying = true
		sig := coverSig(p)
		c.Background(func() {
			err := c.E.ApplyCoverText(id)
			c.Post(func() {
				st.applying = false
				if err != nil {
					c.Error(err)
					return
				}
				st.appliedSig = sig
				c.Toast(gui.ToneSuccess, "Обкладинку збережено — вона готова для YouTube")
			})
		})
	}
	file, _ := coverFile(p)
	abs := pv.Abs(file.Path)
	if st.open.Clicked(gtx) && abs != "" {
		c.OpenPath(abs)
	}
	if st.reveal.Clicked(gtx) && abs != "" {
		c.Reveal(abs)
	}
	if st.save.Clicked(gtx) && abs != "" {
		ext := strings.ToLower(filepath.Ext(abs))
		if ext == "" {
			ext = ".jpg"
		}
		saveAs(c, abs, "Зберегти обкладинку", fileName(p.Title)+"-cover"+ext, ext)
	}
}

// coverPreview is the big live preview (16:9 or a Shorts cover).
func (s *proj) coverPreview(gtx C, c *core.Ctx, pv *core.ProjectView, running bool) D {
	th := c.Th
	st := &s.cov
	p := &pv.P
	ratio := ratioOf(p.Input.Aspect)
	w := gtx.Constraints.Max.X
	if ratio < 1 {
		w = min(w, gtx.Dp(300))
	}
	h := int(float32(w) / ratio)
	img, ok := s.previewImage(gtx, c, pv, w)
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx, layout.Rigid(func(gtx C) D {
		size := pt(w, h)
		defer strokeRR(gtx, image.Rectangle{Max: size}, gtx.Dp(gui.RadiusInner), float32(max(1, gtx.Dp(1))), gui.WithAlpha(th.Pal.Text, 0.1))
		defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(gui.RadiusInner)).Push(gtx.Ops).Pop()
		th2 := gui.Thumb{Image: img, Ratio: ratio, Radius: gui.RadiusInner, Loading: !ok, Fade: &st.fade}
		gg := gtx
		gg.Constraints = layout.Exact(size)
		th2.Layout(gg, th)
		if p.Montage.Cover.Path == "" && ok {
			pill(gtx, th, gtx.Dp(10), size.Y-gtx.Dp(30), "Поки на кадрі сцени — картинки ще немає")
		}
		if running {
			paint.FillShape(gtx.Ops, gui.WithAlpha(th.Pal.Card, 0.78), clip.Rect(image.Rectangle{Max: size}).Op())
			pct := jobPct(s.job(pv), studio.JobCover)
			g := gtx
			g.Constraints = layout.Exact(size)
			layout.Center.Layout(g, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(gui.ProgressRing{Value: float32(max(pct, 0)) / 100, Size: 72, Anim: &s.cov.ring}.Widget(th)),
					layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
					layout.Rigid(gui.Text{Style: th.Label, Color: th.Pal.Text}.Widget(th, "Малюємо обкладинку…")),
				)
			})
		}
		return D{Size: size}
	}))
}

// youtubeMocks shows the cover the way YouTube lists it on a phone.
func (s *proj) youtubeMocks(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	p := &pv.P
	img, ok := s.cov.lastImg, s.cov.lastOK // the big preview's picture, scaled down
	title := strings.TrimSpace(p.Title)
	if title == "" {
		title = "Моє відео"
	}
	dur := core.Clock(s.film.total)
	if out := p.Montage.Output; out.Duration > 0 {
		dur = core.Clock(out.Duration)
	}
	thumb := func(gtx C, w, h int) D {
		size := pt(w, h)
		defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(8)).Push(gtx.Ops).Pop()
		paint.Fill(gtx.Ops, th.Pal.SurfaceHover)
		if ok {
			drawImage(gtx, img, size, true)
		}
		if p.Input.Aspect != "9:16" && s.film.total > 0 {
			st := th.Caption.Weighted(gui.FontSemiBold)
			st.Size = pxToSp(gtx, float32(gtx.Sp(11)))
			tw := th.TextWidth(gtx, st, dur)
			r := image.Rect(w-tw-gtx.Dp(10), h-gtx.Dp(20), w-gtx.Dp(4), h-gtx.Dp(4))
			fillRR(gtx, r, gtx.Dp(4), color.NRGBA{A: 205})
			textAt(gtx, th, r.Min.X+gtx.Dp(3), r.Min.Y+gtx.Dp(1), tw+gtx.Dp(2), st, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, dur)
		}
		return D{Size: size}
	}
	label := func(gtx C, s string) D {
		return gui.Text{Style: th.Label, Color: th.Pal.Text}.Layout(gtx, th, s)
	}
	if p.Input.Aspect == "9:16" {
		// Shorts shelf: two tall tiles, ours first.
		return gui.VStack(gtx, gui.Sp8, func(gtx C) D { return label(gtx, "Так побачать у Shorts") }, func(gtx C) D {
			tw := min((gtx.Constraints.Max.X-gtx.Dp(12))/2, gtx.Dp(150))
			th2 := tw * 16 / 9
			tile := func(gtx C, real bool) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx C) D {
						if real {
							return thumb(gtx, tw, th2)
						}
						size := pt(tw, th2)
						fillRR(gtx, image.Rectangle{Max: size}, gtx.Dp(8), th.Pal.Surface)
						return D{Size: size}
					}),
					layout.Rigid(layout.Spacer{Height: gui.Sp4}.Layout),
					layout.Rigid(func(gtx C) D {
						gtx.Constraints.Max.X = tw
						if !real {
							return gui.Skeleton{Width: pxToDp(gtx, tw*3/4), Height: 10}.Layout(gtx, th)
						}
						return gui.Text{Style: th.Small.Weighted(gui.FontMedium), MaxLines: 2}.Layout(gtx, th, title)
					}),
				)
			}
			return layout.Flex{}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return tile(gtx, true) }),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Rigid(func(gtx C) D { return tile(gtx, false) }),
			)
		})
	}
	phone := func(gtx C) D {
		w := min(gtx.Constraints.Max.X, gtx.Dp(250))
		pad := gtx.Dp(10)
		g := gtx
		g.Constraints = layout.Constraints{Min: pt(w-2*pad, 0), Max: pt(w-2*pad, 1<<16)}
		d, call := measure(g, func(gtx C) D {
			tw := gtx.Constraints.Max.X
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D { return thumb(gtx, tw, tw*9/16) }),
				layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
				layout.Rigid(func(gtx C) D {
					return layout.Flex{Alignment: layout.Start}.Layout(gtx,
						layout.Rigid(func(gtx C) D {
							return layout.Inset{Right: gui.Sp8}.Layout(gtx, gui.Avatar{Name: title, Size: 24}.Widget(th))
						}),
						layout.Flexed(1, func(gtx C) D {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(gui.Text{Style: th.Small.Weighted(gui.FontMedium), MaxLines: 2}.Widget(th, title)),
								layout.Rigid(gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, "Мій канал · 1,2 тис. переглядів")),
							)
						}),
					)
				}),
			)
		})
		size := pt(w, d.Size.Y+2*pad)
		r := image.Rectangle{Max: size}
		fillRR(gtx, r, gtx.Dp(18), th.Pal.Surface) // an inset, not a card in the card
		o := op.Offset(pt(pad, pad)).Push(gtx.Ops)
		call.Add(gtx.Ops)
		o.Pop()
		return D{Size: size}
	}
	side := func(gtx C) D {
		tw := gtx.Dp(150)
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(func(gtx C) D { return thumb(gtx, tw, tw*9/16) }),
			layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
			layout.Flexed(1, func(gtx C) D {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(gui.Text{Style: th.Small.Weighted(gui.FontMedium), MaxLines: 2}.Widget(th, title)),
					layout.Rigid(gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, "Мій канал")),
					layout.Rigid(gui.Text{Style: th.Caption, Color: th.Pal.Text3, MaxLines: 1}.Widget(th, "1,2 тис. переглядів · 2 дні тому")),
				)
			}),
		)
	}
	return gui.VStack(gtx, gui.Sp8, func(gtx C) D { return label(gtx, "Так побачать на YouTube") }, func(gtx C) D {
		if gtx.Constraints.Max.X >= gtx.Dp(470) {
			return layout.Flex{Alignment: layout.Start}.Layout(gtx,
				layout.Rigid(phone),
				layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
				layout.Flexed(1, side),
			)
		}
		return gui.VStack(gtx, gui.Sp12, phone, side)
	})
}

// coverActions: apply the text and the file buttons, with a status line.
func (s *proj) coverActions(gtx C, c *core.Ctx, pv *core.ProjectView, dirty, outdated bool) D {
	th := c.Th
	st := &s.cov
	p := &pv.P
	m := p.Montage
	file, final := coverFile(p)
	var note string
	var tone gui.Tone
	switch {
	case outdated && m.CoverFinal.Path != "":
		note, tone = "Картинку оновлено — застосуй текст ще раз, щоб зберегти нову обкладинку.", gui.ToneWarning
	case dirty && m.CoverFinal.Path != "":
		note, tone = "Є зміни, яких ще немає в збереженій обкладинці.", gui.ToneWarning
	case dirty:
		note, tone = "Натисни «Застосувати», щоб зберегти обкладинку з текстом.", gui.ToneNeutral
	case m.CoverFinal.Status == studio.StatusError:
		note, tone = "Не вдалося застосувати текст: "+core.ErrorText(fmt.Errorf("%s", m.CoverFinal.Error)), gui.ToneError
	case final:
		n := st.finalSize.get(pv.Abs(file.Path), file.UpdatedAt)
		sz := cover.SizeFor(p.Input.Aspect)
		note, tone = fmt.Sprintf("Готова обкладинка %d×%d · %s — можна завантажувати на YouTube.", sz.X, sz.Y, bytesText(n)), gui.ToneSuccess
	}
	has := file.Path != ""
	if m.Cover.Path == "" {
		running := s.running(pv) == studio.JobCover || m.Cover.Status == studio.StatusRunning
		busy := s.running(pv) != ""
		return gui.VStack(gtx, gui.Sp12,
			func(gtx C) D {
				return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx,
					func(gtx C) D {
						return gui.Button{Icon: gui.IconWandSparkles, Text: "Намалювати картинку", Busy: running, Disabled: busy && !running,
							Tip: "Малює модель зображень — це витрачає кредити"}.Layout(gtx, th, &st.gen2)
					},
					func(gtx C) D {
						return gui.Button{Kind: gui.Ghost, Text: "Кадр сцени чи своя картинка", Trail: gui.IconChevronRight}.Layout(gtx, th, &st.toPicture)
					},
				)
			},
			func(gtx C) D {
				return infoRow(gtx, th, gui.IconInfo, gui.ToneNeutral, "Картинку намалює модель зображень — з героями твого фільму, без тексту. Або візьми кадр сцени чи свою картинку. Текст можна готувати вже зараз: перегляд показує його на кадрі сцени.")
			},
		)
	}
	return gui.VStack(gtx, gui.Sp12,
		func(gtx C) D {
			return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx,
				func(gtx C) D {
					kind := gui.Secondary
					if dirty {
						kind = gui.Primary
					}
					return gui.Button{Kind: kind, Icon: gui.IconCheck, Text: "Застосувати", Busy: st.applying || m.CoverFinal.Status == studio.StatusRunning,
						Disabled: m.Cover.Path == "", Tip: "Накласти текст і зберегти готову обкладинку"}.Layout(gtx, th, &st.apply)
				},
				func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Icon: gui.IconExternalLink, Text: "Відкрити", Disabled: !has}.Layout(gtx, th, &st.open)
				},
				func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Icon: gui.IconFolderOpen, Tip: "Показати в папці", Disabled: !has}.Layout(gtx, th, &st.reveal)
				},
				func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Icon: gui.IconDownload, Text: "Зберегти як…", Disabled: !has}.Layout(gtx, th, &st.save)
				},
			)
		},
		func(gtx C) D {
			if note == "" {
				return D{}
			}
			ic := gui.IconInfo
			switch tone {
			case gui.ToneSuccess:
				ic = gui.IconCheckCircle
			case gui.ToneWarning:
				ic = gui.IconRefreshCw
			case gui.ToneError:
				ic = gui.IconAlertTriangle
			}
			return infoRow(gtx, th, ic, tone, note)
		},
	)
}

// coverEditor: the tabs and their content.
func (s *proj) coverEditor(gtx C, c *core.Ctx, pv *core.ProjectView, running bool) D {
	th := c.Th
	st := &s.cov
	tabs := []gui.Tab{
		{Label: "Текст", Icon: gui.IconType},
		{Label: "Шаблони", Icon: gui.IconLayoutGrid},
		{Label: "Стиль", Icon: gui.IconPalette},
		{Label: "Картинка", Icon: gui.IconImage},
	}
	return gui.VStack(gtx, gui.Sp20,
		func(gtx C) D {
			st.tabs.Full = gtx.Constraints.Max.X < gtx.Dp(560)
			return st.tabs.Layout(gtx, th, st.tab, tabs)
		},
		func(gtx C) D {
			switch st.tab {
			case tabTemplates:
				return s.coverTemplates(gtx, c, pv)
			case tabStyle:
				return s.coverStyle(gtx, c, pv)
			case tabPicture:
				return s.coverPicture(gtx, c, pv, running)
			}
			return s.coverText(gtx, c, pv)
		},
	)
}

// coverText: title (with the *highlight* hint and a suggestion), subtitle, badge.
func (s *proj) coverText(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.cov
	p := &pv.P
	id := pv.ID
	badges := []string{"НОВЕ", "ЧАСТИНА 1", "ПРАВДИВА ІСТОРІЯ", "ФІНАЛ"}
	if len(st.badgeChips) < len(badges) {
		st.badgeChips = make([]gui.Clickable, len(badges))
	}
	for i, b := range badges {
		if st.badgeChips[i].Clicked(gtx) {
			v := b
			st.badge.SetText(v)
			c.Edit(id, func(p *studio.Project) { p.Montage.CoverText.Badge = v })
		}
	}
	return gui.VStack(gtx, gui.Sp16,
		func(gtx C) D {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, gui.Text{Style: th.Label}.Widget(th, "Заголовок")),
						layout.Rigid(func(gtx C) D {
							return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Icon: gui.IconSparkles, Text: "Підказати заголовок", Busy: st.suggesting,
								Disabled: c.Offline, Tip: "Штучний інтелект запропонує короткий заголовок мовою відео"}.Layout(gtx, th, &st.suggest)
						}),
					)
				}),
				layout.Rigid(layout.Spacer{Height: gui.Sp4}.Layout),
				layout.Rigid(func(gtx C) D {
					return st.title.Layout(gtx, th, gui.FieldStyle{Placeholder: "2–5 слів, що чіпляють", MaxLen: cover.MaxTitle,
						MinLines: 2, MaxLines: 3,
						Hint: "Виділи головне слово зірочками — *слово* — і воно стане кольоровим. Новий рядок — Enter."})
				}),
			)
		},
		func(gtx C) D {
			if !st.small || strings.TrimSpace(p.Montage.CoverText.Title) == "" {
				return D{}
			}
			return infoRow(gtx, th, gui.IconSmartphone, gui.ToneWarning, "Заголовок задовгий: на телефоні літери будуть дрібні. Скороти до 2–5 слів або вибери «Вузький» шрифт.")
		},
		func(gtx C) D {
			return st.subtitle.Layout(gtx, th, gui.FieldStyle{Label: "Підзаголовок", Placeholder: "Необов'язково: одна коротка фраза", MaxLen: cover.MaxSubtitle})
		},
		func(gtx C) D {
			return gui.VStack(gtx, gui.Sp8,
				func(gtx C) D {
					return st.badge.Layout(gtx, th, gui.FieldStyle{Label: "Бейдж", Placeholder: "Маленька позначка, напр. НОВЕ", MaxLen: cover.MaxBadge})
				},
				func(gtx C) D {
					ws := make([]layout.Widget, len(badges))
					for i, b := range badges {
						i, b := i, b
						ws[i] = func(gtx C) D {
							return gui.Chip{Text: b, Small: true, Selected: strings.EqualFold(strings.TrimSpace(p.Montage.CoverText.Badge), b)}.Layout(gtx, th, &st.badgeChips[i])
						}
					}
					return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
				},
			)
		},
	)
}

// coverTemplates: every template previewed with the user's texts.
func (s *proj) coverTemplates(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.cov
	p := &pv.P
	id := pv.ID
	tpls := studio.CoverTemplates()
	spec := studio.CoverSpec(p.Montage.CoverText, p.Input.Aspect)
	ts := tiles(&st.tplTiles, len(tpls))
	ratio := ratioOf(p.Input.Aspect)
	minW := float32(170)
	if ratio < 1 {
		minW = 118
	}
	return gui.VStack(gtx, gui.Sp12,
		hint(th, "Готові дизайни з твоїм текстом. Шаблон задає шрифт, кольори й розташування — їх можна змінити у вкладці «Стиль»."),
		func(gtx C) D {
			return gui.Grid{MinWidth: unit.Dp(minW), Gap: gui.Sp12}.Layout(gtx, len(tpls), func(gtx C, i int) D {
				t := tpls[i]
				label := t.Label
				return ts[i].layout(gtx, th, tileStyle{Label: label, Tip: t.Description, Selected: t.ID == spec.Template, Ratio: ratio,
					Preview: func(gtx C, size image.Point, sec float64) {
						if img, ok := s.templateImage(gtx, c, pv, t.ID); ok {
							drawImage(gtx, img, size, true)
						} else {
							g := gtx
							g.Constraints = layout.Exact(size)
							gui.Skeleton{Radius: -1}.Layout(g, th)
						}
						if t.Shorts && ratio > 1 {
							pill(gtx, th, gtx.Dp(6), size.Y-gtx.Dp(24), "для Shorts")
						}
					}}, func() {
					tid := t.ID
					c.Edit(id, func(p *studio.Project) {
						ct := &p.Montage.CoverText
						ct.Template = tid
						ct.Font, ct.Color, ct.Stroke, ct.Accent, ct.Position, ct.Size = "", "", "", "", "", 0
					})
				})
			})
		},
	)
}

// coverStyle: font, colours, position and size.
func (s *proj) coverStyle(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.cov
	p := &pv.P
	id := pv.ID
	ct := p.Montage.CoverText
	spec := studio.CoverSpec(ct, p.Input.Aspect)
	tpl, _ := cover.TemplateByID(spec.Template)
	fonts := studio.CoverFonts()
	ts := tiles(&st.fontTiles, len(fonts))
	positions := studio.CoverPositions()
	if len(st.posChips) < len(positions) {
		st.posChips = make([]gui.Clickable, len(positions))
	}
	for i, ps := range positions {
		if st.posChips[i].Clicked(gtx) {
			v := ps.ID
			c.Edit(id, func(p *studio.Project) { p.Montage.CoverText.Position = v })
		}
	}
	textCols := []swatch{{Value: "", Color: parseHex(tpl.Color), Default: true, Tip: "Як у шаблоні"},
		{Value: "#FFFFFF", Tip: "Білий"}, {Value: "#111111", Tip: "Чорний"}, {Value: "#FFE417", Tip: "Жовтий"},
		{Value: "#FFD400", Tip: "Золотий"}, {Value: "#FF8A00", Tip: "Помаранчевий"}, {Value: "#E5162B", Tip: "Червоний"},
		{Value: "#FF3DE5", Tip: "Рожевий"}, {Value: "#18E4FF", Tip: "Блакитний"}, {Value: "#2BD96B", Tip: "Зелений"}, {Value: "#FFF8EC", Tip: "Кремовий"}}
	strokeDef := parseHex(tpl.Stroke)
	if tpl.Stroke == "" {
		strokeDef = th.Pal.Surface
	}
	strokeCols := []swatch{{Value: "", Color: strokeDef, Default: true, Tip: "Як у шаблоні"}, {Value: "none", None: true, Tip: "Без контуру"},
		{Value: "#000000", Tip: "Чорний"}, {Value: "#FFFFFF", Tip: "Білий"}, {Value: "#4A1D6E", Tip: "Фіолетовий"},
		{Value: "#E5162B", Tip: "Червоний"}, {Value: "#1E5BFF", Tip: "Синій"}, {Value: "#18E4FF", Tip: "Блакитний"}}
	accentCols := []swatch{{Value: "", Color: parseHex(tpl.Accent), Default: true, Tip: "Як у шаблоні"},
		{Value: "#FFE417", Tip: "Жовтий"}, {Value: "#E5162B", Tip: "Червоний"}, {Value: "#18E4FF", Tip: "Блакитний"},
		{Value: "#FF3DE5", Tip: "Рожевий"}, {Value: "#2BD96B", Tip: "Зелений"}, {Value: "#FF8A00", Tip: "Помаранчевий"}, {Value: "#FFFFFF", Tip: "Білий"}}
	return gui.VStack(gtx, gui.Sp20,
		func(gtx C) D {
			return fieldLabel(gtx, th, "Шрифт", "", func(gtx C) D {
				return gui.Grid{MinWidth: 150, Gap: gui.Sp12}.Layout(gtx, len(fonts), func(gtx C, i int) D {
					f := fonts[i]
					return ts[i].layout(gtx, th, tileStyle{Label: f.Label, Tip: f.Name + " — " + lower1(f.Hint), Selected: f.ID == spec.Font, Ratio: 2.8,
						Preview: func(gtx C, size image.Point, sec float64) {
							if img, ok := fontSample(c, f); ok {
								drawImage(gtx, img, size, true)
							}
						}}, func() {
						fid := f.ID
						c.Edit(id, func(p *studio.Project) { p.Montage.CoverText.Font = fid })
					})
				})
			})
		},
		func(gtx C) D {
			return fieldLabel(gtx, th, "Колір тексту", "", func(gtx C) D { return st.textColors.Layout(gtx, th, textCols, ct.Color) })
		},
		func(gtx C) D {
			return fieldLabel(gtx, th, "Контур літер", "", func(gtx C) D { return st.strokeColors.Layout(gtx, th, strokeCols, ct.Stroke) })
		},
		func(gtx C) D {
			return fieldLabel(gtx, th, "Акцент (виділене слово й бейдж)", "", func(gtx C) D { return st.accentColors.Layout(gtx, th, accentCols, ct.Accent) })
		},
		func(gtx C) D {
			return fieldLabel(gtx, th, "Розташування", "", func(gtx C) D {
				ws := make([]layout.Widget, len(positions))
				for i, ps := range positions {
					i, ps := i, ps
					ws[i] = func(gtx C) D {
						return gui.Chip{Text: ps.Label, Selected: ps.ID == spec.Position}.Layout(gtx, th, &st.posChips[i])
					}
				}
				return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
			})
		},
		func(gtx C) D {
			auto := ct.Size == 0
			val := "авто"
			if !auto {
				val = fmt.Sprintf("%d пікс.", ct.Size)
			} else if st.titlePx > 0 {
				val = fmt.Sprintf("авто · %d пікс.", st.titlePx)
			}
			return fieldLabel(gtx, th, "Розмір заголовка", val, func(gtx C) D {
				return gui.VStack(gtx, gui.Sp8,
					func(gtx C) D {
						return st.autoSize.Layout(gtx, th, auto, "Підібрати автоматично — якомога більший, що влізе")
					},
					func(gtx C) D {
						v := float32(ct.Size)
						if auto {
							v = float32(max(40, st.titlePx))
						}
						return st.size.Layout(gtx, th, gui.SliderStyle{Value: v, Min: 40, Max: 260, Step: 2, Disabled: auto,
							Format: func(v float32) string { return fmt.Sprintf("%d пікс.", int(v)) }})
					},
				)
			})
		},
	)
}

// coverPicture: generate or redraw the picture, with an optional wish.
func (s *proj) coverPicture(gtx C, c *core.Ctx, pv *core.ProjectView, running bool) D {
	th := c.Th
	st := &s.cov
	p := &pv.P
	cv := p.Montage.Cover
	busy := s.running(pv) != ""
	label := "Намалювати картинку"
	kind := gui.Primary
	if cv.Path != "" {
		label, kind = "Перемалювати", gui.Secondary
	}
	// Quick wishes that add to the prompt.
	ideas := []string{"Крупний план героя з сильною емоцією", "Драматичне контрове світло", "Яскраві соковиті кольори",
		"Багато вільного неба для заголовка", "Таємнича нічна атмосфера"}
	if len(st.ideas) < len(ideas) {
		st.ideas = make([]gui.Clickable, len(ideas))
	}
	for i, idea := range ideas {
		if st.ideas[i].Clicked(gtx) {
			cur := strings.TrimSpace(st.prompt.Text())
			if !strings.Contains(strings.ToLower(cur), strings.ToLower(idea)) {
				if cur != "" {
					cur = strings.TrimRight(cur, ". ") + ". "
				}
				cur += idea + "."
				st.prompt.SetText(cur)
				v := cur
				c.Edit(pv.ID, func(p *studio.Project) { p.Montage.CoverPrompt = v })
			}
		}
	}
	if st.openRaw.Clicked(gtx) && cv.Path != "" {
		c.OpenPath(pv.Abs(cv.Path))
	}
	return gui.VStack(gtx, gui.Sp16,
		func(gtx C) D {
			switch {
			case running:
				j := s.job(pv)
				return gui.VStack(gtx, gui.Sp8,
					func(gtx C) D {
						return gui.Text{Style: th.Label}.Layout(gtx, th, jobLine(j, "Малюємо обкладинку"))
					},
					func(gtx C) D {
						return gui.ProgressBar{Value: core.Progress(j), Anim: &s.cov.bar}.Layout(gtx, th)
					},
				)
			case cv.Status == studio.StatusError:
				return infoRow(gtx, th, gui.IconAlertTriangle, gui.ToneError, "Картинка не вдалась: "+core.ErrorText(fmt.Errorf("%s", cv.Error)))
			case cv.Path != "":
				return infoRow(gtx, th, gui.IconCheckCircle, gui.ToneSuccess, "Картинка є (оновлено "+core.Updated(cv.UpdatedAt)+"). Не подобається — перемалюй за побажанням або візьми кадр сцени чи свою.")
			}
			return infoRow(gtx, th, gui.IconImage, gui.ToneNeutral, "Картинки ще немає. Намалюй її моделлю — яскраву сцену з головними героями, без тексту — або візьми кадр сцени чи свою картинку нижче.")
		},
		func(gtx C) D {
			return st.prompt.Layout(gtx, th, gui.FieldStyle{Label: "Побажання до картинки", MinLines: 3, MaxLines: 6, MaxLen: 2000,
				Placeholder: "Необов'язково: що має бути на обкладинці. Інакше — головні герої крупним планом, яскраве світло, місце для заголовка."})
		},
		func(gtx C) D {
			ws := make([]layout.Widget, len(ideas))
			for i, idea := range ideas {
				i, idea := i, idea
				ws[i] = func(gtx C) D {
					return gui.Chip{Text: idea, Icon: gui.IconPlus, Small: true, Tip: "Додати до побажання"}.Layout(gtx, th, &st.ideas[i])
				}
			}
			return gui.Flow{Gap: gui.Sp8, RowGap: gui.Sp8}.Layout(gtx, ws...)
		},
		func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: kind, Icon: gui.IconWandSparkles, Text: label, Busy: running, Disabled: busy && !running,
						Tip: "Малює модель зображень — це витрачає кредити"}.Layout(gtx, th, &st.gen)
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Flexed(1, func(gtx C) D {
					if busy && !running {
						return gui.Text{Style: th.Small, Color: th.Pal.Text2, MaxLines: 2}.Layout(gtx, th, "Зачекай: на проєкті вже виконується етап.")
					}
					return gui.Text{Style: th.Small, Color: th.Pal.Text3, MaxLines: 2}.Layout(gtx, th, "Малює модель зображень із головними героями — це витрачає кредити.")
				}),
			)
		},
		// Or a ready picture: a scene frame or the user's own.
		func(gtx C) D { return s.coverSources(gtx, c, pv) },
		// The picture itself, without the text.
		func(gtx C) D {
			if cv.Path == "" {
				return D{}
			}
			ratio := ratioOf(p.Input.Aspect)
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					w := gtx.Dp(180)
					if ratio < 1 {
						w = gtx.Dp(96)
					}
					gtx.Constraints = layout.Exact(pt(w, int(float32(w)/ratio)))
					return st.rawThumb.Layout(gtx, th, c.Img, pv.Abs(cv.Path), cv.UpdatedAt, gui.Thumb{Ratio: ratio, Radius: 12})
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp16}.Layout),
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(gui.Text{Style: th.Label}.Widget(th, "Картинка без тексту")),
						layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, "Основа обкладинки. Текст накладається поверх — його можна міняти скільки завгодно.")),
						layout.Rigid(layout.Spacer{Height: gui.Sp8}.Layout),
						layout.Rigid(func(gtx C) D {
							return gui.Link{Text: "Відкрити картинку", Trail: gui.IconExternalLink}.Layout(gtx, th, &st.openRaw)
						}),
					)
				}),
			)
		},
	)
}

// coverSources offers a ready picture as the cover: a frame of a scene or
// the user's own image (SetCoverImage copies it and re-applies the text).
func (s *proj) coverSources(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	th := c.Th
	st := &s.cov
	p := &pv.P
	id := pv.ID
	var frames []*studio.Scene
	for i := range p.Scenes {
		if sc := &p.Scenes[i]; sc.Frame.Path != "" && sc.Frame.Status == studio.StatusDone {
			frames = append(frames, sc)
		}
	}
	// SetCoverImage refuses while a cover or autopilot job runs.
	blocked := st.setting || s.running(pv) == studio.JobCover || s.running(pv) == studio.JobAutopilot
	if st.ownPic.Clicked(gtx) && !blocked {
		c.PickFile("Вибери картинку для обкладинки", []string{".png", ".jpg", ".jpeg", ".webp"}, func(path string) {
			s.setCover(c, id, path, "own")
		})
	}
	if st.moreFrames.Clicked(gtx) {
		st.allFrames = !st.allFrames
	}
	limit := len(frames)
	if !st.allFrames {
		limit = min(limit, 8)
	}
	ratio := ratioOf(p.Input.Aspect)
	minW := unit.Dp(92)
	if ratio < 1 {
		minW = 64
	}
	ts := tiles(&st.frameTiles, len(frames))
	return gui.VStack(gtx, gui.Sp12,
		gui.Divider{}.Widget(th),
		func(gtx C) D {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Flexed(1, func(gtx C) D {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(gui.Text{Style: th.Label}.Widget(th, "Або візьми готову картинку")),
						layout.Rigid(gui.Text{Style: th.Small, Color: th.Pal.Text2}.Widget(th, "Кадр сцени чи твоя картинка стане основою обкладинки — без кредитів, текст ляже поверх.")),
					)
				}),
				layout.Rigid(layout.Spacer{Width: gui.Sp12}.Layout),
				layout.Rigid(func(gtx C) D {
					return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconUpload, Text: "Своя картинка",
						Busy: st.setting && st.settingFrom == "own", Disabled: blocked && !(st.setting && st.settingFrom == "own"),
						Tip: "PNG, JPG або WEBP до 40 МБ"}.Layout(gtx, th, &st.ownPic)
				}),
			)
		},
		func(gtx C) D {
			if len(frames) == 0 {
				return hint(th, "Кадрів сцен ще немає — вони з'являться після кроку «Розкадровка».")(gtx)
			}
			return gui.Grid{MinWidth: minW, Gap: gui.Sp8}.Layout(gtx, limit, func(gtx C, i int) D {
				sc := frames[i]
				sid, src := sc.ID, pv.Abs(sc.Frame.Path)
				n := sceneNumber(p, sid)
				return ts[i].layout(gtx, th, tileStyle{Label: "Сцена " + itoa(n), Tip: "Взяти кадр сцени " + itoa(n) + " для обкладинки",
					Ratio: ratio, Disabled: blocked && st.settingFrom != sid,
					Preview: func(gtx C, size image.Point, sec float64) {
						if img, ok := sceneImage(c, pv, sc, max(size.X, size.Y)); ok {
							drawImage(gtx, img, size, true)
						}
						if st.setting && st.settingFrom == sid {
							paint.FillShape(gtx.Ops, gui.WithAlpha(th.Pal.Card, 0.7), clip.Rect(image.Rectangle{Max: size}).Op())
							g := gtx
							g.Constraints = layout.Exact(size)
							layout.Center.Layout(g, gui.Spinner{Size: 20}.Widget(th))
						}
					}}, func() {
					if !blocked {
						s.setCover(c, id, src, sid)
					}
				})
			})
		},
		func(gtx C) D {
			if len(frames) <= 8 {
				return D{}
			}
			text := "Показати всі кадри (" + itoa(len(frames)) + ")"
			if st.allFrames {
				text = "Згорнути"
			}
			return gui.Link{Text: text, Trail: gui.IconChevronDown}.Layout(gtx, th, &st.moreFrames)
		},
	)
}

// setCover makes src the cover picture (off the UI goroutine).
func (s *proj) setCover(c *core.Ctx, id, src, from string) {
	st := &s.cov
	if st.setting {
		return
	}
	st.setting, st.settingFrom = true, from
	c.Background(func() {
		err := c.E.SetCoverImage(id, src)
		c.Post(func() {
			st.setting, st.settingFrom = false, ""
			if err != nil {
				c.Error(err)
				return
			}
			c.Toast(gui.ToneSuccess, "Картинку обкладинки замінено — текст уже на ній")
		})
	})
}

// ---- files -----------------------------------------------------------------------------------------

// fileName makes a title safe as a file name ("video" when nothing is left).
func fileName(title string) string {
	var b strings.Builder
	for _, r := range title {
		switch {
		case r < 32, strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	s := strings.Trim(strings.TrimSpace(b.String()), ". ")
	if s == "" {
		return "video"
	}
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return s
}

var _ = text.Middle
