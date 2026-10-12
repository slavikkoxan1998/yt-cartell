package gui

import (
	"embed"
	"fmt"
	"os"
	"sync"

	"gioui.org/font"
	"gioui.org/font/opentype"
	"gioui.org/text"
)

// Fira Sans (SIL Open Font License 1.1, fonts/OFL-FiraSans.txt) in the four
// weights the kit uses. Typeface "Fira" is registered with explicit weights so
// the shaper never has to guess from family metadata.
//
//go:embed fonts/fira-regular.ttf fonts/fira-medium.ttf fonts/fira-semibold.ttf fonts/fira-bold.ttf
var fontFS embed.FS

// Typeface is the kit's only typeface: Fira Sans, a humanist sans.
const Typeface font.Typeface = "Fira"

// The four embedded faces. Text styles (see Typography) pick one of them.
var (
	FontRegular  = font.Font{Typeface: Typeface, Weight: font.Normal}
	FontMedium   = font.Font{Typeface: Typeface, Weight: font.Medium}
	FontSemiBold = font.Font{Typeface: Typeface, Weight: font.SemiBold}
	FontBold     = font.Font{Typeface: Typeface, Weight: font.Bold}
)

var fontFiles = []struct {
	file string
	font font.Font
}{
	// The first face is the shaper's default.
	{"fira-regular.ttf", FontRegular},
	{"fira-medium.ttf", FontMedium},
	{"fira-semibold.ttf", FontSemiBold},
	{"fira-bold.ttf", FontBold},
}

// fallbackFontPaths are tried, in order, for one last-resort face covering
// runes Fira lacks (rare symbols users paste). Only the first found is loaded,
// so startup never scans the system font folder.
var fallbackFontPaths = []string{
	`C:\Windows\Fonts\segoeui.ttf`,
	`C:\Windows\Fonts\arial.ttf`,
	"/System/Library/Fonts/Supplemental/Arial.ttf",
	"/Library/Fonts/Arial.ttf",
	"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
	"/usr/share/fonts/TTF/DejaVuSans.ttf",
	"/usr/share/fonts/dejavu/DejaVuSans.ttf",
}

var (
	collOnce sync.Once
	coll     []font.FontFace
	collErr  error
)

// Fonts parses the embedded Fira Sans faces once (plus one system fallback
// face when available) and returns them ready for text.NewShaper.
func Fonts() ([]font.FontFace, error) {
	collOnce.Do(func() {
		for _, f := range fontFiles {
			data, err := fontFS.ReadFile("fonts/" + f.file)
			if err != nil {
				collErr = fmt.Errorf("gui: font %s: %w", f.file, err)
				return
			}
			face, err := opentype.Parse(data)
			if err != nil {
				collErr = fmt.Errorf("gui: font %s: %w", f.file, err)
				return
			}
			coll = append(coll, font.FontFace{Font: f.font, Face: face})
		}
		for _, p := range fallbackFontPaths {
			data, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			if face, err := opentype.Parse(data); err == nil {
				coll = append(coll, font.FontFace{Font: font.Font{Typeface: "Fallback"}, Face: face})
				break
			}
		}
	})
	return coll, collErr
}

// NewShaper returns a text shaper over the embedded faces. System fonts stay
// off: Fira covers Latin, Cyrillic and the punctuation the UI prints, and
// scanning the system font folder costs seconds on first run.
func NewShaper() (*text.Shaper, error) {
	faces, err := Fonts()
	if err != nil {
		return nil, err
	}
	return text.NewShaper(text.NoSystemFonts(), text.WithCollection(faces)), nil
}
