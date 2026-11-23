package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pangxincheng/tunnel-app/desktop/internal/config"
	"github.com/pangxincheng/tunnel-app/desktop/internal/core"
	"github.com/pangxincheng/tunnel-app/desktop/internal/probe"
	"github.com/pangxincheng/tunnel-app/desktop/internal/sharesvc"
	"github.com/pangxincheng/tunnel/tunnel"
)

func TestProbeErrorCodes(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{probe.ErrPassphrase, "passphrase"},
		{fmt.Errorf("wrapped: %w", probe.ErrPassphrase), "passphrase"},
		{&probe.AuthError{Err: errors.New("x")}, "auth"},
		{&probe.DialError{Err: errors.New("x")}, "dial"},
		{&probe.SocketDirError{Err: errors.New("x")}, "socketdir"},
		{core.ErrDeviceOffline, "offline"},
		{core.ErrNoSSHD, "no_sshd"},
		{core.ErrNameTaken, "name_taken"},
		{sharesvc.ErrCancelled, "cancelled"},
		{&tunnel.LocalListenError{Addr: "127.0.0.1:1", Cause: errors.New("in use")}, "port_busy"},
		{core.ErrUnknownServer, "unknown"},
		{core.ErrUnknownDevice, "unknown"},
		{errors.New("something else"), ""},
		{nil, ""},
	}
	for _, c := range cases {
		if got := errCode(c.err); got != c.code {
			t.Errorf("errCode(%v) = %q, want %q", c.err, got, c.code)
		}
		w := wrapErr(c.err)
		switch {
		case c.err == nil:
			if w != nil {
				t.Errorf("wrapErr(nil) = %v", w)
			}
		case c.code == "":
			if w != c.err {
				t.Errorf("uncoded error changed: %v", w)
			}
		default:
			if !strings.HasPrefix(w.Error(), c.code+":") {
				t.Errorf("wrapErr(%v) = %q, want prefix %q", c.err, w, c.code+":")
			}
			if !errors.Is(w, c.err) {
				t.Errorf("wrapErr lost the cause for %v", c.err)
			}
			if wrapErr(w).Error() != w.Error() {
				t.Errorf("double wrap: %q", wrapErr(w))
			}
		}
	}
}

type fakeShare struct{}

func (fakeShare) Status(context.Context, string) (sharesvc.State, error) {
	return sharesvc.State{}, nil
}
func (fakeShare) Install(context.Context, sharesvc.InstallOpts) error { return nil }
func (fakeShare) Uninstall(context.Context, string, bool) error       { return nil }
func (fakeShare) Pause(context.Context, string) error                 { return nil }
func (fakeShare) Resume(context.Context, string) error                { return nil }
func (fakeShare) Retry(context.Context, string) error                 { return nil }
func (fakeShare) SetName(context.Context, string, string) error       { return nil }
func (fakeShare) LogPath(string, bool) string                         { return "" }

type fakeAuto struct {
	calls []string
	fail  error
}

func (f *fakeAuto) Enable(exe string) error {
	f.calls = append(f.calls, "enable "+exe)
	return f.fail
}
func (f *fakeAuto) Disable() error { f.calls = append(f.calls, "disable"); return nil }

func newTestService(t *testing.T) (*AppService, *fakeAuto) {
	t.Helper()
	m, err := core.New(core.Deps{ConfigPath: filepath.Join(t.TempDir(), "config.json"), Share: fakeShare{}})
	if err != nil {
		t.Fatal(err)
	}
	fa := &fakeAuto{}
	return &AppService{mgr: m, auto: fa, exe: func() (string, error) { return "/fake/tunnel-app", nil }}, fa
}

func TestUpdateSettingsTogglesAutostartOnlyOnChange(t *testing.T) {
	s, fa := newTestService(t)
	st := s.GetState().Settings

	st.Mode = "dark" // unrelated change
	if err := s.UpdateSettings(st); err != nil {
		t.Fatal(err)
	}
	if len(fa.calls) != 0 {
		t.Fatalf("autostart touched without change: %v", fa.calls)
	}
	st.LaunchAtLogin = true
	if err := s.UpdateSettings(st); err != nil {
		t.Fatal(err)
	}
	st.LaunchAtLogin = false
	if err := s.UpdateSettings(st); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fa.calls, ","); got != "enable /fake/tunnel-app,disable" {
		t.Fatalf("calls = %q", got)
	}
}

func TestUpdateSettingsAutostartFailureKeepsFlag(t *testing.T) {
	s, fa := newTestService(t)
	fa.fail = errors.New("denied")
	st := s.GetState().Settings
	st.Mode, st.LaunchAtLogin = "dark", true
	err := s.UpdateSettings(st)
	if err == nil || !strings.HasPrefix(err.Error(), "autostart:") {
		t.Fatalf("err = %v", err)
	}
	got := s.GetState().Settings
	if got.LaunchAtLogin || got.Mode != "dark" {
		t.Fatalf("settings = %+v (flag must stay off, other fields saved)", got)
	}
}

func TestJSONShapeIsSnakeCase(t *testing.T) {
	b, _ := json.Marshal(probe.Params{})
	if string(b) != `{"host":"","port":0,"user":"","key_path":"","socket_dir":""}` {
		t.Errorf("Params json = %s", b)
	}
	b, _ = json.Marshal(ProbeView{})
	if string(b) != `{"fingerprint":"","host_key":"","socket_dir":""}` {
		t.Errorf("ProbeView json = %s", b)
	}
}

func TestTestConnectionMapsErrorsAndResult(t *testing.T) {
	s, _ := newTestService(t)
	s.probe = func(context.Context, probe.Params) (probe.Result, error) {
		return probe.Result{Fingerprint: "SHA256:a", HostKey: "ssh-ed25519 AAA", SocketDir: "/run/t"}, nil
	}
	v, err := s.TestConnection(probe.Params{Host: "h"})
	if err != nil || v != (ProbeView{Fingerprint: "SHA256:a", HostKey: "ssh-ed25519 AAA", SocketDir: "/run/t"}) {
		t.Fatalf("got %+v, %v", v, err)
	}
	s.probe = func(context.Context, probe.Params) (probe.Result, error) { return probe.Result{}, probe.ErrPassphrase }
	if _, err := s.TestConnection(probe.Params{}); err == nil || !strings.HasPrefix(err.Error(), "passphrase:") {
		t.Fatalf("err = %v", err)
	}
}

func TestServiceMethodErrorsAreCoded(t *testing.T) {
	s, _ := newTestService(t)
	err := s.SetConnected("nope", true)
	if err == nil || !strings.HasPrefix(err.Error(), "unknown:") {
		t.Fatalf("err = %v", err)
	}
	if err := s.SetForward("nope", "d", true); err == nil || !strings.HasPrefix(err.Error(), "unknown:") {
		t.Fatalf("err = %v", err)
	}
}

func TestOpenLogJournalAndMissing(t *testing.T) {
	s, _ := newTestService(t)
	if err := s.AddServer(config.Server{Name: "home", Host: "h", User: "u", KeyPath: "/k", Port: 22, Disconnected: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.OpenLog("nope"); err == nil || !strings.HasPrefix(err.Error(), "unknown:") {
		t.Fatalf("unknown server: %v", err)
	}
	// LogPath "" (fakeShare) -> journal hint.
	err := s.OpenLog("home")
	if err == nil || err.Error() != "journal: journalctl --user -u tunnel-home -f" {
		t.Fatalf("journal: %v", err)
	}
	// Nonexistent file -> nolog.
	missing := filepath.Join(t.TempDir(), "gone.log")
	err = s.openLogPath("home", missing, false)
	if err == nil || err.Error() != "nolog: "+missing {
		t.Fatalf("nolog: %v", err)
	}
	// Existing file -> handed to the opener (faked).
	f := filepath.Join(t.TempDir(), "t.log")
	os.WriteFile(f, []byte("x"), 0o600)
	var opened string
	s.open = func(p string) error { opened = p; return nil }
	if err := s.openLogPath("home", f, false); err != nil || opened != f {
		t.Fatalf("open: %v %q", err, opened)
	}
}

func TestQuitCallsHookAndTemplateIconIsPNG(t *testing.T) {
	s, _ := newTestService(t)
	s.Quit() // no hook: must not panic
	called := false
	s.quit = func() { called = true }
	s.Quit()
	if !called {
		t.Error("quit hook not called")
	}
	if b := templateIcon(); len(b) < 8 || string(b[1:4]) != "PNG" {
		t.Error("template icon is not a PNG")
	}
}
