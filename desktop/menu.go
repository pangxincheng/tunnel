package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
)

// appMenu is the application menu for goos (runtime.GOOS). On macOS the
// standard app menu supplies Quit, and the edit menu makes copy / paste work
// in text fields.
func appMenu(goos string, quit, closeWindow func()) *application.Menu {
	m := application.NewMenu()
	if goos == "darwin" {
		m.AddRole(application.AppMenu)
	}
	addFileMenu(m, goos, quit, closeWindow)
	m.AddRole(application.EditMenu)
	if goos == "darwin" {
		m.AddRole(application.WindowMenu)
	}
	return m
}

// addFileMenu adds the menu every platform needs: Close Window on
// Cmd/Ctrl+W, which hides the window exactly like its close button, and —
// outside macOS, whose app menu has it — Quit on Ctrl+Q (Linux without a
// tray has no other way to quit).
func addFileMenu(m *application.Menu, goos string, quit, closeWindow func()) {
	label := "Tunnel"
	if goos == "darwin" {
		label = "文件"
	}
	file := m.AddSubmenu(label)
	file.Add("关闭窗口").SetAccelerator("CmdOrCtrl+W").OnClick(func(*application.Context) { closeWindow() })
	if goos != "darwin" {
		file.Add("退出").SetAccelerator("CmdOrCtrl+Q").OnClick(func(*application.Context) { quit() })
	}
}
