package httpdiag

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestDetailAndRedaction(t *testing.T) {
	const key = "sk-live-0123456789abcdefSECRET"
	d := Detail("post", "https://user:pw@api.test/v1/chat?key="+key+"&x=1", 404, []byte(`{"error":"not_found","echo":"`+key+`"}`), "", key)
	for _, want := range []string{"Запит: POST https://api.test/v1/chat?", "x=1", "→ 404 Not Found", `Відповідь: {"error":"not_found"`, "Підказка: "} {
		if !strings.Contains(d, want) {
			t.Errorf("detail lacks %q:\n%s", want, d)
		}
	}
	if strings.Contains(d, key) || strings.Contains(d, "pw@") {
		t.Fatalf("secret leaked:\n%s", d)
	}
	if b := Body([]byte("<!DOCTYPE html><html><head><title>Not Found</title></head><body>x</body></html>"), 100); b != "HTML-сторінка «Not Found» (не API)" {
		t.Fatalf("html body %q", b)
	}
	if got := Redact("token AIzaSyDEMO_not_a_real_key_0000000000001 here"); strings.Contains(got, "AIzaSyDEMO_not") {
		t.Fatalf("google key %q", got)
	}
	if Hint(404, `{"error":{"code":"model_not_found"}}`) == Hint(404, `{"error":"not_found"}`) {
		t.Fatal("a missing model and a missing path get the same hint")
	}
}

func TestTrace(t *testing.T) {
	ctx, tr := WithTrace(context.Background())
	Record(ctx, "GET", "https://a.test/models", 200, []byte(`{}`), nil)
	Record(ctx, "POST", "https://a.test/chat", 401, []byte(`{"error":"bad key"}`), nil)
	Record(context.Background(), "GET", "https://b.test", 500, nil, nil) // no trace: ignored
	if c, ok := tr.Last(); !ok || c.Status != 401 {
		t.Fatalf("last %+v", c)
	}
	msg := tr.Append("Ключ не прийнято")
	if !strings.HasPrefix(msg, "Ключ не прийнято\nЗапит: POST https://a.test/chat → 401 Unauthorized") {
		t.Fatalf("append %q", msg)
	}
	if tr.Append(msg) != msg {
		t.Fatal("appended twice")
	}
	ctx2, tr2 := WithTrace(context.Background())
	Record(ctx2, "GET", "https://down.test/x", 0, nil, errors.New("connection refused"))
	if d := tr2.Detail(); !strings.Contains(d, "→ немає відповіді (connection refused)") {
		t.Fatalf("no answer %q", d)
	}
	if Mask("sk-ant-api03-abcdefghijklmnop") != "sk-…mnop" {
		t.Fatalf("mask %q", Mask("sk-ant-api03-abcdefghijklmnop"))
	}
}
