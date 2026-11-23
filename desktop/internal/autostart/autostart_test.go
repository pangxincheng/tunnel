package autostart

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutostartDarwinPlist(t *testing.T) {
	home := t.TempDir()
	exe := "/Applications/Tunnel & Co.app/Contents/MacOS/tunnel-app"
	if Enabled("darwin", home) {
		t.Fatal("enabled before Enable")
	}
	if err := Enable("darwin", exe, home); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, "Library", "LaunchAgents", "com.pangxincheng.tunnel-app.plist")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"com.pangxincheng.tunnel-app", "Tunnel &amp; Co.app/Contents/MacOS/tunnel-app", "RunAtLoad", "<true/>"} {
		if !strings.Contains(s, want) {
			t.Errorf("plist missing %q:\n%s", want, s)
		}
	}
	if !Enabled("darwin", home) {
		t.Error("not enabled after Enable")
	}
	if err := Disable("darwin", home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("plist still present: %v", err)
	}
	if err := Disable("darwin", home); err != nil {
		t.Errorf("second Disable: %v", err)
	}
}

func TestAutostartLinuxDesktop(t *testing.T) {
	home := t.TempDir()
	exe := `/opt/My "App"/tunnel-app`
	if err := Enable("linux", exe, home); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, ".config", "autostart", "tunnel-app.desktop")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"[Desktop Entry]", "Type=Application", `Exec="/opt/My \\"App\\"/tunnel-app"`, "X-GNOME-Autostart-enabled=true"} {
		if !strings.Contains(s, want) {
			t.Errorf("desktop file missing %q:\n%s", want, s)
		}
	}
	if !Enabled("linux", home) {
		t.Error("not enabled")
	}
	if err := Disable("linux", home); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Errorf("desktop file still present: %v", err)
	}
	if Enabled("linux", home) {
		t.Error("still enabled")
	}
}

func TestAutostartWindowsUsesRegistry(t *testing.T) {
	vals := map[string]string{}
	oldSet, oldDel, oldGet := regSet, regDel, regGet
	t.Cleanup(func() { regSet, regDel, regGet = oldSet, oldDel, oldGet })
	regSet = func(name, value string) error { vals[name] = value; return nil }
	regDel = func(name string) error { delete(vals, name); return nil }
	regGet = func(name string) (string, bool) { v, ok := vals[name]; return v, ok }

	home := t.TempDir()
	if err := Enable("windows", `C:\Program Files\Tunnel\tunnel-app.exe`, home); err != nil {
		t.Fatal(err)
	}
	if got := vals["TunnelApp"]; got != `"C:\Program Files\Tunnel\tunnel-app.exe"` {
		t.Errorf("registry value = %q", got)
	}
	if !Enabled("windows", home) {
		t.Error("not enabled")
	}
	if err := Disable("windows", home); err != nil {
		t.Fatal(err)
	}
	if len(vals) != 0 || Enabled("windows", home) {
		t.Errorf("registry not cleaned: %v", vals)
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Errorf("windows touched the filesystem: %v", entries)
	}
}

func TestAutostartErrors(t *testing.T) {
	if err := Enable("plan9", "/x", t.TempDir()); err == nil {
		t.Error("unsupported OS should error")
	}
	if err := Enable("linux", "", t.TempDir()); err == nil {
		t.Error("empty exe should error")
	}
	old := regSet
	t.Cleanup(func() { regSet = old })
	regSet = func(string, string) error { return errors.New("boom") }
	if err := Enable("windows", `C:\x.exe`, ""); err == nil {
		t.Error("registry error not propagated")
	}
}
