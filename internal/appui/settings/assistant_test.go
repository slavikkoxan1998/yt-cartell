package settings

import (
	"strings"
	"testing"

	"ytcartell/internal/studio"
)

// The assistant card is the first card of Підключення ("assistant" scrolls
// to it); offline it explains instead of probing; «Застосувати» saves a
// backup first and offers «Повернути як було».
func TestAssistantCard(t *testing.T) {
	c, h := demo(t)
	d := &driver{t: t, c: c, h: h, p: New(), section: "assistant"}
	d.settle(3)
	d.mustLabel("Помічник підключення")
	if tabOf("assistant") != 0 {
		t.Fatal("assistant is not under Підключення")
	}
	s := &d.p.assist
	s.run.Click()
	d.frame(nil)
	if !strings.Contains(s.err, "Встав сюди") {
		t.Fatalf("empty input: %q", s.err)
	}
	s.input.SetText("https://gateway.example.com ключ sk-demo-0123456789abcdef")
	s.run.Click()
	d.frame(nil)
	if s.busy || !strings.Contains(s.err, "демо-режимі") {
		t.Fatalf("offline: busy %v err %q", s.busy, s.err)
	}
	if got := c.E.Config().Get().Text.APIKey; got != "" && strings.Contains(got, "sk-demo-0123456789") {
		t.Fatal("the pasted text was saved")
	}

	// A plan (made elsewhere): its switches, «Застосувати», the backup.
	before := len(c.E.Config().Backups())
	s.err = ""
	s.plan = &studio.AssistPlan{Steps: []*studio.AssistStep{
		{Role: studio.RoleText, Target: "gateway.example.com · gemini-3.8-flash", Status: "ok", On: true},
		{Role: studio.RoleTTS, Target: "ElevenLabs", Status: "fail", Msg: "ElevenLabs: невірний API-ключ\nЗапит: GET https://api.elevenlabs.io/v1/user → 401 Unauthorized"},
	}}
	d.settle(2)
	d.mustLabel("Текст → gateway.example.com · gemini-3.8-flash")
	d.mustLabel("Застосувати (1)")
	s.apply.Click()
	d.frame(nil)
	for i := 0; i < 20 && s.applying; i++ {
		h.Flush()
		d.frame(nil)
	}
	h.Flush()
	d.frame(nil)
	if s.err != "" || !strings.Contains(s.applied, "Підключено: Текст") || s.plan != nil || s.backup == nil {
		t.Fatalf("apply: err %q applied %q backup %v", s.err, s.applied, s.backup)
	}
	if len(c.E.Config().Backups()) != before+1 {
		t.Fatal("no backup before applying")
	}
	d.settle(2)
	d.mustLabel("Повернути як було")
}
