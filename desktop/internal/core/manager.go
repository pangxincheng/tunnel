// Package core is the app's core manager: per-server connections, device
// refresh, forwards, share orchestration and state snapshots.
//
// Concurrency: one mutex (Manager.mu) guards the config and all runtime
// views. It is never held while calling blocking work — Session.Run/Devices,
// ShareCtl methods, DetectSSHD — nor while calling OnChange. Session
// AddForward/RemoveForward/State/RetryNow are quick and may be called under
// it (the Session state hook never takes mu). Operations that span blocking
// calls on one server (connect/disconnect, rename, delete, share changes) are
// serialized by that server's op mutex.
package core

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/pangxincheng/tunnel/desktop/internal/config"
	"github.com/pangxincheng/tunnel/desktop/internal/sharesvc"
	"github.com/pangxincheng/tunnel/desktop/internal/sshd"
	"github.com/pangxincheng/tunnel/cli/tunnel"
)

// Session is the subset of *tunnel.Session the manager uses.
type Session interface {
	Run(ctx context.Context) error
	RetryNow()
	State() tunnel.ConnState
	AddForward(name string, port int) error
	RemoveForward(name string)
	Devices(ctx context.Context) ([]tunnel.Device, error)
}

// ShareCtl controls the share service; *sharesvc.Service satisfies it.
type ShareCtl interface {
	Status(ctx context.Context, profile string) (sharesvc.State, error)
	Install(ctx context.Context, o sharesvc.InstallOpts) error
	Uninstall(ctx context.Context, profile string, system bool) error
	Pause(ctx context.Context, profile string) error
	Resume(ctx context.Context, profile string) error
	Retry(ctx context.Context, profile string) error
	SetName(ctx context.Context, profile, name string) error
	LogPath(profile string, system bool) string
}

var _ ShareCtl = (*sharesvc.Service)(nil)

// RealSession creates a *tunnel.Session for production use.
func RealSession(cfg *tunnel.Config, socketDir string, onState func(tunnel.ConnState)) Session {
	return tunnel.NewSession(cfg, socketDir, tunnel.WithStateHook(onState))
}

// Deps are the manager's injected dependencies.
type Deps struct {
	ConfigPath    string
	NewSession    func(cfg *tunnel.Config, socketDir string, onState func(tunnel.ConnState)) Session // default RealSession
	Share         ShareCtl                                                                           // required
	DetectSSHD    func(ctx context.Context) sshd.Result                                              // default sshd.Detect
	DeviceID      string                                                                             // this machine's device id
	PortFree      func(port int) bool                                                                // default: try to listen on 127.0.0.1
	Now           func() time.Time                                                                   // default time.Now
	OnChange      func(AppState)                                                                     // coalesced, at most every 100ms
	RefreshEvery  time.Duration                                                                      // default 30s
	ShareEvery    time.Duration                                                                      // default 3s
	TunnelVersion string                                                                             // version of the bundled tunnel CLI
	Logger        *slog.Logger                                                                       // default slog.Default()
	Hostname      func() (string, error)                                                             // default os.Hostname; seeds new servers' share name
}

var (
	ErrDeviceOffline = errors.New("device is offline")
	ErrNoSSHD        = errors.New("no local sshd is running")
	ErrUnknownServer = errors.New("unknown server")
	ErrUnknownDevice = errors.New("unknown device")
	ErrNameTaken     = errors.New("share name is taken by another device")
)

const (
	coalesce       = 100 * time.Millisecond
	devicesTimeout = 20 * time.Second
	statusTimeout  = 15 * time.Second
)

// Manager is the app's core. Create with New, then Start.
type Manager struct {
	d Deps

	mu      sync.Mutex
	cfg     *config.Config
	servers map[string]*server // runtime state by server name
	// reserved holds new names of renames in progress, so nothing else can
	// take them while the rename waits on the share service.
	reserved map[string]bool
	sshd     sshd.Result
	ctx      context.Context // from Start; parent of all sessions
	started  bool

	changed chan struct{}
	wg      sync.WaitGroup // all background goroutines
	saves   int            // config writes (tests)
}

// New loads the config and prepares the manager; nothing runs until Start.
func New(d Deps) (*Manager, error) {
	if d.Share == nil {
		return nil, errors.New("core: Deps.Share is required")
	}
	if d.NewSession == nil {
		d.NewSession = RealSession
	}
	if d.DetectSSHD == nil {
		d.DetectSSHD = func(ctx context.Context) sshd.Result {
			return sshd.Detect(ctx, runtime.GOOS, sshd.DefaultConfigReader(runtime.GOOS))
		}
	}
	if d.PortFree == nil {
		d.PortFree = portFree
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Hostname == nil {
		d.Hostname = os.Hostname
	}
	if d.RefreshEvery <= 0 {
		d.RefreshEvery = 30 * time.Second
	}
	if d.ShareEvery <= 0 {
		d.ShareEvery = 3 * time.Second
	}
	cfg, err := config.Load(d.ConfigPath)
	if err != nil {
		return nil, err
	}
	m := &Manager{d: d, cfg: cfg, servers: map[string]*server{}, reserved: map[string]bool{},
		ctx: context.Background(), changed: make(chan struct{}, 1)}
	for i := range cfg.Servers {
		sc := &cfg.Servers[i]
		initMaps(sc)
		s := newServer()
		for _, info := range sc.Devices { // cached list; online state unknown until refreshed
			s.devices = append(s.devices, tunnel.Device{DeviceInfo: info})
		}
		m.servers[sc.Name] = s
	}
	return m, nil
}

func portFree(port int) bool {
	l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

func initMaps(s *config.Server) {
	if s.Forwards == nil {
		s.Forwards = map[string]config.Forward{}
	}
	if s.LastSeen == nil {
		s.LastSeen = map[string]time.Time{}
	}
}

// Start restores forwards (per Settings.RestoreForwards), connects every
// server not marked Disconnected, and starts the share sync and OnChange
// loops. Everything stops when ctx is cancelled.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	m.ctx, m.started = ctx, true
	if !m.cfg.Settings.RestoreForwards {
		for i := range m.cfg.Servers {
			for d, f := range m.cfg.Servers[i].Forwards {
				if f.On {
					f.On = false
					m.cfg.Servers[i].Forwards[d] = f
				}
			}
		}
		_ = m.saveLocked()
	}
	for i := range m.cfg.Servers {
		sc := &m.cfg.Servers[i]
		if !sc.Disconnected {
			m.startConnLocked(m.servers[sc.Name], sc)
		}
	}
	m.mu.Unlock()

	m.goBackground(func() { m.notifyLoop(ctx) })
	m.goBackground(func() { m.shareLoop(ctx) })
	m.goBackground(func() { m.detectSSHD(ctx) })
	m.goBackground(func() { // persist in-memory LastSeen / DevicesAt on shutdown
		<-ctx.Done()
		m.mu.Lock()
		_ = m.saveLocked()
		m.mu.Unlock()
	})
	m.notify()
}

// Wait blocks until every background goroutine has returned after the
// Start context was cancelled, including the final config save.
func (m *Manager) Wait() { m.wg.Wait() }

func (m *Manager) goBackground(f func()) {
	m.wg.Add(1)
	go func() { defer m.wg.Done(); f() }()
}

// notify schedules an OnChange delivery; never blocks.
func (m *Manager) notify() {
	select {
	case m.changed <- struct{}{}:
	default:
	}
}

// notifyLoop delivers the latest state at most once per coalesce interval.
func (m *Manager) notifyLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.changed:
		}
		if m.d.OnChange != nil {
			m.d.OnChange(m.State())
		}
		t := time.NewTimer(coalesce)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
	}
}

func (m *Manager) saveLocked() error {
	m.saves++
	return config.Save(m.d.ConfigPath, m.cfg)
}

// lookup returns the config entry and runtime state of server; caller holds mu.
func (m *Manager) lookupLocked(name string) (*config.Server, *server, error) {
	sc, ok := m.cfg.Server(name)
	if !ok || m.servers[name] == nil {
		return nil, nil, ErrUnknownServer
	}
	return sc, m.servers[name], nil
}

// lockOp looks up server and takes its op mutex; the caller must call
// unlockOp. It fails if the server is unknown or was deleted meanwhile.
func (m *Manager) lockOp(name string) (*server, error) {
	m.mu.Lock()
	s := m.servers[name]
	m.mu.Unlock()
	if s == nil {
		return nil, ErrUnknownServer
	}
	s.op.Lock()
	m.mu.Lock()
	ok := m.servers[name] == s
	m.mu.Unlock()
	if !ok { // deleted or renamed while waiting
		s.op.Unlock()
		return nil, ErrUnknownServer
	}
	return s, nil
}

func (m *Manager) detectSSHD(ctx context.Context) sshd.Result {
	r := m.d.DetectSSHD(ctx)
	m.mu.Lock()
	m.sshd = r
	m.mu.Unlock()
	m.notify()
	return r
}

// Select records the server shown in the window.
func (m *Manager) Select(server string) {
	m.mu.Lock()
	m.cfg.Selected = server
	_ = m.saveLocked()
	m.mu.Unlock()
	m.notify()
}

// UpdateSettings replaces the app settings.
func (m *Manager) UpdateSettings(s config.Settings) error {
	m.mu.Lock()
	m.cfg.Settings = s
	err := m.saveLocked()
	m.mu.Unlock()
	m.notify()
	return err
}

// Refresh fetches the server's device list now (called when the window
// opens) and re-detects the local sshd in the background.
func (m *Manager) Refresh(server string) {
	m.mu.Lock()
	s := m.servers[server]
	var c *conn
	if s != nil {
		c = s.conn
	}
	ctx, started := m.ctx, m.started
	m.mu.Unlock()
	if c != nil {
		c.kick()
	}
	if started && ctx.Err() == nil {
		m.goBackground(func() { m.detectSSHD(ctx) })
	}
}
