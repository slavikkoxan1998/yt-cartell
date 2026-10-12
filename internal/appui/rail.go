package appui

import (
	"fmt"
	"image"
	"image/color"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/appui/home"
	"ytcartell/internal/appui/settings"
	"ytcartell/internal/gui"
)

// AnchorQueue is the tour anchor of the rail's «Черга» item.
const AnchorQueue = "shell.queue"

// railState is the state of the navigation rail (or bottom bar).
type railState struct {
	logo, home, projects, queue, settings, social, theme, plus, update gui.Clickable
	logoHover                                                          gui.Anim
}

// railActions handles the rail's clicks (before anything is laid out).
func (s *Shell) railActions(gtx C) {
	r := &s.rail
	if r.logo.Clicked(gtx) || r.home.Clicked(gtx) {
		s.Navigate(core.Route{Page: core.PageHome})
	}
	if r.projects.Clicked(gtx) {
		s.Navigate(core.Route{Page: core.PageProjects})
	}
	if r.queue.Clicked(gtx) {
		if s.drawer.IsOpen() {
			s.drawer.Close()
		} else {
			s.OpenQueue()
		}
	}
	if r.settings.Clicked(gtx) {
		s.Navigate(core.Route{Page: core.PageSettings})
	}
	if r.social.Clicked(gtx) {
		s.c.OpenPath(settings.CommunityURL)
	}
	if r.theme.Clicked(gtx) {
		home.ToggleTheme(s.c)
	}
	if r.update.Clicked(gtx) {
		if s.upd.drawer.IsOpen() {
			s.upd.drawer.Close()
		} else {
			s.OpenUpdates()
		}
	}
	if r.plus.Clicked(gtx) {
		s.OpenSheet(core.Sheet{Kind: core.SheetNewProject})
	}
}

// navItem is one destination of the rail.
type navItem struct {
	icon   *gui.Icon
	label  string
	active bool
	badge  string
	dot    color.NRGBA // a dot on the icon (zero = none; the badge wins)
	dotTip string
	click  *gui.Clickable
	anchor string
}

func (s *Shell) navItems() []navItem {
	p := s.route.Page
	r := &s.rail
	snap := s.feed.Snapshot()
	badge := ""
	if n := snap.Counts.Running; n > 9 {
		badge = "9+"
	} else if n > 0 {
		badge = fmt.Sprint(n)
	}
	open := s.drawer.IsOpen()
	items := []navItem{
		{icon: gui.IconHome, label: "Головна", active: p == core.PageHome && !open, click: &r.home},
		{icon: gui.IconFolder, label: "Проєкти", active: (p == core.PageProjects || p == core.PageProject) && !open, click: &r.projects},
		{icon: gui.IconListVideo, label: "Черга", active: p == core.PageQueue || open, badge: badge, click: &r.queue, anchor: AnchorQueue},
		{icon: gui.IconSettings, label: "Налаштування", active: p == core.PageSettings && !open, click: &r.settings},
	}
	if badge == "" && snap.Counts.Errors > 0 {
		items[2].dot, items[2].dotTip = s.th.Pal.ErrorFill, "У черзі є помилки"
	}
	// No «Оновлення» on the rail (phones, short windows): Settings › Про
	// студію opens the sheet, so Settings carries the dot.
	if v := s.upd.newer(); v != "" && (s.narrow || !s.updInRail) {
		items[3].dot, items[3].dotTip = s.th.Pal.Action, "Є нова версія "+v+" — «Про студію»"
	}
	return items
}

func (s *Shell) navItem(gtx C, it navItem) D {
	w := func(gtx C) D {
		d := gui.NavItem{Icon: it.icon, Label: it.label, Active: it.active, Badge: it.badge}.Layout(gtx, s.th, it.click)
		if it.dot.A != 0 {
			s.iconDot(gtx, d, it.dot)
			if it.click.Hovered() {
				s.th.Tooltip(gtx, it.click, it.click.Pressed(), it.dotTip)
			}
		}
		return d
	}
	if it.anchor != "" {
		return gui.Anchor(gtx, s.th, it.anchor, w)
	}
	return w(gtx)
}

// iconDot marks a nav item: a small dot on its icon's top-right corner
// (where NavItem puts a count badge) in a ring of the rail's colour.
func (s *Shell) iconDot(gtx C, d D, col color.NRGBA) {
	x := (d.Size.X+gtx.Dp(22))/2 - gtx.Dp(5)
	o := op.Offset(image.Pt(x, gtx.Dp(4))).Push(gtx.Ops)
	gui.FillCircle(gtx, 10, s.th.Pal.Card)
	o2 := op.Offset(image.Pt(gtx.Dp(2), gtx.Dp(2))).Push(gtx.Ops)
	gui.FillCircle(gtx, 6, col)
	o2.Pop()
	o.Pop()
}

// layoutRail draws the 92 dp rail: the logo, the four destinations, then at
// the bottom «Оновлення», the community link, the theme switch and the big
// "+". Short windows drop the extras: both from 680 dp, one from 580 dp
// («Оновлення» while a new version waits, else the community link).
func (s *Shell) layoutRail(gtx C) D {
	th := s.th
	w, h := gtx.Constraints.Max.X, gtx.Constraints.Max.Y
	px := max(1, gtx.Dp(1))
	gui.FillRect(gtx, image.Rect(0, 0, w, h), th.Pal.Card)
	gui.FillRect(gtx, image.Rect(w-px, 0, w, h), th.Pal.Border)
	gtx.Constraints = layout.Exact(image.Pt(w-px, h))
	// Short windows tighten the air so the "+" never leaves the screen.
	short := h < gtx.Dp(640)
	top, afterLogo, gap, bottom := unit.Dp(24), unit.Dp(28), unit.Dp(8), unit.Dp(24)
	if short {
		top, afterLogo, gap, bottom = 16, 16, 2, 16
	}
	newer := s.upd.newer()
	showUpd, showSocial := true, true
	switch {
	case h < gtx.Dp(580):
		showUpd, showSocial = false, false
	case h < gtx.Dp(680):
		showUpd = newer != ""
		showSocial = !showUpd
	}
	s.updInRail = showUpd
	items := s.navItems()
	var nav []layout.FlexChild
	for i, it := range items {
		it := it
		if i > 0 {
			nav = append(nav, layout.Rigid(layout.Spacer{Height: gap}.Layout))
		}
		nav = append(nav, layout.Rigid(func(gtx C) D { return s.navItem(gtx, it) }))
	}
	themeIcon, themeTip := gui.IconMoon, "Увімкнути темну тему"
	if th.Dark() {
		themeIcon, themeTip = gui.IconSun, "Увімкнути світлу тему"
	}
	// The foot items read like the destinations above them: icon over label.
	foot := func(ic *gui.Icon, label, tip string, click *gui.Clickable) layout.Widget {
		return func(gtx C) D {
			if click.Hovered() {
				th.Tooltip(gtx, click, click.Pressed(), tip)
			}
			return gui.NavItem{Icon: ic, Label: label}.Layout(gtx, th, click)
		}
	}
	return layout.Flex{Axis: layout.Vertical, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(layout.Spacer{Height: top}.Layout),
		layout.Rigid(s.logo),
		layout.Rigid(layout.Spacer{Height: afterLogo}.Layout),
		layout.Rigid(func(gtx C) D { return layout.Flex{Axis: layout.Vertical}.Layout(gtx, nav...) }),
		layout.Flexed(1, func(gtx C) D { return D{Size: image.Pt(0, gtx.Constraints.Min.Y)} }),
		layout.Rigid(func(gtx C) D {
			if !showUpd {
				return D{}
			}
			return layout.Inset{Bottom: gap}.Layout(gtx, s.updateItem)
		}),
		layout.Rigid(func(gtx C) D {
			if !showSocial {
				return D{} // short windows keep the destinations and the "+"
			}
			return layout.Inset{Bottom: gap}.Layout(gtx, foot(gui.IconSend, "Спільнота", "Спільнота YT Cartell у Telegram", &s.rail.social))
		}),
		layout.Rigid(foot(themeIcon, "Тема", themeTip, &s.rail.theme)),
		layout.Rigid(layout.Spacer{Height: gui.Sp16}.Layout),
		layout.Rigid(func(gtx C) D {
			return gui.Button{Icon: gui.IconPlus, Round: true, Size: gui.Big, Height: 56, Tip: "Новий проєкт (Ctrl+N)"}.Layout(gtx, th, &s.rail.plus)
		}),
		layout.Rigid(layout.Spacer{Height: bottom}.Layout),
	)
}

// updateItem is the rail's «Оновлення»: an orange dot while a newer
// version is known; it opens the update sheet.
func (s *Shell) updateItem(gtx C) D {
	th := s.th
	c := &s.rail.update
	v := s.upd.newer()
	if c.Hovered() {
		tip := "Перевірити, чи вийшла нова версія"
		if v != "" {
			tip = "Є нова версія " + v
		}
		th.Tooltip(gtx, c, c.Pressed(), tip)
	}
	d := gui.NavItem{Icon: gui.IconDownload, Label: "Оновлення", Active: s.upd.drawer.IsOpen()}.Layout(gtx, th, c)
	if v != "" {
		s.iconDot(gtx, d, th.Pal.Action)
	}
	return d
}

// logo is the studio mark: a warm orange square with a clapperboard; it
// leads Home.
func (s *Shell) logo(gtx C) D {
	th := s.th
	c := &s.rail.logo
	sz := gtx.Dp(48)
	if c.Hovered() {
		th.Tooltip(gtx, c, c.Pressed(), "YT Cartell Studio — на головну")
	}
	s.rail.logoHover.Duration = gui.DurHover
	hv := s.rail.logoHover.Animate(gtx, b2f(c.Hovered()))
	return c.Layout(gtx, func(gtx C) D {
		pointer.CursorPointer.Add(gtx.Ops)
		sc := 1 + 0.04*hv
		if c.Pressed() {
			sc = 0.96
		}
		cx := float32(sz) / 2
		defer op.Affine(f32.AffineId().Scale(f32.Pt(cx, cx), f32.Pt(sc, sc))).Push(gtx.Ops).Pop()
		r := image.Rect(0, 0, sz, sz)
		gui.Shadow(gtx, th, r, 14, gui.LerpElevation(gui.ElevControl, gui.ElevHover, hv))
		gui.FillRRect(gtx, r, 14, gui.LerpColor(th.Pal.Action, th.Pal.ActionHover, hv))
		if gtx.Focused(&c.Clickable) {
			gui.FocusRing(gtx, th, r, 14, 1)
		}
		ic := gtx.Dp(26)
		o := op.Offset(image.Pt((sz-ic)/2, (sz-ic)/2)).Push(gtx.Ops)
		gui.IconClapperboard.Layout(gtx, 26, th.Pal.OnAction)
		o.Pop()
		return D{Size: image.Pt(sz, sz)}
	})
}

// layoutBottomBar is the rail of a phone-sized window: four destinations
// with the big "+" raised in the middle.
func (s *Shell) layoutBottomBar(gtx C) D {
	th := s.th
	W := gtx.Constraints.Max.X
	items := s.navItems()
	// The "+" takes a narrow middle slot so the labels keep their width
	// ("Налаштування" on a 390 dp phone).
	plusSlot := min(gtx.Dp(68), W/5)
	slot := (W - plusSlot) / len(items)
	pad := gtx.Dp(4)
	ig := gtx
	// Items may spill a little into their neighbours' air: the labels are
	// centred and short except "Налаштування", which the last slot needs.
	ig.Constraints = layout.Constraints{Max: image.Pt(slot+gtx.Dp(12), gtx.Dp(80))}
	type rec struct {
		d    D
		call op.CallOp
	}
	recs := make([]rec, len(items))
	h := 0
	for i, it := range items {
		it := it
		recs[i].d, recs[i].call = record(ig, func(gtx C) D { return s.navItem(gtx, it) })
		h = max(h, recs[i].d.Size.Y)
	}
	barH := h + 2*pad
	px := max(1, gtx.Dp(1))
	gui.Shadow(gtx, th, image.Rect(0, 0, W, barH+gtx.Dp(40)), 0, gui.Elevation{{Y: -2, Blur: 12, Alpha: 0.06}})
	gui.FillRect(gtx, image.Rect(0, 0, W, barH), th.Pal.Card)
	gui.FillRect(gtx, image.Rect(0, 0, W, px), th.Pal.Border)
	mid := len(items) / 2
	for i := range items {
		x := i * slot
		if i >= mid {
			x += plusSlot // the middle slot is the "+"
		}
		x = max(0, min(W-recs[i].d.Size.X, x+(slot-recs[i].d.Size.X)/2))
		o := op.Offset(image.Pt(x, pad+(h-recs[i].d.Size.Y)/2)).Push(gtx.Ops)
		recs[i].call.Add(gtx.Ops)
		o.Pop()
	}
	b := gtx.Dp(56)
	o := op.Offset(image.Pt(mid*slot+(plusSlot-b)/2, -gtx.Dp(16))).Push(gtx.Ops)
	gui.Button{Icon: gui.IconPlus, Round: true, Size: gui.Big, Height: 56, Tip: "Новий проєкт"}.Layout(gtx, th, &s.rail.plus)
	o.Pop()
	return D{Size: image.Pt(W, barH)}
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
