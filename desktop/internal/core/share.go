package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pangxincheng/tunnel/cli/tunnel"
	"github.com/pangxincheng/tunnel/desktop/internal/config"
	"github.com/pangxincheng/tunnel/desktop/internal/sharesvc"
)

// The share service profile of a server is the server's name.

func (m *Manager) opCtx() context.Context {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ctx
}

func (m *Manager) shareStatus(profile string) (sharesvc.State, error) {
	ctx, cancel := context.WithTimeout(m.opCtx(), statusTimeout)
	defer cancel()
	return m.d.Share.Status(ctx, profile)
}

// syncShare refreshes the cached share state of profile.
func (m *Manager) syncShare(profile string) {
	st, err := m.shareStatus(profile)
	if err != nil {
		return // keep the last known state
	}
	m.mu.Lock()
	if s := m.servers[profile]; s != nil {
		s.share = st
	}
	m.mu.Unlock()
	m.notify()
}

func (m *Manager) shareLoop(ctx context.Context) {
	t := time.NewTicker(m.d.ShareEvery)
	defer t.Stop()
	for {
		m.mu.Lock()
		names := sortedKeys(m.servers)
		m.mu.Unlock()
		for _, n := range names {
			if ctx.Err() != nil {
				return
			}
			m.reconcileShare(n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// shareClass buckets a synced share state: "" (not installed), "stopped",
// "paused" or "running".
func shareClass(st sharesvc.State) string {
	switch {
	case !st.Installed:
		return ""
	case !st.Running || st.Status == nil:
		return "stopped"
	case st.Status.Paused || st.Status.Phase == tunnel.PhasePaused:
		return "paused"
	}
	return "running"
}

// ignoreNotRunning treats a stopped service as nothing to do: the share loop
// applies the intent once the service runs again.
func ignoreNotRunning(err error) error {
	if errors.Is(err, sharesvc.ErrNotRunning) {
		return nil
	}
	return err
}

// reconcileShare syncs profile's share state and, unless an operation on the
// server is in progress, brings config and service in line:
//   - the installed service's name and mode are written back to the config;
//   - a running service the config never decided about (e.g. after a config
//     reset) is adopted as sharing;
//   - when the service's state changes (e.g. it starts again after being
//     stopped) and disagrees with the intent, it is paused or resumed once.
//     A failed fix is not retried until the next change, so a broken service
//     isn't hammered.
func (m *Manager) reconcileShare(profile string) {
	m.mu.Lock()
	s := m.servers[profile]
	m.mu.Unlock()
	if s == nil {
		return
	}
	if !s.op.TryLock() { // the running operation syncs when it finishes
		m.syncShare(profile)
		return
	}
	defer s.op.Unlock()
	st, err := m.shareStatus(profile)
	if err != nil {
		return
	}
	m.mu.Lock()
	sc, ok := m.cfg.Server(profile)
	if !ok || m.servers[profile] != s {
		m.mu.Unlock()
		return
	}
	s.share = st
	class := shareClass(st)
	changed := false
	if st.Installed { // the installed service is authoritative for name and mode
		if st.Mode != "" && (st.Mode == "system") != sc.Share.System {
			sc.Share.System = st.Mode == "system"
			changed = true
		}
		if st.Status != nil && st.Status.Name != "" && st.Status.Name != sc.Share.Name {
			sc.Share.Name = st.Status.Name
			changed = true
		}
	}
	if class == "running" && !sc.Share.On && !sc.Share.Decided {
		sc.Share.On, sc.Share.Decided = true, true
		changed = true
		m.d.Logger.Info("adopted running share service", "server", profile)
	}
	if changed {
		_ = m.saveLocked()
	}
	var fix func(context.Context, string) error
	if class != s.seen {
		s.seen = class
		want := sc.Share.On && !sc.Disconnected
		switch {
		case class == "running" && !want:
			fix = m.d.Share.Pause
		case class == "paused" && want:
			fix = m.d.Share.Resume
		}
	}
	m.mu.Unlock()
	m.notify()
	if fix == nil {
		return
	}
	if err := fix(m.opCtx(), profile); err != nil {
		m.d.Logger.Warn("share service disagrees with the intent; fix failed", "server", profile, "err", err)
	}
	m.syncShare(profile)
}

// installedSystem tells the installed service's mode, falling back to the config.
func installedSystem(st sharesvc.State, cfgSystem bool) bool {
	if st.Mode != "" {
		return st.Mode == "system"
	}
	return cfgSystem
}

func (m *Manager) setBusy(s *server, busy bool) {
	m.mu.Lock()
	s.busy = busy
	m.mu.Unlock()
	m.notify()
}

func (m *Manager) uninstallShare(s *server, profile string, system bool) error {
	m.setBusy(s, true)
	defer m.setBusy(s, false)
	return m.d.Share.Uninstall(m.opCtx(), profile, system)
}

// installShare installs sc's share service in the given mode. If the share
// is meant to be idle (off, or the server disconnected) it is paused again.
func (m *Manager) installShare(s *server, sc *config.Server, system bool) error {
	port, err := m.localSSHPort()
	if err != nil {
		return err
	}
	m.setBusy(s, true)
	defer m.setBusy(s, false)
	ctx := m.opCtx()
	if err := m.d.Share.Install(ctx, sharesvc.InstallOpts{
		Profile: sc.Name, Name: sc.Share.Name, Host: sc.Host, User: sc.User, KeyPath: config.ExpandHome(sc.KeyPath),
		HostKey: sc.HostKey, SocketDir: sc.SocketDir, Port: sc.Port, LocalPort: port, System: system,
	}); err != nil {
		return err
	}
	if !sc.Share.On || sc.Disconnected {
		return m.d.Share.Pause(ctx, sc.Name)
	}
	return nil
}

// localSSHPort returns the detected sshd port, detecting again if needed.
func (m *Manager) localSSHPort() (int, error) {
	m.mu.Lock()
	r := m.sshd
	m.mu.Unlock()
	if !r.Running {
		r = m.detectSSHD(m.opCtx())
	}
	if !r.Running {
		return 0, ErrNoSSHD
	}
	return r.Port, nil
}

// serverCopy returns a copy of the named server's config (maps shared).
func (m *Manager) serverCopy(name string) (config.Server, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	sc, ok := m.cfg.Server(name)
	if !ok {
		return config.Server{}, ErrUnknownServer
	}
	return *sc, nil
}

// updateShareIntent applies f to the server's ShareIntent and saves.
func (m *Manager) updateShareIntent(name string, f func(*config.ShareIntent)) error {
	m.mu.Lock()
	defer m.notify()
	defer m.mu.Unlock()
	sc, ok := m.cfg.Server(name)
	if !ok {
		return ErrUnknownServer
	}
	f(&sc.Share)
	return m.saveLocked()
}

func (m *Manager) setNameErr(s *server, code string) {
	m.mu.Lock()
	s.nameErr = code
	m.mu.Unlock()
	m.notify()
}

// checkName validates a share name and, when the server is reachable,
// rejects a name used by another device. Sets NameErr accordingly.
func (m *Manager) checkName(s *server, name string) error {
	if err := tunnel.ValidateName(name); err != nil {
		m.setNameErr(s, "invalid")
		return err
	}
	m.mu.Lock()
	c := s.conn
	m.mu.Unlock()
	if list, ok := m.liveDevices(m.opCtx(), c); ok {
		for _, d := range list {
			if d.Name == name && d.DeviceID != m.d.DeviceID {
				m.setNameErr(s, "taken")
				return ErrNameTaken
			}
		}
	}
	m.setNameErr(s, "")
	return nil
}

// ensureShareRunning installs the share service if needed, else resumes it.
func (m *Manager) ensureShareRunning(s *server, name string) error {
	st, err := m.shareStatus(name)
	if err != nil {
		return err
	}
	if st.Installed {
		return ignoreNotRunning(m.d.Share.Resume(m.opCtx(), name))
	}
	sc, err := m.serverCopy(name)
	if err != nil {
		return err
	}
	sc.Share.On = true
	return m.installShare(s, &sc, sc.Share.System)
}

// SetShare turns sharing this machine through server on or off. Turning it on
// requires a valid, untaken name and a running local sshd; the service is
// installed on first use and resumed afterwards. Off pauses it.
func (m *Manager) SetShare(server string, on bool) error {
	s, err := m.lockOp(server)
	if err != nil {
		return err
	}
	defer s.op.Unlock()
	defer m.syncShare(server)

	if !on {
		st, err := m.shareStatus(server)
		if err != nil {
			return err
		}
		// A stopped service can't be paused now; the intent is saved anyway and
		// the share loop pauses it once it runs again.
		if st.Running {
			if err := ignoreNotRunning(m.d.Share.Pause(m.opCtx(), server)); err != nil {
				return err
			}
		}
		return m.updateShareIntent(server, func(si *config.ShareIntent) { si.On, si.Decided = false, true })
	}

	sc, err := m.serverCopy(server)
	if err != nil {
		return err
	}
	if err := m.checkName(s, sc.Share.Name); err != nil {
		return err
	}
	if r := m.detectSSHD(m.opCtx()); !r.Running {
		return ErrNoSSHD
	}
	if !sc.Disconnected { // a disconnected server resumes the share on reconnect
		if err := m.ensureShareRunning(s, server); err != nil {
			return err
		}
	}
	return m.updateShareIntent(server, func(si *config.ShareIntent) { si.On, si.Decided = true, true })
}

// SetShareName changes the name this machine is shared under.
func (m *Manager) SetShareName(server, name string) error {
	s, err := m.lockOp(server)
	if err != nil {
		return err
	}
	defer s.op.Unlock()
	defer m.syncShare(server)
	if err := m.checkName(s, name); err != nil {
		return err
	}
	st, err := m.shareStatus(server)
	if err != nil {
		return err
	}
	if st.Installed {
		if err := m.d.Share.SetName(m.opCtx(), server, name); err != nil {
			return err
		}
	}
	return m.updateShareIntent(server, func(si *config.ShareIntent) { si.Name = name })
}

// SetShareSystem switches between user and system service. An installed
// service is uninstalled and reinstalled in the new mode; if authorization
// is cancelled the old mode is kept (reinstalled if already removed).
func (m *Manager) SetShareSystem(server string, system bool) error {
	s, err := m.lockOp(server)
	if err != nil {
		return err
	}
	defer s.op.Unlock()
	defer m.syncShare(server)

	sc, err := m.serverCopy(server)
	if err != nil {
		return err
	}
	st, err := m.shareStatus(server)
	if err != nil {
		return err
	}
	old := installedSystem(st, sc.Share.System) // the installed service decides
	if old == system {
		if sc.Share.System != system {
			return m.updateShareIntent(server, func(si *config.ShareIntent) { si.System = system })
		}
		return nil
	}
	if st.Installed {
		if _, err := m.localSSHPort(); err != nil { // check before removing the old service
			return err
		}
		if err := m.uninstallShare(s, server, old); err != nil {
			return err
		}
		if err := m.installShare(s, &sc, system); err != nil {
			if rerr := m.installShare(s, &sc, old); rerr != nil {
				_ = m.updateShareIntent(server, func(si *config.ShareIntent) { si.On = false })
				return errors.Join(err, fmt.Errorf("restore previous share service: %w", rerr))
			}
			return err
		}
	}
	return m.updateShareIntent(server, func(si *config.ShareIntent) { si.System = system })
}

// RetryShare makes the share service reconnect now; a service that is
// installed but not running is installed again instead.
func (m *Manager) RetryShare(server string) error {
	if _, err := m.serverCopy(server); err != nil {
		return err
	}
	defer m.syncShare(server)
	st, err := m.shareStatus(server)
	if err == nil && st.Installed && !st.Running {
		// Installed but not running (e.g. a failed update unloaded it): a retry
		// sent to the service cannot work, so install it again in its mode,
		// which loads and starts it (system mode asks for authorization).
		s, err := m.lockOp(server)
		if err != nil {
			return err
		}
		defer s.op.Unlock()
		sc, err := m.serverCopy(server)
		if err != nil {
			return err
		}
		return m.installShare(s, &sc, installedSystem(st, sc.Share.System))
	}
	return m.d.Share.Retry(m.opCtx(), server)
}

// UpdateShare reinstalls the share service in its current mode, e.g. when
// it is older than the bundled tunnel (system mode asks for authorization).
func (m *Manager) UpdateShare(server string) error {
	s, err := m.lockOp(server)
	if err != nil {
		return err
	}
	defer s.op.Unlock()
	defer m.syncShare(server)
	st, err := m.shareStatus(server)
	if err != nil || !st.Installed {
		return err
	}
	sc, err := m.serverCopy(server)
	if err != nil {
		return err
	}
	return m.installShare(s, &sc, installedSystem(st, sc.Share.System))
}
