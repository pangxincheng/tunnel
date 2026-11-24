package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pangxincheng/tunnel/cli/tunnel"
)

func TestLoadMissing(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Servers) != 0 || !c.Settings.RestoreForwards || c.Settings.Accent != "green" ||
		c.Settings.Mode != "system" || c.Settings.Lang != "system" {
		t.Fatalf("bad defaults: %+v", c)
	}
}

func TestLoadAppliesDefaultsForAbsentFields(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(`{"settings":{"accent":"blue"}}`), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Settings.Accent != "blue" || !c.Settings.RestoreForwards || c.Settings.Mode != "system" {
		t.Fatalf("%+v", c.Settings)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "config.json")
	c, _ := Load(p)
	c.Selected = "home"
	c.Settings.Accent = "violet"
	c.Settings.RestoreForwards = false
	c.Servers = []Server{{
		Name: "home", Host: "h.example", User: "u", KeyPath: "/k", HostKey: "ssh-ed25519 AAA", SocketDir: "/s", Port: 2222,
		Disconnected: true, Share: ShareIntent{On: true, Name: "mac", System: true},
		Forwards:  map[string]Forward{"d1": {Port: 5102, On: true}},
		LastSeen:  map[string]time.Time{"d1": time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		Devices:   []tunnel.DeviceInfo{{DeviceID: "id1", Name: "d1", OS: "linux", StartedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}},
		DevicesAt: time.Date(2026, 1, 2, 3, 4, 6, 0, time.UTC),
	}}
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	for _, k := range []string{"host_key", "socket_dir", "last_seen", "restore_forwards", "launch_at_login", "key_path", "devices", "devices_at", "device_id"} {
		if !strings.Contains(string(raw), `"`+k+`"`) {
			t.Errorf("missing json key %s", k)
		}
	}
	got, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	s, ok := got.Server("home")
	if !ok || s.Host != "h.example" || s.Port != 2222 || !s.Disconnected || !s.Share.System ||
		s.Forwards["d1"].Port != 5102 || !s.LastSeen["d1"].Equal(c.Servers[0].LastSeen["d1"]) ||
		len(s.Devices) != 1 || s.Devices[0].Name != "d1" || s.Devices[0].OS != "linux" ||
		!s.Devices[0].StartedAt.Equal(c.Servers[0].Devices[0].StartedAt) || !s.DevicesAt.Equal(c.Servers[0].DevicesAt) {
		t.Fatalf("%+v", s)
	}
	if got.Selected != "home" || got.Settings.Accent != "violet" || got.Settings.RestoreForwards {
		t.Fatalf("%+v", got)
	}
	if _, ok := got.Server("zzz"); ok {
		t.Fatal("unexpected")
	}
}

func TestSaveIsAtomicAnd0600(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	p := filepath.Join(dir, "config.json")
	c, _ := Load(p)
	if err := Save(p, c); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("file perm %v", fi.Mode().Perm())
	}
	di, _ := os.Stat(dir)
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("dir perm %v", di.Mode().Perm())
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("leftover temp files: %v", ents)
	}
}

func TestLoadCorrupt(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	os.WriteFile(p, []byte("{"), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Servers) != 0 || !c.Settings.RestoreForwards {
		t.Fatalf("%+v", c)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("original should be renamed")
	}
	m, _ := filepath.Glob(filepath.Join(dir, "config.json.broken-*"))
	if len(m) != 1 {
		t.Fatalf("broken files: %v", m)
	}
	b, _ := os.ReadFile(m[0])
	if string(b) != "{" {
		t.Fatalf("content %q", b)
	}
}

func TestValidateServerName(t *testing.T) {
	c := &Config{Servers: []Server{{Name: "home"}, {Name: "work"}}}
	if err := c.ValidateServerName("lab", ""); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"", "-a", "a b", strings.Repeat("a", 65), "home"} {
		if c.ValidateServerName(n, "") == nil {
			t.Errorf("%q should be rejected", n)
		}
	}
	if err := c.ValidateServerName("home", "home"); err != nil {
		t.Fatalf("rename-to-self: %v", err)
	}
	if c.ValidateServerName("work", "home") == nil {
		t.Fatal("dup on rename should fail")
	}
}

func TestAllocatePortSkipsUsedAndBusy(t *testing.T) {
	c := &Config{Servers: []Server{{Name: "a", Forwards: map[string]Forward{"d": {Port: 5102}}}}}
	got := c.AllocatePort(func(p int) bool { return p != 5103 })
	if got != 5104 {
		t.Fatalf("got %d", got)
	}
	if c.AllocatePort(func(int) bool { return false }) != 0 {
		t.Fatal("expected 0")
	}
}

func TestCheckPort(t *testing.T) {
	c := &Config{Servers: []Server{
		{Name: "a", Forwards: map[string]Forward{"d1": {Port: 5102}}},
		{Name: "b", Forwards: map[string]Forward{"d2": {Port: 5103, On: false}}},
	}}
	if c.CheckPort("a", "d1", 1023) == nil || c.CheckPort("a", "d1", 65536) == nil {
		t.Fatal("range")
	}
	if c.CheckPort("a", "d1", 1024) != nil || c.CheckPort("a", "d1", 65535) != nil {
		t.Fatal("bounds should pass")
	}
	if c.CheckPort("a", "d1", 5103) == nil {
		t.Fatal("conflict with other server's device")
	}
	if c.CheckPort("a", "d1", 5102) != nil {
		t.Fatal("same device same port")
	}
	if c.CheckPort("a", "d9", 5102) == nil {
		t.Fatal("other device in same server conflicts")
	}
}

func TestCheckPortErrors(t *testing.T) {
	c := &Config{Servers: []Server{{Name: "a", Forwards: map[string]Forward{"d1": {Port: 5102}}}}}
	if err := c.CheckPort("a", "d2", 80); !errors.Is(err, ErrPortRange) {
		t.Fatalf("range: %v", err)
	}
	if err := c.CheckPort("a", "d2", 5102); !errors.Is(err, ErrPortConflict) {
		t.Fatalf("conflict: %v", err)
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	cases := map[string]string{
		"~/.ssh/id": filepath.Join(home, ".ssh", "id"),
		"~":         home,
		"/abs/id":   "/abs/id",
		"rel/~/id":  "rel/~/id",
		"~other/id": "~other/id",
		"":          "",
	}
	for in, want := range cases {
		if got := ExpandHome(in); got != want {
			t.Errorf("ExpandHome(%q) = %q, want %q", in, got, want)
		}
	}
}
