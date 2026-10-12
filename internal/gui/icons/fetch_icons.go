//go:build ignore

// fetch_icons downloads Lucide icons (https://lucide.dev, ISC licence — see
// LICENSE next to this file) and writes them as Go string constants into
// ../icons_data.go, so the build never touches the network.
//
//	cd internal/gui && go run ./icons/fetch_icons.go
//
// To add an icon: append its Lucide file name to `want` (and, when Lucide has
// renamed it, the old→new mapping to `renamed`), then run the command again.
package main

import (
	"bytes"
	"fmt"
	"go/format"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
)

// want lists the icons by the name the kit exports them under (IconXxx is
// derived from it). Names that Lucide has renamed resolve through `renamed`.
var want = []string{
	// Requested set (GUI.md).
	"home", "folder", "list-video", "plus", "settings", "sliders-horizontal", "bell", "send",
	"sparkles", "wand-sparkles", "play", "pause", "square", "rotate-ccw", "refresh-cw", "upload",
	"download", "image", "images", "film", "clapperboard", "mic", "music", "captions", "languages",
	"globe", "user", "users", "map-pin", "layers", "layout-grid", "list", "grid-3x3", "timer",
	"clock", "gauge", "zap", "rocket", "check", "check-circle", "x", "x-circle", "alert-triangle",
	"info", "help-circle", "chevron-left", "chevron-right", "chevron-down", "chevron-up",
	"arrow-right", "arrow-left", "external-link", "folder-open", "trash-2", "copy", "pencil", "eye",
	"search", "filter", "star", "heart", "bookmark", "save", "palette", "camera", "video",
	"volume-2", "scissors", "move", "split", "merge", "type", "text", "square-stack", "wand", "bot",
	"book-open", "lightbulb", "message-circle", "log-out", "moon", "sun", "monitor", "keyboard",
	"grip-vertical", "more-horizontal", "more-vertical",
	// Extras the screens are likely to need.
	"arrow-up-right", "arrow-up", "arrow-down", "calendar", "circle-plus", "circle-alert",
	"circle-dot", "circle-play", "circle-pause", "circle-stop", "file-text", "file-audio",
	"file-video", "file-up", "loader-circle", "lock", "key-round", "link", "plug", "menu",
	"panel-right", "panel-left", "sprout", "tag", "hash", "at-sign", "list-checks", "list-ordered",
	"hourglass", "maximize-2", "minimize-2", "zoom-in", "volume-x", "skip-forward", "skip-back",
	"subtitles", "brush", "paintbrush", "clipboard", "clipboard-paste", "import", "sparkle",
	"shield-check", "rotate-cw", "minus", "hard-drive", "cpu", "server", "cloud", "wifi-off",
	"history", "badge-check", "flag", "smile", "compass", "pen-line", "audio-lines", "image-plus",
	"square-pen", "folder-plus", "user-plus", "user-round", "eye-off", "mail", "bell-ring",
	"headphones", "aperture", "crop", "frame", "ratio", "rectangle-horizontal",
	"rectangle-vertical", "smartphone", "monitor-play", "blend", "droplet", "swatch-book", "shapes",
	"mountain", "building-2", "map", "drama", "ghost", "crown", "gift", "coffee", "repeat",
	"shuffle", "sliders-vertical", "settings-2", "sun-moon", "leaf", "scroll-text", "book-text",
	"quote", "megaphone", "target", "trophy", "party-popper", "send-horizontal", "message-square",
	"columns-2", "rows-2", "gallery-horizontal", "command", "grip", "thumbs-up", "thumbs-down",
	"activity", "chart-no-axes-column", "circle", "wand-2",
}

// renamed maps names Lucide has since renamed to the current file name.
var renamed = map[string]string{
	"home":            "house",
	"check-circle":    "circle-check",
	"x-circle":        "circle-x",
	"alert-triangle":  "triangle-alert",
	"help-circle":     "circle-question-mark",
	"more-horizontal": "ellipsis",
	"more-vertical":   "ellipsis-vertical",
	"filter":          "funnel",
	"text":            "text-align-start",
	"wand-2":          "wand-sparkles",
}

// base is the primary source (Lucide main); fallback serves the published
// lucide-static package, which still carries SVGs under deprecated alias names.
const (
	base     = "https://raw.githubusercontent.com/lucide-icons/lucide/main/icons/"
	fallback = "https://unpkg.com/lucide-static@latest/icons/"
)

var (
	elemRe  = regexp.MustCompile(`(?s)<(path|circle|rect|line|polyline|polygon|ellipse)\b[^>]*?/>`)
	spaceRe = regexp.MustCompile(`\s+`)
)

func main() {
	client := &http.Client{Timeout: 30 * time.Second}
	type icon struct{ name, file, body string }
	var icons []icon
	seen := map[string]bool{}
	for _, name := range want {
		if seen[name] {
			log.Fatalf("duplicate icon %q", name)
		}
		seen[name] = true
		file := name
		if r, ok := renamed[name]; ok {
			file = r
		}
		svg, err := get(client, base+file+".svg")
		if err != nil {
			// Renamed on main since; the package keeps the old name as an alias.
			svg, err = get(client, fallback+name+".svg")
			file = name + " (lucide-static)"
		}
		if err != nil {
			log.Fatalf("%s (%s): %v", name, file, err)
		}
		var parts []string
		for _, m := range elemRe.FindAllString(svg, -1) {
			parts = append(parts, spaceRe.ReplaceAllString(m, " "))
		}
		if len(parts) == 0 {
			log.Fatalf("%s: no drawable elements", name)
		}
		icons = append(icons, icon{name, file, strings.Join(parts, "")})
		fmt.Fprintf(os.Stderr, "ok %-22s %s\n", name, file)
	}
	sort.Slice(icons, func(i, j int) bool { return icons[i].name < icons[j].name })

	var b bytes.Buffer
	b.WriteString("// Code generated by icons/fetch_icons.go; DO NOT EDIT.\n\n")
	b.WriteString("// Icon artwork: Lucide (https://lucide.dev), ISC License,\n")
	b.WriteString("// Copyright (c) Lucide Icons and Contributors; icons derived from Feather are\n")
	b.WriteString("// Copyright (c) 2013-present Cole Bemis (MIT).\n")
	b.WriteString("// Full licence text: internal/gui/icons/LICENSE.\n\n")
	b.WriteString("package gui\n\n")
	b.WriteString("// The icon set. Each Icon draws its Lucide artwork as a round-capped stroke.\n")
	b.WriteString("var (\n")
	for _, ic := range icons {
		fmt.Fprintf(&b, "\t// %s draws Lucide %q.\n", goName(ic.name), ic.file)
		if strings.Contains(ic.body, "`") {
			log.Fatalf("%s: backquote in SVG", ic.name)
		}
		fmt.Fprintf(&b, "\t%s = newIcon(%q, `%s`)\n", goName(ic.name), ic.name, ic.body)
	}
	b.WriteString(")\n\n")
	b.WriteString("// AllIcons lists every icon of the set in name order (galleries, pickers).\n")
	b.WriteString("var AllIcons = []*Icon{\n")
	for _, ic := range icons {
		fmt.Fprintf(&b, "\t%s,\n", goName(ic.name))
	}
	b.WriteString("}\n")
	src, err := format.Source(b.Bytes())
	if err != nil {
		log.Fatalf("format: %v", err)
	}
	if err := os.WriteFile("icons_data.go", src, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Fprintf(os.Stderr, "wrote icons_data.go (%d icons)\n", len(icons))
}

func get(c *http.Client, url string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := c.Get(url)
		if err != nil {
			lastErr = err
			time.Sleep(time.Second)
			continue
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		if err != nil {
			lastErr = err
			continue
		}
		return string(body), nil
	}
	return "", lastErr
}

// goName turns "check-circle" into "IconCheckCircle", "grid-3x3" into "IconGrid3x3".
func goName(name string) string {
	var b strings.Builder
	b.WriteString("Icon")
	for _, p := range strings.Split(name, "-") {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}
