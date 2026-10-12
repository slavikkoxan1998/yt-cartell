package reference

// The breakdown as the text the user reads and edits in step 1 («Розбір
// прикладу»). The new script is written from this text, so whatever the user
// changes here (a scene, the hook, the tone) goes into the remake.

import (
	"fmt"
	"strings"
)

// Text renders b in Ukrainian. lang names a language code ("en" →
// "англійська"; nil or "" = the code itself).
func (b Breakdown) Text(lang func(code string) string) string {
	var w strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&w, format+"\n", a...) }
	field := func(label, v string) {
		if v = strings.TrimSpace(v); v != "" {
			line("%s: %s", label, v)
		}
	}
	head := []string{}
	if b.Title != "" {
		head = append(head, "«"+b.Title+"»")
	}
	if b.Duration > 0 {
		head = append(head, Clock(b.Duration))
	}
	if b.Aspect != "" {
		head = append(head, b.Aspect)
	}
	if b.Language != "" {
		name := b.Language
		if lang != nil {
			if n := lang(b.Language); n != "" {
				name = n
			}
		}
		head = append(head, "мова: "+name)
	}
	if len(head) > 0 {
		line("%s", strings.Join(head, " · "))
	}
	field("Про що", b.Summary)
	if b.Hook.What != "" {
		w.WriteByte('\n')
		end := b.Hook.End
		if end <= 0 {
			end = 8
		}
		line("Хук (0:00–%s): %s", Clock(end), b.Hook.What)
		field("Чому чіпляє", b.Hook.Why)
	}
	if len(b.Structure) > 0 {
		w.WriteByte('\n')
		line("Структура:")
		for _, s := range b.Structure {
			t := "• " + Clock(s.Start) + "–" + Clock(s.End) + " · " + s.Name
			if s.Purpose != "" {
				t += " — " + s.Purpose
			}
			line("%s", t)
		}
	}
	if len(b.Scenes) > 0 {
		w.WriteByte('\n')
		line("Сцени (%d):", len(b.Scenes))
		for i, s := range b.Scenes {
			t := fmt.Sprintf("%d. %s–%s", i+1, Clock(s.Start), Clock(s.End))
			if s.Shot != "" {
				t += " · " + s.Shot
			}
			line("%s", t)
			sub := func(label, v string) {
				if v = strings.TrimSpace(v); v != "" {
					line("   %s: %s", label, v)
				}
			}
			sub("Кадр", s.Visual)
			if s.Text != "" {
				sub("Текст на екрані", "«"+s.Text+"»")
			}
			if s.Speech != "" {
				who := speakerLabel(s.Speaker)
				sub(who, "«"+s.Speech+"»")
			}
			sub("Звук", s.Sound)
			if tr := strings.TrimSpace(s.Transition); tr != "" && !strings.EqualFold(tr, "cut") && tr != "склейка" {
				sub("Перехід", tr)
			}
		}
	}
	w.WriteByte('\n')
	field("Подача", b.Narration)
	field("Музика й звук", b.Music)
	field("Візуальний стиль", b.VisualStyle)
	field("Палітра", b.Palette)
	pace := ""
	if b.CutsPerMin > 0 {
		pace = fmt.Sprintf("%s склейки/хв", num(b.CutsPerMin))
	}
	if b.AvgShot > 0 {
		if pace != "" {
			pace += ", "
		}
		pace += fmt.Sprintf("план у середньому %s с", num(b.AvgShot))
	}
	if b.Pacing != "" {
		if pace != "" {
			pace += ". "
		}
		pace += b.Pacing
	}
	field("Темп", pace)
	var who []string
	switch b.People {
	case "characters":
		who = append(who, "люди чи персонажі в кадрі")
	case "nopeople":
		who = append(who, "без людей у кадрі")
	case "motion":
		who = append(who, "моушн-графіка")
	case "mixed":
		who = append(who, "мішано: люди, предмети, графіка")
	}
	switch b.AudioMode {
	case "voiceover":
		who = append(who, "голос за кадром")
	case "invideo":
		who = append(who, "говорять у кадрі")
	case "silent":
		who = append(who, "без голосу — музика й атмосфера")
	}
	field("Формат", strings.Join(who, " · "))
	if len(b.Why) > 0 {
		line("Чому працює:")
		for _, x := range b.Why {
			line("• %s", x)
		}
	}
	return strings.TrimSpace(w.String())
}

// speakerLabel is "Говорить (за кадром)" etc.
func speakerLabel(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "voice-over", "voiceover", "narrator", "за кадром", "диктор":
		return "Голос за кадром"
	case "on-camera", "on camera", "у кадрі", "в кадрі":
		return "Говорять у кадрі"
	}
	return "Говорить " + strings.TrimSpace(s)
}

// num is "2,5" / "24".
func num(x float64) string {
	s := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", x), "0"), ".")
	return strings.Replace(s, ".", ",", 1)
}
