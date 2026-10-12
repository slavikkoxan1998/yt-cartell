package studio

// The text endpoint's checks: «Перевірити» of Settings → Текст і сценарій and
// the connection assistant. llm.Client.Check tries the address forms (+/v1,
// /api/v1, /openai/v1), the auth headers and a one-word prompt; a form or a
// header that works is stored in the settings, so the next generation goes
// straight there. A generation that finds another form by itself (llm
// OnResolved) stores it too.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ytcartell/internal/config"
	"ytcartell/internal/llm"
)

// wireText stores an address form the text client found at run time (the
// configured one answered 404): only while the settings still hold the old one.
func (e *Engine) wireText() {
	e.text.OnResolved(func(from, to string) {
		go func() {
			_ = e.cfg.Update(func(c *config.Config) {
				if strings.EqualFold(strings.TrimRight(strings.TrimSpace(c.Text.BaseURL), "/"), strings.TrimRight(from, "/")) {
					c.Text.BaseURL = to
				}
			})
		}()
	})
}

// testText is TestText: the Google provider gets its tiny prompt; the others
// the full llm Check, and a fixed address / auth header is saved.
func (e *Engine) testText(ctx context.Context) (bool, string) {
	c := e.cfg.Get()
	if c.Text.Provider == "google" {
		t0 := time.Now()
		reply, err := e.text.Text(ctx, llm.Request{Prompt: "Reply with exactly: OK", Temperature: 0, Timeout: 60 * time.Second})
		if err != nil {
			return false, clipRunes(errText(err), 900)
		}
		return true, fmt.Sprintf("Відповідь за %d мс (%s): %s", time.Since(t0).Milliseconds(), c.Google.StoryModel, clipRunes(strings.TrimSpace(reply), 40))
	}
	p, err := e.CheckText(ctx, c.Text, true)
	fixes := e.applyTextProbe(c.Text, p)
	if err != nil {
		msg := errText(err)
		if fixes != "" {
			msg = fixes + "\n" + msg
		}
		return false, clipRunes(msg, 1200)
	}
	msg := p.Summary()
	if fixes != "" {
		msg = fixes + "\n" + msg
	}
	return true, msg
}

// CheckText checks a text connection (it need not be the saved one — the
// assistant checks its proposals): address forms, auth, models, and with chat
// a one-word prompt.
func (e *Engine) CheckText(ctx context.Context, t config.Text, chat bool) (llm.Probe, error) {
	return e.text.Check(ctx, llm.ConnOf(t), chat)
}

// TextModelsOf lists the models of a text connection that may not be saved yet.
func (e *Engine) TextModelsOf(ctx context.Context, t config.Text) ([]string, error) {
	return e.text.ModelsOf(ctx, llm.ConnOf(t))
}

// applyTextProbe stores what a check found out about the saved connection —
// the address form that answered, the auth header that worked, an Azure
// api-version — and says so ("" = nothing changed). Only fields still as
// checked are touched.
func (e *Engine) applyTextProbe(was config.Text, p llm.Probe) string {
	if p.Conn.BaseURL == "" {
		return ""
	}
	var notes []string
	base := strings.TrimRight(strings.TrimSpace(was.BaseURL), "/")
	newBase := p.Conn.BaseURL != "" && !strings.EqualFold(base, p.Conn.BaseURL)
	newAuth := p.AuthSet && p.Conn.Auth != was.Auth
	newVer := p.Conn.APIVersion != "" && p.Conn.APIVersion != was.APIVersion
	if !newBase && !newAuth && !newVer {
		return ""
	}
	_ = e.cfg.Update(func(c *config.Config) {
		if c.Text.Provider != was.Provider || !strings.EqualFold(strings.TrimRight(strings.TrimSpace(c.Text.BaseURL), "/"), base) {
			return
		}
		if newBase {
			c.Text.BaseURL = p.Conn.BaseURL
		}
		if newAuth && c.Text.Auth == was.Auth {
			c.Text.Auth = p.Conn.Auth
		}
		if newVer {
			c.Text.APIVersion = p.Conn.APIVersion
		}
	})
	if newBase {
		notes = append(notes, "Адресу виправлено: "+firstNonEmptyStr(base, "—")+" → "+p.Conn.BaseURL)
	}
	if newAuth {
		notes = append(notes, "Ключ працює в заголовку "+authLabel(p.Conn.Auth)+" — збережено")
	}
	return strings.Join(notes, "\n")
}

func authLabel(a string) string {
	switch a {
	case "x-api-key":
		return "x-api-key"
	case "api-key":
		return "api-key"
	case "none":
		return "(без ключа)"
	}
	return "Authorization: Bearer"
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
