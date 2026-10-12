package nle

// A small ordered JSON writer for the .otio file: keys keep the order they
// are written in and floating-point fields always carry a decimal point
// (OpenTimelineIO's C++ reader wants doubles where its schema has them).

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

type kv struct {
	k string
	v any
}

// obj is a JSON object with ordered keys.
type obj []kv

// dbl is a number written as a double ("24.0").
type dbl float64

func writeJSON(b *strings.Builder, v any, depth int) {
	ind := strings.Repeat("    ", depth)
	switch x := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		b.WriteString(strconv.FormatBool(x))
	case int:
		b.WriteString(strconv.Itoa(x))
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case dbl:
		f := float64(x)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			f = 0
		}
		s := strconv.FormatFloat(f, 'g', 17, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		b.WriteString(s)
	case string:
		e, _ := json.Marshal(x)
		b.Write(e)
	case obj:
		if len(x) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, p := range x {
			e, _ := json.Marshal(p.k)
			b.WriteString(ind + "    ")
			b.Write(e)
			b.WriteString(": ")
			writeJSON(b, p.v, depth+1)
			if i < len(x)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(ind + "}")
	case []any:
		if len(x) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, e := range x {
			b.WriteString(ind + "    ")
			writeJSON(b, e, depth+1)
			if i < len(x)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(ind + "]")
	default:
		e, _ := json.Marshal(x)
		b.Write(e)
	}
}
