// Package autostart registers the app as a per-user login item.
//
// macOS: a LaunchAgent plist; Linux: an XDG autostart .desktop file;
// Windows: a value under HKCU\...\Run (via replaceable registry funcs).
package autostart

import (
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
)

const (
	label       = "com.pangxincheng.tunnel-app"
	desktopName = "tunnel-app.desktop"
	// RunValueName is the value name under the Windows Run key.
	RunValueName = "TunnelApp"
)

// Replaceable by tests; the real implementations live in registry_windows.go
// (other platforms: registry_other.go).
var (
	regSet = realRegSet
	regDel = realRegDel
	regGet = realRegGet
)

func plistPath(home string) string {
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist")
}

func desktopPath(home string) string {
	return filepath.Join(home, ".config", "autostart", desktopName)
}

// Enable registers exe to start when the user logs in.
func Enable(goos, exe, home string) error {
	if exe == "" {
		return errors.New("autostart: empty executable path")
	}
	switch goos {
	case "darwin":
		return writeFile(plistPath(home), darwinPlist(exe))
	case "linux":
		return writeFile(desktopPath(home), linuxDesktop(exe))
	case "windows":
		return regSet(RunValueName, `"`+exe+`"`)
	}
	return fmt.Errorf("autostart: unsupported OS %q", goos)
}

// Disable removes the login item; removing a missing one is not an error.
func Disable(goos, home string) error {
	switch goos {
	case "darwin":
		return removeFile(plistPath(home))
	case "linux":
		return removeFile(desktopPath(home))
	case "windows":
		return regDel(RunValueName)
	}
	return fmt.Errorf("autostart: unsupported OS %q", goos)
}

// Enabled reports whether the login item is registered.
func Enabled(goos, home string) bool {
	switch goos {
	case "darwin":
		return exists(plistPath(home))
	case "linux":
		return exists(desktopPath(home))
	case "windows":
		_, ok := regGet(RunValueName)
		return ok
	}
	return false
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func writeFile(p, content string) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func removeFile(p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func darwinPlist(exe string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + html.EscapeString(exe) + `</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`
}

// linuxDesktop quotes exe per the Desktop Entry spec: inside double quotes
// ", `, $ and \ are backslash-escaped, and backslashes in the string value
// are doubled again.
func linuxDesktop(exe string) string {
	var q strings.Builder
	for _, r := range exe {
		switch r {
		case '"', '`', '$':
			q.WriteString(`\\`)
		case '\\':
			q.WriteString(`\\\\`)
			continue
		}
		q.WriteRune(r)
	}
	return `[Desktop Entry]
Type=Application
Name=Tunnel
Exec="` + q.String() + `"
Terminal=false
X-GNOME-Autostart-enabled=true
`
}
