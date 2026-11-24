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

// Replaceable in tests.
var (
	executable  = os.Executable // the app's own executable path
	goos        = runtime.GOOS
	linuxLibDir = "/usr/lib/tunnel-app" // where the Linux packages install the CLI
)

// TunnelPath returns the tunnel CLI path: $TUNNEL_APP_TUNNEL_BIN if set,
// otherwise the "tunnel" (Windows: "tunnel.exe") file next to the app
// executable (inside a macOS .app that is Contents/MacOS/), and on Linux
// then /usr/lib/tunnel-app/tunnel (the .deb/.rpm location, kept off PATH so
// it never shadows a separately installed tunnel CLI).
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
	if goos == "windows" {
		name = "tunnel.exe"
	}
	dirs := []string{filepath.Dir(exe)}
	if goos == "linux" {
		dirs = append(dirs, linuxLibDir)
	}
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", ErrMissing
}
