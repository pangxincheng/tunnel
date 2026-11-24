package main

import (
	"embed"
	"log"
	"runtime"
	"sync/atomic"

	"github.com/pangxincheng/tunnel/desktop/internal/core"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// singleInstanceID identifies the app for Wails' single-instance lock.
const singleInstanceID = "com.pangxincheng.tunnel-app"

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
	var showWindow atomic.Pointer[func()] // set once the window exists
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
		// A second launch (e.g. from the app menu while the window is hidden in
		// the tray) exits and brings this instance's window forward instead.
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: singleInstanceID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if f := showWindow.Load(); f != nil {
					(*f)()
				}
			},
		},
	})

	appPtr.Store(app)
	svc.emit = func(name string, data any) { app.Event.Emit(name, data) }

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
	showWindow.Store(&show)
	quit := func() { quitting.Store(true); app.Quit() }
	svc.quit = quit
	var hasTray bool // set below, before the app runs
	// Closing the window only hides it (to the tray, or minimised when there
	// is no tray); both the close button and Cmd/Ctrl+W land here.
	closeWindow := func() {
		if hasTray {
			win.Hide()
		} else {
			win.Minimise()
		}
	}
	app.Menu.Set(appMenu(runtime.GOOS, quit, closeWindow))
	hasTray = setupTray(app, show, quit)

	win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		if quitting.Load() {
			return
		}
		e.Cancel()
		closeWindow()
	})
	if runtime.GOOS == "darwin" { // dock-icon click re-opens the window
		app.Event.OnApplicationEvent(events.Mac.ApplicationShouldHandleReopen, func(*application.ApplicationEvent) { show() })
	}

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
