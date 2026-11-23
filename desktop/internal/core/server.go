package core

import (
	"context"
	"errors"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/pangxincheng/tunnel-app/desktop/internal/config"
	"github.com/pangxincheng/tunnel-app/desktop/internal/sharesvc"
	"github.com/pangxincheng/tunnel/tunnel"
)

// server is the runtime state of one configured server. The pointer is
// stable across renames (the map key moves).
type server struct {
	op sync.Mutex // serializes multi-step operations on this server

	// guarded by Manager.mu
	conn      *conn // nil while disconnected
	devices   []tunnel.Device
	devicesAt time.Time
	fwdErr    map[string]string // device -> DeviceView.Err code
	share     sharesvc.State    // last synced share state
	nameErr   string
	busy      bool
}

func newServer() *server { return &server{fwdErr: map[string]string{}} }

// conn is one running Session with its refresh loop.
type conn struct {
	sess    Session
	cancel  context.CancelFunc
	done    chan struct{} // closed when Run and the refresh loop have returned
	refresh chan struct{}
}

func (c *conn) kick() {
	select {
	case c.refresh <- struct{}{}:
	default:
	}
}

func tunnelConfig(sc *config.Server) *tunnel.Config {
	return &tunnel.Config{
		SSHAddr: net.JoinHostPort(sc.Host, strconv.Itoa(sc.Port)),
		User:    sc.User,
		KeyPath: sc.KeyPath,
		HostPub: sc.HostKey,
	}
}

// startConnLocked creates and runs a Session for sc and opens its On
// forwards. Caller holds mu; no-op before Start.
func (m *Manager) startConnLocked(s *server, sc *config.Server) {
	if !m.started {
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	c := &conn{cancel: cancel, done: make(chan struct{}), refresh: make(chan struct{}, 1)}
	c.sess = m.d.NewSession(tunnelConfig(sc), sc.SocketDir, func(st tunnel.ConnState) {
		// Runs synchronously in the session's supervisor: must not block or take mu.
		if st.Phase == tunnel.PhaseReady {
			c.kick()
		}
		m.notify()
	})
	s.conn = c
	if m.openForwardsLocked(s, sc) {
		_ = m.saveLocked()
	}
	var inner sync.WaitGroup
	inner.Add(2)
	m.goBackground(func() { defer inner.Done(); _ = c.sess.Run(ctx) })
	m.goBackground(func() { defer inner.Done(); m.refreshLoop(ctx, s, c) })
	m.goBackground(func() { inner.Wait(); close(c.done) })
}

// openForwardsLocked AddForwards every On forward of sc; a busy port turns
// the forward off with a "port_busy" error. Reports whether config changed.
func (m *Manager) openForwardsLocked(s *server, sc *config.Server) bool {
	changed := false
	for _, d := range sortedKeys(sc.Forwards) {
		f := sc.Forwards[d]
		if !f.On {
			continue
		}
		if err := s.conn.sess.AddForward(d, f.Port); err != nil {
			s.fwdErr[d] = fwdErrCode(err)
			f.On = false
			sc.Forwards[d] = f
			changed = true
		} else {
			delete(s.fwdErr, d)
		}
	}
	return changed
}

func fwdErrCode(err error) string {
	var le *tunnel.LocalListenError
	if errors.As(err, &le) {
		return "port_busy"
	}
	return "port_invalid"
}

// stopConn cancels the Session and waits until its listeners are closed.
// Must be called without mu.
func stopConn(c *conn) {
	if c == nil {
		return
	}
	c.cancel()
	<-c.done
}

func (m *Manager) refreshLoop(ctx context.Context, s *server, c *conn) {
	t := time.NewTicker(m.d.RefreshEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.refresh:
		}
		m.refreshDevices(ctx, s, c)
	}
}

// liveDevices lists devices if the server's session is ready.
func (m *Manager) liveDevices(ctx context.Context, c *conn) ([]tunnel.Device, bool) {
	if c == nil || c.sess.State().Phase != tunnel.PhaseReady {
		return nil, false
	}
	ctx, cancel := context.WithTimeout(ctx, devicesTimeout)
	defer cancel()
	list, err := c.sess.Devices(ctx)
	if err != nil {
		return nil, false
	}
	return list, true
}

func (m *Manager) refreshDevices(ctx context.Context, s *server, c *conn) {
	list, ok := m.liveDevices(ctx, c)
	if !ok {
		return
	}
	m.mu.Lock()
	defer m.notify()
	defer m.mu.Unlock()
	if s.conn != c { // disconnected or restarted meanwhile
		return
	}
	var sc *config.Server
	for name, rs := range m.servers {
		if rs == s {
			sc, _ = m.cfg.Server(name)
		}
	}
	if sc == nil {
		return
	}
	now := m.d.Now()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	s.devices, s.devicesAt = list, now
	changed := false
	for _, d := range list {
		if d.DeviceID == m.d.DeviceID {
			continue
		}
		if _, ok := sc.Forwards[d.Name]; !ok {
			if p := m.cfg.AllocatePort(m.d.PortFree); p != 0 {
				sc.Forwards[d.Name] = config.Forward{Port: p}
				changed = true
			}
		}
		if d.Online {
			sc.LastSeen[d.Name] = now
			changed = true
		}
	}
	if changed {
		_ = m.saveLocked()
	}
}

// findDevice returns the last listed device named name (own device excluded).
func (m *Manager) findDeviceLocked(s *server, name string) (tunnel.Device, bool) {
	for _, d := range s.devices {
		if d.Name == name && d.DeviceID != m.d.DeviceID {
			return d, true
		}
	}
	return tunnel.Device{}, false
}

// SetForward opens or closes the forward to device. Offline devices cannot be
// opened; a busy local port leaves it off with Err "port_busy".
func (m *Manager) SetForward(server, device string, on bool) error {
	m.mu.Lock()
	defer m.notify()
	defer m.mu.Unlock()
	sc, s, err := m.lookupLocked(server)
	if err != nil {
		return err
	}
	d, listed := m.findDeviceLocked(s, device)
	f, has := sc.Forwards[device]
	if !listed && !has {
		return ErrUnknownDevice
	}
	if !on {
		if s.conn != nil {
			s.conn.sess.RemoveForward(device)
		}
		delete(s.fwdErr, device)
		f.On = false
		sc.Forwards[device] = f
		return m.saveLocked()
	}
	if !listed || !d.Online {
		return ErrDeviceOffline
	}
	if !has || f.Port == 0 {
		if f.Port = m.cfg.AllocatePort(m.d.PortFree); f.Port == 0 {
			return errors.New("no free local port")
		}
	}
	if s.conn != nil {
		if err := s.conn.sess.AddForward(device, f.Port); err != nil {
			s.fwdErr[device] = fwdErrCode(err)
			sc.Forwards[device] = f
			_ = m.saveLocked()
			return err
		}
	}
	delete(s.fwdErr, device)
	f.On = true
	sc.Forwards[device] = f
	return m.saveLocked()
}

// SetPort changes device's local port; an open forward is reopened on it.
func (m *Manager) SetPort(server, device string, port int) error {
	m.mu.Lock()
	defer m.notify()
	defer m.mu.Unlock()
	sc, s, err := m.lookupLocked(server)
	if err != nil {
		return err
	}
	f, has := sc.Forwards[device]
	if _, listed := m.findDeviceLocked(s, device); !listed && !has {
		return ErrUnknownDevice
	}
	if err := m.cfg.CheckPort(server, device, port); err != nil {
		if port < 1024 || port > 65535 {
			s.fwdErr[device] = "port_invalid"
		} else {
			s.fwdErr[device] = "port_conflict"
		}
		return err
	}
	delete(s.fwdErr, device)
	f.Port = port
	var addErr error
	if f.On && s.conn != nil {
		s.conn.sess.RemoveForward(device)
		if addErr = s.conn.sess.AddForward(device, port); addErr != nil {
			s.fwdErr[device] = fwdErrCode(addErr)
			f.On = false
		}
	}
	sc.Forwards[device] = f
	if err := m.saveLocked(); err != nil {
		return err
	}
	return addErr
}

// AddServer adds and (once started) connects a new server.
func (m *Manager) AddServer(s config.Server) error {
	m.mu.Lock()
	defer m.notify()
	defer m.mu.Unlock()
	if err := m.cfg.ValidateServerName(s.Name, ""); err != nil {
		return err
	}
	s.Disconnected = false
	initMaps(&s)
	m.cfg.Servers = append(m.cfg.Servers, s)
	if m.cfg.Selected == "" {
		m.cfg.Selected = s.Name
	}
	if err := m.saveLocked(); err != nil {
		m.cfg.Servers = m.cfg.Servers[:len(m.cfg.Servers)-1]
		return err
	}
	rs := newServer()
	m.servers[s.Name] = rs
	sc, _ := m.cfg.Server(s.Name)
	m.startConnLocked(rs, sc)
	return nil
}

func connParamsChanged(a, b *config.Server) bool {
	return a.Host != b.Host || a.Port != b.Port || a.User != b.User ||
		a.KeyPath != b.KeyPath || a.HostKey != b.HostKey || a.SocketDir != b.SocketDir
}

// UpdateServer edits a server's name and connection parameters, keeping its
// forwards, last-seen times and share intent. A rename or changed connection
// restarts the Session and reinstalls an installed share service.
func (m *Manager) UpdateServer(oldName string, ns config.Server) error {
	s, err := m.lockOp(oldName)
	if err != nil {
		return err
	}
	defer s.op.Unlock()

	m.mu.Lock()
	sc, _ := m.cfg.Server(oldName)
	if err := m.cfg.ValidateServerName(ns.Name, oldName); err != nil {
		m.mu.Unlock()
		return err
	}
	ns.Disconnected, ns.Share, ns.Forwards, ns.LastSeen = sc.Disconnected, sc.Share, sc.Forwards, sc.LastSeen
	renamed := ns.Name != oldName
	restart := renamed || connParamsChanged(sc, &ns)
	m.mu.Unlock()

	var shareErr error
	if restart {
		st, err := m.shareStatus(oldName)
		if err != nil {
			return err
		}
		if st.Installed {
			system := installedSystem(st, ns.Share.System)
			if err := m.uninstallShare(s, oldName, system); err != nil {
				return err
			}
			if shareErr = m.installShare(s, &ns, system); shareErr != nil {
				ns.Share.On = false // the old service is gone; don't claim sharing
			}
		}
	}
	err = m.commitServerUpdate(s, oldName, ns, restart)
	m.syncShare(ns.Name)
	if shareErr != nil {
		return shareErr
	}
	return err
}

// commitServerUpdate stores ns in place of oldName and restarts its Session if needed.
func (m *Manager) commitServerUpdate(s *server, oldName string, ns config.Server, restart bool) error {
	var old *conn
	if restart {
		m.mu.Lock()
		old, s.conn = s.conn, nil
		m.mu.Unlock()
		stopConn(old) // wait: the new Session reuses the same local ports
	}
	m.mu.Lock()
	defer m.notify()
	defer m.mu.Unlock()
	sc, _ := m.cfg.Server(oldName)
	*sc = ns
	if ns.Name != oldName {
		delete(m.servers, oldName)
		m.servers[ns.Name] = s
		if m.cfg.Selected == oldName {
			m.cfg.Selected = ns.Name
		}
		s.devices, s.devicesAt = nil, time.Time{}
	}
	if restart && !sc.Disconnected {
		m.startConnLocked(s, sc)
	}
	return m.saveLocked()
}

// DeleteServer uninstalls the server's share service, stops its Session and
// removes it from the config. A cancelled authorization aborts the delete.
func (m *Manager) DeleteServer(name string) error {
	s, err := m.lockOp(name)
	if err != nil {
		return err
	}
	defer s.op.Unlock()

	st, err := m.shareStatus(name)
	if err != nil {
		return err
	}
	if st.Installed {
		m.mu.Lock()
		sc, _ := m.cfg.Server(name)
		system := installedSystem(st, sc.Share.System)
		m.mu.Unlock()
		if err := m.uninstallShare(s, name, system); err != nil {
			return err
		}
	}

	m.mu.Lock()
	c := s.conn
	s.conn = nil
	delete(m.servers, name)
	for i := range m.cfg.Servers {
		if m.cfg.Servers[i].Name == name {
			m.cfg.Servers = append(m.cfg.Servers[:i], m.cfg.Servers[i+1:]...)
			break
		}
	}
	if m.cfg.Selected == name {
		m.cfg.Selected = ""
		if len(m.cfg.Servers) > 0 {
			m.cfg.Selected = m.cfg.Servers[0].Name
		}
	}
	err = m.saveLocked()
	m.mu.Unlock()
	stopConn(c)
	m.notify()
	return err
}

// SetConnected connects or disconnects a server. Disconnecting stops the
// Session and pauses the share but keeps every switch; connecting reopens
// the On forwards and resumes the share.
func (m *Manager) SetConnected(name string, on bool) error {
	s, err := m.lockOp(name)
	if err != nil {
		return err
	}
	defer s.op.Unlock()
	defer m.notify()

	m.mu.Lock()
	sc, _ := m.cfg.Server(name)
	if sc.Disconnected == !on {
		m.mu.Unlock()
		return nil
	}
	sc.Disconnected = !on
	share := sc.Share
	var old *conn
	if on {
		m.startConnLocked(s, sc)
	} else {
		old, s.conn = s.conn, nil
	}
	err = m.saveLocked()
	m.mu.Unlock()
	stopConn(old)
	if err != nil {
		return err
	}
	if !share.On {
		return nil
	}
	defer m.syncShare(name)
	if on {
		return m.ensureShareRunning(s, name)
	}
	st, err := m.shareStatus(name)
	if err != nil || !st.Installed {
		return err
	}
	return m.d.Share.Pause(m.opCtx(), name)
}

// Retry reconnects the server's Session immediately.
func (m *Manager) Retry(server string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.servers[server]; s != nil && s.conn != nil {
		s.conn.sess.RetryNow()
	}
}

func sortedKeys[V any](mp map[string]V) []string {
	ks := make([]string, 0, len(mp))
	for k := range mp {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
