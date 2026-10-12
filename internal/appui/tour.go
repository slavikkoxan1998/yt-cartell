package appui

import (
	"ytcartell/internal/appui/home"
	"ytcartell/internal/gui"
)

// tourSteps is the first-run tour (GUI.md → Screens → 6): Головна → the
// story box → profiles → «Автопілот» → «Черга» on the rail. It starts by
// itself while config.UI.TourDone is false; Settings replays it through
// Host.StartTour.
func tourSteps() []gui.TourStep {
	return []gui.TourStep{
		{
			Title: "Вітаємо в YT Cartell Studio!",
			Text:  "Тут історія стає відео: сценарій, кадри, озвучка й монтаж — в одному місці. Покажемо головне за хвилину.",
		},
		{
			Anchor: home.AnchorStory,
			Title:  "Почни з історії",
			Text:   "Встав текст або коротко опиши ідею. Можна імпортувати .txt — студія сама розіб'є історію на сцени.",
		},
		{
			Anchor: home.AnchorProfiles,
			Title:  "Профілі — готові налаштування",
			Text:   "Профіль задає формат, стиль, голос і темп. Обери потрібний або натисни «Керувати», щоб створити свій.",
		},
		{
			Anchor: home.AnchorAutopilot,
			Title:  "Автопілот зробить усе сам",
			Text:   "Одна кнопка — і студія пройде всі п'ять кроків: план, кадри, відео, озвучку й монтаж. «Покроково» — щоб керувати кожним кроком.",
		},
		{
			Anchor: AnchorQueue,
			Title:  "Черга",
			Text:   "Тут видно все, що генерується, в усіх проєктах. Проєкт можна закрити — робота триває, а результат чекатиме в черзі.",
		},
	}
}
