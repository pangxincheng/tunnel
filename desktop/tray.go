package main

import (
	_ "embed"

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
	t.SetIcon(trayIcon)
	t.SetTooltip("Tunnel")
	menu := application.NewMenu()
	menu.Add("打开 Tunnel").OnClick(func(*application.Context) { show() })
	menu.Add("退出").OnClick(func(*application.Context) { quit() })
	t.SetMenu(menu)
	t.OnClick(show)
	return true
}
