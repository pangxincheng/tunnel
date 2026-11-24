package core

import (
	"runtime"
	"time"

	"github.com/pangxincheng/tunnel/cli/tunnel"
	"github.com/pangxincheng/tunnel/desktop/internal/config"
	"github.com/pangxincheng/tunnel/desktop/internal/sshd"
)

// AppState is the frontend's single source of truth.
type AppState struct {
	Servers  []ServerView    `json:"servers"`
	Selected string          `json:"selected"`
	Settings config.Settings `json:"settings"`
	Platform string          `json:"platform"` // runtime.GOOS
	SSHD     sshd.Result     `json:"sshd"`
}

type ServerView struct {
	Name      string       `json:"name"`
	Host      string       `json:"host"`
	User      string       `json:"user"`
	Port      int          `json:"port"`
	KeyPath   string       `json:"key_path"`
	HostKey   string       `json:"host_key"`   // trusted host public key
	SocketDir string       `json:"socket_dir"` // empty = automatic
	Conn      string       `json:"conn"`       // ready|connecting|waiting|failed|disconnected
	ConnErr   string       `json:"conn_err"`
	RetryAt   time.Time    `json:"retry_at,omitzero"`   // set when Conn == "waiting"
	DevicesAt time.Time    `json:"devices_at,omitzero"` // when the device list was fetched
	Connected int          `json:"connected"`           // forwards on + device online + Conn == "ready"
	Online    int          `json:"online"`              // online devices, excluding this machine
	Share     ShareView    `json:"share"`
	Devices   []DeviceView `json:"devices"` // excluding this machine, sorted by name
}

type ShareView struct {
	On       bool      `json:"on"`
	Name     string    `json:"name"`
	System   bool      `json:"system"`
	Phase    string    `json:"phase"` // off|connecting|ready|waiting|failed|paused|not_running
	Err      string    `json:"err"`
	RetryAt  time.Time `json:"retry_at,omitzero"`
	NameErr  string    `json:"name_err"` // ""|invalid|taken
	Busy     bool      `json:"busy"`     // installing or uninstalling
	LogPath  string    `json:"log_path"`
	Outdated bool      `json:"outdated"` // installed service older than the bundled tunnel
}

type DeviceView struct {
	tunnel.DeviceInfo
	Online   bool      `json:"online"`
	LastSeen time.Time `json:"last_seen,omitzero"` // offline: max(started_at, last seen online)
	Port     int       `json:"port"`
	On       bool      `json:"on"`
	Err      string    `json:"err"` // ""|port_busy|port_invalid|port_conflict
}

// State returns a full snapshot.
func (m *Manager) State() AppState {
	m.mu.Lock()
	st := AppState{
		Servers:  make([]ServerView, 0, len(m.cfg.Servers)),
		Selected: m.cfg.Selected,
		Settings: m.cfg.Settings,
		Platform: runtime.GOOS,
		SSHD:     m.sshd,
	}
	for i := range m.cfg.Servers {
		sc := &m.cfg.Servers[i]
		st.Servers = append(st.Servers, m.serverViewLocked(sc, m.servers[sc.Name]))
	}
	m.mu.Unlock()
	for i := range st.Servers { // ShareCtl is never called under mu
		v := &st.Servers[i]
		v.Share.LogPath = m.d.Share.LogPath(v.Name, v.Share.System)
	}
	return st
}

func (m *Manager) serverViewLocked(sc *config.Server, s *server) ServerView {
	v := ServerView{Name: sc.Name, Host: sc.Host, User: sc.User, Port: sc.Port,
		KeyPath: sc.KeyPath, HostKey: sc.HostKey, SocketDir: sc.SocketDir,
		DevicesAt: sc.DevicesAt, Devices: []DeviceView{}}
	switch {
	case sc.Disconnected:
		v.Conn = "disconnected"
	case s.conn == nil:
		v.Conn = string(tunnel.PhaseConnecting)
	default:
		cs := s.conn.sess.State()
		v.Conn, v.ConnErr = string(cs.Phase), cs.Err
		if cs.Phase == tunnel.PhaseWaiting {
			v.RetryAt = cs.RetryAt
		}
	}
	for _, d := range s.devices { // already sorted by name
		if d.DeviceID == m.d.DeviceID {
			continue
		}
		f := sc.Forwards[d.Name]
		dv := DeviceView{DeviceInfo: d.DeviceInfo, Online: d.Online, Port: f.Port, On: f.On,
			Err: s.fwdErr[d.Name], LastSeen: sc.LastSeen[d.Name]}
		if !d.Online && d.StartedAt.After(dv.LastSeen) {
			dv.LastSeen = d.StartedAt
		}
		if d.Online {
			v.Online++
			if f.On && v.Conn == string(tunnel.PhaseReady) {
				v.Connected++
			}
		}
		v.Devices = append(v.Devices, dv)
	}
	v.Share = m.shareViewLocked(sc, s)
	return v
}

func (m *Manager) shareViewLocked(sc *config.Server, s *server) ShareView {
	sv := ShareView{On: sc.Share.On, Name: sc.Share.Name, System: sc.Share.System,
		NameErr: s.nameErr, Busy: s.busy, Phase: "off"}
	st := s.share
	if st.Installed { // name and mode come from the installed service (spec §6.3)
		if st.Mode != "" {
			sv.System = st.Mode == "system"
		}
		if st.Status != nil && st.Status.Name != "" {
			sv.Name = st.Status.Name
		}
	}
	switch {
	case !st.Installed:
	case !st.Running || st.Status == nil:
		sv.Phase = "not_running"
	default:
		sv.Phase = string(st.Status.Phase)
		sv.Err, sv.RetryAt = st.Status.Err, st.Status.RetryAt
		v, tv := st.Status.Version, m.d.TunnelVersion
		sv.Outdated = v != "" && tv != "" && tv != "dev" && v != tv
	}
	return sv
}
