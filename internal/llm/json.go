package llm

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
)

var (
	leadFenceRe  = regexp.MustCompile("(?i)^```(?:json)?\\s*")
	trailFenceRe = regexp.MustCompile("\\s*```$")
	fenceBlockRe = regexp.MustCompile("(?is)```(?:json)?[ \\t]*\\r?\\n?(.*?)```")
)

// maxScans bounds the balanced-bracket search on pathological replies.
const maxScans = 32

// ExtractJSON pulls a JSON value out of a model reply. It tolerates ```json
// fences, prose before/after the JSON and a fenced block in the middle of
// prose. Returns the raw JSON bytes.
func ExtractJSON(raw string) ([]byte, error) {
	t := strings.TrimSpace(strings.TrimPrefix(raw, string(rune(0xFEFF))))
	t = strings.TrimSpace(trailFenceRe.ReplaceAllString(leadFenceRe.ReplaceAllString(t, ""), ""))
	if json.Valid([]byte(t)) && t != "" {
		return []byte(t), nil
	}
	// A fenced block anywhere: "Here you go:\n```json\n{…}\n```\nEnjoy!".
	for _, m := range fenceBlockRe.FindAllStringSubmatch(raw, -1) {
		if c := strings.TrimSpace(m[1]); c != "" && json.Valid([]byte(c)) {
			return []byte(c), nil
		}
	}
	// The first balanced object/array (string-aware scan).
	scans := 0
	for i := 0; i < len(t) && scans < maxScans; i++ {
		if t[i] != '{' && t[i] != '[' {
			continue
		}
		scans++
		if end := balancedEnd(t, i); end > i {
			if c := t[i:end]; json.Valid([]byte(c)) {
				return []byte(c), nil
			}
		}
	}
	// Last resort (llm.ts): first opener … last matching closer.
	start := firstOf(t, '[', '{')
	if start < 0 {
		return nil, errors.New("відповідь моделі не містить JSON")
	}
	closer := byte('}')
	if t[start] == '[' {
		closer = ']'
	}
	end := strings.LastIndexByte(t, closer)
	if end <= start {
		return nil, errors.New("відповідь моделі містить незавершений JSON")
	}
	c := t[start : end+1]
	var v any
	if err := json.Unmarshal([]byte(c), &v); err != nil {
		return nil, fmt.Errorf("відповідь моделі містить некоректний JSON: %v", err)
	}
	return []byte(c), nil
}

func firstOf(s string, a, b byte) int {
	i, j := strings.IndexByte(s, a), strings.IndexByte(s, b)
	switch {
	case i < 0:
		return j
	case j < 0:
		return i
	case i < j:
		return i
	default:
		return j
	}
}

// balancedEnd returns the index just past the bracket that closes s[start],
// skipping brackets inside strings; -1 when unbalanced.
func balancedEnd(s string, start int) int {
	var stack []byte
	inStr, esc := false, false
	for i := start; i < len(s); i++ {
		ch := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case ch == '\\':
				esc = true
			case ch == '"':
				inStr = false
			}
			continue
		}
		switch ch {
		case '"':
			inStr = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) == 0 || stack[len(stack)-1] != ch {
				return -1
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// decodeInto unmarshals like JSON.parse + the TS asArray habit:
//   - out is a slice but the reply is {"key":[…]} → the first array value;
//   - a wrong type deep inside (e.g. "index":"3") keeps the rest of the data
//     instead of failing the whole plan, as the loosely-typed TS code did.
func decodeInto(b []byte, out any) error {
	err := json.Unmarshal(b, out)
	if err == nil {
		return nil
	}
	var te *json.UnmarshalTypeError
	if !errors.As(err, &te) {
		return fmt.Errorf("не вдалося розібрати JSON від моделі: %w", err)
	}
	if te.Field != "" {
		return nil // nested mismatch: everything else is filled in
	}
	if wantsSlice(out) && firstNonSpace(b) == '{' {
		if inner := firstArrayValue(b); inner != nil {
			if err2 := json.Unmarshal(inner, out); err2 == nil {
				return nil
			} else if errors.As(err2, &te) && te.Field != "" {
				return nil
			}
		}
	}
	return fmt.Errorf("не вдалося розібрати JSON від моделі: %w", err)
}

func wantsSlice(out any) bool {
	v := reflect.ValueOf(out)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return false
	}
	k := v.Elem().Kind()
	return k == reflect.Slice || k == reflect.Array
}

func firstNonSpace(b []byte) byte {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return 0
	}
	return b[0]
}

// firstArrayValue returns the first top-level member value of an object that
// is an array, in document order.
func firstArrayValue(b []byte) json.RawMessage {
	dec := json.NewDecoder(bytes.NewReader(b))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	for dec.More() {
		if _, err := dec.Token(); err != nil { // key
			return nil
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil
		}
		if firstNonSpace(v) == '[' {
			return v
		}
	}
	return nil
}
