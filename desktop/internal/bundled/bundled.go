// Package bundled locates the tunnel CLI shipped alongside the desktop app.
package bundled

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

// EnvVar overrides the bundled tunnel location (development and tests).
const EnvVar = "TUNNEL_APP_TUNNEL_BIN"

// ErrMissing is returned when no bundled tunnel executable can be found.
var ErrMissing = errors.New("bundled tunnel executable not found")

// executable returns the app's own executable path; replaceable in tests.
var executable = os.Executable

// TunnelPath returns the tunnel CLI path: $TUNNEL_APP_TUNNEL_BIN if set,
// otherwise the "tunnel" (Windows: "tunnel.exe") file next to the app
// executable (inside a macOS .app that is Contents/MacOS/).
func TunnelPath() (string, error) {
	if p := os.Getenv(EnvVar); p != "" {
		return p, nil
	}
	exe, err := executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	name := "tunnel"
	if runtime.GOOS == "windows" {
		name = "tunnel.exe"
	}
	p := filepath.Join(filepath.Dir(exe), name)
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return "", ErrMissing
	}
	return p, nil
}
