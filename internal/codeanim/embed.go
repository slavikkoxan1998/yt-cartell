package codeanim

import (
	"embed"
	"io/fs"
	"path"
	"strings"
)

// The vendored upstream engine (see engine/README.md for what is included,
// the changes and the licences).
//
//go:embed engine/LICENSE engine/README.md engine/clip.html engine/clip.js engine/engine.js engine/transitions.js engine/clips engine/lib
var engineFS embed.FS

// readEngine returns an embedded engine file by its slash path relative to
// engine/ ("lib/util.js").
func readEngine(name string) ([]byte, error) {
	name = path.Clean("/" + name)[1:]
	if name == "" || strings.HasPrefix(name, ".") {
		return nil, fs.ErrNotExist
	}
	return engineFS.ReadFile("engine/" + name)
}
