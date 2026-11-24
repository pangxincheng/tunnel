package main

import (
	"testing"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// Every platform needs Close Window (hide to tray) on Cmd/Ctrl+W and Quit on
// Cmd/Ctrl+Q; on macOS Quit comes from the standard app menu role.
func TestFileMenuShortcuts(t *testing.T) {
	for _, goos := range []string{"darwin", "windows", "linux"} {
		t.Run(goos, func(t *testing.T) {
			m := application.NewMenu()
			addFileMenu(m, goos, func() {}, func() {})
			closeItem := m.FindByLabel("关闭窗口")
			if closeItem == nil {
				t.Fatal("no 关闭窗口 item")
			}
			if got := closeItem.GetAccelerator(); !cmdOrCtrl(got, "W") {
				t.Errorf("关闭窗口 accelerator = %q, want Cmd/Ctrl+W", got)
			}
			if goos == "darwin" {
				return
			}
			quitItem := m.FindByLabel("退出")
			if quitItem == nil {
				t.Fatal("no 退出 item")
			}
			if got := quitItem.GetAccelerator(); !cmdOrCtrl(got, "Q") {
				t.Errorf("退出 accelerator = %q, want Cmd/Ctrl+Q", got)
			}
		})
	}
}

// cmdOrCtrl reports whether acc is CmdOrCtrl+key as Wails renders it for the
// host OS ("Cmd+W" on macOS, "Ctrl+W" elsewhere).
func cmdOrCtrl(acc, key string) bool {
	return acc == "Cmd+"+key || acc == "Ctrl+"+key
}
