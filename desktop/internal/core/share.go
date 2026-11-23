package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/pangxincheng/tunnel-app/desktop/internal/config"
	"github.com/pangxincheng/tunnel-app/desktop/internal/sharesvc"
	"github.com/pangxincheng/tunnel/tunnel"
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
			m.syncShare(n)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
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
		Profile: sc.Name, Name: sc.Share.Name, Host: sc.Host, User: sc.User, KeyPath: sc.KeyPath,
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
		return m.d.Share.Resume(m.opCtx(), name)
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
		if st.Installed {
			if err := m.d.Share.Pause(m.opCtx(), server); err != nil {
				return err
			}
		}
		return m.updateShareIntent(server, func(si *config.ShareIntent) { si.On = false })
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
	return m.updateShareIntent(server, func(si *config.ShareIntent) { si.On = true })
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
	if sc.Share.System == system {
		return nil
	}
	st, err := m.shareStatus(server)
	if err != nil {
		return err
	}
	if st.Installed {
		old := installedSystem(st, sc.Share.System)
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

// RetryShare makes the share service reconnect now.
func (m *Manager) RetryShare(server string) error {
	if _, err := m.serverCopy(server); err != nil {
		return err
	}
	defer m.syncShare(server)
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
