package main

import (
	"bytes"
	_ "embed"
	"image"
	"image/color"
	"image/png"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed build/appicon.png
var trayIcon []byte

// setupTray creates the tray icon with "open" / "quit" entries. It returns
// false when the platform has no usable tray (the caller then minimises
// instead of hiding the window on close).
func setupTray(app *application.App, show, quit func()) bool {
	if !trayAvailable() {
		return false
	}
	t := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		t.SetTemplateIcon(templateIcon())
	} else {
		t.SetIcon(trayIcon)
	}
	t.SetTooltip("Tunnel")
	menu := application.NewMenu()
	menu.Add("打开 Tunnel").OnClick(func(*application.Context) { show() })
	menu.Add("退出").OnClick(func(*application.Context) { quit() })
	t.SetMenu(menu)
	t.OnClick(show)
	return true
}

// templateIcon draws a monochrome 44x44 glyph (ring with a centre dot) for the
// macOS menu bar; AppKit tints template images to match the menu bar.
func templateIcon() []byte {
	const n = 44
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	c := float64(n-1) / 2
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			dx, dy := float64(x)-c, float64(y)-c
			d := dx*dx + dy*dy
			if (d <= 18*18 && d >= 13*13) || d <= 6*6 {
				img.Set(x, y, color.NRGBA{A: 255})
			}
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}
