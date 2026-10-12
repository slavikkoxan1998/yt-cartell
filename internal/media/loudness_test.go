package media

import (
	"strings"
	"testing"
)

func TestParseLoudnorm(t *testing.T) {
	out := `[Parsed_loudnorm_0 @ 0x1] 
{
	"input_i" : "-25.93",
	"input_tp" : "-6.12",
	"input_lra" : "4.30",
	"input_thresh" : "-36.20",
	"output_i" : "-14.05",
	"output_tp" : "-1.50",
	"output_lra" : "3.90",
	"output_thresh" : "-24.30",
	"normalization_type" : "dynamic",
	"target_offset" : "0.05"
}`
	m, ok := parseLoudnorm(out)
	if !ok || m.I != -25.93 || m.TP != -6.12 || m.Offset != 0.05 {
		t.Fatalf("%+v %v", m, ok)
	}
	// −25.93 → −14 needs +11.93 dB; the peak −6.12 allows only +4.62.
	if f := m.filter(); f != "volume=4.62dB" {
		t.Fatal(f)
	}
	if g := (loudMeasure{I: -20, TP: -12}).gain(); g != 6 {
		t.Fatalf("gain %v", g)
	}
	if _, ok := parseLoudnorm(strings.Replace(out, `"-25.93"`, `"-inf"`, 1)); ok {
		t.Fatal("silence must not be levelled")
	}
	if _, ok := parseLoudnorm("no report"); ok {
		t.Fatal("no report")
	}
}
