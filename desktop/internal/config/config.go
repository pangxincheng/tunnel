// Package config persists the app's local configuration (servers, forwards, settings).
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pangxincheng/tunnel/cli/tunnel"
)

type Server struct {
	Name         string               `json:"name"`
	Host         string               `json:"host"`
	User         string               `json:"user"`
	KeyPath      string               `json:"key_path"`
	HostKey      string               `json:"host_key"`   // trusted host public key (authorized_keys line)
	SocketDir    string               `json:"socket_dir"` // empty = automatic
	Port         int                  `json:"port"`
	Disconnected bool                 `json:"disconnected"`
	Share        ShareIntent          `json:"share"`
	Forwards     map[string]Forward   `json:"forwards"`  // key: device name
	LastSeen     map[string]time.Time `json:"last_seen"` // last time a device was seen online
	// Devices caches the last fetched device list (this machine excluded) so
	// rows stay visible while the server is disconnected or unreachable.
	Devices   []tunnel.DeviceInfo `json:"devices"`
	DevicesAt time.Time           `json:"devices_at"` // when Devices was fetched
}

type ShareIntent struct {
	On     bool   `json:"on"`
	Name   string `json:"name"`
	System bool   `json:"system"`
	// Decided is set once the user turned sharing on or off here (or the app
	// adopted an already running service); until then a running service
	// found on this server is adopted instead of paused.
	Decided bool `json:"decided,omitempty"`
}

type Forward struct {
	Port int  `json:"port"`
	On   bool `json:"on"`
}

type Settings struct {
	Mode            string `json:"mode"`   // system|light|dark
	Accent          string `json:"accent"` // green|blue|violet|amber|graphite
	Lang            string `json:"lang"`   // system|zh|en
	LaunchAtLogin   bool   `json:"launch_at_login"`
	RestoreForwards bool   `json:"restore_forwards"`
	// AutoCleanDays removes devices offline for more than this many days
	// from the server (forward-on devices excepted); 0 = off.
	AutoCleanDays int `json:"auto_clean_days"`
	// LogFontSize is the share log viewer's font size in px; 0 = default.
	LogFontSize int `json:"log_font_size"`
}

type Config struct {
	Servers  []Server `json:"servers"`
	Settings Settings `json:"settings"`
	Selected string   `json:"selected"`
}

func defaults() *Config {
	return &Config{Settings: Settings{Mode: "system", Accent: "green", Lang: "system", RestoreForwards: true}}
}

// ExpandHome expands a leading "~" or "~/" in p to the user's home
// directory; other paths (and "~user/...") are returned unchanged.
func ExpandHome(p string) string {
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~`+string(filepath.Separator)) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return filepath.Join(home, p[1:])
}

// DefaultPath returns os.UserConfigDir()/tunnel-app/config.json.
func DefaultPath() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "tunnel-app", "config.json"), nil
}

// Load reads the config. A missing file yields defaults; a corrupt file is
// renamed to <path>.broken-<unix> and defaults are returned.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaults(), nil
	}
	if err != nil {
		return nil, err
	}
	c := defaults()
	if err := json.Unmarshal(b, c); err != nil {
		broken := fmt.Sprintf("%s.broken-%d", path, time.Now().UnixNano())
		if rerr := os.Rename(path, broken); rerr != nil {
			return nil, fmt.Errorf("config corrupt and could not be moved aside: %w", rerr)
		}
		return defaults(), nil
	}
	return c, nil
}

// Save writes the config atomically (temp file + rename), file 0600, dir 0700.
func Save(path string, c *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp) // no-op after successful rename
	if err := f.Chmod(0o600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Server returns a pointer into c.Servers for the named server.
func (c *Config) Server(name string) (*Server, bool) {
	for i := range c.Servers {
		if c.Servers[i].Name == name {
			return &c.Servers[i], true
		}
	}
	return nil, false
}

// ValidateServerName checks the name rule and local uniqueness; except is the
// current name when renaming (excluded from the uniqueness check).
func (c *Config) ValidateServerName(name, except string) error {
	if err := tunnel.ValidateName(name); err != nil {
		return fmt.Errorf("名称不合法:%w", err)
	}
	for _, s := range c.Servers {
		if s.Name == name && name != except {
			return fmt.Errorf("名称 %q 已存在", name)
		}
	}
	return nil
}
