package media

// Catalogs of the montage building blocks offered in the UI: transitions
// between scenes, motion effects, colour looks and subtitle presets.
//
// The IDs are stored in projects — never rename or reuse them. Labels are the
// Ukrainian UI text; Hint is a short Ukrainian description for tooltips.

import "strings"

// ---- transitions ----------------------------------------------------------------------

// TransitionKind says how a transition is rendered.
type TransitionKind string

const (
	// KindCut is a hard cut.
	KindCut TransitionKind = "cut"
	// KindDip fades the scene out to a colour and the next one in from it; the
	// scenes do not overlap (fast path: segments are simply concatenated).
	KindDip TransitionKind = "dip"
	// KindXFade overlaps the two scenes for the transition duration and blends
	// them with an ffmpeg xfade pattern.
	KindXFade TransitionKind = "xfade"
)

// Transition is one way to go from a scene to the next.
type Transition struct {
	ID    string
	Label string // Ukrainian UI label
	Hint  string // short Ukrainian description
	Kind  TransitionKind
	XFade string // ffmpeg xfade transition name (KindXFade)
	Color string // dip colour, "black" or "white" (KindDip)
}

// Overlaps reports whether the two scenes share screen time (KindXFade): the
// film gets shorter by the transition duration at every such cut.
func (t Transition) Overlaps() bool { return t.Kind == KindXFade }

const (
	// DefaultTransition is the transition of a new project (dip to black).
	DefaultTransition = "fade"
	// DefaultTransitionDuration in seconds.
	DefaultTransitionDuration = 0.5
	// MinTransitionDuration / MaxTransitionDuration bound the duration setting.
	MinTransitionDuration = 0.2
	MaxTransitionDuration = 2.0
)

// Transitions is the transition catalog (UI order).
var Transitions = []Transition{
	{ID: "none", Label: "Без переходу", Hint: "Різка склейка", Kind: KindCut},
	{ID: "fade", Label: "Затемнення", Hint: "Сцена гасне в чорне, наступна проявляється", Kind: KindDip, Color: "black"},
	{ID: "fadewhite", Label: "Висвітлення", Hint: "Перехід через біле, як спалах", Kind: KindDip, Color: "white"},
	{ID: "dissolve", Label: "Розчинення", Hint: "Сцени плавно перетікають одна в одну", Kind: KindXFade, XFade: "fade"},
	{ID: "wipeleft", Label: "Шторка вліво", Hint: "Нова сцена відкривається справа наліво", Kind: KindXFade, XFade: "wipeleft"},
	{ID: "wiperight", Label: "Шторка вправо", Hint: "Нова сцена відкривається зліва направо", Kind: KindXFade, XFade: "wiperight"},
	{ID: "wipeup", Label: "Шторка вгору", Hint: "Нова сцена відкривається знизу вгору", Kind: KindXFade, XFade: "wipeup"},
	{ID: "wipedown", Label: "Шторка вниз", Hint: "Нова сцена відкривається згори вниз", Kind: KindXFade, XFade: "wipedown"},
	{ID: "slideleft", Label: "Зсув уліво", Hint: "Нова сцена виштовхує стару вліво", Kind: KindXFade, XFade: "slideleft"},
	{ID: "slideright", Label: "Зсув управо", Hint: "Нова сцена виштовхує стару вправо", Kind: KindXFade, XFade: "slideright"},
	{ID: "slideup", Label: "Зсув угору", Hint: "Нова сцена виштовхує стару вгору", Kind: KindXFade, XFade: "slideup"},
	{ID: "slidedown", Label: "Зсув униз", Hint: "Нова сцена виштовхує стару вниз", Kind: KindXFade, XFade: "slidedown"},
	{ID: "smoothleft", Label: "М'яко вліво", Hint: "Шторка з розмитим краєм справа наліво", Kind: KindXFade, XFade: "smoothleft"},
	{ID: "smoothright", Label: "М'яко вправо", Hint: "Шторка з розмитим краєм зліва направо", Kind: KindXFade, XFade: "smoothright"},
	{ID: "circleopen", Label: "Коло", Hint: "Нова сцена розкривається колом з центру", Kind: KindXFade, XFade: "circleopen"},
	{ID: "circleclose", Label: "Коло до центру", Hint: "Стара сцена стискається колом у центр", Kind: KindXFade, XFade: "circleclose"},
	{ID: "radial", Label: "Радар", Hint: "Нова сцена з'являється за стрілкою годинника", Kind: KindXFade, XFade: "radial"},
	{ID: "pixelize", Label: "Пікселі", Hint: "Кадр розсипається на пікселі й збирається знову", Kind: KindXFade, XFade: "pixelize"},
	{ID: "zoomin", Label: "Наближення", Hint: "Стара сцена наближається й розчиняється", Kind: KindXFade, XFade: "zoomin"},
	{ID: "hblur", Label: "Розмиття", Hint: "Перехід через горизонтальне розмиття", Kind: KindXFade, XFade: "hblur"},
	{ID: "squeezeh", Label: "Стиснення", Hint: "Стара сцена стискається по горизонталі", Kind: KindXFade, XFade: "squeezeh"},
	{ID: "squeezev", Label: "Сплющення", Hint: "Стара сцена стискається по вертикалі", Kind: KindXFade, XFade: "squeezev"},
	{ID: "distance", Label: "Перетікання", Hint: "Кольори перетікають від старої сцени до нової", Kind: KindXFade, XFade: "distance"},
}

// TransitionByID finds a transition ("" or unknown → false).
func TransitionByID(id string) (Transition, bool) {
	for _, t := range Transitions {
		if t.ID == id {
			return t, true
		}
	}
	return Transition{}, false
}

// ---- motion effects -----------------------------------------------------------------------

// Effect is a camera move over a still frame (Ken Burns) — also available,
// at half strength, for clips when chosen explicitly for a scene.
type Effect struct {
	ID    string
	Label string // Ukrainian UI label
	Hint  string // short Ukrainian description
}

// DefaultEffect is the motion used for stills when nothing is chosen.
const DefaultEffect = "zoomin"

// Effects is the motion effect catalog (UI order).
var Effects = []Effect{
	{ID: "none", Label: "Без руху", Hint: "Нерухомий кадр"},
	{ID: "zoomin", Label: "Наближення", Hint: "Камера повільно наближається"},
	{ID: "zoomout", Label: "Віддалення", Hint: "Камера повільно віддаляється"},
	{ID: "panleft", Label: "Панорама вліво", Hint: "Камера пливе вліво"},
	{ID: "panright", Label: "Панорама вправо", Hint: "Камера пливе вправо"},
	{ID: "panup", Label: "Панорама вгору", Hint: "Камера піднімається"},
	{ID: "pandown", Label: "Панорама вниз", Hint: "Камера опускається"},
	{ID: "diagonal", Label: "Кен Бернс", Hint: "Наближення з рухом по діагоналі"},
	{ID: "pushin", Label: "Наїзд", Hint: "Швидкий драматичний наїзд"},
	{ID: "drift", Label: "Дрейф", Hint: "Ледь помітний повільний рух"},
	{ID: "shake", Label: "Ручна камера", Hint: "Легке живе тремтіння"},
	{ID: "rotate", Label: "Нахил", Hint: "Повільний легкий поворот кадру"},
}

// EffectByID finds an effect ("" or unknown → false).
func EffectByID(id string) (Effect, bool) {
	for _, e := range Effects {
		if e.ID == id {
			return e, true
		}
	}
	return Effect{}, false
}

// ---- colour looks -------------------------------------------------------------------------

// Filter is a colour look applied to every scene of the film.
type Filter struct {
	ID    string
	Label string // Ukrainian UI label
	Hint  string // short Ukrainian description
	// chain is the ffmpeg filter chain; "" = unchanged. It may contain one
	// split/blend branch written with the placeholders {a} {b} {c} (see look()).
	chain string
}

// Filters is the colour look catalog (UI order). Every chain was tuned on
// real frames (illustrated and photographic) to keep skin tones believable.
var Filters = []Filter{
	{ID: "none", Label: "Без фільтра", Hint: "Оригінальні кольори"},
	{ID: "cinematic", Label: "Кіно", Hint: "Бірюзові тіні й теплі обличчя, як у блокбастері",
		chain: "selectivecolor=reds='-0.10 0.02 0.10 0':yellows='-0.08 0.04 0.06 0':greens='0.10 0 -0.05 0':neutrals='0.06 -0.01 -0.05 0':blacks='0.10 -0.02 -0.06 0',eq=contrast=1.08:saturation=1.10"},
	{ID: "warm", Label: "Тепло", Hint: "Затишні теплі тони",
		chain: "colortemperature=temperature=5200:pl=1,eq=saturation=1.05"},
	{ID: "cool", Label: "Холод", Hint: "Прохолодні сині тони",
		chain: "colortemperature=temperature=9000:mix=0.9:pl=1"},
	{ID: "vintage", Label: "Вінтаж", Hint: "Вицвіла стара плівка",
		chain: "curves=r='0/0.10 0.5/0.55 1/0.95':g='0/0.07 0.5/0.50 1/0.90':b='0/0.14 0.5/0.42 1/0.76',eq=saturation=0.78:contrast=0.98,noise=c0s=5:c0f=t+u,vignette=a=PI/5"},
	{ID: "noir", Label: "Нуар", Hint: "Контрастне чорно-біле",
		chain: "colorchannelmixer=.3:.45:.25:0:.3:.45:.25:0:.3:.45:.25,eq=contrast=1.35:brightness=-0.02,vignette=a=PI/4.5"},
	{ID: "sepia", Label: "Сепія", Hint: "Коричневе старе фото",
		chain: "colorchannelmixer=.32:.62:.15:0:.28:.56:.13:0:.22:.43:.10,eq=contrast=1.05"},
	{ID: "faded", Label: "Матова плівка", Hint: "Підняті тіні й приглушені кольори",
		chain: "curves=all='0/0.10 0.5/0.52 1/0.90',eq=saturation=0.78"},
	{ID: "contrast", Label: "Контраст", Hint: "Глибокі тіні, яскраві світла",
		chain: "curves=preset=increase_contrast,eq=saturation=1.06"},
	{ID: "vivid", Label: "Яскраво", Hint: "Соковиті насичені кольори",
		chain: "vibrance=intensity=0.35,eq=saturation=1.18:contrast=1.04"},
	{ID: "dreamy", Label: "Мрія", Hint: "М'яке сяйво, казковий серпанок",
		chain: "format=gbrp,split{a}{b};{b}gblur=sigma=16{c};{a}{c}blend=all_mode=screen:all_opacity=0.38,eq=contrast=0.95:saturation=0.95"},
	{ID: "moonlight", Label: "Місячне світло", Hint: "Синя ніч",
		chain: "colortemperature=temperature=11000,eq=brightness=-0.06:saturation=0.55:gamma=0.9,colorbalance=bs=0.08:bm=0.05"},
	{ID: "golden", Label: "Золота година", Hint: "Тепле вечірнє сонце",
		chain: "colortemperature=temperature=3800:mix=0.7,colorbalance=rh=0.06:gh=0.02:bh=-0.06,eq=saturation=1.12:contrast=1.04"},
	{ID: "grain", Label: "Плівкове зерно", Hint: "Живе зерно кіноплівки",
		chain: "noise=c0s=14:c0f=t+u,eq=contrast=1.04"},
	{ID: "vignette", Label: "Віньєтка", Hint: "Затемнені краї кадру",
		chain: "vignette=a=PI/4"},
}

// FilterByID finds a colour look ("" or unknown → false).
func FilterByID(id string) (Filter, bool) {
	for _, f := range Filters {
		if f.ID == id {
			return f, true
		}
	}
	return Filter{}, false
}

// look returns the look's filter graph from label in to label out, or "" when
// the look changes nothing. tag makes the inner labels unique in the graph.
func (f Filter) look(in, out, tag string) string {
	if f.chain == "" {
		return ""
	}
	r := strings.NewReplacer("{a}", "["+tag+"a]", "{b}", "["+tag+"b]", "{c}", "["+tag+"c]")
	return in + r.Replace(f.chain) + out
}

// ---- subtitle presets -----------------------------------------------------------------------

// SubtitlePreset is a ready-made look for burnt-in subtitles. Sizes are
// pixels on a 1080p frame (the short side = 1080); they scale with the video.
type SubtitlePreset struct {
	ID    string
	Label string // Ukrainian UI label
	Hint  string // short Ukrainian description

	Size, SizeVertical int    // font size for 16:9 / 9:16
	Position           string // "bottom" | "middle" | "top"
	Color              string // text colour #RRGGBB
	Bold               bool
	Uppercase          bool
	// Outline (px) around the letters in OutlineColor; 0 = none.
	Outline      float64
	OutlineColor string
	// Shadow (px) in black, ShadowAlpha 0 (opaque) … 1 (invisible).
	Shadow      float64
	ShadowAlpha float64
	// Box draws a plate behind every line in BoxColor with BoxAlpha
	// transparency (0 opaque … 1 invisible) and BoxPad px of padding.
	Box      bool
	BoxColor string
	BoxAlpha float64
	BoxPad   float64
	// MaxWords > 0 cuts the subtitles into phrases of 1…MaxWords words that
	// change in step with the speech (0 = cues of up to two lines).
	MaxWords int
}

// DefaultSubtitlePreset is used when SubtitleStyle.Preset is empty/unknown.
const DefaultSubtitlePreset = "classic"

// SubtitlePresets is the subtitle look catalog (UI order).
var SubtitlePresets = []SubtitlePreset{
	{ID: "classic", Label: "Класичні білі", Hint: "Білі літери з чорною обводкою",
		Size: 64, SizeVertical: 72, Position: "bottom", Color: "#FFFFFF", Bold: true,
		Outline: 3.5, OutlineColor: "#000000", Shadow: 1.5, ShadowAlpha: 0.5},
	{ID: "youtube", Label: "Жовті YouTube", Hint: "Яскраво-жовті з обводкою — як у блогерів",
		Size: 66, SizeVertical: 76, Position: "bottom", Color: "#FFE000", Bold: true,
		Outline: 4, OutlineColor: "#000000", Shadow: 2, ShadowAlpha: 0.4},
	{ID: "plate", Label: "Біла плашка", Hint: "Темний текст на білій плашці",
		Size: 56, SizeVertical: 64, Position: "bottom", Color: "#141414", Bold: true,
		Box: true, BoxColor: "#FFFFFF", BoxAlpha: 0.05, BoxPad: 12},
	{ID: "darkbox", Label: "Темна плашка", Hint: "Білий текст на напівпрозорій чорній плашці",
		Size: 56, SizeVertical: 64, Position: "bottom", Color: "#FFFFFF", Bold: false,
		Box: true, BoxColor: "#000000", BoxAlpha: 0.4, BoxPad: 12},
	{ID: "shorts", Label: "Великі по центру", Hint: "Великі капсом посередині кадру — для Shorts і Reels",
		Size: 84, SizeVertical: 92, Position: "middle", Color: "#FFFFFF", Bold: true, Uppercase: true,
		Outline: 6, OutlineColor: "#000000", Shadow: 3, ShadowAlpha: 0.3},
	{ID: "minimal", Label: "Мінімал", Hint: "Тонкий акуратний текст з м'якою тінню",
		Size: 50, SizeVertical: 58, Position: "bottom", Color: "#FFFFFF",
		Outline: 1.2, OutlineColor: "#000000", Shadow: 2.5, ShadowAlpha: 0.45},
	{ID: "karaoke", Label: "Караоке", Hint: "Білі літери з товстою синьою обводкою",
		Size: 70, SizeVertical: 80, Position: "bottom", Color: "#FFFFFF", Bold: true,
		Outline: 5, OutlineColor: "#1E5BFF", Shadow: 3, ShadowAlpha: 0.2},
	{ID: "cinema", Label: "Кінотеатр", Hint: "Теплий кремовий текст, як у фільмах",
		Size: 58, SizeVertical: 66, Position: "bottom", Color: "#F6E7B8",
		Outline: 2, OutlineColor: "#000000", Shadow: 2, ShadowAlpha: 0.35},
	{ID: "phrase", Label: "Фрази по 1–4 слова", Hint: "Невеликі білі слова з м'якою тінню над нижньою зоною — змінюються в такт мові, як у Shorts",
		Size: 46, SizeVertical: 46, Position: "bottom", Color: "#FFFFFF", Bold: true,
		Outline: 0.8, OutlineColor: "#000000", Shadow: 3, ShadowAlpha: 0.3, MaxWords: 4},
}

// SubtitlePresetByID finds a subtitle preset ("" or unknown → false).
func SubtitlePresetByID(id string) (SubtitlePreset, bool) {
	for _, p := range SubtitlePresets {
		if p.ID == id {
			return p, true
		}
	}
	return SubtitlePreset{}, false
}
