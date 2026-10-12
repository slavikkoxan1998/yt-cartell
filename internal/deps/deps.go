//go:build tools

// Package deps pins third-party modules used across the app so parallel work
// never needs to edit go.mod. Not compiled into the binary.
package deps

import (
	_ "gioui.org/app"
	_ "gioui.org/font/opentype"
	_ "gioui.org/gpu/headless"
	_ "gioui.org/io/clipboard"
	_ "gioui.org/layout"
	_ "gioui.org/widget/material"
	_ "gioui.org/x/component"
	_ "gioui.org/x/explorer"
	_ "github.com/ebitengine/oto/v3"
	_ "golang.org/x/exp/shiny/materialdesign/icons"
	_ "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)
