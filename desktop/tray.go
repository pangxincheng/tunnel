package main

import (
	_ "embed"
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Tray icons, generated from build/icon-src/ by build/make-icons.sh.
var (
	// trayTemplateIcon is the macOS menu bar glyph (44x44, i.e. 22pt @2x): a
	// wall with an arrow passing through it. Only alpha matters; AppKit tints
	// template images to match the menu bar.
	//go:embed build/tray-template.png
	trayTemplateIcon []byte

	// trayColorIcon is the full-colour logo for the Windows / Linux tray.
	//go:embed build/tray.png
	trayColorIcon []byte
)

// setupTray creates the tray icon with "open" / "quit" entries. It returns
// false when the platform has no usable tray (the caller then minimises
// instead of hiding the window on close).
func setupTray(app *application.App, show, quit func()) bool {
	if !trayAvailable() {
		return false
	}
	t := app.SystemTray.New()
	if runtime.GOOS == "darwin" {
		t.SetTemplateIcon(trayTemplateIcon)
	} else {
		t.SetIcon(trayColorIcon)
	}
	t.SetTooltip("Tunnel")
	menu := application.NewMenu()
	menu.Add("打开 Tunnel").OnClick(func(*application.Context) { show() })
	menu.Add("退出").OnClick(func(*application.Context) { quit() })
	t.SetMenu(menu)
	t.OnClick(show)
	return true
}
