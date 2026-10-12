// Package gui is the "warm app" UI kit of YT Cartell Studio (GUI.md): soft
// white cards on a warm ground, Fira Sans, Lucide icons and exactly one bright
// action colour — always something you can press.
//
// KIT GUIDE (one page) — everything a screen needs.
//
// SETUP. th := gui.MustTheme() once per window; th.SetInvalidate(w.Invalidate).
// Every frame, wrap the whole window in th.Root: it paints the ground, runs the
// light/dark crossfade and draws the overlay layers (dialogs, drawers,
// pickers, toasts, tour, tooltip) above your content, in window coordinates.
//
//	th.Root(gtx, func(gtx gui.C) gui.D { return app.Layout(gtx) })
//
// th.SetMode(gui.ModeLight|ModeDark|ModeSystem) crossfades colours in 250 ms;
// gui.SetReducedMotion(true) makes every animation instant ("Менше анімацій").
// Colours come only from th.Pal: Ground, Surface, SurfaceHover, Card, Border,
// BorderStrong, Text, Text2, Text3, Action(+Hover/Pressed), ActionSoft,
// ActionText (links), OnAction, Focus, Success/Running/Warning/Error with
// …Soft (fills) and …Ink (text on …Soft), ErrorFill, Scrim, Track;
// th.Pal.ToneColors(tone) → (strong, soft, ink); gui.OnColor(fill) picks the
// text colour for a solid fill.
//
// TOKENS. Spacing Sp4 Sp8 Sp12 Sp16 Sp20 Sp24 Sp32 Sp40 Sp56. Radii RadiusCard
// 22, RadiusInner 16, RadiusControl 12, RadiusSmall 8, RadiusPill. Heights
// HeightSm 36, HeightMd 44, HeightBig 52. Elevation ElevCard, ElevHover,
// ElevPop, ElevSheet, ElevControl — gui.Shadow(gtx, th, rect, radius, elev).
// Durations DurHover, DurTabs, DurSheet, DurDialog, DurValue, DurRoute …
// Type (th.<Style>): Display 40, H1 32, H2 24, H3 18, Body 15, BodyStrong,
// Label 14 medium, Small 13, Caption 12, Stat 28 (no eyebrow/kicker text above
// headings — group labels are sentence-case Label or Small) — drawn by
// gui.Text{Style: th.H3, Color: th.Pal.Text2, MaxLines: 1}.Layout(gtx, th, s)
// (zero Color = Text; .Widget(th, s) for flex children). PluralUK for counts.
// Icons: gui.IconHome, IconRocket … (AllIcons, 199 Lucide) — ic.Layout(gtx, 20, th.Pal.Text).
//
// STATE. Pressable widgets take a *gui.Clickable you own (embeds
// widget.Clickable + hover/press/focus animation); c.Clicked(gtx) works before
// or after Layout. Model-bound controls keep only interaction state: pass the
// model value every frame and apply Changed(gtx):
//
//	if v, ok := s.format.Changed(gtx); ok { p.Aspect = v }
//	s.format.Layout(gtx, th, p.Aspect, []gui.SegOption{{Value: "16:9", Label: "16:9"}, …})
//
// WIDGETS (struct value = style; pointer = state you keep):
//
//	Button{Kind: Primary|Secondary|Tinted|Ghost|Danger, Size: Md|Sm|Big, Text, Icon, Trail,
//	    Round, Height, Busy, Disabled, Full, Tip}.Layout(gtx, th, &c)   // Text "" = icon button
//	IconButton{Icon, Size, Tip} · Link{Text, Trail} · Chip{Text, Icon, Dot, Selected, Remove: &c2, Small}
//	Card{Padding, Radius, Click: &c, Selected, Tint, Flat}.Layout(gtx, th, body)   // NoPadding
//	Tile{Icon, Label} · NavItem{Icon, Label, Active, Badge} · Badge{Text, Tone, Solid} · Avatar{Name, Image}
//	(*PillTabs).Layout(gtx, th, sel, []Tab{{Label, Icon, Number, Done, Badge}})  // .Full; collapses when narrow
//	(*Segmented).Layout(gtx, th, value, []SegOption) · (*Toggle).Layout(gtx, th, on, "label")
//	(*Slider).Layout(gtx, th, SliderStyle{Value, Min, Max, Step, Format})       // bubble while dragging
//	(*TextField).Layout(gtx, th, FieldStyle{Label, Placeholder, Hint, Error, MaxLen, Counter, Password,
//	    MinLines, MaxLines, Leading, Submit, Pending [a, b) = interim runes, faded}) + Changed / Sync / SetText / Submitted / Focus
//	(*SearchField).Layout(gtx, th, "Пошук") · (*Select).Layout(gtx, th, SelectStyle{Label, Options, Value,
//	    Placeholder, Leading})   // anchored popover, flips up, search when > 8, ↑↓ Enter Esc
//	(*BigPicker).Layout(gtx, th, PickerStyle{Title, Items: []PickerItem, Value}) + Open / Changed
//	(*Dialog).Layout(gtx, th, DialogStyle{Title, Text, Icon, Tone, Body, Actions}) + Open / Close / Dismissed
//	(*Drawer).Layout(gtx, th, DrawerStyle{Title, Subtitle, Body, Footer}) + Open / Close / Toggle
//	th.Toast(gui.ToneSuccess, "Збережено") — any goroutine · Tip: on controls, th.Tooltip(gtx, owner, pressed, s)
//	ProgressBar{Value, Tone, Anim: &a} · ProgressRing{Value, Size, Caption, Anim: &a} · Spinner{Size}
//	Skeleton{Width, Height} · SkeletonLines(gtx, th, n) · EmptyState{Icon, Title, Text, Action}
//	Thumb{Image, Fit, Ratio, Radius, Loading, Overlay} · (*ImageView).Layout(gtx, th, cache, path, ver, Thumb{…})
//	(*AudioPill).Layout(gtx, th, AudioStyle{Src, Label, Duration, Backend})  // Backend wraps player.Audio()
//	StatusPill{State: ParseState(asset.State), Percent} · StatTile{Icon, Label, Value, Caption}
//	NextStepCard{Icon, Title, Text, Busy} · ActivityRow{Icon, Tone, Solid, Title, Meta, Progress, Value, Trailing}
//	TipCard{Icon, Title, Text, Action} · Divider{} · (*Tour).Layout(gtx, th) with Steps anchored by
//	    gui.Anchor(gtx, th, "home.story", w) — Start / Finished
//
// LAYOUT. Grid{MinWidth: 300, Gap: Sp24, Stretch}.Layout(gtx, n, item) —
// responsive card grids; Columns{}.Layout(gtx, Col{Span: 8, W: main}, Col{Span:
// 4, W: side}); Flow{} wraps chips; VStack/HStack(gtx, gap, ws…);
// (*Scroll).Layout / LayoutWidget scroll with a slim fading bar.
//
// MOTION. Anim{Duration, Ease}.Animate(gtx, target), Spring (press),
// ColorAnim, Stagger + FadeUp (list entry), Fade/Offset/Scale, Route (page
// change: 90 ms out, 180 ms in rising 8 dp). All run on gtx.Now and request
// frames only while moving.
//
// SCREENSHOTS. gui.Snapshot(w, h, scale, draw, gui.Frames(n), gui.Settle(0))
// renders headlessly on a fixed clock; gui.SavePNG writes the PNG. Widgets'
// Force (ForceHover|ForcePress|ForceFocus) pins interaction looks.
package gui

import (
	"image/color"
	"sync/atomic"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/text"
	"gioui.org/unit"
)

// Palette holds every colour token. Widgets read them from th.Pal, which
// crossfades while the theme switches; never keep copies across frames.
type Palette struct {
	Ground       color.NRGBA // window background (warm tint)
	Surface      color.NRGBA // tinted fills: tracks, tinted buttons, wells
	SurfaceHover color.NRGBA // hovered tinted fills
	Card         color.NRGBA // cards, sheets, fields
	Border       color.NRGBA // warm hairlines
	BorderStrong color.NRGBA // hovered borders, control outlines
	Text         color.NRGBA // primary text
	Text2        color.NRGBA // secondary text
	Text3        color.NRGBA // tertiary text (≥ 4.5:1 on Card and Surface)

	Action        color.NRGBA // the one bright colour — only on pressable things
	ActionHover   color.NRGBA // hovered Action: a touch brighter (the ink stays ≥ 4.5:1)
	ActionPressed color.NRGBA // pressed Action: a touch deeper, still ≥ 4.5:1 with the ink
	ActionSoft    color.NRGBA // tint behind action icons, active nav pill
	ActionSoftHi  color.NRGBA // hovered ActionSoft
	ActionText    color.NRGBA // action-coloured text (links), ≥ 4.5:1 on Card
	// OnAction is the ink for every label and icon on an Action fill (primary
	// buttons, the next-step arrow, the round "+", action badges …): ≥ 4.5:1
	// on Action, ActionHover and ActionPressed.
	OnAction color.NRGBA
	Focus    color.NRGBA // focus ring (Action at 40 %)

	Success, SuccessSoft, SuccessInk color.NRGBA // done
	Running, RunningSoft, RunningInk color.NRGBA // generating
	Warning, WarningSoft, WarningInk color.NRGBA // stale / changed
	Error, ErrorSoft, ErrorInk       color.NRGBA // failed
	// ErrorFill fills destructive buttons (white text on it passes 4.5:1).
	ErrorFill color.NRGBA

	Scrim      color.NRGBA // behind dialogs, drawers and the tour
	Shadow     color.NRGBA // shadow tint (alpha comes from the elevation)
	TooltipBg  color.NRGBA
	TooltipFg  color.NRGBA
	Selection  color.NRGBA // selected text in fields
	Shimmer    color.NRGBA // skeleton highlight band
	Track      color.NRGBA // progress tracks, slider rails
	Overlay    color.NRGBA // translucent chrome over images (play buttons)
	OverlayInk color.NRGBA // icons on Overlay
}

// LightPalette is the default warm light theme.
//
// Contrast notes (WCAG 2.x): GUI.md's text-3 #8E8177 measured 3.78:1 on
// Card and 3.22:1 on Surface, so it is darkened to #736960 (5.36:1 / 4.56:1).
// Action-coloured text uses ActionText #AF492A (5.4:1 on Card, 4.5:1 on
// ActionSoft) because #FF6A3D itself is 2.85:1 on white. White text on the
// orange was 2.85:1, so labels and icons on Action fills use the ink #2B241F
// (5.37:1); hover brightens to #FF7B52 (5.97:1) and press deepens only to
// #F9602F (4.90:1) — a darker press would drop below 4.5:1 with the ink.
// Status "Ink" tones are the status hues darkened to ≥ 4.5:1 on their Soft
// fill; ErrorFill #CF2F46 carries white text at 5.06:1.
var LightPalette = Palette{
	Ground:       rgb(0xFFF4E6),
	Surface:      rgb(0xF7EBDD),
	SurfaceHover: rgb(0xF0E1D0),
	Card:         rgb(0xFFFFFF),
	Border:       rgb(0xE7DACB),
	BorderStrong: rgb(0xD9C8B4),
	Text:         rgb(0x2B241F),
	Text2:        rgb(0x6F625A),
	Text3:        rgb(0x736960),

	Action:        rgb(0xFF6A3D),
	ActionHover:   rgb(0xFF7B52),
	ActionPressed: rgb(0xF9602F),
	ActionSoft:    rgb(0xFFE3D6),
	ActionSoftHi:  rgb(0xFFD5C2),
	ActionText:    rgb(0xAF492A),
	OnAction:      rgb(0x2B241F),
	Focus:         argb(0x66FF6A3D),

	Success: rgb(0x1F9D55), SuccessSoft: rgb(0xDDF3E6), SuccessInk: rgb(0x187B43),
	Running: rgb(0x7A5AF8), RunningSoft: rgb(0xECE7FF), RunningInk: rgb(0x6D50DE),
	Warning: rgb(0xC98A00), WarningSoft: rgb(0xFCF1D2), WarningInk: rgb(0x906300),
	Error: rgb(0xD6334A), ErrorSoft: rgb(0xFBE1E5), ErrorInk: rgb(0xC22E43),
	ErrorFill: rgb(0xCF2F46),

	Scrim:      argb(0x6B2B241F),
	Shadow:     rgb(0x4A2E1C),
	TooltipBg:  rgb(0x2B241F),
	TooltipFg:  rgb(0xFFF8F1),
	Selection:  argb(0x66FF9A70),
	Shimmer:    rgb(0xFFFCF8),
	Track:      rgb(0xF1E4D5),
	Overlay:    argb(0xE6FFFFFF),
	OverlayInk: rgb(0x2B241F),
}

// DarkPalette is "Вечір": the same grammar on a dark warm ground. Status
// colours are brightened so their text passes 4.5:1 on the dark soft fills;
// text-3 is #9E9083 (4.85:1 on Card, 4.5:1 on hovered surfaces). On the
// orange the ink #1B1714 reads 6.91:1 (hover #FF8B62 7.73:1, pressed #F0683A
// 5.72:1); ErrorFill #C7364B carries white at 5.18:1.
var DarkPalette = Palette{
	Ground:       rgb(0x1B1714),
	Surface:      rgb(0x26201B),
	SurfaceHover: rgb(0x322A24),
	Card:         rgb(0x2C2520),
	Border:       rgb(0x3D342D),
	BorderStrong: rgb(0x52463D),
	Text:         rgb(0xF6EEE6),
	Text2:        rgb(0xC2B4A6),
	Text3:        rgb(0x9E9083),

	Action:        rgb(0xFF7A4D),
	ActionHover:   rgb(0xFF8B62),
	ActionPressed: rgb(0xF0683A),
	ActionSoft:    rgb(0x4A2C20),
	ActionSoftHi:  rgb(0x5A3527),
	ActionText:    rgb(0xFF8A61),
	OnAction:      rgb(0x1B1714),
	Focus:         argb(0x73FF7A4D),

	Success: rgb(0x4CC784), SuccessSoft: rgb(0x1E3A2A), SuccessInk: rgb(0x5FD394),
	Running: rgb(0xA08CFF), RunningSoft: rgb(0x2E2750), RunningInk: rgb(0xB3A3FF),
	Warning: rgb(0xE8B03A), WarningSoft: rgb(0x3D3118), WarningInk: rgb(0xF0BF55),
	Error: rgb(0xFF6B7F), ErrorSoft: rgb(0x45232A), ErrorInk: rgb(0xFF8292),
	ErrorFill: rgb(0xC7364B),

	Scrim:      argb(0xA6000000),
	Shadow:     rgb(0x000000),
	TooltipBg:  rgb(0xF6EEE6),
	TooltipFg:  rgb(0x1B1714),
	Selection:  argb(0x66FF7A4D),
	Shimmer:    rgb(0x3E342C),
	Track:      rgb(0x3A3029),
	Overlay:    argb(0xD92C2520),
	OverlayInk: rgb(0xF6EEE6),
}

// fields lists pointers to every colour of p (crossfades iterate them).
func (p *Palette) fields() []*color.NRGBA {
	return []*color.NRGBA{
		&p.Ground, &p.Surface, &p.SurfaceHover, &p.Card, &p.Border, &p.BorderStrong,
		&p.Text, &p.Text2, &p.Text3,
		&p.Action, &p.ActionHover, &p.ActionPressed, &p.ActionSoft, &p.ActionSoftHi,
		&p.ActionText, &p.OnAction, &p.Focus,
		&p.Success, &p.SuccessSoft, &p.SuccessInk,
		&p.Running, &p.RunningSoft, &p.RunningInk,
		&p.Warning, &p.WarningSoft, &p.WarningInk,
		&p.Error, &p.ErrorSoft, &p.ErrorInk, &p.ErrorFill,
		&p.Scrim, &p.Shadow, &p.TooltipBg, &p.TooltipFg, &p.Selection, &p.Shimmer,
		&p.Track, &p.Overlay, &p.OverlayInk,
	}
}

// LerpPalette blends a towards b by t in [0,1].
func LerpPalette(a, b Palette, t float32) Palette {
	out := a
	fa, fb, fo := a.fields(), b.fields(), out.fields()
	for i := range fo {
		*fo[i] = LerpColor(*fa[i], *fb[i], t)
	}
	return out
}

// Tone names a semantic colour family for badges, pills, toasts and icons.
type Tone uint8

const (
	ToneNeutral Tone = iota
	ToneAction
	ToneSuccess
	ToneRunning
	ToneWarning
	ToneError
	// ToneInfo is the neutral tone used for informational toasts.
	ToneInfo = ToneNeutral
)

// ToneColors returns (strong, soft fill, readable ink) for tone t.
func (p *Palette) ToneColors(t Tone) (strong, soft, ink color.NRGBA) {
	switch t {
	case ToneAction:
		return p.Action, p.ActionSoft, p.ActionText
	case ToneSuccess:
		return p.Success, p.SuccessSoft, p.SuccessInk
	case ToneRunning:
		return p.Running, p.RunningSoft, p.RunningInk
	case ToneWarning:
		return p.Warning, p.WarningSoft, p.WarningInk
	case ToneError:
		return p.Error, p.ErrorSoft, p.ErrorInk
	default:
		return p.Text2, p.Surface, p.Text2
	}
}

// Spacing scale (GUI.md: 4/8/12/16/20/24/32/40/56).
const (
	Sp4  unit.Dp = 4
	Sp8  unit.Dp = 8
	Sp12 unit.Dp = 12
	Sp16 unit.Dp = 16
	Sp20 unit.Dp = 20
	Sp24 unit.Dp = 24
	Sp32 unit.Dp = 32
	Sp40 unit.Dp = 40
	Sp56 unit.Dp = 56
)

// Corner radii.
const (
	RadiusCard    unit.Dp = 22  // cards, sheets, dialogs
	RadiusInner   unit.Dp = 16  // cards inside cards, tiles, thumbs, popovers
	RadiusControl unit.Dp = 12  // buttons, fields, selects
	RadiusSmall   unit.Dp = 8   // tooltips, small tags
	RadiusPill    unit.Dp = 999 // chips, pills, tabs
)

// Control heights.
const (
	HeightSm  unit.Dp = 36
	HeightMd  unit.Dp = 44
	HeightBig unit.Dp = 52
)

// Motion durations (GUI.md "Motion").
const (
	DurHover   = 120 * time.Millisecond
	DurPress   = 220 * time.Millisecond
	DurTabs    = 200 * time.Millisecond
	DurSheet   = 240 * time.Millisecond
	DurDialog  = 180 * time.Millisecond
	DurToast   = 220 * time.Millisecond
	DurValue   = 300 * time.Millisecond // progress bars and rings
	DurTheme   = 250 * time.Millisecond
	DurRoute   = 180 * time.Millisecond
	DurFade    = 90 * time.Millisecond
	DurShimmer = 1400 * time.Millisecond
	TipDelay   = 400 * time.Millisecond
)

// Mode selects the colour theme.
type Mode uint8

const (
	ModeLight  Mode = iota // "Світла"
	ModeDark               // "Темна" (Вечір)
	ModeSystem             // "Як у системі": Windows app theme, light elsewhere
)

// Theme carries the shaper, the current palette, the type scale and the
// window-wide services (overlay layers, toasts, tooltips, anchors, tour).
// One Theme per window; use it from the UI goroutine only (Toast is safe
// from any goroutine).
type Theme struct {
	Typography
	Shaper *text.Shaper
	// Pal is the palette of the current frame (crossfading during a theme
	// switch). Read-only for widgets.
	Pal Palette
	// InkBoost paints dark text twice to offset Gio's linear-light blending,
	// which otherwise renders dark glyphs thinner than the design intends.
	InkBoost bool

	mode      Mode
	dark      bool
	fadeFrom  Palette
	fadeStart time.Time // zero while a switch waits for its first frame
	fading    bool
	fadeT     float32 // eased crossfade progress

	svc services
}

var reducedMotion atomic.Bool

// SetReducedMotion turns every animation of the kit instant ("Менше анімацій").
func SetReducedMotion(on bool) { reducedMotion.Store(on) }

// ReducedMotion reports the reduced-motion setting.
func ReducedMotion() bool { return reducedMotion.Load() }

// NewTheme builds a light theme with the embedded fonts.
func NewTheme() (*Theme, error) {
	sh, err := NewShaper()
	if err != nil {
		return nil, err
	}
	th := &Theme{Typography: DefaultTypography(), Shaper: sh, Pal: LightPalette, InkBoost: true}
	th.svc.init()
	return th, nil
}

// MustTheme is NewTheme that panics; the fonts are embedded, so it only fails
// on a broken binary.
func MustTheme() *Theme {
	th, err := NewTheme()
	if err != nil {
		panic(err)
	}
	return th
}

// Mode returns the selected mode (ModeSystem stays ModeSystem).
func (th *Theme) Mode() Mode { return th.mode }

// Dark reports whether the (target) palette is the dark one.
func (th *Theme) Dark() bool { return th.dark }

// SetMode switches the theme. The palette crossfades over 250 ms starting
// with the next frame (instantly with reduced motion). ModeSystem follows the
// Windows app theme (registry AppsUseLightTheme) and falls back to light.
func (th *Theme) SetMode(m Mode) {
	th.mode = m
	dark := m == ModeDark || (m == ModeSystem && SystemDark())
	if dark == th.dark && !th.fading {
		return
	}
	th.dark = dark
	th.fadeFrom = th.Pal
	th.fadeStart = time.Time{}
	th.fading = true
}

// SetDark is SetMode(ModeDark) or SetMode(ModeLight).
func (th *Theme) SetDark(dark bool) {
	if dark {
		th.SetMode(ModeDark)
	} else {
		th.SetMode(ModeLight)
	}
}

// SetPaletteNow switches to the target palette without a crossfade
// (screenshots, first frame).
func (th *Theme) SetPaletteNow(dark bool) {
	th.dark = dark
	th.fading = false
	th.Pal = th.target()
	if dark {
		th.mode = ModeDark
	} else {
		th.mode = ModeLight
	}
}

func (th *Theme) target() Palette {
	if th.dark {
		return DarkPalette
	}
	return LightPalette
}

// advancePalette moves the crossfade to gtx.Now (called by Root).
func (th *Theme) advancePalette(gtx layout.Context) {
	if !th.fading {
		return
	}
	if ReducedMotion() {
		th.Pal, th.fading = th.target(), false
		return
	}
	if th.fadeStart.IsZero() {
		th.fadeStart = gtx.Now
	}
	t := float32(gtx.Now.Sub(th.fadeStart)) / float32(DurTheme)
	if t >= 1 {
		th.Pal, th.fading = th.target(), false
		return
	}
	th.fadeT = EaseInOutCubic(max(0, t))
	th.Pal = LerpPalette(th.fadeFrom, th.target(), th.fadeT)
	gtx.Execute(op.InvalidateCmd{})
}

// Colour helpers.

func rgb(c uint32) color.NRGBA {
	return color.NRGBA{R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c), A: 0xFF}
}

func argb(c uint32) color.NRGBA {
	return color.NRGBA{A: uint8(c >> 24), R: uint8(c >> 16), G: uint8(c >> 8), B: uint8(c)}
}

// WithAlpha returns c with its alpha multiplied by f (0…1).
func WithAlpha(c color.NRGBA, f float32) color.NRGBA {
	c.A = uint8(min(255, max(0, float32(c.A)*f+0.5)))
	return c
}

// LerpColor blends a towards b by t (straight sRGB lerp, like CSS transitions).
func LerpColor(a, b color.NRGBA, t float32) color.NRGBA {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	l := func(x, y uint8) uint8 { return uint8(float32(x) + (float32(y)-float32(x))*t + 0.5) }
	return color.NRGBA{R: l(a.R, b.R), G: l(a.G, b.G), B: l(a.B, b.B), A: l(a.A, b.A)}
}

// Over composites fg (with its alpha) over the opaque bg in sRGB space, the
// way CSS does, and returns an opaque colour. Use it for tints whose
// background is known: Gio itself blends in linear light, which makes
// translucent tints look lighter than the design.
func Over(bg, fg color.NRGBA) color.NRGBA {
	a := float32(fg.A) / 255
	l := func(b, f uint8) uint8 { return uint8(float32(b)*(1-a) + float32(f)*a + 0.5) }
	return color.NRGBA{R: l(bg.R, fg.R), G: l(bg.G, fg.G), B: l(bg.B, fg.B), A: 255}
}

// luma is the perceived lightness of c in [0,1] (Rec. 709 weights on sRGB).
func luma(c color.NRGBA) float32 {
	return (0.2126*float32(c.R) + 0.7152*float32(c.G) + 0.0722*float32(c.B)) / 255
}

// shadowPalette is the palette shadows are built from. Shadow textures are
// cached by colour, so during a crossfade they switch once, half-way, instead
// of being rebuilt every frame.
func (th *Theme) shadowPalette() (Palette, bool) {
	if th.fading && th.fadeT < 0.5 {
		return th.fadeFrom, !th.dark
	}
	return th.target(), th.dark
}

// OnColor returns white or near-black, whichever reads better on bg (text and
// icons on solid status fills: bright in the dark theme, deep in the light).
func OnColor(bg color.NRGBA) color.NRGBA {
	white, dark := rgb(0xFFFFFF), rgb(0x1B1714)
	if contrastRatio(white, bg) >= contrastRatio(dark, bg) {
		return white
	}
	return dark
}

func contrastRatio(a, b color.NRGBA) float32 {
	lum := func(c color.NRGBA) float32 {
		return 0.2126*srgbToLinear(float32(c.R)/255) + 0.7152*srgbToLinear(float32(c.G)/255) + 0.0722*srgbToLinear(float32(c.B)/255)
	}
	la, lb := lum(a), lum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}
