package produce

// «Промт переписано автоматично»: a frame, board, reference or clip whose
// prompt the studio rewrote after a content refusal (studio.PromptRewrite)
// says so on its card — what changed (a word diff), the original and the
// new prompt on «Показати», and «Повернути» (studio.Engine.RevertRewrite:
// the next generation uses the studio's own prompt again).

import (
	"strings"

	"ytcartell/internal/appui/core"
	"ytcartell/internal/gui"
	"ytcartell/internal/studio"
)

// rewriteNote is the card's notice of an automatic rewrite.
type rewriteNote struct {
	show, revert gui.Clickable
	open         bool
}

// update handles the notice's buttons: kind/itemID name the asset.
func (n *rewriteNote) update(gtx C, c *core.Ctx, projectID string, kind studio.RegenKind, itemID string) {
	if n.show.Clicked(gtx) {
		n.open = !n.open
	}
	if n.revert.Clicked(gtx) {
		c.Confirm(core.Confirm{
			Title: "Повернути свій промт?",
			Text:  "Студія забуде переписаний промт: наступна генерація піде з початковим. Фільтр може знову його відхилити — тоді студія перепише ще раз.",
			Icon:  gui.IconRotateCcw, OK: "Повернути",
			OnOK: func() {
				c.Do(func() error { return c.E.RevertRewrite(projectID, kind, itemID) }, func() {
					c.Toast(gui.ToneSuccess, "Початковий промт повернуто — перегенеруй, коли будеш готовий")
				})
				n.open = false
			},
		})
	}
}

// Layout draws the notice for rw (nothing for nil).
func (n *rewriteNote) Layout(gtx C, th *gui.Theme, rw *studio.PromptRewrite) D {
	if rw == nil {
		return D{}
	}
	text := rewriteSummary(rw)
	if n.open {
		var b strings.Builder
		if rw.Reason != "" {
			b.WriteString("Причина: " + rw.Reason + "\n\n")
		}
		b.WriteString("Було: " + strings.TrimSpace(rw.Original) + "\n\nСтало: " + strings.TrimSpace(rw.Prompt))
		text = b.String()
	}
	lines := 3
	if n.open {
		lines = 40
	}
	showTxt := "Показати промти"
	if n.open {
		showTxt = "Сховати"
	}
	return notice{Tone: gui.ToneInfo, Icon: gui.IconWandSparkles, Title: "Промт переписано автоматично",
		Text: text, Lines: lines, Small: true,
		Action: func(gtx C) D {
			return gui.Flow{Gap: gui.Sp12}.Layout(gtx,
				func(gtx C) D { return gui.Link{Text: showTxt, Style: th.Small}.Layout(gtx, th, &n.show) },
				func(gtx C) D {
					return gui.Link{Text: "Повернути свій", Trail: gui.IconRotateCcw, Style: th.Small}.Layout(gtx, th, &n.revert)
				},
			)
		}}.Layout(gtx, th)
}

// rewriteSummary is the short line of a rewrite: what the filter refused and
// the changed words ("− Taylor Swift → + a young singer · …").
func rewriteSummary(rw *studio.PromptRewrite) string {
	hunks := wordDiff(rw.Original, rw.Prompt)
	var parts []string
	for _, h := range hunks {
		switch {
		case h.del != "" && h.ins != "":
			parts = append(parts, "«"+clipWords(h.del, 8)+"» → «"+clipWords(h.ins, 8)+"»")
		case h.del != "":
			parts = append(parts, "прибрано «"+clipWords(h.del, 8)+"»")
		default:
			parts = append(parts, "додано «"+clipWords(h.ins, 8)+"»")
		}
		if len(parts) == 3 {
			break
		}
	}
	head := "Фільтр відхилив початковий промт — студія переписала його"
	if rw.Count > 1 {
		head += " (за " + core.Count(rw.Count, "спробу", "спроби", "спроб") + ")"
	}
	if len(parts) == 0 {
		return head + "."
	}
	return head + ": " + strings.Join(parts, "; ")
}

// hunk is one change of a word diff.
type hunk struct{ del, ins string }

// wordDiff lists the changed runs of words between a and b (LCS; prompts
// are a few hundred words at most — longer ones are compared by their
// first 600 words).
func wordDiff(a, b string) []hunk {
	x, y := strings.Fields(a), strings.Fields(b)
	if len(x) > 600 {
		x = x[:600]
	}
	if len(y) > 600 {
		y = y[:600]
	}
	n, m := len(x), len(y)
	lcs := make([][]int32, n+1)
	for i := range lcs {
		lcs[i] = make([]int32, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if x[i] == y[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var out []hunk
	var del, ins []string
	flush := func() {
		if len(del) > 0 || len(ins) > 0 {
			out = append(out, hunk{strings.Join(del, " "), strings.Join(ins, " ")})
			del, ins = nil, nil
		}
	}
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && x[i] == y[j]:
			flush()
			i, j = i+1, j+1
		case j < m && (i == n || lcs[i][j+1] >= lcs[i+1][j]):
			ins = append(ins, y[j])
			j++
		default:
			del = append(del, x[i])
			i++
		}
	}
	flush()
	return out
}

// clipWords keeps the first n words (with "…").
func clipWords(s string, n int) string {
	w := strings.Fields(s)
	if len(w) <= n {
		return strings.Join(w, " ")
	}
	return strings.Join(w[:n], " ") + "…"
}
