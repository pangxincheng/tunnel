package svc

import (
	"errors"
	"strings"
	"testing"
)

type fakeRunner struct {
	cmds []string
	fail map[string]error
}

func (f *fakeRunner) Run(name string, args ...string) error {
	c := strings.Join(append([]string{name}, args...), " ")
	f.cmds = append(f.cmds, c)
	return f.fail[c]
}

type fakeFS struct {
	files   map[string][]byte
	removed []string
}

func newFS() *fakeFS { return &fakeFS{files: map[string][]byte{}} }
func (f *fakeFS) WriteFile(p string, b []byte) error {
	f.files[p] = b
	return nil
}
func (f *fakeFS) Remove(p string) error {
	delete(f.files, p)
	f.removed = append(f.removed, p)
	return nil
}
func (f *fakeFS) Exists(p string) bool { _, ok := f.files[p]; return ok }

func TestServiceName(t *testing.T) {
	if got := ServiceName("default", "nas"); got != "tunnel-default-nas" {
		t.Fatal(got)
	}
}

func TestPathsForTable(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	const s = "tunnel-default-nas"
	type c struct {
		mode       Mode
		goos, home string
		want       Paths
	}
	cases := []c{
		{ModeSystem, "linux", "/home/u", Paths{"/etc/tunnel/" + s, "/var/lib/tunnel/" + s + "/state.json", "/run/tunnel/" + s + ".sock", "/usr/local/bin/tunnel", ""}},
		{ModeSystem, "darwin", "/Users/u", Paths{"/etc/tunnel/" + s, "/var/lib/tunnel/" + s + "/state.json", "/var/run/tunnel/" + s + ".sock", "/usr/local/bin/tunnel", "/var/log/tunnel/" + s + ".log"}},
		{ModeUser, "linux", "/home/u", Paths{"/home/u/.config/tunnel/" + s, "/home/u/.config/tunnel/" + s + "/state.json", "/run/user/1000/tunnel/" + s + ".sock", "/home/u/.local/bin/tunnel", ""}},
		{ModeUser, "darwin", "/Users/u", Paths{"/Users/u/Library/Application Support/tunnel/" + s, "/Users/u/Library/Application Support/tunnel/" + s + "/state.json", "/Users/u/Library/Application Support/tunnel/" + s + "/control.sock", "/Users/u/Library/Application Support/tunnel/bin/tunnel", "/Users/u/Library/Application Support/tunnel/" + s + "/tunnel.log"}},
		{ModeUser, "windows", `C:\Users\u`, Paths{`C:\Users\u\AppData\Roaming\tunnel\` + s, `C:\Users\u\AppData\Roaming\tunnel\` + s + `\state.json`, `\\.\pipe\tunnel-` + s, `C:\Users\u\AppData\Local\tunnel\tunnel.exe`, `C:\Users\u\AppData\Roaming\tunnel\` + s + `\tunnel.log`}},
	}
	for _, tc := range cases {
		if got := PathsFor(tc.mode, s, tc.goos, tc.home); got != tc.want {
			t.Errorf("%s/%s:\n got %+v\nwant %+v", tc.mode, tc.goos, got, tc.want)
		}
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	got := PathsFor(ModeUser, s, "linux", "/home/u")
	if got.ControlAddr != "/home/u/.config/tunnel/"+s+"/control.sock" {
		t.Error(got.ControlAddr)
	}
}

func TestRenderSystemdUnit(t *testing.T) {
	sp := Spec{Name: "tunnel-default-nas", Mode: ModeSystem, Exec: "/usr/local/bin/tunnel", Args: []string{"run-service", "--mode", "system", "--profile", "default", "--name", "my nas"}}
	u := renderSystemdUnit(sp)
	for _, w := range []string{"Restart=on-failure", "RestartSec=10", "WantedBy=multi-user.target", "After=network-online.target",
		`ExecStart=/usr/local/bin/tunnel run-service --mode system --profile default --name "my nas"`} {
		if !strings.Contains(u, w) {
			t.Errorf("missing %q in\n%s", w, u)
		}
	}
	sp.Mode = ModeUser
	u = renderSystemdUnit(sp)
	if !strings.Contains(u, "WantedBy=default.target") || strings.Contains(u, "multi-user") {
		t.Error(u)
	}
	if q := systemdQuote(`a"b\c%d$e`); q != `"a\"b\\c%%d$$e"` {
		t.Error(q)
	}
}

func TestRenderLaunchdPlist(t *testing.T) {
	sp := Spec{Name: "tunnel-default-nas", Exec: "/Users/u/Library/Application Support/tunnel/bin/tunnel", Args: []string{"--name", "a&b<c>"}, LogPath: "/x/tunnel.log"}
	p := renderLaunchdPlist(sp)
	for _, w := range []string{"<string>com.pangxincheng.tunnel-default-nas</string>", "<key>RunAtLoad</key>", "<key>KeepAlive</key>",
		"<key>SuccessfulExit</key>\n\t\t<false/>", "<key>ThrottleInterval</key>\n\t<integer>10</integer>",
		"<key>StandardOutPath</key>\n\t<string>/x/tunnel.log</string>", "<key>StandardErrorPath</key>",
		"<string>/Users/u/Library/Application Support/tunnel/bin/tunnel</string>", "<string>a&amp;b&lt;c&gt;</string>"} {
		if !strings.Contains(p, w) {
			t.Errorf("missing %q in\n%s", w, p)
		}
	}
}

func TestRenderTaskXML(t *testing.T) {
	sp := Spec{Name: "tunnel-default-nas", Exec: `C:\Users\u\AppData\Local\tunnel\tunnel.exe`, Args: []string{"run-service", "--name", `my "nas" & co`}}
	x := renderTaskXML(sp)
	for _, w := range []string{"<LogonTrigger>", "<RestartOnFailure>", "<Interval>PT1M</Interval>", "<Count>999</Count>",
		"<Hidden>true</Hidden>", "<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		`<Command>C:\Users\u\AppData\Local\tunnel\tunnel.exe</Command>`,
		`<Arguments>run-service --name &#34;my \&#34;nas\&#34; &amp; co&#34;</Arguments>`} {
		if !strings.Contains(x, w) {
			t.Errorf("missing %q in\n%s", w, x)
		}
	}
}

func spec(mode Mode) Spec {
	return Spec{Name: "tunnel-default-nas", Mode: mode, Exec: "/bin/tunnel", Args: []string{"run-service"}}
}

func newMgr(t *testing.T, mode Mode, goos string) (Manager, *fakeRunner, *fakeFS) {
	t.Helper()
	r, fs := &fakeRunner{}, newFS()
	m, err := NewManager(mode, goos, "/home/u", r, WithFS(fs), WithUID(func() int { return 501 }))
	if err != nil {
		t.Fatal(err)
	}
	return m, r, fs
}

func eq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestInstallCommandSequence(t *testing.T) {
	m, r, fs := newMgr(t, ModeSystem, "linux")
	if err := m.Install(spec(ModeSystem)); err != nil {
		t.Fatal(err)
	}
	eq(t, r.cmds, []string{"systemctl daemon-reload", "systemctl enable --now tunnel-default-nas", "systemctl restart tunnel-default-nas"})
	if !fs.Exists("/etc/systemd/system/tunnel-default-nas.service") {
		t.Error(fs.files)
	}
	ok, _ := m.Installed("tunnel-default-nas")
	if !ok {
		t.Error("not installed")
	}
	r.cmds = nil
	if err := m.Uninstall("tunnel-default-nas"); err != nil {
		t.Fatal(err)
	}
	eq(t, r.cmds, []string{"systemctl disable --now tunnel-default-nas", "systemctl daemon-reload"})
	if ok, _ := m.Installed("tunnel-default-nas"); ok {
		t.Error("still installed")
	}

	m, r, fs = newMgr(t, ModeUser, "linux")
	m.Install(spec(ModeUser))
	eq(t, r.cmds, []string{"systemctl --user daemon-reload", "systemctl --user enable --now tunnel-default-nas", "systemctl --user restart tunnel-default-nas"})
	if !fs.Exists("/home/u/.config/systemd/user/tunnel-default-nas.service") {
		t.Error(fs.files)
	}

	m, r, fs = newMgr(t, ModeUser, "darwin")
	m.Install(spec(ModeUser))
	eq(t, r.cmds, []string{
		"launchctl bootout gui/501/com.pangxincheng.tunnel-default-nas",
		"launchctl bootstrap gui/501 /home/u/Library/LaunchAgents/com.pangxincheng.tunnel-default-nas.plist"})
	r.cmds = nil
	m.Uninstall("tunnel-default-nas")
	eq(t, r.cmds, []string{"launchctl bootout gui/501/com.pangxincheng.tunnel-default-nas"})
	if len(fs.files) != 0 {
		t.Error(fs.files)
	}

	m, r, _ = newMgr(t, ModeSystem, "darwin")
	m.Install(spec(ModeSystem))
	eq(t, r.cmds, []string{
		"launchctl bootout system/com.pangxincheng.tunnel-default-nas",
		"launchctl bootstrap system /Library/LaunchDaemons/com.pangxincheng.tunnel-default-nas.plist"})

	m, r, fs = newMgr(t, ModeUser, "windows")
	m.Install(spec(ModeUser))
	xmlPath := `/home/u\AppData\Roaming\tunnel\tunnel-default-nas\task.xml`
	eq(t, r.cmds, []string{
		"schtasks /Create /TN tunnel-default-nas /XML " + xmlPath + " /F",
		"schtasks /End /TN tunnel-default-nas",
		"schtasks /Run /TN tunnel-default-nas"})
	b := fs.files[xmlPath]
	if len(b) < 2 || b[0] != 0xFF || b[1] != 0xFE {
		t.Error("task xml must be UTF-16LE with BOM")
	}
	r.cmds = nil
	m.Uninstall("tunnel-default-nas")
	eq(t, r.cmds, []string{"schtasks /End /TN tunnel-default-nas", "schtasks /Delete /TN tunnel-default-nas /F"})
}

func TestInstallErrorsStop(t *testing.T) {
	m, r, _ := newMgr(t, ModeSystem, "linux")
	r.fail = map[string]error{"systemctl daemon-reload": errors.New("boom")}
	if err := m.Install(spec(ModeSystem)); err == nil {
		t.Fatal("want error")
	}
	if len(r.cmds) != 1 {
		t.Error(r.cmds)
	}
}

func TestWindowsSystemUnsupported(t *testing.T) {
	_, err := NewManager(ModeSystem, "windows", `C:\Users\u`, &fakeRunner{})
	if err == nil || !strings.Contains(err.Error(), "Windows 暂不支持开机即运行(--mode system)") {
		t.Fatal(err)
	}
}
