package core

import (
	"fmt"
	"math"
	"strings"
	"time"

	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// Clock formats seconds as m:ss (h:mm:ss from an hour).
func Clock(sec float64) string {
	if sec < 0 || math.IsNaN(sec) {
		sec = 0
	}
	s := int(sec + 0.5)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// Duration formats a target length in words: "45 с", "1 хв", "2 хв 30 с".
func Duration(sec int) string {
	switch {
	case sec <= 0:
		return "—"
	case sec < 60:
		return fmt.Sprintf("%d с", sec)
	case sec%60 == 0:
		return fmt.Sprintf("%d хв", sec/60)
	}
	return fmt.Sprintf("%d хв %d с", sec/60, sec%60)
}

// Count is "<n> <word>" with the right Ukrainian plural, e.g.
// Count(3, "сцена", "сцени", "сцен") = "3 сцени".
func Count(n int, one, few, many string) string {
	return fmt.Sprintf("%d %s", n, gui.PluralUK(n, one, few, many))
}

// Updated is a friendly timestamp (unix ms): "щойно", "5 хв тому",
// "сьогодні, 14:20", "вчора, 09:05", "12 вер", "12 вер 2025".
func Updated(ms int64) string {
	if ms <= 0 {
		return ""
	}
	t := time.UnixMilli(ms).Local()
	now := time.Now()
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "щойно"
	case d < time.Hour:
		return fmt.Sprintf("%d хв тому", int(d.Minutes()))
	}
	y1, m1, d1 := now.Date()
	y2, m2, d2 := t.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return "сьогодні, " + t.Format("15:04")
	}
	yest := now.AddDate(0, 0, -1)
	if y, m, dd := yest.Date(); y == y2 && m == m2 && dd == d2 {
		return "вчора, " + t.Format("15:04")
	}
	months := [...]string{"січ", "лют", "бер", "кві", "тра", "чер", "лип", "сер", "вер", "жов", "лис", "гру"}
	s := fmt.Sprintf("%d %s", d2, months[m2-1])
	if y2 != y1 {
		s += fmt.Sprintf(" %d", y2)
	}
	return s
}

// StepNames are the five steps of a project, in order (index 0 = step 1).
var StepNames = [5]string{"Сценарій", "План", "Розкадровка", "Відео й озвучка", "Монтаж"}

// JobLabel is the Ukrainian name of a job kind ("Кадри сцен", "Автопілот" …).
func JobLabel(k studio.JobKind) string {
	switch k {
	case studio.JobPlan:
		return "План"
	case studio.JobRefs:
		return "Референси"
	case studio.JobFrames:
		return "Кадри сцен"
	case studio.JobVideos:
		return "Відео сцен"
	case studio.JobVoices:
		return "Озвучка"
	case studio.JobRender:
		return "Відео й озвучка"
	case studio.JobMontage:
		return "Монтаж"
	case studio.JobCover:
		return "Обкладинка"
	case studio.JobAutopilot:
		return "Автопілот"
	case "translate":
		return "Переклад"
	case "transcribe":
		return "Розпізнавання мови"
	case studio.JobRemake:
		return "Перезйомка"
	}
	return string(k)
}

// Progress is a job's 0–1 progress (0 when idle).
func Progress(j *studio.Job) float32 {
	if j == nil {
		return 0
	}
	return float32(max(0, min(100, j.Progress))) / 100
}

// PipelineCost is what a production mode generates, as one short line:
// "8 кадрів · 8 кліпів · 3 реф. · ~12 хв" (a mode without video says so).
func PipelineCost(c studio.PipelineCost) string {
	parts := []string{Count(c.Frames, "кадр", "кадри", "кадрів")}
	if c.Code > 0 { // «Анімація кодом»: no frames, animations drawn here
		parts = []string{Count(c.Code, "анімація", "анімації", "анімацій")}
	}
	if c.Clips > 0 {
		parts = append(parts, Count(c.Clips, "кліп", "кліпи", "кліпів"))
	} else if c.Code == 0 {
		parts = append(parts, "без відео")
	}
	if c.Refs > 0 {
		parts = append(parts, fmt.Sprintf("%d реф.", c.Refs))
	}
	return strings.Join(parts, " · ") + fmt.Sprintf(" · ~%d хв", c.Minutes)
}

// StepLabel is the name of step n (1–5) in the step tabs and the badge that
// marks what a lighter production mode («Режим виробництва») skips there:
// «Розкадровка · без референсів», «Озвучка · без відео».
func StepLabel(in studio.Input, n int) (label, badge string) {
	if n < 1 || n > len(StepNames) {
		return "", ""
	}
	label = StepNames[n-1]
	switch {
	case n == 3 && in.Pipeline == studio.PipelineStock:
		badge = "стоки (без кадрів)"
	case n == 4 && in.Pipeline == studio.PipelineStock:
		label, badge = "Стокові відео", "Pexels/Pixabay/Wikimedia"
	case n == 3 && in.CodeAnim():
		label, badge = "Анімація", "озвучка й код"
	case n == 4 && in.CodeAnim():
		label, badge = "Рендер", "без відеомоделі"
	case n == 3 && in.SkipRefs():
		badge = "без референсів"
	case n == 4 && in.StillsOnly():
		label, badge = "Озвучка", "відео пропущено"
		if in.SoundMode() != studio.AudioVoiceover {
			label = "Відео"
		}
	}
	return label, badge
}
