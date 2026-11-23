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
	var appPtr atomic.Pointer[application.App] // OnChange runs on other goroutines
	mgr, err := newManager(func(st core.AppState) {
		if a := appPtr.Load(); a != nil {
			a.Event.Emit("state", st)
		}
	})
	if err != nil {
		log.Fatal(err)
	}
	svc := newAppService(mgr)

	var quitting atomic.Bool
	app := application.New(application.Options{
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

	appPtr.Store(app)

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
		// The WindowShow event is not emitted by every backend (GTK), so
		// refresh explicitly whenever the window is brought back.
		svc.refreshSelected()
	}
	quit := func() { quitting.Store(true); app.Quit() }
	svc.quit = quit
	app.Menu.Set(appMenu(app, quit))
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
	if runtime.GOOS == "darwin" { // dock-icon click re-opens the window
		app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { show() })
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
