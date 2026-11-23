package sharesvc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type call struct {
	name string
	args []string
}

type fakeRunner struct {
	calls  []call
	stdout string
	stderr string
	exit   int
	err    error
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
	f.calls = append(f.calls, call{name, args})
	return []byte(f.stdout), []byte(f.stderr), f.exit, f.err
}

func TestStatusRunning(t *testing.T) {
	r := &fakeRunner{stdout: `{"phase":"ready","err":"","attempt":0,"name":"mac","socket":"/s","paused":false,"version":"1","mode":"user"}` + "\n"}
	s := New("/bin/tunnel", r, "darwin", "alice")
	st, err := s.Status(context.Background(), "p1")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Installed || !st.Running || st.Mode != "user" || st.Status == nil || st.Status.Phase != "ready" || st.Status.Name != "mac" {
		t.Fatalf("state = %+v", st)
	}
	want := call{"/bin/tunnel", []string{"status", "--json", "--profile", "p1"}}
	if !reflect.DeepEqual(r.calls[0], want) {
		t.Fatalf("call = %+v", r.calls[0])
	}
}

func TestStatusNotRunning(t *testing.T) {
	r := &fakeRunner{exit: 3, stdout: `{"installed":true,"running":false,"mode":"system"}`}
	st, err := New("t", r, "linux", "a").Status(context.Background(), "p")
	if err != nil || !st.Installed || st.Running || st.Mode != "system" || st.Status != nil {
		t.Fatalf("st=%+v err=%v", st, err)
	}
}

func TestStatusNotInstalled(t *testing.T) {
	r := &fakeRunner{exit: 4, stdout: `{"installed":false}`}
	st, err := New("t", r, "linux", "a").Status(context.Background(), "p")
	if err != nil || st.Installed || st.Running || st.Mode != "" {
		t.Fatalf("st=%+v err=%v", st, err)
	}
}

func TestStatusError(t *testing.T) {
	r := &fakeRunner{exit: 1, stderr: "time=x level=ERROR msg=\"tunnel status 失败\" err=\"boom here\"\n"}
	_, err := New("t", r, "linux", "a").Status(context.Background(), "p")
	if err == nil || !strings.Contains(err.Error(), "boom here") || strings.Contains(err.Error(), "level=") {
		t.Fatalf("err = %v", err)
	}
	r = &fakeRunner{exit: 1, stderr: "  plain failure \n"}
	_, err = New("t", r, "linux", "a").Status(context.Background(), "p")
	if err == nil || !strings.Contains(err.Error(), "plain failure") {
		t.Fatalf("err = %v", err)
	}
}

func TestSimpleCommands(t *testing.T) {
	r := &fakeRunner{}
	s := New("t", r, "linux", "a")
	ctx := context.Background()
	for _, e := range []error{s.Pause(ctx, "p"), s.Resume(ctx, "p"), s.Retry(ctx, "p"), s.SetName(ctx, "p", "n"), s.Uninstall(ctx, "p", false)} {
		if e != nil {
			t.Fatal(e)
		}
	}
	want := [][]string{
		{"pause", "--profile", "p"},
		{"resume", "--profile", "p"},
		{"retry", "--profile", "p"},
		{"set-name", "n", "--profile", "p"},
		{"uninstall", "--profile", "p"},
	}
	for i, w := range want {
		if r.calls[i].name != "t" || !reflect.DeepEqual(r.calls[i].args, w) {
			t.Errorf("call %d = %+v, want %v", i, r.calls[i], w)
		}
	}
}

func TestSimpleCommandFailure(t *testing.T) {
	r := &fakeRunner{exit: 2, stderr: "no socket"}
	if err := New("t", r, "linux", "a").Pause(context.Background(), "p"); err == nil || !strings.Contains(err.Error(), "no socket") {
		t.Fatalf("err = %v", err)
	}
}

func opts() InstallOpts {
	return InstallOpts{Profile: "p", Name: "mac", Host: "1.2.3.4", Port: 22, User: "u",
		KeyPath: "/k/id", HostKey: "ssh-ed25519 AAAA", LocalPort: 2222, SocketDir: "/run/t"}
}

func TestInstallUserArgs(t *testing.T) {
	r := &fakeRunner{}
	if err := New("/bin/tunnel", r, "darwin", "alice").Install(context.Background(), opts()); err != nil {
		t.Fatal(err)
	}
	want := call{"/bin/tunnel", []string{"install", "--mode", "user", "--profile", "p", "--name", "mac",
		"--remote-ip", "1.2.3.4", "--remote-ssh-port", "22", "--user", "u", "--local-ssh-key", "/k/id",
		"--remote-ssh-pub", "ssh-ed25519 AAAA", "--local-ssh-port", "2222", "--remote-socket-dir", "/run/t"}}
	if len(r.calls) != 1 || !reflect.DeepEqual(r.calls[0], want) {
		t.Fatalf("calls = %+v", r.calls)
	}
}

func TestInstallOmitsEmptySocketDir(t *testing.T) {
	r := &fakeRunner{}
	o := opts()
	o.SocketDir = ""
	if err := New("t", r, "linux", "a").Install(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	for _, a := range r.calls[0].args {
		if a == "--remote-socket-dir" {
			t.Fatal("unexpected --remote-socket-dir")
		}
	}
}

func TestInstallSystemDarwinUsesOsascript(t *testing.T) {
	r := &fakeRunner{}
	o := opts()
	o.System = true
	o.KeyPath = "/Users/a b/it's/id"
	if err := New("/App/My App/tunnel", r, "darwin", "alice").Install(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	c := r.calls[0]
	if c.name != "osascript" || len(c.args) != 2 || c.args[0] != "-e" {
		t.Fatalf("call = %+v", c)
	}
	sc := c.args[1]
	for _, w := range []string{"--for-user alice", "with administrator privileges", "do shell script \"",
		`'/App/My App/tunnel'`, `'/Users/a b/it'\\''s/id'`, "--mode system"} {
		if !strings.Contains(sc, w) {
			t.Errorf("script missing %q:\n%s", w, sc)
		}
	}
}

func TestInstallSystemLinuxUsesPkexec(t *testing.T) {
	r := &fakeRunner{}
	o := opts()
	o.System = true
	if err := New("/opt/tunnel", r, "linux", "alice").Install(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	c := r.calls[0]
	if c.name != "pkexec" || c.args[0] != "/opt/tunnel" || c.args[1] != "install" {
		t.Fatalf("call = %+v", c)
	}
	j := strings.Join(c.args, " ")
	if !strings.Contains(j, "--mode system") || !strings.Contains(j, "--for-user alice") {
		t.Fatalf("args = %v", c.args)
	}
}

func TestUninstallSystemElevates(t *testing.T) {
	r := &fakeRunner{}
	if err := New("/opt/tunnel", r, "linux", "alice").Uninstall(context.Background(), "p", true); err != nil {
		t.Fatal(err)
	}
	want := call{"pkexec", []string{"/opt/tunnel", "uninstall", "--mode", "system", "--profile", "p"}}
	if !reflect.DeepEqual(r.calls[0], want) {
		t.Fatalf("call = %+v", r.calls[0])
	}
	r = &fakeRunner{}
	if err := New("/opt/tunnel", r, "darwin", "alice").Uninstall(context.Background(), "p", true); err != nil {
		t.Fatal(err)
	}
	if r.calls[0].name != "osascript" || !strings.Contains(r.calls[0].args[1], "uninstall --mode system --profile p") {
		t.Fatalf("call = %+v", r.calls[0])
	}
}

func TestElevationCancelled(t *testing.T) {
	o := opts()
	o.System = true
	r := &fakeRunner{exit: 1, stderr: "execution error: User canceled. (-128)"}
	if err := New("t", r, "darwin", "a").Install(context.Background(), o); !errors.Is(err, ErrCancelled) {
		t.Fatalf("darwin err = %v", err)
	}
	for _, code := range []int{126, 127} {
		r = &fakeRunner{exit: code}
		if err := New("t", r, "linux", "a").Install(context.Background(), o); !errors.Is(err, ErrCancelled) {
			t.Fatalf("linux %d err = %v", code, err)
		}
	}
	r = &fakeRunner{exit: 1, stderr: "real failure"}
	err := New("t", r, "linux", "a").Install(context.Background(), o)
	if err == nil || errors.Is(err, ErrCancelled) || !strings.Contains(err.Error(), "real failure") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstallSystemWindowsRejected(t *testing.T) {
	r := &fakeRunner{}
	o := opts()
	o.System = true
	s := New("t", r, "windows", "a")
	if err := s.Install(context.Background(), o); err == nil {
		t.Fatal("want error")
	}
	if err := s.Uninstall(context.Background(), "p", true); err == nil {
		t.Fatal("want error")
	}
	if len(r.calls) != 0 {
		t.Fatalf("calls = %+v", r.calls)
	}
}

func TestLogPath(t *testing.T) {
	old := userHome
	userHome = func() (string, error) { return filepath.FromSlash("/home/x"), nil }
	defer func() { userHome = old }()
	if got := New("t", nil, "darwin", "a").LogPath("p", true); got != "/var/log/tunnel/tunnel-p.log" {
		t.Error(got)
	}
	want := filepath.Join("/home/x", "Library", "Application Support", "tunnel", "tunnel-p", "tunnel.log")
	if got := New("t", nil, "darwin", "a").LogPath("p", false); got != want {
		t.Error(got)
	}
	want = filepath.Join("/home/x", "AppData", "Roaming", "tunnel", "tunnel-p", "tunnel.log")
	if got := New("t", nil, "windows", "a").LogPath("p", false); got != want {
		t.Error(got)
	}
	if got := New("t", nil, "linux", "a").LogPath("p", true); got != "" {
		t.Error(got)
	}
}

func TestExecRunnerCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _, err := ExecRunner{}.Run(ctx, os.Args[0], "-test.run=^$")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestExecRunnerRunsInEmptyTempDir(t *testing.T) {
	// the test binary re-executed with -test.run=^$ exits 0 and ignores cwd; just prove the happy path
	_, _, exit, err := ExecRunner{}.Run(context.Background(), os.Args[0], "-test.run=^$")
	if err != nil || exit != 0 {
		t.Fatalf("exit=%d err=%v", exit, err)
	}
}

func TestInvalidInputNeverRunsCLI(t *testing.T) {
	ctx := context.Background()
	r := &fakeRunner{}
	s := New("t", r, "linux", "alice")
	bad := []error{
		s.SetName(ctx, "home", "-h"),
		s.SetName(ctx, "../x", "ok"),
		s.Pause(ctx, "../x"),
		s.Resume(ctx, "../x"),
		s.Retry(ctx, "../x"),
		s.Uninstall(ctx, "../x", false),
		s.Uninstall(ctx, "../x", true),
	}
	_, err := s.Status(ctx, "../x")
	bad = append(bad, err)
	o := opts()
	o.Profile = "../x"
	bad = append(bad, s.Install(ctx, o))
	o = opts()
	o.Name = "-bad"
	bad = append(bad, s.Install(ctx, o))
	for i, e := range bad {
		if e == nil {
			t.Errorf("case %d: want error", i)
		}
	}
	if len(r.calls) != 0 {
		t.Fatalf("runner called: %+v", r.calls)
	}
}

func TestSystemRequiresCurrentUser(t *testing.T) {
	r := &fakeRunner{}
	s := New("t", r, "linux", "")
	o := opts()
	o.System = true
	if err := s.Install(context.Background(), o); err == nil {
		t.Error("install: want error")
	}
	if err := s.Uninstall(context.Background(), "p", true); err == nil {
		t.Error("uninstall: want error")
	}
	if len(r.calls) != 0 {
		t.Fatalf("calls = %+v", r.calls)
	}
}

func TestOsascriptCancelMarkerIsExact(t *testing.T) {
	o := opts()
	o.System = true
	r := &fakeRunner{exit: 1, stderr: "failed with code -1280 something"}
	if err := New("t", r, "darwin", "a").Install(context.Background(), o); errors.Is(err, ErrCancelled) {
		t.Fatal("bare -128 substring must not mean cancelled")
	}
}
