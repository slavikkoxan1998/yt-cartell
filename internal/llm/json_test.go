package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExtractJSON(t *testing.T) {
	ok := []struct{ in, want string }{
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{"```JSON\n{}\n```", `{}`},
		{"```\n[1, 2]\n```", `[1, 2]`},
		{string(rune(0xFEFF)) + "  {\"a\":1}  ", `{"a":1}`},
		{"Sure! Here it is:\n{\"a\":{\"b\":[1]}}\nHope it helps.", `{"a":{"b":[1]}}`},
		{"Here:\n```json\n{\"a\":1}\n```\nNote: use {x} carefully", `{"a":1}`},
		{"prefix {\"s\":\"}{][\\\"\"} suffix }", `{"s":"}{][\""}`},
		{"[{\"a\":1},{\"b\":2}] trailing ]", `[{"a":1},{"b":2}]`},
		{"note [draft] then {\"ok\":true}", `{"ok":true}`},
		{"Відповідь: [\"пам’ять\", \"сон\"] — готово", `["пам’ять", "сон"]`},
		{"42", `42`},
	}
	for _, c := range ok {
		got, err := ExtractJSON(c.in)
		if err != nil {
			t.Errorf("ExtractJSON(%q) error: %v", c.in, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("ExtractJSON(%q) = %s, want %s", c.in, got, c.want)
		}
		if !json.Valid(got) {
			t.Errorf("ExtractJSON(%q) returned invalid JSON %s", c.in, got)
		}
	}

	bad := []struct{ in, msg string }{
		{"no json here", "не містить JSON"},
		{"", "не містить JSON"},
		{"```json\n```", "не містить JSON"},
		{"{\"a\": 1", "незавершений"},
		{"{\"a\": } and }", "некоректний"},
	}
	for _, c := range bad {
		_, err := ExtractJSON(c.in)
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("ExtractJSON(%q) err = %v, want containing %q", c.in, err, c.msg)
		}
	}
}

func TestBalancedEnd(t *testing.T) {
	s := `x{"a":[1,{"b":"]"}]}y`
	if end := balancedEnd(s, 1); s[1:end] != `{"a":[1,{"b":"]"}]}` {
		t.Fatalf("balancedEnd = %d", end)
	}
	if balancedEnd(`{"a":[}`, 0) != -1 || balancedEnd(`{"a":1`, 0) != -1 {
		t.Fatal("unbalanced must be -1")
	}
}

func TestExtractJSONPathological(t *testing.T) {
	// Many unmatched openers must not blow up (bounded scans).
	in := strings.Repeat("{[", 50000) + "no json"
	if _, err := ExtractJSON(in); err == nil {
		t.Fatal("expected error")
	}
}
