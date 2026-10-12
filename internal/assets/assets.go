// Package assets embeds the static images shipped inside the executable:
// the logo and one gallery preview per built-in style (styles/<id>.jpg,
// 640×480, rendered by cmd/stylegen through G-Labs with one fixed scene).
package assets

import "embed"

//go:embed styles/*.jpg logo.png icon256.png
var FS embed.FS

// StylePreview returns the JPEG bytes of a built-in style preview, or nil.
func StylePreview(id string) []byte {
	b, err := FS.ReadFile("styles/" + id + ".jpg")
	if err != nil {
		return nil
	}
	return b
}

// Logo returns the 128px PNG logo.
func Logo() []byte {
	b, _ := FS.ReadFile("logo.png")
	return b
}

// Icon returns the 256px PNG app icon.
func Icon() []byte {
	b, _ := FS.ReadFile("icon256.png")
	return b
}
