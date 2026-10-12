package studio

// The human side of an export package (export.go): README.txt with the
// import steps for the chosen editor and what maps only approximately, and
// edit-list.csv — the cut list any editor (or a person) can follow.

import (
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"ytcartell/internal/media"
	"ytcartell/internal/nle"
)

// bom starts the text files: Notepad and Excel then read them as UTF-8.
const bom = "\uFEFF"

// editRow is one line of the cut list.
type editRow struct {
	Track      string // V1, V2, A (voice), M (music)
	Scene      int
	File       string
	In, Out    float64 // source seconds (0, 0 for pictures)
	Start, Len float64 // on the film
	Transition string  // the transition into the scene ("" = cut / first)
	Note       string
}

// textRow is an on-screen text for the instructions (CapCut, «Лише файли»).
type textRow struct {
	Scene      int
	File       string
	Start, Len float64
}

// clockMs is "00:01:23.458".
func clockMs(s float64) string {
	if s < 0 || math.IsNaN(s) {
		s = 0
	}
	ms := int64(math.Round(s * 1000))
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, ms/60000%60, ms/1000%60, ms%1000)
}

// clockShort is "1:23" / "1:02:03".
func clockShort(s float64) string {
	n := int(math.Round(s))
	if n >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", n/3600, n/60%60, n%60)
	}
	return fmt.Sprintf("%d:%02d", n/60, n%60)
}

// secUK is "3,2 с".
func secUK(s float64) string {
	return strings.Replace(fmt.Sprintf("%.1f", s), ".", ",", 1) + " с"
}

// editList is the cut list as CSV (UTF-8 with BOM, ";" — Excel opens it as is).
func (x *exporter) editList() []byte {
	rows := append([]editRow(nil), x.rows...)
	order := map[string]int{"V1": 0, "V2": 1, "A": 2, "M": 3}
	sort.SliceStable(rows, func(a, b int) bool {
		if order[rows[a].Track] != order[rows[b].Track] {
			return order[rows[a].Track] < order[rows[b].Track]
		}
		return rows[a].Start < rows[b].Start
	})
	esc := func(s string) string {
		if strings.ContainsAny(s, ";\"\n") {
			return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
		}
		return s
	}
	var b strings.Builder
	b.WriteString(bom)
	b.WriteString("Доріжка;Сцена;Файл;Вхід у файлі;Вихід у файлі;Початок на таймлайні;Тривалість;Перехід перед сценою;Примітка\r\n")
	names := map[string]string{"V1": "V1 сцени", "V2": "V2 текст", "A": "голос", "M": "музика"}
	for _, r := range rows {
		scene, in, out := "", "", ""
		if r.Scene > 0 {
			scene = fmt.Sprint(r.Scene)
		}
		if r.Out > r.In {
			in, out = clockMs(r.In), clockMs(r.Out)
		}
		fields := []string{names[r.Track], scene, r.File, in, out, clockMs(r.Start), clockMs(r.Len), r.Transition, r.Note}
		for i, f := range fields {
			if i > 0 {
				b.WriteByte(';')
			}
			b.WriteString(esc(f))
		}
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

// readme is README.txt (UTF-8 with BOM, CRLF — Notepad shows it right).
func (x *exporter) readme(t *nle.Timeline, res ExportResult) []byte {
	var lines []string
	add := func(s ...string) { lines = append(lines, s...) }
	title := strings.TrimSpace(x.p.Title)
	if title == "" {
		title = "Без назви"
	}
	add("YT Cartell Studio — експорт для " + x.tgt.Label)
	add("Проєкт: "+title, "Створено: "+time.Now().Format("02.01.2006 15:04"))
	add(fmt.Sprintf("Фільм: %s · %d×%d · %s к/с · %d %s",
		clockShort(x.total), x.W, x.H, x.rate, len(x.slots), pluralUK(len(x.slots), "сцена", "сцени", "сцен")))
	if res.Partial {
		add("Увага: не все було готово — " + x.ready.Missing() + ".")
	}
	add("")
	add("ЩО В ПАПЦІ")
	files := x.packageFiles()
	add(files...)
	add("")
	add("ЯК ВІДКРИТИ В " + strings.ToUpper(x.tgt.Label))
	for i, s := range x.tgt.Steps {
		add(fmt.Sprintf("  %d. %s", i+1, s))
	}
	if x.tgt.Timeline {
		add(fmt.Sprintf("  Частота кадрів фільму — %s к/с, кадр %d×%d: якщо редактор спитає про налаштування проєкту, вибери такі самі.", x.rate, x.W, x.H))
	}
	if x.tgt.Note != "" {
		add("", "  "+x.tgt.Note)
	}
	if len(x.texts) > 0 && !x.tgt.Timeline {
		add("", "ТЕКСТ У КАДРІ (прозорі PNG — поклади на доріжку над сценами)")
		for _, r := range x.texts {
			add(fmt.Sprintf("  %s — з %s, %s", filepath.Base(r.File), clockMs(r.Start), secUK(r.Len)))
		}
	}
	if x.tgt.Timeline {
		add("", "ДОРІЖКИ")
		for _, tr := range t.Video {
			add("  " + tr.Name)
		}
		for _, tr := range t.Audio {
			line := "  " + tr.Name
			if tr.SceneSound {
				line += fmt.Sprintf(" (гучність як у фільмі: %d%%)", int(math.Round(clamp01(x.opts.AmbientVolume)*100)))
			}
			if len(tr.Clips) > 0 && tr.Clips[0].Role == nle.RoleMusic {
				line += fmt.Sprintf(" (гучність %d%%", int(math.Round(clamp01(x.opts.MusicVolume)*100)))
				if x.opts.Ducking {
					line += ", під голосом тихіше"
				}
				line += ", затухання в кінці)"
			}
			add(line)
		}
	}
	add("", "ЩО ПЕРЕНЕСЕНО")
	switch {
	case x.tgt.Timeline:
		add("  Точно: порядок і довжина сцен, точки входу/виходу кліпів, утримання останнього кадру,",
			"  розчинення й затемнення між сценами (тривалість як у фільмі), місця реплік голосу,",
			"  субтитри (subtitles.srt — ті самі, що робить монтаж).")
	case x.opt.Target == ExportCapCut:
		add("  Точно: порядок і довжина сцен (кожна вже нарізана під фільм, з рухом кадру й утриманням),",
			"  голос і музика (stems — як у фільмі, з 0:00), субтитри (subtitles.srt — ті самі, що робить монтаж).")
	default:
		add("  Точно: файли, точки входу/виходу й місця на таймлайні (edit-list.csv), голос і музика",
			"  (stems — як у фільмі, з 0:00), субтитри (subtitles.srt — ті самі, що робить монтаж).")
	}
	if len(res.Notes) > 0 {
		add("  Приблизно:")
		for _, n := range res.Notes {
			add("  • " + n)
		}
	}
	if x.tgt.Timeline {
		add("", "ЯКЩО ПЕРЕНЕСЕШ ПАПКУ")
		add("  Файл таймлайну посилається на media за повним шляхом:",
			"  "+filepath.Join(x.out, "media"),
			"  Якщо перемістиш, скопіюєш на інший комп'ютер або розпакуєш ZIP деінде — редактор",
			"  покаже файли як відсутні: вибери «Relink / Знайти файли» і вкажи папку media.")
		if x.opt.Target == ExportKdenlive {
			add("  timeline.mlt (Shotcut) посилається на media відносно себе — його можна переносити разом із папкою.")
		}
	}
	add("", "Зроблено в YT Cartell Studio.")
	return []byte(bom + strings.Join(lines, "\r\n") + "\r\n")
}

// packageFiles describes the folder's contents.
func (x *exporter) packageFiles() []string {
	var out []string
	add := func(name, what string) { out = append(out, fmt.Sprintf("  %-22s %s", name, what)) }
	switch x.opt.Target {
	case ExportResolve:
		add("timeline.xml", "таймлайн для DaVinci Resolve (Final Cut Pro 7 XML) — найточніший")
		add("timeline.fcpxml", "той самий таймлайн у FCPXML 1.10")
		add("timeline.edl", "запасний варіант: лише сцени (CMX 3600 EDL)")
	case ExportFinalCut:
		add("timeline.fcpxmld", "таймлайн для Final Cut Pro (пакет FCPXML 1.10)")
		add("timeline.fcpxml", "той самий таймлайн одним файлом")
	case ExportPremiere:
		add("timeline.xml", "таймлайн для Premiere Pro (Final Cut Pro 7 XML)")
	case ExportKdenlive:
		add("timeline.otio", "таймлайн OpenTimelineIO (Kdenlive 25.04+)")
		add("timeline.mlt", "проєкт Shotcut (MLT XML)")
	case ExportOTIO:
		add("timeline.otio", "таймлайн OpenTimelineIO")
	}
	if x.opt.Target == ExportCapCut {
		add("media/*_scene.mp4", "сцени, вже нарізані точно під фільм, по порядку")
	} else {
		add("media/*_scene.*", "кліпи сцен — як їх згенеровано (обрізку тримає таймлайн)")
		add("media/*_frame.jpg", "кадри сцен без відео")
		add("media/*_hold.jpg", "останній кадр кліпу, коли сцена його утримує")
	}
	if x.opt.Voice && x.ready.Voices > 0 {
		add("media/*_voice.*", "репліки голосу")
	}
	if x.opt.Overlays && len(x.texts) > 0 || x.opt.Overlays && x.ready.Overlays {
		add("media/*_text.png", "текст у кадрі (прозорий PNG на весь кадр)")
	}
	if x.opt.Music && x.ready.Music {
		add("media/music.*", "музика")
	}
	if x.opt.Stems {
		if x.opt.Voice && x.ready.Voices > 0 {
			add("stems/voice.wav", "увесь голос однією доріжкою — постав з 0:00")
		}
		if x.opt.Music && x.ready.Music {
			add("stems/music.wav", "музика як у фільмі (гучність, приглушення, затухання) — з 0:00")
		}
	}
	if x.opt.Subtitles && x.ready.Subtitles {
		add("subtitles.srt", "субтитри")
	}
	if x.opt.Cover && x.ready.Cover {
		add("cover.jpg", "обкладинка для YouTube")
	}
	add("edit-list.csv", "монтажний лист: файл, вхід/вихід, місце на таймлайні")
	return out
}

var _ = media.FPS
