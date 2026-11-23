package main

import (
	"runtime"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// appMenu is the application menu. It guarantees a Quit entry with a
// keyboard shortcut on every platform (Linux without a tray has no other way
// to quit); on macOS it also provides the standard app and edit menus so
// copy / paste work in text fields.
func appMenu(app *application.App, quit func()) *application.Menu {
	m := app.Menu.New()
	if runtime.GOOS == "darwin" {
		m.AddRole(application.AppMenu)
		m.AddRole(application.EditMenu)
		m.AddRole(application.WindowMenu)
		return m
	}
	file := m.AddSubmenu("Tunnel")
	file.Add("退出").SetAccelerator("CmdOrCtrl+Q").OnClick(func(*application.Context) { quit() })
	m.AddRole(application.EditMenu)
	return m
}
