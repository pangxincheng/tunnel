package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/pangxincheng/tunnel/desktop/internal/autostart"
	"github.com/pangxincheng/tunnel/desktop/internal/config"
	"github.com/pangxincheng/tunnel/desktop/internal/core"
	"github.com/pangxincheng/tunnel/desktop/internal/probe"
	"github.com/wailsapp/wails/v3/pkg/application"
)

// version is the app version; set with -ldflags "-X main.version=...".
var version = "dev"

// ProbeView is the result of TestConnection as the frontend sees it.
type ProbeView struct {
	Fingerprint string `json:"fingerprint"`
	HostKey     string `json:"host_key"`
	SocketDir   string `json:"socket_dir"`
}

// autostarter registers / removes the login item for this app.
type autostarter interface {
	Enable(exe string) error
	Disable() error
}

type realAutostart struct{}

func (realAutostart) Enable(exe string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return autostart.Enable(runtime.GOOS, exe, home)
}

func (realAutostart) Disable() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return autostart.Disable(runtime.GOOS, home)
}

// AppService is the object bound to the frontend. Every method returns
// errors whose message starts with a stable code where one applies (see
// errCode).
type AppService struct {
	mgr    *core.Manager
	cancel context.CancelFunc

	// Replaceable in tests.
	auto  autostarter
	exe   func() (string, error)
	probe func(context.Context, probe.Params) (probe.Result, error)
	open  func(path string) error
	emit  func(name string, data any) // frontend events; set by main
	quit  func()                      // set by main

	ctx    context.Context // from ServiceStartup; parent of log streams
	logMu  sync.Mutex
	logs   map[string]logStream // running log stream per server
	logSeq int
}

func newAppService(m *core.Manager) *AppService {
	return &AppService{mgr: m, auto: realAutostart{}, exe: os.Executable, probe: probe.Test, open: openPath}
}

func (s *AppService) ServiceStartup(ctx context.Context, _ application.ServiceOptions) error {
	ctx, s.cancel = context.WithCancel(ctx)
	s.ctx = ctx
	s.mgr.Start(ctx)
	return nil
}

// shutdownWait bounds how long quitting waits for the manager's final save.
const shutdownWait = 3 * time.Second

func (s *AppService) ServiceShutdown() error {
	if s.cancel != nil {
		s.cancel()
		if !waitTimeout(s.mgr.Wait, shutdownWait) {
			slog.Warn("core manager did not stop in time; quitting anyway")
		}
	}
	return nil
}

// waitTimeout runs wait and reports whether it returned within d.
func waitTimeout(wait func(), d time.Duration) bool {
	done := make(chan struct{})
	go func() { wait(); close(done) }()
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		return false
	}
}

// refreshSelected refreshes the currently selected server (window re-shown).
func (s *AppService) refreshSelected() {
	s.mgr.Refresh(s.mgr.State().Selected)
}

func (s *AppService) GetState() core.AppState { return s.mgr.State() }
func (s *AppService) Select(name string)      { s.mgr.Select(name) }
func (s *AppService) Refresh(name string)     { s.mgr.Refresh(name) }
func (s *AppService) Retry(name string)       { s.mgr.Retry(name) }
func (s *AppService) AppVersion() string      { return version }

func (s *AppService) TestConnection(p probe.Params) (ProbeView, error) {
	r, err := s.probe(context.Background(), p)
	if err != nil {
		return ProbeView{}, wrapErr(err)
	}
	return ProbeView{Fingerprint: r.Fingerprint, HostKey: r.HostKey, SocketDir: r.SocketDir}, nil
}

func (s *AppService) AddServer(sv config.Server) error { return wrapErr(s.mgr.AddServer(sv)) }
func (s *AppService) UpdateServer(old string, sv config.Server) error {
	return wrapErr(s.mgr.UpdateServer(old, sv))
}
func (s *AppService) DeleteServer(name string) error { return wrapErr(s.mgr.DeleteServer(name)) }
func (s *AppService) SetConnected(name string, on bool) error {
	return wrapErr(s.mgr.SetConnected(name, on))
}
func (s *AppService) SetForward(server, device string, on bool) error {
	return wrapErr(s.mgr.SetForward(server, device, on))
}
func (s *AppService) SetPort(server, device string, port int) error {
	return wrapErr(s.mgr.SetPort(server, device, port))
}
func (s *AppService) SetShare(server string, on bool) error {
	return wrapErr(s.mgr.SetShare(server, on))
}
func (s *AppService) SetShareName(server, name string) error {
	return wrapErr(s.mgr.SetShareName(server, name))
}
func (s *AppService) SetShareSystem(server string, on bool) error {
	return wrapErr(s.mgr.SetShareSystem(server, on))
}
func (s *AppService) RetryShare(server string) error  { return wrapErr(s.mgr.RetryShare(server)) }
func (s *AppService) UpdateShare(server string) error { return wrapErr(s.mgr.UpdateShare(server)) }

// UpdateSettings saves the settings and, when LaunchAtLogin changed, adds or
// removes the login item.
func (s *AppService) UpdateSettings(st config.Settings) error {
	was := s.mgr.State().Settings.LaunchAtLogin
	var autoErr error
	if st.LaunchAtLogin != was {
		if !st.LaunchAtLogin {
			autoErr = s.auto.Disable()
		} else if exe, err := s.exe(); err != nil {
			autoErr = err
		} else {
			autoErr = s.auto.Enable(exe)
		}
		if autoErr != nil {
			st.LaunchAtLogin = was // keep the persisted flag truthful
		}
	}
	if err := s.mgr.UpdateSettings(st); err != nil {
		return wrapErr(err)
	}
	if autoErr != nil {
		return fmt.Errorf("autostart: %w", autoErr)
	}
	return nil
}

// Quit exits the app (the window's close button only hides it).
func (s *AppService) Quit() {
	if s.quit != nil {
		s.quit()
	}
}

// RemoveDevice deletes an offline device's record from the server.
func (s *AppService) RemoveDevice(server, device string) error {
	return wrapErr(s.mgr.RemoveDevice(server, device))
}

// OpenLog opens the share service log of server in the system viewer.
func (s *AppService) OpenLog(server string) error {
	for _, v := range s.mgr.State().Servers {
		if v.Name == server {
			return s.openLogPath(server, v.Share.LogPath, v.Share.System)
		}
	}
	return wrapErr(core.ErrUnknownServer)
}

func (s *AppService) openLogPath(server, path string, system bool) error {
	if path == "" { // Linux: logs live in the journal
		flag := "--user "
		if system {
			flag = ""
		}
		return fmt.Errorf("journal: journalctl %s-u tunnel-%s -f", flag, server)
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("nolog: %s", path)
	}
	return s.open(path)
}

func openPath(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}

// PickKeyFile shows an open-file dialog starting in ~/.ssh. Cancelling
// returns "" and no error.
func (s *AppService) PickKeyFile() (string, error) {
	d := application.Get().Dialog.OpenFile().
		SetTitle("Select private key").
		CanChooseFiles(true).
		CanChooseDirectories(false).
		ShowHiddenFiles(true)
	if home, err := os.UserHomeDir(); err == nil {
		ssh := filepath.Join(home, ".ssh")
		if st, err := os.Stat(ssh); err == nil && st.IsDir() {
			d.SetDirectory(ssh)
		}
	}
	p, err := d.PromptForSingleSelection()
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return "", nil
		}
		return "", err
	}
	return p, nil
}
