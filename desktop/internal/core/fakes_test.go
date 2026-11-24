package core

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pangxincheng/tunnel/desktop/internal/config"
	"github.com/pangxincheng/tunnel/desktop/internal/sharesvc"
	"github.com/pangxincheng/tunnel/desktop/internal/sshd"
	"github.com/pangxincheng/tunnel/cli/tunnel"
)

// fakeSession is a controllable Session.
type fakeSession struct {
	cfg  *tunnel.Config
	hook func(tunnel.ConnState)

	mu       sync.Mutex
	state    tunnel.ConnState
	devices  []tunnel.Device
	addErr   map[string]error
	calls    []string
	running  bool
	devCalls int
	retries  int
}

func (s *fakeSession) Run(ctx context.Context) error {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	<-ctx.Done()
	s.mu.Lock()
	s.running = false
	s.mu.Unlock()
	return nil
}

func (s *fakeSession) RetryNow() { s.mu.Lock(); s.retries++; s.mu.Unlock() }

func (s *fakeSession) State() tunnel.ConnState { s.mu.Lock(); defer s.mu.Unlock(); return s.state }

func (s *fakeSession) AddForward(name string, port int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, fmt.Sprintf("add %s %d", name, port))
	return s.addErr[name]
}

func (s *fakeSession) RemoveForward(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, "remove "+name)
}

func (s *fakeSession) Devices(ctx context.Context) ([]tunnel.Device, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devCalls++
	return append([]tunnel.Device(nil), s.devices...), nil
}

func (s *fakeSession) setState(st tunnel.ConnState) {
	s.mu.Lock()
	s.state = st
	s.mu.Unlock()
	if s.hook != nil {
		s.hook(st)
	}
}

func (s *fakeSession) setDevices(d ...tunnel.Device) {
	s.mu.Lock()
	s.devices = d
	s.mu.Unlock()
}

func (s *fakeSession) setAddErr(name string, err error) {
	s.mu.Lock()
	if s.addErr == nil {
		s.addErr = map[string]error{}
	}
	s.addErr[name] = err
	s.mu.Unlock()
}

func (s *fakeSession) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

func (s *fakeSession) DevCalls() int { s.mu.Lock(); defer s.mu.Unlock(); return s.devCalls }

func (s *fakeSession) Running() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.running }

// sessFactory records every Session created, keyed by host (tests use unique hosts).
type sessFactory struct {
	mu   sync.Mutex
	all  []*fakeSession
	init []tunnel.Device // devices pre-set on new sessions
}

func (f *sessFactory) New(cfg *tunnel.Config, socketDir string, onState func(tunnel.ConnState)) Session {
	s := &fakeSession{cfg: cfg, hook: onState, state: tunnel.ConnState{Phase: tunnel.PhaseConnecting}}
	f.mu.Lock()
	s.devices = append([]tunnel.Device(nil), f.init...)
	f.all = append(f.all, s)
	f.mu.Unlock()
	return s
}

// sessions returns all sessions created for host, oldest first.
func (f *sessFactory) sessions(host string) []*fakeSession {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*fakeSession
	for _, s := range f.all {
		if s.cfg.SSHAddr == host+":22" {
			out = append(out, s)
		}
	}
	return out
}

// fakeShare records ShareCtl calls in order.
type fakeShare struct {
	mu          sync.Mutex
	svc         map[string]*fakeSvc // profile -> installed service
	calls       []string
	installErrs []error // popped per Install call
	pauseErrs   []error // popped per Pause call
	statusErr   error
	// installBlock, if set, makes Install wait for it to be closed after
	// signalling installStarted (non-blocking send).
	installBlock   chan struct{}
	installStarted chan struct{}
	lastInstall    sharesvc.InstallOpts
}

type fakeSvc struct {
	system, paused bool
	stopped        bool // installed but not running (control calls fail with ErrNotRunning)
	name, version  string
}

// setSvc changes an installed service's runtime state.
func (f *fakeShare) setSvc(profile string, fn func(*fakeSvc)) {
	f.mu.Lock()
	if s, ok := f.svc[profile]; ok {
		fn(s)
	}
	f.mu.Unlock()
}

func (f *fakeShare) count(call string) int {
	n := 0
	for _, c := range f.Calls() {
		if c == call {
			n++
		}
	}
	return n
}

// notRunningLocked reports ErrNotRunning for a stopped service; caller holds mu.
func (f *fakeShare) notRunningLocked(what, profile string) error {
	if s, ok := f.svc[profile]; ok && s.stopped {
		return fmt.Errorf("%s: %w", what, sharesvc.ErrNotRunning)
	}
	return nil
}

func newFakeShare() *fakeShare { return &fakeShare{svc: map[string]*fakeSvc{}} }

func (f *fakeShare) installed(profile string, system bool, name string) {
	f.mu.Lock()
	f.svc[profile] = &fakeSvc{system: system, name: name}
	f.mu.Unlock()
}

func (f *fakeShare) record(s string) { f.calls = append(f.calls, s) }

func (f *fakeShare) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func (f *fakeShare) Status(ctx context.Context, profile string) (sharesvc.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.statusErr != nil {
		return sharesvc.State{}, f.statusErr
	}
	s, ok := f.svc[profile]
	if !ok {
		return sharesvc.State{}, nil
	}
	mode := "user"
	if s.system {
		mode = "system"
	}
	if s.stopped {
		return sharesvc.State{Installed: true, Mode: mode}, nil
	}
	ph := tunnel.PhaseReady
	if s.paused {
		ph = tunnel.PhasePaused
	}
	return sharesvc.State{Installed: true, Running: true, Mode: mode, Status: &tunnel.ShareStatus{
		ConnState: tunnel.ConnState{Phase: ph}, Name: s.name, Paused: s.paused, Version: s.version}}, nil
}

func (f *fakeShare) Install(ctx context.Context, o sharesvc.InstallOpts) error {
	f.mu.Lock()
	block, started := f.installBlock, f.installStarted
	f.mu.Unlock()
	if block != nil {
		select {
		case started <- struct{}{}:
		default:
		}
		<-block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(fmt.Sprintf("install %s name=%s system=%v local=%d", o.Profile, o.Name, o.System, o.LocalPort))
	f.lastInstall = o
	if len(f.installErrs) > 0 {
		err := f.installErrs[0]
		f.installErrs = f.installErrs[1:]
		if err != nil {
			return err
		}
	}
	f.svc[o.Profile] = &fakeSvc{system: o.System, name: o.Name}
	return nil
}

func (f *fakeShare) Uninstall(ctx context.Context, profile string, system bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(fmt.Sprintf("uninstall %s system=%v", profile, system))
	delete(f.svc, profile)
	return nil
}

func (f *fakeShare) Pause(ctx context.Context, profile string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("pause " + profile)
	if err := f.notRunningLocked("tunnel pause", profile); err != nil {
		return err
	}
	if len(f.pauseErrs) > 0 {
		err := f.pauseErrs[0]
		f.pauseErrs = f.pauseErrs[1:]
		if err != nil {
			return err
		}
	}
	if s, ok := f.svc[profile]; ok {
		s.paused = true
	}
	return nil
}

func (f *fakeShare) Resume(ctx context.Context, profile string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("resume " + profile)
	if err := f.notRunningLocked("tunnel resume", profile); err != nil {
		return err
	}
	if s, ok := f.svc[profile]; ok {
		s.paused = false
	}
	return nil
}

func (f *fakeShare) Retry(ctx context.Context, profile string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("retry " + profile)
	return f.notRunningLocked("tunnel retry", profile)
}

func (f *fakeShare) SetName(ctx context.Context, profile, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record(fmt.Sprintf("setname %s %s", profile, name))
	if err := f.notRunningLocked("tunnel set-name", profile); err != nil {
		return err
	}
	if s, ok := f.svc[profile]; ok {
		s.name = name
	}
	return nil
}

func (f *fakeShare) LogPath(profile string, system bool) string { return "/log/" + profile }

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time  { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) Set(t time.Time) { c.mu.Lock(); c.t = t; c.mu.Unlock() }

// env is a started Manager wired to fakes.
type env struct {
	t        *testing.T
	m        *Manager
	path     string
	sess     *sessFactory
	share    *fakeShare
	clock    *fakeClock
	mu       sync.Mutex
	sshd     sshd.Result
	hostname string
	changes  int
	detects  int
	lastSeen AppState

	refreshEvery, shareEvery time.Duration // set in prep to override 10ms
	cancel                   context.CancelFunc
}

// validHostKey is a parsable authorized_keys line (a fixed ed25519 public key).
const validHostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"

func srvCfg(name string) config.Server {
	return config.Server{Name: name, Host: name + ".example", User: "u", KeyPath: "/k", HostKey: validHostKey, Port: 22}
}

func dev(name, id string, online bool) tunnel.Device {
	return tunnel.Device{DeviceInfo: tunnel.DeviceInfo{Name: name, DeviceID: id}, Online: online}
}

var t0 = time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)

// newEnv saves cfg (nil = empty defaults) and starts a Manager on it.
func newEnv(t *testing.T, cfg *config.Config, prep ...func(*env)) *env {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if cfg == nil {
		cfg, _ = config.Load(path)
	}
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	return startEnv(t, path, &fakeClock{t: t0}, prep...)
}

// reopen stops e's Manager and starts a fresh one on the same config file
// (simulates an app restart).
func (e *env) reopen(prep ...func(*env)) *env {
	e.t.Helper()
	e.cancel()
	e.m.wg.Wait()
	return startEnv(e.t, e.path, e.clock, prep...)
}

func startEnv(t *testing.T, path string, clock *fakeClock, prep ...func(*env)) *env {
	t.Helper()
	e := &env{t: t, path: path, sess: &sessFactory{}, share: newFakeShare(), clock: clock,
		sshd: sshd.Result{Running: true, Port: 22}, hostname: "Test-Host.local", refreshEvery: 10 * time.Millisecond, shareEvery: 10 * time.Millisecond}
	for _, p := range prep {
		p(e)
	}
	m, err := New(Deps{
		ConfigPath: e.path,
		NewSession: e.sess.New,
		Share:      e.share,
		DetectSSHD: func(context.Context) sshd.Result { e.mu.Lock(); defer e.mu.Unlock(); e.detects++; return e.sshd },
		DeviceID:   "self",
		PortFree:   func(int) bool { return true },
		Hostname:   func() (string, error) { return e.hostname, nil },
		Now:        e.clock.Now,
		OnChange: func(s AppState) {
			e.mu.Lock()
			e.changes++
			e.lastSeen = s
			e.mu.Unlock()
		},
		RefreshEvery:  e.refreshEvery,
		ShareEvery:    e.shareEvery,
		TunnelVersion: "1.2.0",
		Logger:        slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.m = m
	ctx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	t.Cleanup(func() { cancel(); m.wg.Wait() })
	m.Start(ctx)
	return e
}

func (e *env) setSSHD(r sshd.Result) { e.mu.Lock(); e.sshd = r; e.mu.Unlock() }

func eventually(t *testing.T, what string, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func (e *env) view(name string) ServerView {
	e.t.Helper()
	for _, v := range e.m.State().Servers {
		if v.Name == name {
			return v
		}
	}
	e.t.Fatalf("server %q not in state", name)
	return ServerView{}
}

func (e *env) device(server, name string) DeviceView {
	e.t.Helper()
	for _, d := range e.view(server).Devices {
		if d.Name == name {
			return d
		}
	}
	e.t.Fatalf("device %q not in view", name)
	return DeviceView{}
}

func (e *env) waitView(name, what string, f func(ServerView) bool) {
	e.t.Helper()
	eventually(e.t, what, func() bool { return f(e.view(name)) })
}

// session waits for the n-th (1-based) session of server.
func (e *env) session(server string, n int) *fakeSession {
	e.t.Helper()
	var s *fakeSession
	eventually(e.t, fmt.Sprintf("session %d of %s", n, server), func() bool {
		all := e.sess.sessions(server + ".example")
		if len(all) >= n {
			s = all[n-1]
			return true
		}
		return false
	})
	return s
}

// ready makes the latest session of server ready with devices and waits until they're shown.
func (e *env) ready(server string, devs ...tunnel.Device) *fakeSession {
	e.t.Helper()
	all := e.sess.sessions(server + ".example")
	s := e.session(server, max(1, len(all)))
	s.setDevices(devs...)
	before := s.DevCalls()
	s.setState(tunnel.ConnState{Phase: tunnel.PhaseReady})
	eventually(e.t, "devices refreshed", func() bool { return s.DevCalls() > before })
	e.refreshNow(server, s)
	return s
}

// refreshNow forces a refresh and waits until it has been applied.
func (e *env) refreshNow(server string, s *fakeSession) {
	e.t.Helper()
	before := s.DevCalls()
	e.m.Refresh(server)
	eventually(e.t, "refresh", func() bool { return s.DevCalls() > before+1 }) // +1: a full refresh after the call
}

func (e *env) cfg() *config.Config {
	e.t.Helper()
	c, err := config.Load(e.path)
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

func (e *env) cfgServer(name string) config.Server {
	e.t.Helper()
	s, ok := e.cfg().Server(name)
	if !ok {
		e.t.Fatalf("server %q not in config", name)
	}
	return *s
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

func cfgWith(servers ...config.Server) *config.Config {
	c, _ := config.Load(filepath.Join(os.TempDir(), "nonexistent-tunnel-app-core", "c.json"))
	c.Servers = servers
	return c
}
