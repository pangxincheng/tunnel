package svc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	cmds []string
	fail map[string]error
	seq  map[string][]error // per-command results, popped in order (then fail[c])
}

func (f *fakeRunner) Run(name string, args ...string) error {
	c := strings.Join(append([]string{name}, args...), " ")
	f.cmds = append(f.cmds, c)
	if q := f.seq[c]; len(q) > 0 {
		f.seq[c] = q[1:]
		return q[0]
	}
	if err, ok := f.fail[c]; ok {
		return err
	}
	if strings.HasPrefix(c, "launchctl print ") { // by default a booted-out service is gone at once
		return errors.New("Could not find service")
	}
	return nil
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
	if got := ServiceName("default"); got != "tunnel-default" {
		t.Fatal(got)
	}
}

func TestPathsForTable(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	old := getuid
	getuid = func() int { return 501 }
	t.Cleanup(func() { getuid = old })
	const s = "tunnel-default"
	type c struct {
		mode       Mode
		goos, home string
		want       Paths
	}
	cases := []c{
		{ModeSystem, "linux", "/home/u", Paths{"/etc/tunnel/" + s, "/var/lib/tunnel/" + s + "/state.json", "/run/tunnel/" + s + ".sock", "/usr/local/bin/tunnel", ""}},
		{ModeSystem, "darwin", "/Users/u", Paths{"/etc/tunnel/" + s, "/var/lib/tunnel/" + s + "/state.json", "/var/run/tunnel/" + s + ".sock", "/usr/local/bin/tunnel", "/var/log/tunnel/" + s + ".log"}},
		{ModeUser, "linux", "/home/u", Paths{"/home/u/.config/tunnel/" + s, "/home/u/.config/tunnel/" + s + "/state.json", "/run/user/1000/tunnel/" + s + ".sock", "/home/u/.local/bin/tunnel", ""}},
		{ModeUser, "darwin", "/Users/u", Paths{"/Users/u/Library/Application Support/tunnel/" + s, "/Users/u/Library/Application Support/tunnel/" + s + "/state.json", "/tmp/tunnel-501/" + s + ".sock", "/Users/u/Library/Application Support/tunnel/bin/tunnel", "/Users/u/Library/Application Support/tunnel/" + s + "/tunnel.log"}},
		{ModeUser, "windows", `C:\Users\u`, Paths{`C:\Users\u\AppData\Roaming\tunnel\` + s, `C:\Users\u\AppData\Roaming\tunnel\` + s + `\state.json`, `\\.\pipe\tunnel-` + s, `C:\Users\u\AppData\Local\tunnel\` + s + `\tunnel.exe`, `C:\Users\u\AppData\Roaming\tunnel\` + s + `\tunnel.log`}},
	}
	for _, tc := range cases {
		if got := PathsFor(tc.mode, s, tc.goos, tc.home); got != tc.want {
			t.Errorf("%s/%s:\n got %+v\nwant %+v", tc.mode, tc.goos, got, tc.want)
		}
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	got := PathsFor(ModeUser, s, "linux", "/home/u")
	if got.ControlAddr != "/tmp/tunnel-501/"+s+".sock" {
		t.Error(got.ControlAddr)
	}
}

// 控制 socket 完整路径超过 100 字节时,文件名改用 hex(sha256(svc))[:12].sock。
func TestControlSockLengthFallback(t *testing.T) {
	long := "tunnel-" + strings.Repeat("p", 64) // 最长的服务名
	sum := sha256.Sum256([]byte(long))
	short := hex.EncodeToString(sum[:])[:12] + ".sock"

	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000/"+strings.Repeat("x", 20))
	got := PathsFor(ModeUser, long, "linux", "/home/u").ControlAddr
	if got != "/run/user/1000/"+strings.Repeat("x", 20)+"/tunnel/"+short || len(got) > 100 {
		t.Errorf("超长路径应回退为短文件名: %q", got)
	}
	// 未超长时保持可读的服务名
	if got := controlSock("/tmp/tunnel-501", long); got != "/tmp/tunnel-501/"+long+".sock" {
		t.Errorf("got %q", got)
	}
	if got := controlSock("/tmp/"+strings.Repeat("d", 90), "tunnel-x"); got != "/tmp/"+strings.Repeat("d", 90)+"/"+func() string {
		s := sha256.Sum256([]byte("tunnel-x"))
		return hex.EncodeToString(s[:])[:12]
	}()+".sock" {
		t.Errorf("got %q", got)
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
	sp := Spec{Name: "tunnel-default", User: `PC\alice&co`, Exec: `C:\Users\u\AppData\Local\tunnel\tunnel-default\tunnel.exe`, Args: []string{"run-service", "--profile", `my "nas" & co`}}
	x := renderTaskXML(sp)
	for _, w := range []string{"<LogonTrigger>", "<RestartOnFailure>", "<Interval>PT1M</Interval>", "<Count>999</Count>",
		"<Hidden>true</Hidden>", "<ExecutionTimeLimit>PT0S</ExecutionTimeLimit>",
		`<Command>C:\Users\u\AppData\Local\tunnel\tunnel-default\tunnel.exe</Command>`,
		`<Arguments>run-service --profile &#34;my \&#34;nas\&#34; &amp; co&#34;</Arguments>`} {
		if !strings.Contains(x, w) {
			t.Errorf("missing %q in\n%s", w, x)
		}
	}
	// 结构检查:登录触发器与 Principal 都限定为当前用户,以交互令牌、最低权限运行
	var task struct {
		Trigger struct {
			UserID string `xml:"UserId"`
		} `xml:"Triggers>LogonTrigger"`
		Principal struct {
			UserID    string `xml:"UserId"`
			LogonType string `xml:"LogonType"`
			RunLevel  string `xml:"RunLevel"`
		} `xml:"Principals>Principal"`
	}
	dec := xml.NewDecoder(strings.NewReader(strings.Replace(x, `encoding="UTF-16"`, `encoding="UTF-8"`, 1)))
	if err := dec.Decode(&task); err != nil {
		t.Fatalf("任务 XML 不合法: %v\n%s", err, x)
	}
	if task.Trigger.UserID != `PC\alice&co` || task.Principal.UserID != `PC\alice&co` ||
		task.Principal.LogonType != "InteractiveToken" || task.Principal.RunLevel != "LeastPrivilege" {
		t.Fatalf("got %+v", task)
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
		"launchctl print gui/501/com.pangxincheng.tunnel-default-nas",
		"launchctl bootstrap gui/501 /home/u/Library/LaunchAgents/com.pangxincheng.tunnel-default-nas.plist"})
	r.cmds = nil
	m.Uninstall("tunnel-default-nas")
	eq(t, r.cmds, []string{
		"launchctl bootout gui/501/com.pangxincheng.tunnel-default-nas",
		"launchctl print gui/501/com.pangxincheng.tunnel-default-nas"})
	if len(fs.files) != 0 {
		t.Error(fs.files)
	}

	m, r, _ = newMgr(t, ModeSystem, "darwin")
	m.Install(spec(ModeSystem))
	eq(t, r.cmds, []string{
		"launchctl bootout system/com.pangxincheng.tunnel-default-nas",
		"launchctl print system/com.pangxincheng.tunnel-default-nas",
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

// Stop 只在 Windows 上结束正在运行的任务(复制新的 tunnel.exe 前调用);其他平台无操作。
func TestStop(t *testing.T) {
	m, r, _ := newMgr(t, ModeUser, "windows")
	if err := m.Stop("tunnel-default"); err != nil {
		t.Fatal(err)
	}
	eq(t, r.cmds, []string{"schtasks /End /TN tunnel-default"})
	for _, goos := range []string{"linux", "darwin"} {
		m, r, _ := newMgr(t, ModeUser, goos)
		if err := m.Stop("tunnel-default"); err != nil {
			t.Fatal(err)
		}
		eq(t, r.cmds, nil)
	}
}

// bootout is asynchronous: bootstrapping the same label while the old
// process is still exiting fails with "Bootstrap failed: 5: Input/output
// error". Install waits until the old service is gone, and retries bootstrap.
func TestLaunchdUpdateWaitsForBootout(t *testing.T) {
	r, fs := &fakeRunner{seq: map[string][]error{}}, newFS()
	var slept []time.Duration
	m, err := NewManager(ModeSystem, "darwin", "/home/u", r, WithFS(fs),
		WithSleep(func(d time.Duration) { slept = append(slept, d) }))
	if err != nil {
		t.Fatal(err)
	}
	target := "system/com.pangxincheng.tunnel-default-nas"
	plist := "/Library/LaunchDaemons/com.pangxincheng.tunnel-default-nas.plist"
	gone := errors.New("Could not find service")
	io := errors.New("Bootstrap failed: 5: Input/output error")
	r.seq["launchctl print "+target] = []error{nil, nil, gone}    // still loaded twice, then gone
	r.seq["launchctl bootstrap system "+plist] = []error{io, nil} // first attempt still races
	if err := m.Install(spec(ModeSystem)); err != nil {
		t.Fatal(err)
	}
	eq(t, r.cmds, []string{
		"launchctl bootout " + target,
		"launchctl print " + target,
		"launchctl print " + target,
		"launchctl print " + target,
		"launchctl bootstrap system " + plist,
		"launchctl bootstrap system " + plist,
	})
	if len(slept) != 3 {
		t.Fatalf("slept %v", slept)
	}
}

func TestLaunchdInstallGivesUp(t *testing.T) {
	r, fs := &fakeRunner{seq: map[string][]error{}, fail: map[string]error{}}, newFS()
	m, _ := NewManager(ModeSystem, "darwin", "/home/u", r, WithFS(fs), WithSleep(func(time.Duration) {}))
	target := "system/com.pangxincheng.tunnel-default-nas"
	io := errors.New("Bootstrap failed: 5: Input/output error")
	r.fail["launchctl print "+target] = nil // never goes away
	r.fail["launchctl bootstrap system /Library/LaunchDaemons/com.pangxincheng.tunnel-default-nas.plist"] = io
	if err := m.Install(spec(ModeSystem)); !errors.Is(err, io) {
		t.Fatalf("err %v", err)
	}
	prints, boots := 0, 0
	for _, c := range r.cmds {
		if strings.HasPrefix(c, "launchctl print") {
			prints++
		}
		if strings.HasPrefix(c, "launchctl bootstrap") {
			boots++
		}
	}
	if prints != 50 || boots != 3 {
		t.Fatalf("prints %d boots %d", prints, boots)
	}
}

// Not loaded: bootout fails and there is nothing to wait for.
func TestLaunchdFreshInstallSkipsWait(t *testing.T) {
	r := &fakeRunner{fail: map[string]error{"launchctl bootout gui/501/com.pangxincheng.tunnel-default-nas": errors.New("No such process")}}
	m, _ := NewManager(ModeUser, "darwin", "/home/u", r, WithFS(newFS()), WithUID(func() int { return 501 }), WithSleep(func(time.Duration) {}))
	if err := m.Install(spec(ModeUser)); err != nil {
		t.Fatal(err)
	}
	eq(t, r.cmds, []string{
		"launchctl bootout gui/501/com.pangxincheng.tunnel-default-nas",
		"launchctl bootstrap gui/501 /home/u/Library/LaunchAgents/com.pangxincheng.tunnel-default-nas.plist"})
}
