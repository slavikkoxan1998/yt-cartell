package studio

// The built-in renderer of «Анімація кодом»: internal/codeanim (the embedded
// huashu-art-motion engine driven through a local browser).

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"ytcartell/internal/codeanim"
)

func init() {
	newCodeRenderer = func() CodeRenderer { return codeanimRenderer{} }
}

// codeanimRenderer adapts package codeanim to CodeRenderer.
type codeanimRenderer struct{}

func (codeanimRenderer) Grammars() []CodeGrammar {
	gs := codeanim.Grammars()
	out := make([]CodeGrammar, 0, len(gs))
	for _, g := range gs {
		out = append(out, grammarOf(g))
	}
	return out
}

func (codeanimRenderer) Available(ctx context.Context) (string, error) {
	return codeanim.Available(ctx)
}

func (codeanimRenderer) Render(ctx context.Context, spec []byte, out string, opt CodeRenderOptions) error {
	useFFmpeg(opt.FFmpeg)
	return codeanim.Render(ctx, spec, out, codeanim.Options{AssetsDir: opt.AssetsDir, Progress: opt.Progress})
}

func (codeanimRenderer) Still(ctx context.Context, spec []byte, at float64, outPNG string, opt CodeRenderOptions) error {
	return codeanim.Still(ctx, spec, at, outPNG, codeanim.Options{AssetsDir: opt.AssetsDir})
}

var ffmpegMu sync.Mutex

// useFFmpeg points the renderer at the FFmpeg the studio located (the path
// set in Settings wins over the renderer's own search).
func useFFmpeg(path string) {
	ffmpegMu.Lock()
	defer ffmpegMu.Unlock()
	if path != "" && codeanim.FFmpegPath != path {
		codeanim.FFmpegPath = path
	}
}

// grammarOf is a codeanim grammar as the studio describes it to the text
// model and the UI.
func grammarOf(g codeanim.Grammar) CodeGrammar {
	cg := CodeGrammar{ID: g.ID, Label: firstNonBlank(g.Label, g.ID), Description: strings.TrimSpace(g.Description), Example: g.Example,
		Reference: strings.TrimSpace(g.Reference), Examples: g.Examples}
	var schema []string
	if len(g.Uses) > 0 {
		schema = append(schema, "Use it for: "+strings.Join(g.Uses, "; ")+".")
	}
	if a := strings.TrimSpace(g.Avoid); a != "" {
		schema = append(schema, "Avoid it for: "+a+".")
	}
	field := func(f codeanim.Field) string {
		s := f.Name
		if f.Type != "" {
			s += " (" + f.Type + ")"
		}
		if d := strings.TrimSpace(f.Doc); d != "" {
			s += " — " + d
		}
		if strings.Contains(strings.ToLower(f.Name+" "+f.Doc), "sprite") {
			cg.Sprites = true
		}
		return s
	}
	if len(g.Data) > 0 {
		var fs []string
		for _, f := range g.Data {
			fs = append(fs, field(f))
		}
		schema = append(schema, `spec "data": `+strings.Join(fs, "; ")+".")
	}
	for _, c := range g.Cues {
		cg.Kinds = append(cg.Kinds, c.Kind)
		cg.Kinds = append(cg.Kinds, c.Aliases...)
		line := fmt.Sprintf(`cue "%s"`, c.Kind)
		if len(c.Aliases) > 0 {
			line += " (= " + strings.Join(c.Aliases, ", ") + ")"
		}
		if d := strings.TrimSpace(c.Doc); d != "" {
			line += ": " + d
		}
		if len(c.Fields) > 0 {
			var fs []string
			for _, f := range c.Fields {
				fs = append(fs, field(f))
			}
			line += " Fields: " + strings.Join(fs, "; ") + "."
		}
		if len(c.Example) > 0 {
			line += " e.g. " + strings.TrimSpace(string(c.Example))
		}
		schema = append(schema, line)
	}
	if g.ID == "map" {
		cg.Sprites = true
	}
	cg.Schema = strings.Join(schema, "\n")
	return cg
}
