package main

import (
	"embed"
	"log"
	"runtime"
	"sync/atomic"

	"github.com/pangxincheng/tunnel-app/desktop/internal/core"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	var app *application.App
	mgr, err := newManager(func(st core.AppState) {
		if app != nil {
			app.Event.Emit("state", st)
		}
	})
	if err != nil {
		log.Fatal(err)
	}
	svc := newAppService(mgr)

	var quitting atomic.Bool
	app = application.New(application.Options{
		Name:        "Tunnel",
		Description: "Manage tunnel forwards and share this computer",
		Services:    []application.Service{application.NewService(svc)},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
		Mac: application.MacOptions{
			// Closing the window only hides it; the tray (or Cmd+Q) quits.
			ApplicationShouldTerminateAfterLastWindowClosed: false,
		},
		ShouldQuit: func() bool { quitting.Store(true); return true },
	})

	opts := application.WebviewWindowOptions{
		Title:     "Tunnel",
		Width:     940,
		Height:    600,
		MinWidth:  720,
		MinHeight: 460,
		URL:       "/",
	}
	if runtime.GOOS == "darwin" {
		// Hidden inset title bar: traffic lights sit over the sidebar.
		opts.Mac.TitleBar = application.MacTitleBarHiddenInset
	}
	win := app.Window.NewWithOptions(opts)

	show := func() {
		if win.IsMinimised() {
			win.UnMinimise()
		}
		win.Show()
		win.Focus()
	}
	quit := func() { quitting.Store(true); app.Quit() }
	hasTray := setupTray(app, show, quit)

	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting.Load() {
			return
		}
		e.Cancel()
		if hasTray {
			win.Hide()
		} else {
			win.Minimise()
		}
	})
	win.OnWindowEvent(events.Common.WindowShow, func(*application.WindowEvent) { svc.refreshSelected() })
	if runtime.GOOS == "darwin" { // dock-icon click re-opens the window
		app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { show() })
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
