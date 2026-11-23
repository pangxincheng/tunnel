package bundled

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func exeName() string {
	if runtime.GOOS == "windows" {
		return "tunnel.exe"
	}
	return "tunnel"
}

// fakeExecutable points the package at an app executable inside dir.
func fakeExecutable(t *testing.T, dir string) {
	t.Helper()
	old := executable
	executable = func() (string, error) { return filepath.Join(dir, "app"), nil }
	t.Cleanup(func() { executable = old })
}

func TestTunnelPathEnv(t *testing.T) {
	t.Setenv(EnvVar, "/custom/path/tunnel")
	got, err := TunnelPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != "/custom/path/tunnel" {
		t.Fatalf("got %q", got)
	}
}

func TestTunnelPathMissing(t *testing.T) {
	t.Setenv(EnvVar, "")
	fakeExecutable(t, t.TempDir())
	if _, err := TunnelPath(); !errors.Is(err, ErrMissing) {
		t.Fatalf("want ErrMissing, got %v", err)
	}
}

func TestTunnelPathBesideExecutable(t *testing.T) {
	t.Setenv(EnvVar, "")
	dir := t.TempDir()
	want := filepath.Join(dir, exeName())
	if err := os.WriteFile(want, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeExecutable(t, dir)
	got, err := TunnelPath()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestTunnelPathDirectoryIsMissing(t *testing.T) {
	t.Setenv(EnvVar, "")
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, exeName()), 0o755); err != nil {
		t.Fatal(err)
	}
	fakeExecutable(t, dir)
	if _, err := TunnelPath(); !errors.Is(err, ErrMissing) {
		t.Fatalf("want ErrMissing, got %v", err)
	}
}

func TestTunnelPathFollowsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	t.Setenv(EnvVar, "")
	real := t.TempDir()
	want := filepath.Join(real, exeName())
	if err := os.WriteFile(want, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "app"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := t.TempDir()
	if err := os.Symlink(filepath.Join(real, "app"), filepath.Join(link, "app")); err != nil {
		t.Fatal(err)
	}
	fakeExecutable(t, link)
	got, err := TunnelPath()
	if err != nil {
		t.Fatal(err)
	}
	// EvalSymlinks also resolves e.g. /var -> /private/var on macOS.
	wantResolved, _ := filepath.EvalSymlinks(want)
	if got != wantResolved {
		t.Fatalf("got %q want %q", got, wantResolved)
	}
}

func TestTunnelPathLinuxLibDir(t *testing.T) {
	t.Setenv(EnvVar, "")
	lib := t.TempDir()
	oldGOOS, oldLib := goos, linuxLibDir
	goos, linuxLibDir = "linux", lib
	t.Cleanup(func() { goos, linuxLibDir = oldGOOS, oldLib })
	exeDir := t.TempDir()
	fakeExecutable(t, exeDir)

	if _, err := TunnelPath(); !errors.Is(err, ErrMissing) {
		t.Fatalf("want ErrMissing, got %v", err)
	}
	want := filepath.Join(lib, "tunnel")
	if err := os.WriteFile(want, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := TunnelPath(); err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
	// the executable's own directory wins
	beside := filepath.Join(exeDir, "tunnel")
	if err := os.WriteFile(beside, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := TunnelPath(); err != nil || got != beside {
		t.Fatalf("got %q, %v; want %q", got, err, beside)
	}
	// other systems never look in the Linux lib dir
	goos = "darwin"
	os.Remove(beside)
	if _, err := TunnelPath(); !errors.Is(err, ErrMissing) {
		t.Fatalf("darwin: want ErrMissing, got %v", err)
	}
}
