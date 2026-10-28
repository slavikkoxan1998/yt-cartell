package produce

// Step 4 — Відео й озвучка: a header card with the summary tiles (clips,
// voices, film length, errors), the model/cost line and the bulk actions
// (the running job is the workspace banner's); then every scene as a card:
// the clip player (poster = the frame), separate video/voice states, the
// narration, the voice pill (TTS or a slice of the user's own recording),
// the clip-vs-voice length hint, the Omni Flash chain hint, a motion-prompt
// editor and retries.

import (
	"fmt"
	"image/color"
	"strings"
	"sync"
	"time"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/config"
	"ytcartell/internal/glabs"
	"ytcartell/internal/gui"
	"ytcartell/internal/providers"
	"ytcartell/internal/studio"
	"ytcartell/internal/tts"
)

type render struct {
	projects map[string]*rdState
}

func newRender() *render { return &render{projects: map[string]*rdState{}} }

type rdState struct {
	list     pageList
	stagger  gui.Stagger
	hl       highlight
	cards    map[string]*clipCard
	head     rdHead
	batch    batch
	view     gui.Segmented
	toFrames gui.Clickable
	toPlan   gui.Clickable
	side     sideState
	tl       timeline
}

type rdHead struct {
	videos, voices, all, stale, again gui.Clickable
	settings                          gui.Clickable
	job                               jobBox // the "re-shoot changed" batch
}

type clipCard struct {
	video     core.Video
	voice     gui.AudioPill
	veil      veil
	shoot     gui.Clickable
	revoice   gui.Clickable
	motion    gui.Clickable
	open      gui.Clickable
	reveal    gui.Clickable
	redo      gui.Clickable
	collapse  gui.Clickable
	textClick widget.Clickable
	editing   bool
	focus     bool
	field     gui.TextField
	exp       expander
	rw        rewriteNote
}

func (v *render) state(id string) *rdState {
	st := v.projects[id]
	if st == nil {
		st = &rdState{cards: map[string]*clipCard{}}
		st.stagger.Max = 12
		v.projects[id] = st
	}
	return st
}

func (st *rdState) card(id string) *clipCard {
	cc := st.cards[id]
	if cc == nil {
		cc = &clipCard{}
		st.cards[id] = cc
	}
	return cc
}

// ---- counts ---------------------------------------------------------------------------

type clipStats struct {
	scenes, withFrame, noFrames                 int
	vidDone, vidBusy, vidErr, vidStale, vidLeft int
	vo                                          bool
	voiceable, voiceDone, voiceBusy, voiceErr   int
	voiceStale, voiceLeft                       int
	redo                                        int // scenes with a changed clip or line
	// stills: «Тільки картинки» — no clips are shot (the montage animates the frames)
	stills bool
	// code: «Анімація кодом» — the clips are drawn by code here (no frames needed)
	code bool
}

func isStockMode(cfg config.Config, p *studio.Project) bool {
	if p != nil && p.Input.Pipeline == "stock" {
		return true
	}
	return cfg.Stock.Enabled || cfg.Video.Provider == "stock"
}

func countClips(p *studio.Project, isStock ...bool) clipStats {
	stock := len(isStock) > 0 && isStock[0]
	n := clipStats{scenes: len(p.Scenes), vo: p.Input.Voiceover, stills: p.Input.StillsOnly(), code: p.Input.CodeAnim()}
	for i := range p.Scenes {
		s := &p.Scenes[i]
		if s.Frame.Path != "" || (n.code && !s.Code.Fallback) || stock {
			n.withFrame++
		}
		switch {
		case busy(s.Video):
			n.vidBusy++
		case s.Video.Status == studio.StatusDone:
			n.vidDone++
		case s.Video.Status == studio.StatusError:
			n.vidErr++
		}
		if (s.Frame.Path != "" || stock) && s.Video.Status != studio.StatusDone && !busy(s.Video) && !n.stills && !n.code {
			n.vidLeft++
		}
		if n.code && !s.Code.Fallback && (s.Video.Status != studio.StatusDone || s.Video.Stale) && !busy(s.Video) {
			n.vidLeft++ // an animation to render (a changed one renders again by itself)
		}
		if clipStale(s) && !n.stills {
			n.vidStale++
		}
		if spoken(s) {
			n.voiceable++
			switch {
			case busy(s.Audio):
				n.voiceBusy++
			case s.Audio.Status == studio.StatusDone:
				n.voiceDone++
			case s.Audio.Status == studio.StatusError:
				n.voiceErr++
			}
			if n.vo && s.Audio.Status != studio.StatusDone && !busy(s.Audio) {
				n.voiceLeft++
			}
		}
		if lineStale(p, s) {
			n.voiceStale++
		}
		if (clipStale(s) && !n.stills) || lineStale(p, s) {
			n.redo++
		}
	}
	n.noFrames = n.scenes - n.withFrame
	return n
}

// ---- timeline (exact lengths, computed off the UI goroutine) ------------------------------

// timeline caches Engine.Timeline for a project revision: it checks files,
// so it runs in the background and the cards show an estimate until then.
type timeline struct {
	mu    sync.Mutex
	key   tlKey
	busy  bool
	at    time.Time // last computation started
	items map[string]studio.TimelineItem
	total float64
	ok    bool
}

// tlEvery bounds the recomputation while a job streams progress updates
// (every update is a new project revision).
const tlEvery = time.Second

type tlKey struct {
	pv  *core.ProjectView
	ver int64
}

func (t *timeline) get(gtx C, c *core.Ctx, pv *core.ProjectView) (map[string]studio.TimelineItem, float64, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	k := tlKey{pv, pv.Ver}
	if t.key != k && !t.busy {
		if wait := t.at.Add(tlEvery).Sub(gtx.Now); t.ok && t.key.pv == pv && wait > 0 {
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(wait)})
			return t.items, t.total, t.ok
		}
		t.busy, t.at = true, gtx.Now
		p := pv.P
		e := c.E
		c.Background(func() {
			items := e.Timeline(p)
			m := make(map[string]studio.TimelineItem, len(items))
			for _, it := range items {
				m[it.SceneID] = it
			}
			total := studio.TimelineDuration(items)
			t.mu.Lock()
			t.items, t.total, t.ok, t.key, t.busy = m, total, true, k, false
			t.mu.Unlock()
			c.Invalidate()
		})
	}
	return t.items, t.total, t.ok
}

// ---- model names ----------------------------------------------------------------------

var videoModelNames = map[string]string{
	"veo_31_fast":         "Veo 3.1 Fast",
	"veo_31_lite":         "Veo 3.1 Lite",
	"veo_31_quality":      "Veo 3.1 Quality",
	"veo_31_lite_relaxed": "Veo 3.1 Lite Relaxed",
	glabs.OmniFlash:       "Omni Flash",
}

// modelLabel is "Veo 3.1 Fast · 720p · 8 с" (or "Google · Gemini Omni Flash · 720p · 10 с").
func modelLabel(cfg config.Config) string {
	v := cfg.Video
	if v.Provider == "google" {
		if config.GoogleRendersOmni(cfg.Google.VeoModel) {
			return fmt.Sprintf("Google · Gemini Omni Flash · %s · %d с", v.Resolution, v.Length)
		}
		return fmt.Sprintf("Google · %s · %d с", cfg.Google.VeoModel, v.Length)
	}
	if v.Provider == "custom" {
		name := cfg.Custom.VideoPreset
		if pr, ok := providers.FindVideoPreset(cfg.Custom.VideoPreset); ok {
			name = pr.Label
		}
		model := cfg.Custom.VideoModel
		if model == "" {
			if pr, ok := providers.FindVideoPreset(cfg.Custom.VideoPreset); ok {
				model = pr.DefaultModel
			}
		}
		if model == "" { // the universal preset may need no model
			return fmt.Sprintf("%s · %d с", name, v.Length)
		}
		return fmt.Sprintf("%s · %s · %d с", name, model, v.Length)
	}
	name := videoModelNames[v.GlabsModel]
	if name == "" {
		name = v.GlabsModel
	}
	return fmt.Sprintf("%s · %s · %d с", name, v.Resolution, v.Length)
}

func ttsName(provider string) string {
	if info, ok := tts.ProviderByID(provider); ok && info.Label != "" {
		return info.Label
	}
	return "ElevenLabs"
}

func omniFlash(cfg config.Config) bool { return studio.OmniFlash(cfg) }

// ---- layout ---------------------------------------------------------------------------

func (v *render) Layout(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	if pv == nil {
		return D{Size: gtx.Constraints.Max}
	}
	th := c.Th
	st := v.state(pv.ID)
	p := &pv.P
	if len(p.Scenes) == 0 {
		if st.toPlan.Clicked(gtx) {
			goStep(c, pv.ID, 2)
		}
		return emptyColumn(gtx, c, pv, &st.toPlan, gui.IconClapperboard, "Сцени з'являться після плану",
			"Відео знімається з кадрів сцен, а голос — з реплік плану. Спершу створи план на кроці «План».",
			[]howStep{
				{gui.IconVideo, "Кадр оживає", "Кожен кадр сцени стає коротким кліпом."},
				{gui.IconMic, "Голос", "Кожна репліка звучить голосом героя чи оповідача — або твоїм записом."},
				{gui.IconScissors, "Монтаж", "Кліпи й голоси зберуться у фільм на кроці «Монтаж»."},
			})
	}
	cfg := c.E.Config().Get()
	stock := isStockMode(cfg, p)
	n := countClips(p, stock)
	tl, total, tlOK := st.tl.get(gtx, c, pv)
	u := c.UI(pv.ID)
	inset := gtx.Dp(bleed)
	W := max(0, gtx.Constraints.Max.X-2*inset)
	listMode := u.List && W >= gtx.Dp(700)
	g := gui.Grid{MinWidth: 300, Gap: gui.Sp24, Stretch: true}
	if W < gtx.Dp(700) {
		g.Gap = gui.Sp16
	}
	cols := fillCols(g.Columns(gtx, W), len(p.Scenes))
	g.MaxCols = cols
	if listMode {
		cols = 1
	}
	rows := (len(p.Scenes) + cols - 1) / cols
	gapPx := gtx.Dp(g.Gap)
	env := clipEnv{cfg: cfg, tl: tl, chain: p.Input.ChainFrames && omniFlash(cfg), clipSec: float64(c.E.ClipSeconds())}

	els := make([]layout.Widget, 0, rows+5)
	els = append(els, func(gtx C) D {
		return layout.Inset{Top: gui.Sp8, Bottom: gui.Sp32}.Layout(gtx, func(gtx C) D {
			return gui.FadeUp(gtx, st.stagger.Progress(gtx, 0), 8, func(gtx C) D {
				return v.header(gtx, c, pv, st, n, total, tlOK, cfg)
			})
		})
	})
	if n.noFrames > 0 && !stock {
		txt := "Відео знімається з кадру сцени, тож для них кліпів поки не буде."
		if n.vo {
			txt += " Озвучити їх можна вже зараз."
		}
		els = append(els, func(gtx C) D {
			if st.toFrames.Clicked(gtx) {
				goStep(c, pv.ID, 3)
			}
			return layout.Inset{Bottom: gui.Sp24}.Layout(gtx, func(gtx C) D {
				return notice{Tone: gui.ToneWarning, Icon: gui.IconImage,
					Title: fmt.Sprintf("%s без кадру", core.Count(n.noFrames, "сцена", "сцени", "сцен")),
					Text:  txt,
					Action: func(gtx C) D {
						return gui.Button{Kind: gui.Tinted, Size: gui.Sm, Text: "До розкадровки", Trail: gui.IconArrowRight}.Layout(gtx, th, &st.toFrames)
					}}.Layout(gtx, th)
			})
		})
	}
	els = append(els, func(gtx C) D {
		return layout.Inset{Bottom: gui.Sp16}.Layout(gtx, func(gtx C) D { return v.scenesHead(gtx, c, pv, st, n) })
	})
	firstRow := len(els)
	for r := 0; r < rows; r++ {
		start, end := r*cols, min(len(p.Scenes), (r+1)*cols)
		last := r == rows-1
		els = append(els, func(gtx C) D {
			d := func() D {
				if listMode {
					return v.clipRow(gtx, c, pv, st, start, env)
				}
				return g.Layout(gtx, end-start, func(gtx C, k int) D { return v.clipCard(gtx, c, pv, st, start+k, env) })
			}()
			if last {
				d.Size.Y += gtx.Dp(gui.Sp32)
			} else {
				d.Size.Y += gapPx
			}
			return d
		})
	}
	if id := u.ScrollTo; id != "" {
		u.ScrollTo = ""
		if _, i := pv.Scene(id); i >= 0 {
			st.list.ScrollTo(gtx, firstRow+i/cols, gtx.Dp(gui.Sp8))
			st.hl.Flash(id)
			u.Scene = id
		}
	}
	return st.list.Layout(gtx, th, len(els), inset, func(gtx C, i int) D { return els[i](gtx) })
}

// clipEnv is what every card of a frame shares.
type clipEnv struct {
	cfg     config.Config
	tl      map[string]studio.TimelineItem
	chain   bool // ChainFrames on Omni Flash
	clipSec float64
}

// ---- header -----------------------------------------------------------------------------

func (v *render) header(gtx C, c *core.Ctx, pv *core.ProjectView, st *rdState, n clipStats, total float64, exact bool, cfg config.Config) D {
	th := c.Th
	h := &st.head
	p := &pv.P
	running := pv.Running()
	kind := pv.RunningKind()
	batchOn := st.batch.active()
	if h.videos.Clicked(gtx) {
		c.Run(pv.ID, studio.StageVideos, false, "")
	}
	if h.voices.Clicked(gtx) {
		c.Run(pv.ID, studio.StageVoices, false, "")
	}
	if h.all.Clicked(gtx) {
		c.Run(pv.ID, studio.StageRender, false, "")
	}
	if h.stale.Clicked(gtx) {
		v.redoStale(c, pv, st)
	}
	if h.again.Clicked(gtx) {
		c.Confirm(core.Confirm{
			Title: "Зняти й озвучити все заново?",
			Text:  fmt.Sprintf("Усі кліпи будуть зняті знову (%s), а репліки — озвучені заново. Це витрачає кредити.", modelLabel(cfg)),
			Icon:  gui.IconRotateCcw, OK: "Усе заново",
			OnOK: func() { c.Run(pv.ID, studio.StageRender, true, "") },
		})
	}
	if h.settings.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageSettings, Section: "connections"})
	}

	tiles := func(gtx C) D {
		// Narrow: two tiles a row, the captions would not fit.
		captions := gtx.Constraints.Max.X >= gtx.Dp(640)
		return gui.Grid{MinWidth: 150, Gap: gui.Sp16, MaxCols: 4, Stretch: true}.Layout(gtx, 4, func(gtx C, i int) D {
			var t gui.StatTile
			switch i {
			case 0:
				t = gui.StatTile{Icon: gui.IconFilm, Label: "Кліпи", Value: fmt.Sprintf("%d/%d", n.vidDone, n.scenes), Caption: "знято"}
				if n.stills {
					t.Value, t.Caption = "—", "пропущено"
				}
				if n.code {
					t.Label, t.Caption = "Анімації", "відрендерено"
				}
			case 1:
				t = gui.StatTile{Icon: gui.IconMic, Label: "Озвучка", Value: fmt.Sprintf("%d/%d", n.voiceDone, n.voiceable), Caption: "готово"}
				if !n.vo {
					t.Value, t.Caption = "—", "вимкнено"
				}
			case 2:
				t = gui.StatTile{Icon: gui.IconClock, Label: "Тривалість", Value: core.Clock(total), Caption: "загалом"}
				if !exact {
					t.Value, t.Caption = core.Clock(estimateTotal(p, float64(c.E.ClipSeconds()))), "орієнтовно"
				}
			default:
				errs := n.vidErr + n.voiceErr
				t = gui.StatTile{Icon: gui.IconCheckCircle, Tone: gui.ToneSuccess, Label: "Помилки", Value: "0", Caption: "усе гаразд"}
				if errs > 0 {
					t = gui.StatTile{Icon: gui.IconAlertTriangle, Tone: gui.ToneError, Label: "Помилки", Value: fmt.Sprint(errs), Caption: "у картках нижче"}
				}
			}
			if !captions {
				t.Caption = ""
			}
			return t.Layout(gtx, th)
		})
	}
	actions := func(gtx C) D {
		left := func(gtx C) D {
			stock := isStockMode(cfg, p)
			txt := modelLabel(cfg)
			if stock {
				provName := cfg.Stock.Provider
				if provName == "" || provName == "auto" {
					provName = "Авто"
				}
				txt = "Стокові відео · " + strings.Title(provName)
			}
			if n.code {
				txt = "Анімація кодом · рендер на цьому комп'ютері"
			}
			if n.stills {
				txt = "Відео пропущено — «" + studio.PipelineLabel(p.Input.Pipeline) + "»"
			}
			if n.vo {
				txt += " · голос: " + ttsName(cfg.TTS.Provider)
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx C) D {
					note := runningNote(pv, false)
					if note == "" {
						return D{}
					}
					return layout.Inset{Bottom: gui.Sp4}.Layout(gtx, label(th, th.Label, th.Pal.RunningInk, 1, note))
				}),
				layout.Rigid(func(gtx C) D {
					return hrow(gtx, gui.Sp8, gui.IconClapperboard.Widget(16, th.Pal.Text3), label(th, th.Label, th.Pal.Text, 1, txt))
				}),
				layout.Rigid(layout.Spacer{Height: 2}.Layout),
				layout.Rigid(func(gtx C) D {
					return gui.Flow{Gap: gui.Sp8, RowGap: 2}.Layout(gtx,
						label(th, th.Small, th.Pal.Text2, 2, clipsCostLine(n, stock)),
						func(gtx C) D {
							if n.code {
								return D{}
							}
							return gui.Link{Text: "Змінити модель", Style: th.Small.Weighted(gui.FontMedium)}.Layout(gtx, th, &h.settings)
						},
					)
				}),
			)
		}
		right := func(gtx C) D {
			stock := isStockMode(cfg, p)
			dis := running || batchOn
			var ws []layout.Widget
			left := n.vidLeft + n.voiceLeft
			if left == 0 && n.redo == 0 && n.vidBusy+n.voiceBusy == 0 {
				ws = append(ws,
					func(gtx C) D {
						return hrow(gtx, gui.Sp8, gui.IconCheckCircle.Widget(18, th.Pal.SuccessInk), label(th, th.Label, th.Pal.SuccessInk, 1, doneLine(n)))
					},
					func(gtx C) D {
						return gui.Button{Kind: gui.Ghost, Text: "Усе заново…", Icon: gui.IconRotateCcw, Disabled: dis}.Layout(gtx, th, &h.again)
					})
				return gui.Flow{Gap: gui.Sp12}.Layout(gtx, ws...)
			}
			if n.vo && n.voiceable > 0 {
				txt := "Озвучити"
				if n.voiceLeft > 0 {
					txt = fmt.Sprintf("Озвучити · %d", n.voiceLeft)
				}
				ws = append(ws, func(gtx C) D {
					return gui.Button{Kind: gui.Secondary, Text: txt, Icon: gui.IconMic, Busy: kind == studio.JobVoices,
						Disabled: dis || n.voiceLeft == 0, Tip: "Лише голос, без зйомки відео"}.Layout(gtx, th, &h.voices)
				})
			}
			if n.stills {
				return gui.Flow{Gap: gui.Sp12}.Layout(gtx, ws...)
			}
			vtxt := "Зняти відео"
			if stock {
				vtxt = "Знайти відео"
			}
			if n.vidLeft > 0 {
				vtxt = fmt.Sprintf("%s · %d", vtxt, n.vidLeft)
			}
			if n.code {
				vtxt = "Відрендерити анімації"
				if n.vidLeft > 0 {
					vtxt = fmt.Sprintf("Відрендерити анімації · %d", n.vidLeft)
				}
			}
			ws = append(ws, func(gtx C) D {
				return gui.Button{Kind: gui.Secondary, Text: vtxt, Icon: gui.IconVideo, Busy: kind == studio.JobVideos,
					Disabled: dis || n.vidLeft == 0, Tip: "Лише кліпи, без озвучки"}.Layout(gtx, th, &h.videos)
			})
			if n.redo > 0 && !running {
				ws = append(ws, func(gtx C) D {
					txt := fmt.Sprintf("Перезняти змінені · %d", n.redo)
					if stock {
						txt = fmt.Sprintf("Оновити стокові · %d", n.redo)
					} else if n.code {
						txt = fmt.Sprintf("Оновити змінені · %d", n.redo)
					}
					return gui.Button{Kind: gui.Tinted, Text: txt, Icon: gui.IconRefreshCw, Busy: batchOn,
						Tip: "Кліпи й репліки, чиї промти чи текст змінились після зйомки"}.Layout(gtx, th, &h.stale)
				})
			}
			if left > 0 {
				allTxt := "Все разом"
				allTip := "Зняти кліпи й озвучити репліки одночасно"
				if stock {
					allTxt = "Стокові відео й озвучка"
					allTip = "Знайти стокові відео та озвучити репліки одночасно"
				}
				ws = append(ws, func(gtx C) D {
					return gui.Button{Kind: gui.Primary, Text: allTxt, Icon: gui.IconSparkles, Busy: kind == studio.JobRender,
						Disabled: dis, Tip: allTip}.Layout(gtx, th, &h.all)
				})
			}
			return gui.Flow{Gap: gui.Sp12}.Layout(gtx, ws...)
		}
		return spread(gtx, gui.Sp24, 280, left, right)
	}
	// The running job and a failed one are shown by the workspace banner
	// above the tabs; the header only adds its own "re-shoot changed" batch.
	ws := []layout.Widget{tiles, actions}
	if batchOn {
		ws = append(ws, func(gtx C) D {
			done, all := st.batch.progress()
			pct := 0
			if all > 0 {
				pct = done * 100 / all
			}
			d, stop := h.job.Layout(gtx, th, st.batch.label, fmt.Sprintf("Готово %d з %d", done, all), pct)
			if stop {
				st.batch.Stop()
				c.Toast(gui.ToneInfo, "Нові задачі не стартують; ті, що вже йдуть, доробляться")
			}
			return d
		})
	}
	return gui.Card{Padding: headPad(gtx)}.Layout(gtx, th, func(gtx C) D {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return vstack(gtx, gui.Sp20, ws...)
	})
}

// estimateTotal is the film length before the exact timeline is known:
// per scene the clip, or the voice plus its breath when longer.
func estimateTotal(p *studio.Project, clip float64) float64 {
	t := 0.0
	for i := range p.Scenes {
		s := &p.Scenes[i]
		d := clip
		if s.Video.Duration > 0 {
			d = max(0.5, s.Video.Duration-s.TrimIn-s.TrimOut)
		}
		if a := s.Audio.Duration; a > 0 && p.Input.Voiceover {
			d = max(d, a+0.6)
		}
		t += d
	}
	return t
}

// redoStale re-shoots the changed clips (redrawing a changed frame first)
// and re-voices the changed lines.
func (v *render) redoStale(c *core.Ctx, pv *core.ProjectView, st *rdState) {
	st.batch.start(c, pv.ID, "Перезнімаємо змінене", staleRenderItems(&pv.P))
}

// staleRenderItems are the changed clips (a changed frame is redrawn first)
// and the changed voice lines.
func staleRenderItems(p *studio.Project) []batchItem {
	var items []batchItem
	for i := range p.Scenes {
		s := &p.Scenes[i]
		if clipStale(s) && !busy(s.Video) && !busy(s.Frame) {
			it := batchItem{kind: studio.RegenVideo, id: s.ID}
			if frameStale(s) {
				video := it
				it = batchItem{kind: studio.RegenFrame, id: s.ID, then: &video}
			}
			items = append(items, it)
		}
		if lineStale(p, s) && !busy(s.Audio) {
			items = append(items, batchItem{kind: studio.RegenVoice, id: s.ID})
		}
	}
	return items
}

func (v *render) scenesHead(gtx C, c *core.Ctx, pv *core.ProjectView, st *rdState, n clipStats) D {
	th := c.Th
	u := c.UI(pv.ID)
	if val, ok := st.view.Changed(gtx); ok {
		u.List = val == "list"
	}
	desc := "Кожен кадр оживає в кліп"
	if n.code {
		desc = "Кожна сцена — анімація, яку малює код"
	}
	if n.vo {
		desc += ", кожна репліка отримує голос"
	}
	desc += ". Натисни на кадр, щоб переглянути кліп."
	var right layout.Widget
	if gtx.Constraints.Max.X >= gtx.Dp(700) {
		right = func(gtx C) D {
			val := "grid"
			if u.List {
				val = "list"
			}
			return st.view.Layout(gtx, th, val, []gui.SegOption{
				{Value: "grid", Label: "Сітка", Icon: gui.IconLayoutGrid},
				{Value: "list", Label: "Список", Icon: gui.IconList},
			})
		}
	}
	return sectionHead(gtx, th, "Сцени", fmt.Sprint(n.scenes), desc, right)
}

// ---- scene cards -------------------------------------------------------------------------

func (v *render) clipEvents(gtx C, c *core.Ctx, pv *core.ProjectView, cc *clipCard, s *studio.Scene) {
	id := s.ID
	if cc.shoot.Clicked(gtx) || cc.redo.Clicked(gtx) {
		if c.Regen(pv.ID, studio.RegenVideo, id, "") {
			cc.editing = false
			c.UI(pv.ID).Scene = id
		}
	}
	if cc.revoice.Clicked(gtx) {
		if c.Regen(pv.ID, studio.RegenVoice, id, "") {
			c.UI(pv.ID).Scene = id
		}
	}
	if cc.motion.Clicked(gtx) || cc.textClick.Clicked(gtx) {
		cc.editing = !cc.editing
		if cc.editing {
			cc.field.SetText(s.MotionPrompt)
			cc.focus = true
		}
	}
	if cc.collapse.Clicked(gtx) {
		cc.editing = false
	}
	if cc.open.Clicked(gtx) && s.Video.Path != "" {
		c.OpenPath(pv.Abs(s.Video.Path))
	}
	if cc.reveal.Clicked(gtx) && s.Video.Path != "" {
		c.Reveal(pv.Abs(s.Video.Path))
	}
	if val, ok := cc.field.Changed(gtx); ok {
		c.Edit(pv.ID, func(p *studio.Project) {
			for k := range p.Scenes {
				if p.Scenes[k].ID == id {
					p.Scenes[k].MotionPrompt = val
				}
			}
		})
	}
}

func (v *render) clipCard(gtx C, c *core.Ctx, pv *core.ProjectView, st *rdState, i int, env clipEnv) D {
	th := c.Th
	s := &pv.P.Scenes[i]
	cc := st.card(s.ID)
	v.clipEvents(gtx, c, pv, cc, s)
	cc.rw.update(gtx, c, pv.ID, studio.RegenVideo, s.ID)
	return v.wrap(gtx, th, st, i, s.ID, func(gtx C) D {
		return gui.Card{Padding: gui.Sp12}.Layout(gtx, th, func(gtx C) D {
			inset := func(w layout.Widget) layout.Widget {
				return func(gtx C) D { return layout.Inset{Left: 2, Right: 2}.Layout(gtx, w) }
			}
			top := []layout.Widget{
				func(gtx C) D { return v.clipMedia(gtx, c, pv, cc, s, i) },
				inset(func(gtx C) D { return v.clipTitle(gtx, c, pv, cc, s, i) }),
				inset(func(gtx C) D { return v.pills(gtx, th, &pv.P, s) }),
				inset(func(gtx C) D { return narration(gtx, th, s) }),
				func(gtx C) D { return v.voiceRow(gtx, c, pv, cc, s) },
				inset(func(gtx C) D { return v.hints(gtx, th, &pv.P, s, i, env) }),
				func(gtx C) D {
					return v.motionEditor(gtx, th, cc, s, false, isStockMode(env.cfg, &pv.P))
				},
				func(gtx C) D { return clipNotices(gtx, th, &pv.P, s) },
				func(gtx C) D { return cc.rw.Layout(gtx, th, s.Video.Rewrite) },
			}
			return pinned(gtx, gui.Sp12, top, func(gtx C) D { return v.clipActions(gtx, c, pv, cc, s) })
		})
	})
}

// clipRow is the list-mode card: the player on the left, the rest beside it.
func (v *render) clipRow(gtx C, c *core.Ctx, pv *core.ProjectView, st *rdState, i int, env clipEnv) D {
	th := c.Th
	s := &pv.P.Scenes[i]
	cc := st.card(s.ID)
	v.clipEvents(gtx, c, pv, cc, s)
	cc.rw.update(gtx, c, pv.ID, studio.RegenVideo, s.ID)
	return v.wrap(gtx, th, st, i, s.ID, func(gtx C) D {
		return gui.Card{Padding: gui.Sp16}.Layout(gtx, th, func(gtx C) D {
			W := gtx.Constraints.Max.X
			mw := min(W*40/100, gtx.Dp(480))
			if pv.P.Input.Aspect == "9:16" {
				mw = min(W*30/100, gtx.Dp(300))
			}
			gap := gtx.Dp(gui.Sp20)
			mg := gtx
			mg.Constraints = layout.Constraints{Max: pt(mw, gtx.Constraints.Max.Y)}
			md, mc := record(mg, func(gtx C) D { return v.clipMedia(gtx, c, pv, cc, s, i) })
			dg := gtx
			dg.Constraints = layout.Constraints{Min: pt(0, md.Size.Y), Max: pt(max(0, W-mw-gap), gtx.Constraints.Max.Y)}
			dd, dc := record(dg, func(gtx C) D {
				return pinned(gtx, gui.Sp12, []layout.Widget{
					func(gtx C) D { return v.clipTitle(gtx, c, pv, cc, s, i) },
					func(gtx C) D { return v.pills(gtx, th, &pv.P, s) },
					func(gtx C) D { return narration(gtx, th, s) },
					func(gtx C) D {
						gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(460))
						return v.voiceRow(gtx, c, pv, cc, s)
					},
					func(gtx C) D { return v.hints(gtx, th, &pv.P, s, i, env) },
					func(gtx C) D {
						return v.motionEditor(gtx, th, cc, s, true, isStockMode(env.cfg, &pv.P))
					},
					func(gtx C) D { return clipNotices(gtx, th, &pv.P, s) },
					func(gtx C) D { return cc.rw.Layout(gtx, th, s.Video.Rewrite) },
				}, func(gtx C) D {
					gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(460))
					return v.clipActions(gtx, c, pv, cc, s)
				})
			})
			mc.Add(gtx.Ops)
			drawAt(gtx, mw+gap, 0, dc)
			return D{Size: pt(W, max(md.Size.Y, dd.Size.Y))}
		})
	})
}

func (v *render) wrap(gtx C, th *gui.Theme, st *rdState, i int, id string, w layout.Widget) D {
	d := gui.FadeUp(gtx, st.stagger.Progress(gtx, 1+i), 12, w)
	hlRing(gtx, th, d.Size, st.hl.Alpha(gtx, id))
	return d
}

// clipMedia is the player: the clip once shot (poster = the frame), the
// frame dimmed under the progress while it is being shot. Vertical films
// sit centred in a warm well so the cards keep one height.
func (v *render) clipMedia(gtx C, c *core.Ctx, pv *core.ProjectView, cc *clipCard, s *studio.Scene, i int) D {
	th := c.Th
	p := &pv.P
	vid := s.Video
	ratio := ratioOf(p.Input.Aspect)
	W := gtx.Constraints.Max.X
	style := core.VideoStyle{
		Poster: pv.Abs(s.Frame.Path), PosterVer: s.Frame.UpdatedAt,
		Ratio: ratio, Compact: W < gtx.Dp(300),
	}
	code := p.Input.CodeAnim() && !s.Code.Fallback
	if code && s.Code.Preview.Path != "" { // the animation's preview still
		style.Poster, style.PosterVer = pv.Abs(s.Code.Preview.Path), s.Code.Preview.UpdatedAt
	}
	if vid.Status == studio.StatusDone && vid.Path != "" && !busy(vid) {
		style.Path, style.Ver = pv.Abs(vid.Path), vid.UpdatedAt
	}
	style.Overlay = func(gtx C) D {
		size := gtx.Constraints.Max
		m := gtx.Dp(10)
		cc.veil.Layout(gtx, th, size, busy(vid), busyPct(vid))
		cfg := c.E.Config().Get()
		stock := isStockMode(cfg, p)
		switch {
		case code && vid.Path == "" && !busy(vid) && s.Code.Preview.Path == "":
			emptyCaption(gtx, th, size, "Анімацію ще не відрендерено")
		case stock && s.Frame.Path == "" && vid.Path == "" && !busy(vid):
			emptyCaption(gtx, th, size, "Стокове відео ще не знайдено")
		case !code && !stock && s.Frame.Path == "" && !busy(vid):
			emptyCaption(gtx, th, size, "Спершу потрібен кадр")
		}
		corner(gtx, size, m, false, false, func(gtx C) D { return numBadge(gtx, th, i+1) })
		return D{Size: size}
	}
	if ratio >= 1 {
		return cc.video.Layout(gtx, c, style)
	}
	// Vertical: a warm well with the 9:16 player in the middle.
	wh := min(W*5/4, gtx.Dp(460))
	vw := min(W, int(float32(wh)*ratio))
	gui.FillRRect(gtx, rectWH(W, wh), gui.RadiusInner, th.Pal.Surface)
	vg := gtx
	vg.Constraints = layout.Constraints{Max: pt(vw, wh)}
	_, call := record(vg, func(gtx C) D { return cc.video.Layout(gtx, c, style) })
	drawAt(gtx, (W-vw)/2, 0, call)
	return D{Size: pt(W, wh)}
}

func (v *render) clipTitle(gtx C, c *core.Ctx, pv *core.ProjectView, cc *clipCard, s *studio.Scene, i int) D {
	th := c.Th
	has := s.Video.Path != "" && s.Video.Status == studio.StatusDone
	mc := th.Pal.Text2
	if cc.editing {
		mc = th.Pal.ActionText
	}
	left := label(th, th.BodyStrong.Lined(1.35), th.Pal.Text, 1, sceneTitle(s, i+1))
	right := func(gtx C) D {
		return hrow(gtx, 0,
			func(gtx C) D {
				tip := "Змінити рух камери"
				if cc.editing {
					tip = "Сховати рух камери"
				}
				return gui.IconButton{Icon: gui.IconPenLine, Size: 34, Color: mc, Tip: tip}.Layout(gtx, th, &cc.motion)
			},
			func(gtx C) D {
				return gui.IconButton{Icon: gui.IconExternalLink, Size: 34, Tip: "Відкрити в програвачі системи", Disabled: !has || c.Offline}.Layout(gtx, th, &cc.open)
			},
			func(gtx C) D {
				return gui.IconButton{Icon: gui.IconFolderOpen, Size: 34, Tip: "Показати у папці", Disabled: !has || c.Offline}.Layout(gtx, th, &cc.reveal)
			},
		)
	}
	return spread(gtx, gui.Sp8, 100, left, right)
}

// pills shows the clip's and the voice's states side by side.
func (v *render) pills(gtx C, th *gui.Theme, p *studio.Project, s *studio.Scene) D {
	vp := assetPill(s.Video)
	vp.Small = true
	if p.Input.CodeAnim() { // «Анімація кодом»: the animation's state
		vp = codePill(s)
		vp.Small = true
		ws := []layout.Widget{vp.Widget(th)}
		if p.Input.Voiceover && spoken(s) {
			ap := assetPill(s.Audio)
			ap.Small, ap.Text = true, map[gui.AssetState]string{gui.StateDone: "Голос готовий", gui.StateRunning: "Озвучується",
				gui.StateQueued: "Голос у черзі", gui.StateError: "Голос: помилка", gui.StateStale: "Текст змінено"}[ap.State]
			if ap.Text == "" {
				ap.Text = "Голосу ще немає"
			}
			ws = append(ws, ap.Widget(th))
		}
		return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
	}
	switch vp.State {
	case gui.StateDone:
		vp.Text = "Кліп готовий"
	case gui.StateRunning:
		vp.Text = "Знімається"
		if pct := busyPct(s.Video); pct > 0 {
			vp.Text = fmt.Sprintf("Знімається %d%%", pct)
		}
	case gui.StateQueued:
		vp.Text = "Кліп у черзі"
	case gui.StateError:
		vp.Text = "Кліп: помилка"
	case gui.StateStale:
		vp.Text = "Рух змінено"
	default:
		vp.Text = "Кліпу ще немає"
	}
	if vp.State == gui.StateDone && clipStale(s) {
		vp.State, vp.Text = gui.StateStale, "Кадр змінено"
	}
	if _, _, short := voiceOutgrew(p, s); short && vp.State == gui.StateStale {
		vp.Text = "Озвучка довша за кліп"
	}
	if p.Input.StillsOnly() { // «Тільки картинки»: the frame is the scene
		vp = gui.StatusPill{State: gui.StateDone, Text: "Кадр із рухом", Small: true}
		if s.Frame.Path == "" {
			vp = gui.StatusPill{State: gui.StateEmpty, Text: "Кадру ще немає", Small: true}
		}
	}
	ws := []layout.Widget{vp.Widget(th)}
	if p.Input.Voiceover && spoken(s) {
		ap := assetPill(s.Audio)
		ap.Small = true
		switch ap.State {
		case gui.StateDone:
			ap.Text = "Голос готовий"
		case gui.StateRunning:
			ap.Text = "Озвучується"
		case gui.StateQueued:
			ap.Text = "Голос у черзі"
		case gui.StateError:
			ap.Text = "Голос: помилка"
		case gui.StateStale:
			ap.Text = "Текст змінено"
			if s.AudioSource == studio.AudioUpload {
				ap.Text = "Фрагмент змінено"
			}
		default:
			ap.Text = "Голосу ще немає"
		}
		ws = append(ws, ap.Widget(th))
	}
	return gui.Flow{Gap: gui.Sp8}.Layout(gtx, ws...)
}

// voiceOutgrew: the clip is outdated because its new voice line outgrew it
// (studio.VoiceOutgrowsClip: the line with its air and the transitions vs
// what the clip plays) — the card asks to shoot it again.
func voiceOutgrew(p *studio.Project, s *studio.Scene) (need, clip float64, short bool) {
	if s.Video.Status != studio.StatusDone || !s.Video.Stale {
		return 0, 0, false
	}
	return studio.VoiceOutgrowsClip(p, s)
}

// narration is the line of the scene (what the voice says).
func narration(gtx C, th *gui.Theme, s *studio.Scene) D {
	txt := strings.TrimSpace(s.Narration)
	if txt == "" {
		return label(th, th.Small, th.Pal.Text3, 1, "Без тексту — у цій сцені ніхто не говорить.")(gtx)
	}
	return label(th, th.Small, th.Pal.Text2, 2, "«"+txt+"»")(gtx)
}

// voiceRow is the voice player (a disabled pill until the voice exists).
func (v *render) voiceRow(gtx C, c *core.Ctx, pv *core.ProjectView, cc *clipCard, s *studio.Scene) D {
	th := c.Th
	p := &pv.P
	if !p.Input.Voiceover || !spoken(s) {
		return D{}
	}
	lbl := speakerName(pv, s)
	if s.AudioSource == studio.AudioUpload {
		lbl = fmt.Sprintf("Твій запис %s–%s", core.Clock(s.VoiceStart), core.Clock(s.VoiceEnd))
	}
	au := s.Audio
	st := gui.AudioStyle{Label: lbl}
	if au.Status == studio.StatusDone && au.Path != "" && !busy(au) {
		st.Src = pv.Abs(au.Path)
		st.Duration = time.Duration(au.Duration * float64(time.Second))
		st.Backend = c.Audio
	}
	return cc.voice.Layout(gtx, th, st)
}

func speakerName(pv *core.ProjectView, s *studio.Scene) string {
	if s.Speaker != "" && s.Speaker != "narrator" {
		if ch := pv.Character(s.Speaker); ch != nil && strings.TrimSpace(ch.Name) != "" {
			return strings.TrimSpace(ch.Name)
		}
	}
	return "Оповідач"
}

// hints: the clip against the voice, and the chain (Omni Flash).
func (v *render) hints(gtx C, th *gui.Theme, p *studio.Project, s *studio.Scene, i int, env clipEnv) D {
	var ws []layout.Widget
	if p.Input.CodeAnim() && !s.Code.Fallback {
		ws = append(ws, hintLine(th, gui.IconChartNoAxesColumn, "Анімація кодом — рівно як голос сцени, без руху камери", false))
	} else if p.Input.StillsOnly() || p.Input.CodeAnim() {
		ws = append(ws, hintLine(th, gui.IconMove, "Сцена триває, як її голос; рух камери — у «Монтажі»", false))
	} else if txt, warn := lengthHint(p, s, env); txt != "" {
		ws = append(ws, hintLine(th, gui.IconTimer, txt, warn))
	}
	if env.chain {
		txt := "Ланцюжок: перша сцена стартує з власного кадру"
		if i > 0 {
			txt = fmt.Sprintf("Ланцюжок: старт з останнього кадру сцени %d", i)
		}
		ws = append(ws, hintLine(th, gui.IconLink, txt, false))
	}
	return vstack(gtx, gui.Sp4, ws...)
}

func hintLine(th *gui.Theme, ic *gui.Icon, txt string, warn bool) layout.Widget {
	col := th.Pal.Text3
	if warn {
		col = th.Pal.WarningInk
	}
	return hintLineC(th, ic, txt, col)
}

func hintLineC(th *gui.Theme, ic *gui.Icon, txt string, col color.NRGBA) layout.Widget {
	return func(gtx C) D {
		return layout.Flex{Alignment: layout.Start}.Layout(gtx,
			layout.Rigid(func(gtx C) D { return layout.Inset{Top: 1, Right: 6}.Layout(gtx, ic.Widget(14, col)) }),
			layout.Flexed(1, label(th, th.Caption.Lined(1.45), col, 2, txt)),
		)
	}
}

// lengthHint compares the clip with the voice: exact from the timeline
// once known, estimated before.
func lengthHint(p *studio.Project, s *studio.Scene, env clipEnv) (string, bool) {
	if it, ok := env.tl[s.ID]; ok && it.HasClip {
		if it.VoiceDuration > 0 && p.Input.Voiceover {
			if hold := it.Duration - it.ClipDuration; hold > 0.3 {
				return fmt.Sprintf("Голос %s довший за кліп %s — останній кадр потримається ще %s", secs(it.VoiceDuration), secs(it.ClipDuration), secs(hold)), true
			}
			return fmt.Sprintf("Кліп %s · голос %s", secs(it.ClipDuration), secs(it.VoiceDuration)), false
		}
		return "Кліп " + secs(it.ClipDuration), false
	}
	clip, est := env.clipSec, true
	if s.Video.Duration > 0 {
		clip, est = max(0.5, s.Video.Duration-s.TrimIn-s.TrimOut), false
	}
	voice, vest := 0.0, false
	if p.Input.Voiceover {
		switch {
		case s.Audio.Duration > 0:
			voice = s.Audio.Duration
		case s.AudioSource == studio.AudioUpload && s.VoiceEnd > s.VoiceStart:
			voice = s.VoiceEnd - s.VoiceStart
		case strings.TrimSpace(s.Narration) != "":
			voice, vest = studio.SpeechSeconds(s.Narration), true
		}
	}
	approx := func(v float64, e bool) string {
		if e {
			return "≈" + secs(v)
		}
		return secs(v)
	}
	if voice <= 0 {
		return "Кліп " + approx(clip, est), false
	}
	if hold := voice + 0.6 - clip; hold > 0.3 {
		return fmt.Sprintf("Голос %s довший за кліп %s — останній кадр потримається довше", approx(voice, vest), approx(clip, est)), true
	}
	return fmt.Sprintf("Кліп %s · голос %s", approx(clip, est), approx(voice, vest)), false
}

// motionEditor is the motion prompt: hidden in grid cards until opened
// (list rows show a line of it), an editor committing through c.Edit with
// "re-shoot with this motion".
func (v *render) motionEditor(gtx C, th *gui.Theme, cc *clipCard, s *studio.Scene, preview bool, isStock ...bool) D {
	stock := len(isStock) > 0 && isStock[0]
	return cc.exp.Layout(gtx, func(gtx C) D {
		if !cc.editing {
			if !preview {
				return D{}
			}
			txt := strings.TrimSpace(s.MotionPrompt)
			col := th.Pal.Text2
			if txt == "" {
				txt, col = "Рух камери не задано — модель зробить легкий наїзд.", th.Pal.Text3
			}
			d, call := record(gtx, hintLineC(th, gui.IconMove, txt, col))
			cc.textClick.Layout(gtx, func(gtx C) D { return D{Size: d.Size} })
			call.Add(gtx.Ops)
			return d
		}
		cc.field.Sync(gtx, s.MotionPrompt)
		if cc.focus {
			cc.focus = false
			cc.field.Focus(gtx)
		}
		return vstack(gtx, gui.Sp8,
			func(gtx C) D {
				return cc.field.Layout(gtx, th, gui.FieldStyle{Label: "Рух камери і дія", MinLines: 3, MaxLines: 8, MaxLen: 2000,
					Style:       th.Small.Sized(14).Lined(1.5),
					Placeholder: "Англійською: як рухається камера і що відбувається в кадрі",
					Hint:        "Зміна позначить кліп як застарілий"})
			},
			func(gtx C) D {
				btnText := "Перезняти з цим рухом"
				if stock {
					btnText = "Знайти відео за описом"
				}
				return gui.Flow{Gap: gui.Sp8}.Layout(gtx,
					func(gtx C) D {
						return gui.Button{Kind: gui.Primary, Size: gui.Sm, Text: btnText, Icon: gui.IconVideo,
							Busy: busy(s.Video), Disabled: s.Frame.Path == "" && !stock}.Layout(gtx, th, &cc.redo)
					},
					func(gtx C) D {
						return gui.Button{Kind: gui.Ghost, Size: gui.Sm, Text: "Згорнути"}.Layout(gtx, th, &cc.collapse)
					},
				)
			},
		)
	})
}

// clipNotices explain a failed clip or voice (the retries are the card's
// buttons).
func clipNotices(gtx C, th *gui.Theme, p *studio.Project, s *studio.Scene) D {
	var ws []layout.Widget
	if need, clip, short := voiceOutgrew(p, s); short && !busy(s.Video) {
		ws = append(ws, func(gtx C) D {
			return notice{Tone: gui.ToneWarning, Icon: gui.IconTimer, Title: "Озвучка довша за кліп — перезняти?",
				Text: fmt.Sprintf("Новій репліці з паузою й переходами потрібно %s, а кліп грає %s — у фільмі застигне останній кадр. "+
					"Перезніми кліп (з динамічною довжиною він підлаштується під голос) або скороти репліку.", secs(need), secs(clip)),
				Lines: 4, Small: true}.Layout(gtx, th)
		})
	}
	if p.Input.CodeAnim() && s.Code.Fallback {
		ws = append(ws, func(gtx C) D {
			return notice{Tone: gui.ToneWarning, Icon: gui.IconAlertTriangle, Title: "Кадр замість анімації",
				Text: s.Code.Error, Lines: errLines, Small: true}.Layout(gtx, th)
		})
	}
	if s.Video.Status == studio.StatusError {
		title := "Кліп не вдався"
		if p.Input.CodeAnim() {
			title = "Анімація не вдалася"
		}
		ws = append(ws, func(gtx C) D {
			return notice{Tone: gui.ToneError, Icon: gui.IconAlertTriangle, Title: title,
				Text: assetErr(s.Video), Lines: errLines, Small: true}.Layout(gtx, th)
		})
	}
	if p.Input.Voiceover && s.Audio.Status == studio.StatusError {
		ws = append(ws, func(gtx C) D {
			return notice{Tone: gui.ToneError, Icon: gui.IconAlertTriangle, Title: "Голос не вдався",
				Text: assetErr(s.Audio), Lines: errLines, Small: true}.Layout(gtx, th)
		})
	}
	return vstack(gtx, gui.Sp8, ws...)
}

func (v *render) clipActions(gtx C, c *core.Ctx, pv *core.ProjectView, cc *clipCard, s *studio.Scene) D {
	th := c.Th
	p := &pv.P
	running := pv.Running()
	cfg := c.E.Config().Get()
	stock := isStockMode(cfg, p)
	vLabel := "Зняти"
	if stock {
		vLabel = "Знайти відео"
	}
	vb := gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconVideo, Text: vLabel, Busy: busy(s.Video),
		Disabled: running || (s.Frame.Path == "" && !stock), Full: true}
	switch {
	case busy(s.Video):
		vb.Text = "Шукаємо…"
		if !stock {
			vb.Text = "Знімається…"
		}
	case s.Video.Status == studio.StatusError:
		vb.Text, vb.Icon = "Повторити", gui.IconRotateCcw
	case s.Video.Path != "":
		vb.Text = "Перезняти"
		if stock {
			vb.Text = "Переобрати"
		}
		if clipStale(s) {
			vb.Kind = gui.Primary
		}
		if _, _, short := voiceOutgrew(p, s); short {
			vb.Tip = "Озвучка довша за кліп — зняти кліп під новий голос"
		}
	}
	if s.Frame.Path == "" && !stock {
		vb.Tip = "Спершу потрібен кадр сцени"
	}
	if p.Input.CodeAnim() { // «Анімація кодом»: rendered here, no frame needed
		vb.Icon, vb.Tip, vb.Disabled = gui.IconFilm, "", running
		switch {
		case busy(s.Video):
			vb.Text = "Рендериться…"
		case s.Code.Fallback:
			vb.Text, vb.Icon = "Спробувати анімацію", gui.IconRotateCcw
		case s.Video.Status == studio.StatusError:
			vb.Text, vb.Icon = "Повторити", gui.IconRotateCcw
		case s.Video.Path != "":
			vb.Text = "Перерендерити"
		default:
			vb.Text = "Відрендерити"
		}
	}
	if p.Input.StillsOnly() {
		vb.Disabled, vb.Busy, vb.Icon, vb.Text = true, false, gui.IconImage, "Без відео"
		vb.Tip = "Режим «Тільки картинки»: відео не знімається — сцена змонтується з кадру з рухом камери"
	}
	voice := p.Input.Voiceover && spoken(s)
	if !voice {
		return vb.Layout(gtx, th, &cc.shoot)
	}
	ab := gui.Button{Kind: gui.Tinted, Size: gui.Sm, Icon: gui.IconMic, Text: "Озвучити", Busy: busy(s.Audio), Disabled: running, Full: true}
	switch {
	case busy(s.Audio):
		ab.Text = "Озвучується…"
	case s.Audio.Status == studio.StatusError:
		ab.Text, ab.Icon = "Повторити", gui.IconRotateCcw
	case s.Audio.Path != "":
		ab.Text = "Переозвучити"
		if lineStale(p, s) {
			ab.Kind = gui.Primary
		}
	}
	if s.AudioSource == studio.AudioUpload {
		ab.Tip = "Вирізати фрагмент твого запису ще раз"
	}
	return layout.Flex{}.Layout(gtx,
		layout.Flexed(1, func(gtx C) D { return vb.Layout(gtx, th, &cc.shoot) }),
		layout.Rigid(layout.Spacer{Width: gui.Sp8}.Layout),
		layout.Flexed(1, func(gtx C) D { return ab.Layout(gtx, th, &cc.revoice) }),
	)
}

// ---- side column -------------------------------------------------------------------------

func (v *render) Side(gtx C, c *core.Ctx, pv *core.ProjectView) D {
	if pv == nil {
		return D{}
	}
	th := c.Th
	st := v.state(pv.ID)
	p := &pv.P
	if len(p.Scenes) == 0 {
		return gui.TipCard{Icon: gui.IconLightbulb, Title: "Що буде на цьому кроці",
			Text: "Кожен кадр оживе в коротке відео, а кожна репліка отримає голос. Потім — монтаж."}.Layout(gtx, th, nil)
	}
	cfg := c.E.Config().Get()
	stock := isStockMode(cfg, p)
	n := countClips(p, stock)
	u := c.UI(pv.ID)
	first := func(pred func(s *studio.Scene) bool) string {
		for i := range p.Scenes {
			if pred(&p.Scenes[i]) {
				return p.Scenes[i].ID
			}
		}
		return ""
	}
	rows := []sideRow{
		{Tone: gui.ToneSuccess, Label: "Кліпи готові", Count: n.vidDone - n.vidStale, Always: true},
	}
	if n.stills {
		rows = rows[:0]
	}
	if n.vo {
		rows = append(rows, sideRow{Tone: gui.ToneSuccess, Label: "Голос готовий", Count: n.voiceDone - n.voiceStale, Always: true})
	}
	rows = append(rows,
		sideRow{Tone: gui.ToneRunning, Label: "У роботі", Count: n.vidBusy + n.voiceBusy, Scene: first(func(s *studio.Scene) bool { return busy(s.Video) || busy(s.Audio) })},
		sideRow{Tone: gui.ToneWarning, Label: "Змінено після зйомки", Count: n.redo, Scene: first(func(s *studio.Scene) bool { return clipStale(s) || lineStale(p, s) })},
		sideRow{Tone: gui.ToneError, Label: "Помилки", Count: n.vidErr + n.voiceErr, Scene: first(func(s *studio.Scene) bool {
			return s.Video.Status == studio.StatusError || (p.Input.Voiceover && s.Audio.Status == studio.StatusError)
		})},
		sideRow{Tone: gui.ToneNeutral, Label: "Без кадру", Count: n.noFrames, Scene: first(func(s *studio.Scene) bool { return s.Frame.Path == "" })},
	)
	if id := st.side.clicked(gtx, rows); id != "" {
		u.ScrollTo = id
	}
	if st.side.tip.Clicked(gtx) {
		c.Navigate(core.Route{Page: core.PageSettings, Section: "connections"})
	}
	ws := []layout.Widget{
		func(gtx C) D {
			done := n.vidDone
			if n.stills {
				done = n.voiceDone
			}
			return st.side.card(gtx, th, "Стан сцен", fmt.Sprintf("%d з %d", done, n.scenes), rows)
		},
	}
	switch {
	case n.code:
		ws = append(ws, func(gtx C) D {
			return gui.TipCard{Icon: gui.IconChartNoAxesColumn, Title: "Анімація кодом",
				Text: "Кліпи малює код на цьому комп'ютері: кожен рівно як голос сцени, з елементами на словах диктора. " +
					"Змінив текст чи анімацію на кроці «Анімація» — кліп відрендериться знову. Сцена, де анімація не вийшла, змонтується з кадру."}.Layout(gtx, th, nil)
		})
	case n.stills:
		ws = append(ws, func(gtx C) D {
			return gui.TipCard{Icon: gui.IconImages, Title: "Тільки картинки",
				Text: "Відео не знімається: на монтажі кожна сцена — її кадр із рухом камери (наближення, панорама, Кен Бернс, дрейф), " +
					"завдовжки як її голос. Рух кожної сцени можна змінити на кроці «Монтаж»."}.Layout(gtx, th, nil)
		})
	case p.Input.ChainFrames && omniFlash(cfg):
		ws = append(ws, func(gtx C) D {
			return gui.TipCard{Icon: gui.IconLink, Title: "Ланцюжок кадрів",
				Text: "Кліпи знімаються по черзі: кожен продовжує останній кадр попередньої сцени, тож рух іде без стиків. " +
					"Перезнімеш сцену — наступна позначиться як змінена."}.Layout(gtx, th, nil)
		})
	case p.Input.ChainFrames:
		ws = append(ws, func(gtx C) D {
			return gui.TipCard{Icon: gui.IconLink, Title: "Ланцюжок чекає на Omni Flash",
				Text:   "Продовжувати кліп з останнього кадру вміє лише модель Omni Flash. Зараз обрано " + modelLabel(cfg) + ", тож кліпи зніматимуться як завжди.",
				Action: "Налаштування відео"}.Layout(gtx, th, &st.side.tip)
		})
	default:
		ws = append(ws, func(gtx C) D {
			return gui.TipCard{Icon: gui.IconLightbulb, Title: "Голос довший за кліп?",
				Text: "Не біда: на монтажі останній кадр потримається, поки звучить репліка. Хочеш руху до кінця — перезніми сцену з повільнішим рухом камери."}.Layout(gtx, th, nil)
		})
	}
	return sideStack(gtx, ws...)
}

// ---- next step ---------------------------------------------------------------------------

func (v *render) Next(c *core.Ctx, pv *core.ProjectView) core.Next {
	if pv == nil {
		return core.Next{}
	}
	p := &pv.P
	id := pv.ID
	if len(p.Scenes) == 0 {
		return core.Next{Icon: gui.IconListChecks, Title: "Спершу створи план",
			Text: "Відео знімається з кадрів сцен плану.", Do: func() { goStep(c, id, 2) }}
	}
	if pv.Running() {
		return core.Next{Icon: gui.IconClapperboard, Title: jobTitle(pv.RunningKind(), false) + "…",
			Text: firstText(p.Job.Message, "Студія працює — кліпи з'являтимуться по одному."), Busy: true}
	}
	cfg := c.E.Config().Get()
	stock := isStockMode(cfg, p)
	n := countClips(p, stock)
	st := v.state(id)
	switch {
	case st.batch.active():
		done, total := st.batch.progress()
		return core.Next{Icon: gui.IconRefreshCw, Title: st.batch.label + "…", Text: fmt.Sprintf("Готово %d з %d.", done, total), Busy: true}
	case n.withFrame == 0 && !stock:
		return core.Next{Icon: gui.IconImages, Title: "Спершу — кадри сцен",
			Text: "Відео знімається з кадрів. Намалюй їх на кроці «Розкадровка».", Do: func() { goStep(c, id, 3) }}
	case n.vidLeft > 0 || n.voiceLeft > 0:
		title := "Зняти відео й озвучити"
		if stock {
			title = "Знайти відео й озвучити"
		}
		stage := studio.StageRender
		switch {
		case n.voiceLeft == 0:
			if stock {
				title, stage = fmt.Sprintf("Знайти відео · %d", n.vidLeft), studio.StageVideos
			} else {
				title, stage = fmt.Sprintf("Зняти відео · %d", n.vidLeft), studio.StageVideos
			}
		case n.vidLeft == 0:
			title, stage = fmt.Sprintf("Озвучити · %d", n.voiceLeft), studio.StageVoices
		}
		text := "Кожен кліп витрачає кредити (" + modelLabel(cfg) + ")."
		if stock {
			text = "Пошук безкоштовних стокових відео без витрати кредитів ШІ."
		}
		if n.code {
			title = strings.ReplaceAll(title, "Зняти відео", "Відрендерити анімації")
			text = "Анімації малює код на цьому комп'ютері — без кредитів на відео."
		}
		return core.Next{Icon: gui.IconClapperboard, Title: title, Text: text, Do: func() { c.Run(id, stage, false, "") }}
	case n.redo > 0:
		title := fmt.Sprintf("Перезняти змінені · %d", n.redo)
		if stock {
			title = fmt.Sprintf("Оновити стокові · %d", n.redo)
		}
		return core.Next{Icon: gui.IconRefreshCw, Title: title,
			Text: "Промти чи текст змінились після зйомки.", Do: func() { v.redoStale(c, pv, st) }}
	}
	return core.Next{Icon: gui.IconScissors, Title: "Змонтувати",
		Text: "Усі кліпи й голоси готові — зберемо фільм.", Do: func() { goStep(c, id, 5) }}
}

// clipsCostLine is the header's line about what the clips cost.
func clipsCostLine(n clipStats, isStock ...bool) string {
	if len(isStock) > 0 && isStock[0] {
		return "Стокові відео безкоштовні — без витрати кредитів на ШІ."
	}
	if n.code {
		return "Анімації малює код на цьому комп'ютері — без кредитів на відео; ШІ лише пише їх під слова диктора."
	}
	if n.stills {
		return "Кожна сцена змонтується з кадру з рухом камери — без кредитів на відео."
	}
	return "Кожен кліп витрачає кредити акаунта."
}

// doneLine is the header's line when nothing is left to do.
func doneLine(n clipStats) string {
	if n.code {
		return "Усе відрендерено й озвучено"
	}
	if n.stills {
		return "Усе озвучено"
	}
	return "Усе знято й озвучено"
}
