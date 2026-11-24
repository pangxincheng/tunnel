package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	osuser "os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docopt/docopt-go"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/pangxincheng/tunnel/cli/internal/svc"
	"github.com/pangxincheng/tunnel/cli/tunnel"
)

// ---------- 进程内测试 SSH 服务器(公钥认证、keepalive、sftp、streamlocal 转发) ----------

type miniSSHServer struct {
	l       net.Listener
	cfg     *ssh.ServerConfig
	hostPub string // authorized_keys 格式的主机公钥
	mu      sync.Mutex
	unix    map[string]net.Listener
}

func startMiniSSH(t *testing.T, authorized ssh.PublicKey) *miniSSHServer {
	t.Helper()
	_, hk, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ssh.NewSignerFromKey(hk)
	s := &miniSSHServer{unix: map[string]net.Listener{}}
	s.hostPub = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	s.cfg = &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if bytes.Equal(k.Marshal(), authorized.Marshal()) {
			return nil, nil
		}
		return nil, errors.New("denied")
	}}
	s.cfg.AddHostKey(signer)
	var err error
	s.l, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		s.l.Close()
		s.mu.Lock()
		for _, l := range s.unix {
			l.Close()
		}
		s.mu.Unlock()
	})
	go func() {
		for {
			c, err := s.l.Accept()
			if err != nil {
				return
			}
			go s.handle(c)
		}
	}()
	return s
}

func (s *miniSSHServer) addr() (string, int) {
	a := s.l.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

func (s *miniSSHServer) handle(c net.Conn) {
	_, chans, reqs, err := ssh.NewServerConn(c, s.cfg)
	if err != nil {
		return
	}
	go func() {
		for req := range reqs {
			var p struct{ SocketPath string }
			switch req.Type {
			case "keepalive@openssh.com":
				req.Reply(true, nil)
			case "streamlocal-forward@openssh.com":
				if ssh.Unmarshal(req.Payload, &p) != nil {
					req.Reply(false, nil)
					continue
				}
				os.Remove(p.SocketPath)
				l, err := net.Listen("unix", p.SocketPath)
				if err != nil {
					req.Reply(false, nil)
					continue
				}
				s.mu.Lock()
				s.unix[p.SocketPath] = l
				s.mu.Unlock()
				req.Reply(true, nil)
				go func() {
					for {
						uc, err := l.Accept()
						if err != nil {
							return
						}
						uc.Close()
					}
				}()
			case "cancel-streamlocal-forward@openssh.com":
				if ssh.Unmarshal(req.Payload, &p) == nil {
					s.mu.Lock()
					if l, ok := s.unix[p.SocketPath]; ok {
						l.Close()
						delete(s.unix, p.SocketPath)
					}
					s.mu.Unlock()
				}
				req.Reply(true, nil)
			default:
				req.Reply(false, nil)
			}
		}
	}()
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "unsupported")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for req := range creqs {
				var p struct{ Name string }
				if req.Type == "subsystem" && ssh.Unmarshal(req.Payload, &p) == nil && p.Name == "sftp" {
					req.Reply(true, nil)
					srv, err := sftp.NewServer(ch)
					if err != nil {
						return
					}
					srv.Serve()
					srv.Close()
					return
				}
				req.Reply(false, nil)
			}
		}()
	}
}

// ---------- 测试夹具 ----------

type fakeManager struct {
	mu          sync.Mutex
	installed   []svc.Spec
	uninstalled []string
	stopped     []string
	installErr  error
	onStop      func(name string)
}

func (m *fakeManager) Stop(name string) error {
	if m.onStop != nil {
		m.onStop(name)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopped = append(m.stopped, name)
	return nil
}

func (m *fakeManager) Install(s svc.Spec) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.installErr != nil {
		return m.installErr
	}
	m.installed = append(m.installed, s)
	return nil
}

// Uninstall 像 schtasks /Delete 一样:服务登记不存在时报错。
func (m *fakeManager) Uninstall(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.has(name) {
		return errors.New("找不到任务 " + name)
	}
	m.uninstalled = append(m.uninstalled, name)
	return nil
}
func (m *fakeManager) Installed(name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.has(name), nil
}

// has 报告 name 是否已安装且未卸载;调用方持有 mu。
func (m *fakeManager) has(name string) bool {
	n := 0
	for _, s := range m.installed {
		if s.Name == name {
			n = 1
		}
	}
	for _, u := range m.uninstalled {
		if u == name {
			n = 0
		}
	}
	return n == 1
}

type fixture struct {
	root    string // 一切服务路径的根(临时目录)
	keyPath string // 客户端私钥
	server  *miniSSHServer
	sockDir string // "VPS" 上的 socket 目录
	env     *svcEnv
	mgr     *fakeManager
	out     *bytes.Buffer
}

const testDeviceID = "0123456789abcdef0123456789abcdef"

func newFixture(t *testing.T) *fixture {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "tsvc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	block, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(root, "id_test")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	sshPub, _ := ssh.NewPublicKey(pub)
	server := startMiniSSH(t, sshPub)
	sockDir := filepath.Join(root, "vps")
	if err := os.MkdirAll(sockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "self-binary")
	if err := os.WriteFile(exe, []byte("#!fake tunnel binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	mgr := &fakeManager{}
	out := &bytes.Buffer{}
	f := &fixture{root: root, keyPath: keyPath, server: server, sockDir: sockDir, mgr: mgr, out: out}
	f.env = &svcEnv{
		goos:       "linux",
		home:       filepath.Join(root, "home"),
		user:       "tester",
		ownerUID:   -1,
		account:    `PC\tester`,
		euid:       501,
		isTerminal: true,
		exe:        exe,
		stdout:     out,
		logger:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		paths: func(mode svc.Mode, name string) svc.Paths {
			base := filepath.Join(root, string(mode))
			return svc.Paths{
				ConfigDir:   filepath.Join(base, "etc", name),
				StatePath:   filepath.Join(base, "lib", name, "state.json"),
				ControlAddr: filepath.Join(base, "run", name+".sock"),
				BinPath:     filepath.Join(base, "bin", "tunnel"),
				LogPath:     filepath.Join(base, "log", name+".log"),
			}
		},
		manager:  func(svc.Mode) (svc.Manager, error) { return mgr, nil },
		dial:     tunnel.Dial,
		deviceID: func() (string, error) { return testDeviceID, nil },
		getenv:   func(string) string { return "" },
		lookupUser: func(n string) (*osuser.User, error) {
			return nil, errors.New("unknown user " + n)
		},
		lookupUserID: func(id string) (*osuser.User, error) {
			return nil, errors.New("unknown uid " + id)
		},
	}
	return f
}

// testParser 遇到用法错误时返回 error 而不是退出进程。
var testParser = &docopt.Parser{HelpHandler: docopt.NoHelpHandler, OptionsFirst: false}

// parse 按 docopt 解析一条命令行。
func parse(t *testing.T, argv ...string) map[string]interface{} {
	t.Helper()
	args, err := testParser.ParseArgs(usageText, argv, "test")
	if err != nil {
		t.Fatalf("docopt 解析 %v 失败: %v", argv, err)
	}
	return args
}

func (f *fixture) installArgv(mode string, extra ...string) []string {
	host, port := f.server.addr()
	argv := []string{"install", "--mode", mode,
		"--remote-ip", host, "--remote-ssh-port", fmt.Sprint(port),
		"--remote-ssh-pub", f.server.hostPub, "--local-ssh-key", f.keyPath,
		"--user", "vpsuser", "--remote-socket-dir", f.sockDir, "--local-ssh-port", "2222"}
	return append(argv, extra...)
}

// ---------- checkPrivilege ----------

func TestCheckPrivilegeSystemReexec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不做 sudo 重新执行")
	}
	args := []string{"install", "--mode", "system", "--name", "nas", "--for-user", "alice"}
	re, err := checkPrivilege(svc.ModeSystem, 501, true, args)
	if err != nil {
		t.Fatal(err)
	}
	if len(re) < 2 || re[0] != "sudo" {
		t.Fatalf("应以 sudo 重新执行,got %q", re)
	}
	if got := re[len(re)-len(args):]; strings.Join(got, "\x00") != strings.Join(args, "\x00") {
		t.Fatalf("参数应原样保留,got %q", re)
	}
}

func TestCheckPrivilegeSystemNoTerminal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不检查")
	}
	re, err := checkPrivilege(svc.ModeSystem, 501, false, nil)
	if err == nil || !strings.Contains(err.Error(), "sudo") || re != nil {
		t.Fatalf("无终端时应报错提示 sudo,got %q, %v", re, err)
	}
}

func TestCheckPrivilegeUserModeAsRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 不检查")
	}
	_, err := checkPrivilege(svc.ModeUser, 0, true, nil)
	if err == nil || !strings.Contains(err.Error(), "不能以 root 运行") {
		t.Fatalf("got %v", err)
	}
}

func TestCheckPrivilegeOK(t *testing.T) {
	if re, err := checkPrivilege(svc.ModeSystem, 0, false, nil); err != nil || re != nil {
		t.Fatalf("root + system 应通过,got %q, %v", re, err)
	}
	if re, err := checkPrivilege(svc.ModeUser, 501, false, nil); err != nil || re != nil {
		t.Fatalf("普通用户 + user 应通过,got %q, %v", re, err)
	}
}

// ---------- install ----------

func TestUsageParsesServiceCommands(t *testing.T) {
	for _, argv := range [][]string{
		{"install", "--mode", "user", "--name", "nas"},
		{"install", "--mode=system", "--profile", "home", "-c", "x.toml"},
		{"install", "--mode=system", "--for-user=alice", "--name", "nas"},
		{"uninstall"},
		{"uninstall", "--mode", "user", "--profile", "p"},
		{"status", "--json"},
		{"pause", "--profile", "home"},
		{"resume"},
		{"set-name", "nas2", "--mode", "user", "--profile", "home"},
		{"retry", "--mode", "system"},
		{"run-service", "--mode", "user", "--profile", "default"},
		{"server", "--user", "alice", "--name", "nas"},
		{"server", "-c", "x.toml", "--log-level", "debug", "--remote-listen-port", "0"},
		{"client", "--remote-ip", "h", "--remote-ssh-port", "22", "--remote-ssh-pub", "k", "--local-ssh-key", "p",
			"--user", "u", "--local-ssh-port", "5", "--name", "n", "--remote-socket-dir", "/d"},
		{"version"},
	} {
		parse(t, argv...)
	}
	if _, err := testParser.ParseArgs(usageText, []string{"install", "--name", "nas"}, "test"); err == nil {
		t.Fatal("install 缺少 --mode 应为用法错误")
	}
	// 服务按 profile 定位,--name 不再用于选择服务;--for-user 只属于 install
	for _, argv := range [][]string{{"status", "--name", "nas"}, {"uninstall", "--name", "nas"},
		{"pause", "--name", "nas"}, {"server", "--for-user", "alice"}} {
		if _, err := testParser.ParseArgs(usageText, argv, "test"); err == nil {
			t.Fatalf("%v 应为用法错误", argv)
		}
	}
}

func TestInstallRejectsPortMode(t *testing.T) {
	f := newFixture(t)
	argv := []string{"install", "--mode", "user", "--remote-listen-port", "5102"}
	_, err := installFlow(f.env, parse(t, argv...), argv)
	if err == nil || !strings.Contains(err.Error(), "只支持 --name") {
		t.Fatalf("got %v", err)
	}
	if len(f.mgr.installed) != 0 {
		t.Fatal("不应安装")
	}
}

func TestInstallPrivilegeCheckedBeforeSideEffects(t *testing.T) {
	f := newFixture(t)
	f.env.isTerminal = false
	argv := f.installArgv("system", "--name", "nas")
	_, err := installFlow(f.env, parse(t, argv...), argv)
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	if err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "system")); !os.IsNotExist(err) {
		t.Fatal("权限检查失败前不应写任何文件")
	}
	// 有终端:返回 sudo 重新执行命令,同样不写文件
	f.env.isTerminal = true
	re, err := installFlow(f.env, parse(t, argv...), argv)
	if err != nil || len(re) == 0 || re[0] != "sudo" {
		t.Fatalf("got %q, %v", re, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "system")); !os.IsNotExist(err) {
		t.Fatal("重新执行前不应写任何文件")
	}
}

func TestInstallSystemWritesFilesAndSpec(t *testing.T) {
	f := newFixture(t)
	f.env.euid = 0
	f.env.ownerUID = 501
	argv := f.installArgv("system", "--name", "nas", "--profile", "home")
	re, err := installFlow(f.env, parse(t, argv...), argv)
	if err != nil || re != nil {
		t.Fatalf("got %q, %v", re, err)
	}
	p := f.env.paths(svc.ModeSystem, "tunnel-home")

	key, err := os.Stat(filepath.Join(p.ConfigDir, "id_key"))
	if err != nil || key.Mode().Perm() != 0o600 {
		t.Fatalf("私钥应复制为 0600: %v %v", key, err)
	}
	cfgPath := filepath.Join(p.ConfigDir, "tunnel.toml")
	st, err := os.Stat(cfgPath)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("配置应为 0600: %v %v", st, err)
	}
	fc, err := tunnel.LoadTOML(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if fc.Name != "nas" || fc.Profile != "home" || fc.User != "vpsuser" || fc.LocalSSHPort != 2222 ||
		fc.LocalSSHKey != filepath.Join(p.ConfigDir, "id_key") || fc.RemoteSSHPub != f.server.hostPub ||
		fc.RemoteSocketDir != f.sockDir || fc.RemoteListenPort != nil {
		t.Fatalf("配置内容不对: %+v", fc)
	}
	raw, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(raw), "PRIVATE KEY") {
		t.Fatal("配置中不应出现私钥内容")
	}
	owner, err := os.ReadFile(filepath.Join(p.ConfigDir, "owner"))
	if err != nil || strings.TrimSpace(string(owner)) != "501" {
		t.Fatalf("owner = %q, %v", owner, err)
	}
	if b, err := os.ReadFile(p.BinPath); err != nil || string(b) != "#!fake tunnel binary" {
		t.Fatalf("应复制自身到 BinPath: %q %v", b, err)
	}
	if fi, err := os.Stat(filepath.Dir(p.LogPath)); err != nil || !fi.IsDir() {
		t.Fatalf("应创建日志目录: %v", err)
	}
	if len(f.mgr.installed) != 1 {
		t.Fatalf("Install 调用次数 = %d", len(f.mgr.installed))
	}
	s := f.mgr.installed[0]
	want := []string{"run-service", "--mode", "system", "--profile", "home"}
	if s.Name != "tunnel-home" || s.Mode != svc.ModeSystem || s.Exec != p.BinPath || s.LogPath != p.LogPath ||
		strings.Join(s.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("Spec 不对: %+v", s)
	}
	if !strings.Contains(f.out.String(), "tunnel-home") {
		t.Fatalf("输出应包含服务名: %s", f.out.String())
	}

	// 重复执行 = 覆盖并再次 Install
	if _, err := installFlow(f.env, parse(t, argv...), argv); err != nil {
		t.Fatal(err)
	}
	if len(f.mgr.installed) != 2 {
		t.Fatal("重复安装应再次调用 Install")
	}
}

func TestInstallUserKeepsKeyPath(t *testing.T) {
	f := newFixture(t)
	argv := f.installArgv("user", "--name", "nas")
	if _, err := installFlow(f.env, parse(t, argv...), argv); err != nil {
		t.Fatal(err)
	}
	p := f.env.paths(svc.ModeUser, "tunnel-default")
	fc, err := tunnel.LoadTOML(filepath.Join(p.ConfigDir, "tunnel.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if fc.LocalSSHKey != f.keyPath || fc.Profile != "default" {
		t.Fatalf("user 模式应引用原私钥: %+v", fc)
	}
	for _, n := range []string{"id_key", "owner"} {
		if _, err := os.Stat(filepath.Join(p.ConfigDir, n)); !os.IsNotExist(err) {
			t.Fatalf("user 模式不应写 %s", n)
		}
	}
}

func TestInstallFailsWhenVPSUnreachable(t *testing.T) {
	f := newFixture(t)
	f.env.dial = func(*tunnel.Config) (*ssh.Client, error) { return nil, errors.New("连接被拒绝") }
	argv := f.installArgv("user", "--name", "nas")
	if _, err := installFlow(f.env, parse(t, argv...), argv); err == nil {
		t.Fatal("VPS 连不上应报错")
	}
	if _, err := os.Stat(filepath.Join(f.root, "user")); !os.IsNotExist(err) {
		t.Fatal("连接验证失败前不应写任何文件")
	}
}

// ---------- run-service / status / control ----------

func waitPhase(t *testing.T, addr string, want tunnel.Phase) tunnel.ShareStatus {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var last tunnel.ControlResponse
	var lastErr error
	for time.Now().Before(deadline) {
		last, lastErr = tunnel.ControlCall(addr, tunnel.ControlRequest{Op: "status"})
		if lastErr == nil && last.Status != nil && last.Status.Phase == want {
			return *last.Status
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("等待 %s 超时: %+v %v", want, last.Status, lastErr)
	return tunnel.ShareStatus{}
}

func TestRunServiceEndToEnd(t *testing.T) {
	f := newFixture(t)
	argv := f.installArgv("user", "--name", "nas")
	if _, err := installFlow(f.env, parse(t, argv...), argv); err != nil {
		t.Fatal(err)
	}
	p := f.env.paths(svc.ModeUser, "tunnel-default")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runService(ctx, f.env, svc.ModeUser, "default") }()

	st := waitPhase(t, p.ControlAddr, tunnel.PhaseReady)
	if st.Name != "nas" {
		t.Fatalf("name = %q", st.Name)
	}
	if fi, err := os.Stat(p.ControlAddr); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("控制 socket 应为 0600: %v", err)
	}

	// status 命令(经控制通道)
	f.out.Reset()
	if code := statusCmd(f.env, parse(t, "status")); code != 0 {
		t.Fatalf("status 退出码 = %d, 输出 %s", code, f.out.String())
	}
	if !strings.Contains(f.out.String(), "nas") {
		t.Fatalf("status 输出: %s", f.out.String())
	}
	f.out.Reset()
	if code := statusCmd(f.env, parse(t, "status", "--json")); code != 0 {
		t.Fatal(code)
	}
	var js tunnel.ShareStatus
	if err := json.Unmarshal(f.out.Bytes(), &js); err != nil || js.Phase != tunnel.PhaseReady {
		t.Fatalf("--json 输出 %q: %v", f.out.String(), err)
	}

	// pause 命令
	if code := controlCmd(f.env, parse(t, "pause"), "pause"); code != 0 {
		t.Fatalf("pause 退出码 = %d: %s", code, f.out.String())
	}
	waitPhase(t, p.ControlAddr, tunnel.PhasePaused)
	if _, err := os.Stat(filepath.Join(f.sockDir, "nas.json")); err != nil {
		t.Fatal("暂停不应删除设备信息文件")
	}
	if code := controlCmd(f.env, parse(t, "resume"), "resume"); code != 0 {
		t.Fatal(code)
	}
	waitPhase(t, p.ControlAddr, tunnel.PhaseReady)

	// set-name 改写 tunnel.toml,服务名不变
	resp, err := tunnel.ControlCall(p.ControlAddr, tunnel.ControlRequest{Op: "set-name", Name: "nas2"})
	if err != nil || !resp.OK {
		t.Fatalf("set-name: %+v %v", resp, err)
	}
	fc, err := tunnel.LoadTOML(filepath.Join(p.ConfigDir, "tunnel.toml"))
	if err != nil || fc.Name != "nas2" {
		t.Fatalf("tunnel.toml name 应改写为 nas2: %+v %v", fc, err)
	}
	if fi, _ := os.Stat(filepath.Join(p.ConfigDir, "tunnel.toml")); fi.Mode().Perm() != 0o600 {
		t.Fatal("改写后的配置应保持 0600")
	}
	// 改名后仍按 profile 找到同一个服务
	waitPhase(t, p.ControlAddr, tunnel.PhaseReady)
	f.out.Reset()
	if code := statusCmd(f.env, parse(t, "status")); code != 0 || !strings.Contains(f.out.String(), "nas2") ||
		!strings.Contains(f.out.String(), "tunnel-default") {
		t.Fatalf("改名后 status: code=%d %s", code, f.out.String())
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runService 未在取消后返回")
	}
}

func TestStatusNotRunning(t *testing.T) {
	f := newFixture(t)
	p := f.env.paths(svc.ModeUser, "tunnel-default")
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.ConfigDir, "tunnel.toml"), []byte("name = \"nas\"\nprofile = \"default\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code := statusCmd(f.env, parse(t, "status"))
	if code != 3 || !strings.Contains(f.out.String(), "已安装,未运行") {
		t.Fatalf("code=%d out=%q", code, f.out.String())
	}
}

func TestStatusNotInstalled(t *testing.T) {
	f := newFixture(t)
	if code := statusCmd(f.env, parse(t, "status")); code != 4 {
		t.Fatalf("未安装时退出码应为 4,got %d", code)
	}
}

func TestResolveTargetAmbiguous(t *testing.T) {
	f := newFixture(t)
	for _, mode := range []svc.Mode{svc.ModeUser, svc.ModeSystem} {
		p := f.env.paths(mode, svc.ServiceName("default"))
		os.MkdirAll(p.ConfigDir, 0o700)
		os.WriteFile(filepath.Join(p.ConfigDir, "tunnel.toml"), []byte("profile = \"default\"\nname = \"nas\"\n"), 0o600)
	}
	if _, err := resolveTarget(f.env, parse(t, "status")); err == nil || !strings.Contains(err.Error(), "--mode") {
		t.Fatalf("两种模式都有时应要求 --mode: %v", err)
	}
	tg, err := resolveTarget(f.env, parse(t, "status", "--mode", "user"))
	if err != nil || tg.mode != svc.ModeUser || tg.profile != "default" || tg.service() != "tunnel-default" {
		t.Fatalf("got %+v %v", tg, err)
	}
	if _, err := resolveTarget(f.env, parse(t, "status", "--profile", "other")); err == nil || !strings.Contains(err.Error(), "other") {
		t.Fatalf("未安装的 profile 应报错: %v", err)
	}
}

// ---------- uninstall ----------

func TestUninstallRemovesEverything(t *testing.T) {
	f := newFixture(t)
	argv := f.installArgv("user", "--name", "nas")
	if _, err := installFlow(f.env, parse(t, argv...), argv); err != nil {
		t.Fatal(err)
	}
	p := f.env.paths(svc.ModeUser, "tunnel-default")
	os.MkdirAll(filepath.Dir(p.StatePath), 0o700)
	os.WriteFile(p.StatePath, []byte(`{"paused":true}`), 0o600)
	// VPS 上本机留下的设备信息与 socket(sshd 不会删除 socket 文件)
	os.WriteFile(filepath.Join(f.sockDir, "nas.json"), []byte(`{"device_id":"`+testDeviceID+`","name":"nas"}`), 0o600)
	os.WriteFile(filepath.Join(f.sockDir, "nas.sock"), nil, 0o600)
	// 其他设备的文件不能动
	os.WriteFile(filepath.Join(f.sockDir, "other.json"), []byte(`{"device_id":"ffff","name":"other"}`), 0o600)

	uargv := []string{"uninstall"}
	if _, err := uninstallFlow(f.env, parse(t, uargv...), uargv); err != nil {
		t.Fatal(err)
	}
	if len(f.mgr.uninstalled) != 1 || f.mgr.uninstalled[0] != "tunnel-default" {
		t.Fatalf("Uninstall 调用: %v", f.mgr.uninstalled)
	}
	for _, gone := range []string{p.ConfigDir, p.StatePath, filepath.Join(f.sockDir, "nas.json"), filepath.Join(f.sockDir, "nas.sock")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s 应被删除", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(f.sockDir, "other.json")); err != nil {
		t.Fatal("其他设备的信息文件不应被删除")
	}
}

func TestUninstallVPSUnreachableOnlyWarns(t *testing.T) {
	f := newFixture(t)
	argv := f.installArgv("user", "--name", "nas")
	if _, err := installFlow(f.env, parse(t, argv...), argv); err != nil {
		t.Fatal(err)
	}
	f.env.dial = func(*tunnel.Config) (*ssh.Client, error) { return nil, errors.New("连接超时") }
	var logBuf bytes.Buffer
	f.env.logger = slog.New(slog.NewTextHandler(&logBuf, nil))
	uargv := []string{"uninstall", "--mode", "user"}
	if _, err := uninstallFlow(f.env, parse(t, uargv...), uargv); err != nil {
		t.Fatalf("VPS 连不上不应阻止卸载: %v", err)
	}
	if !strings.Contains(logBuf.String(), "WARN") {
		t.Fatalf("应打印警告: %s", logBuf.String())
	}
	p := f.env.paths(svc.ModeUser, "tunnel-default")
	if _, err := os.Stat(p.ConfigDir); !os.IsNotExist(err) {
		t.Fatal("配置目录应被删除")
	}
}

func TestUninstallSystemChecksPrivilege(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	f := newFixture(t)
	p := f.env.paths(svc.ModeSystem, "tunnel-default")
	os.MkdirAll(p.ConfigDir, 0o700)
	os.WriteFile(filepath.Join(p.ConfigDir, "tunnel.toml"), []byte("name = \"nas\"\n"), 0o600)
	f.env.isTerminal = false
	uargv := []string{"uninstall"}
	if _, err := uninstallFlow(f.env, parse(t, uargv...), uargv); err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("got %v", err)
	}
	if len(f.mgr.uninstalled) != 0 {
		t.Fatal("权限不足时不应卸载")
	}
	if _, err := os.Stat(p.ConfigDir); err != nil {
		t.Fatal("权限不足时不应删除配置")
	}
}

// 安装半途失败(配置已写、服务未登记)时,uninstall 仍应成功并清理干净。
func TestUninstallWithoutRegistration(t *testing.T) {
	f := newFixture(t)
	f.mgr.installErr = errors.New("launchctl bootstrap 失败")
	argv := f.installArgv("user", "--name", "nas")
	if _, err := installFlow(f.env, parse(t, argv...), argv); err == nil {
		t.Fatal("Install 失败应报错")
	}
	p := f.env.paths(svc.ModeUser, "tunnel-default")
	if _, err := os.Stat(filepath.Join(p.ConfigDir, "tunnel.toml")); err != nil {
		t.Fatalf("前提:半途失败后配置仍在: %v", err)
	}
	os.MkdirAll(filepath.Dir(p.StatePath), 0o700)
	os.WriteFile(p.StatePath, []byte(`{"paused":false}`), 0o600)
	os.WriteFile(filepath.Join(f.sockDir, "nas.json"), []byte(`{"device_id":"`+testDeviceID+`","name":"nas"}`), 0o600)

	f.out.Reset()
	if code := statusCmd(f.env, parse(t, "status")); code != 3 {
		t.Fatalf("半途失败应显示已安装,未运行,code=%d", code)
	}
	uargv := []string{"uninstall"}
	if _, err := uninstallFlow(f.env, parse(t, uargv...), uargv); err != nil {
		t.Fatalf("服务未登记时 uninstall 仍应成功: %v", err)
	}
	if len(f.mgr.uninstalled) != 0 {
		t.Fatal("服务未登记时不应调用 Manager.Uninstall")
	}
	for _, gone := range []string{p.ConfigDir, p.StatePath, filepath.Join(f.sockDir, "nas.json")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Fatalf("%s 应被删除", gone)
		}
	}
}

// ---------- 每个 profile 一个服务(I3) ----------

func TestInstallOtherModeRefused(t *testing.T) {
	f := newFixture(t)
	argv := f.installArgv("user", "--name", "nas")
	if _, err := installFlow(f.env, parse(t, argv...), argv); err != nil {
		t.Fatal(err)
	}
	f.env.euid = 0
	argv = f.installArgv("system", "--name", "nas")
	_, err := installFlow(f.env, parse(t, argv...), argv)
	if err == nil || !strings.Contains(err.Error(), "已以 user 方式安装,请先 tunnel uninstall --mode user") {
		t.Fatalf("另一种模式已安装时应报错: %v", err)
	}
	if len(f.mgr.installed) != 1 {
		t.Fatal("不应安装第二个服务")
	}
	if _, err := os.Stat(f.env.paths(svc.ModeSystem, "tunnel-default").ConfigDir); !os.IsNotExist(err) {
		t.Fatal("拒绝时不应写 system 配置")
	}

	// 反过来:system 已安装时装 user 同样拒绝
	g := newFixture(t)
	g.env.euid = 0
	argv = g.installArgv("system", "--name", "nas")
	if _, err := installFlow(g.env, parse(t, argv...), argv); err != nil {
		t.Fatal(err)
	}
	g.env.euid = 501
	argv = g.installArgv("user", "--name", "nas")
	if _, err := installFlow(g.env, parse(t, argv...), argv); err == nil || !strings.Contains(err.Error(), "已以 system 方式安装,请先 tunnel uninstall --mode system") {
		t.Fatalf("got %v", err)
	}
}

// 同一模式重复安装 = 更新同一个服务;换名字时删除 VPS 上本机旧名字的设备信息。
func TestInstallSameModeUpdatesAndRenames(t *testing.T) {
	f := newFixture(t)
	argv := f.installArgv("user", "--name", "nas")
	if _, err := installFlow(f.env, parse(t, argv...), argv); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(f.sockDir, "nas.json"), []byte(`{"device_id":"`+testDeviceID+`","name":"nas"}`), 0o600)
	argv = f.installArgv("user", "--name", "nas2")
	if _, err := installFlow(f.env, parse(t, argv...), argv); err != nil {
		t.Fatal(err)
	}
	if len(f.mgr.installed) != 2 || f.mgr.installed[0].Name != "tunnel-default" || f.mgr.installed[1].Name != "tunnel-default" {
		t.Fatalf("应更新同一个服务: %+v", f.mgr.installed)
	}
	fc, err := tunnel.LoadTOML(filepath.Join(f.env.paths(svc.ModeUser, "tunnel-default").ConfigDir, "tunnel.toml"))
	if err != nil || fc.Name != "nas2" {
		t.Fatalf("配置名字应为 nas2: %+v %v", fc, err)
	}
	if _, err := os.Stat(filepath.Join(f.sockDir, "nas.json")); !os.IsNotExist(err) {
		t.Fatal("旧名字的本机设备信息应被删除")
	}
}

// ---------- 安装用户解析(I4) ----------

func TestInstallOwnerResolution(t *testing.T) {
	users := map[string]*osuser.User{
		"alice": {Username: "alice", Uid: "1001", HomeDir: "/home/alice"},
		"bob":   {Username: "bob", Uid: "1002", HomeDir: "/home/bob"},
	}
	cases := []struct {
		name     string
		env      map[string]string
		forUser  string
		wantUser string
		wantUID  string
		wantHome string
		wantErr  string
	}{
		{name: "--for-user 优先于 SUDO_USER", env: map[string]string{"SUDO_USER": "bob", "SUDO_UID": "1002"}, forUser: "alice", wantUser: "alice", wantUID: "1001", wantHome: "/home/alice"},
		{name: "SUDO_USER", env: map[string]string{"SUDO_USER": "bob", "SUDO_UID": "1002"}, wantUser: "bob", wantUID: "1002", wantHome: "/home/bob"},
		{name: "只有 SUDO_UID", env: map[string]string{"SUDO_UID": "1001"}, wantUser: "alice", wantUID: "1001", wantHome: "/home/alice"},
		{name: "无提示时为当前用户(提权方式不设置 SUDO_*)", wantUser: "root", wantUID: "0", wantHome: "ROOTHOME"},
		{name: "--for-user 不存在", forUser: "nobody-here", wantErr: "nobody-here"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.env.euid = 0
			f.env.user, f.env.ownerUID = "root", 0
			rootHome := f.env.home
			f.env.getenv = func(k string) string { return tc.env[k] }
			f.env.lookupUser = func(n string) (*osuser.User, error) {
				if u, ok := users[n]; ok {
					return u, nil
				}
				return nil, errors.New("unknown user " + n)
			}
			f.env.lookupUserID = func(id string) (*osuser.User, error) {
				for _, u := range users {
					if u.Uid == id {
						return u, nil
					}
				}
				return nil, errors.New("unknown uid " + id)
			}
			// 不给 --user / --local-ssh-key:默认值取自解析出的安装用户
			cfg := filepath.Join(f.root, "c.toml")
			os.WriteFile(cfg, []byte("local_ssh_key = \"~/id_test\"\n"), 0o600)
			host, port := f.server.addr()
			argv := []string{"install", "--mode", "system", "-c", cfg, "--name", "nas",
				"--remote-ip", host, "--remote-ssh-port", fmt.Sprint(port), "--remote-ssh-pub", f.server.hostPub,
				"--remote-socket-dir", f.sockDir}
			if tc.forUser != "" {
				argv = append(argv, "--for-user", tc.forUser)
			}
			var gotKey, gotUser string
			f.env.dial = func(c *tunnel.Config) (*ssh.Client, error) {
				gotKey, gotUser = c.KeyPath, c.User
				return nil, errors.New("stop here")
			}
			_, err := installFlow(f.env, parse(t, argv...), argv)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("got %v", err)
				}
				return
			}
			home := tc.wantHome
			if home == "ROOTHOME" {
				home = rootHome
			}
			if gotUser != tc.wantUser || gotKey != filepath.Join(home, "id_test") {
				t.Fatalf("VPS 用户 / 私钥 = %q %q,期望 %q %q", gotUser, gotKey, tc.wantUser, filepath.Join(home, "id_test"))
			}
			if want := tc.wantUID; strconv.Itoa(f.env.ownerUID) != want {
				t.Fatalf("控制通道属主 = %d,期望 %s", f.env.ownerUID, want)
			}
		})
	}
}

func TestInstallForUserOnlySystem(t *testing.T) {
	f := newFixture(t)
	argv := f.installArgv("user", "--name", "nas", "--for-user", "alice")
	if _, err := installFlow(f.env, parse(t, argv...), argv); err == nil || !strings.Contains(err.Error(), "--for-user") {
		t.Fatalf("user 模式给 --for-user 应报错: %v", err)
	}
}

// 安装用户写入 owner 文件(system 模式控制通道属主)。
func TestInstallForUserWritesOwner(t *testing.T) {
	f := newFixture(t)
	f.env.euid = 0
	f.env.lookupUser = func(n string) (*osuser.User, error) {
		return &osuser.User{Username: n, Uid: "1001", HomeDir: filepath.Join(f.root, "alice")}, nil
	}
	argv := f.installArgv("system", "--name", "nas", "--for-user", "alice")
	if _, err := installFlow(f.env, parse(t, argv...), argv); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(f.env.paths(svc.ModeSystem, "tunnel-default").ConfigDir, "owner"))
	if err != nil || strings.TrimSpace(string(b)) != "1001" {
		t.Fatalf("owner = %q %v", b, err)
	}
}

// ---------- Windows:任务账户与按服务的二进制(I5 / I6) ----------

func TestInstallWindowsStopsTaskBeforeCopy(t *testing.T) {
	f := newFixture(t)
	f.env.goos = "windows"
	p := f.env.paths(svc.ModeUser, "tunnel-default")
	f.mgr.onStop = func(name string) {
		if _, err := os.Stat(p.BinPath); !os.IsNotExist(err) {
			t.Errorf("Stop 应在复制二进制之前调用")
		}
	}
	argv := f.installArgv("user", "--name", "nas")
	if _, err := installFlow(f.env, parse(t, argv...), argv); err != nil {
		t.Fatal(err)
	}
	if len(f.mgr.stopped) != 1 || f.mgr.stopped[0] != "tunnel-default" {
		t.Fatalf("应先停止任务: %v", f.mgr.stopped)
	}
	if _, err := os.Stat(p.BinPath); err != nil {
		t.Fatal(err)
	}
	if s := f.mgr.installed[0]; s.User != `PC\tester` {
		t.Fatalf("Spec.User = %q,应为当前账户", s.User)
	}
}

// ---------- app 用:status --json / 退出码 / set-name / 自动 profile ----------

// writeInstalled 在假路径下摆出一个已安装服务的配置。
func (f *fixture) writeInstalled(t *testing.T, mode svc.Mode, profile string) svc.Paths {
	t.Helper()
	p := f.env.paths(mode, svc.ServiceName(profile))
	if err := os.MkdirAll(p.ConfigDir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := "name = \"nas\"\nprofile = \"" + profile + "\"\n"
	if err := os.WriteFile(filepath.Join(p.ConfigDir, "tunnel.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// fakeControl 在 addr 上起一个假控制通道:每个请求发给 reqs,回复 resp。
func fakeControl(t *testing.T, addr string, resp tunnel.ControlResponse) <-chan tunnel.ControlRequest {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(addr), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	reqs := make(chan tunnel.ControlRequest, 8)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			var req tunnel.ControlRequest
			json.NewDecoder(bufio.NewReader(c)).Decode(&req)
			reqs <- req
			json.NewEncoder(c).Encode(resp)
			c.Close()
		}
	}()
	return reqs
}

func TestStatusJSONIncludesMode(t *testing.T) {
	f := newFixture(t)
	p := f.writeInstalled(t, svc.ModeUser, "default")
	fakeControl(t, p.ControlAddr, tunnel.ControlResponse{OK: true,
		Status: &tunnel.ShareStatus{ConnState: tunnel.ConnState{Phase: tunnel.PhaseReady}, Name: "nas", Version: "x"}})
	if code := statusCmd(f.env, parse(t, "status", "--json")); code != 0 {
		t.Fatalf("code=%d out=%s", code, f.out.String())
	}
	var m map[string]interface{}
	if err := json.Unmarshal(f.out.Bytes(), &m); err != nil {
		t.Fatalf("输出应为一行 JSON: %q: %v", f.out.String(), err)
	}
	if m["mode"] != "user" || m["phase"] != "ready" || m["name"] != "nas" {
		t.Fatalf("got %v", m)
	}
	if strings.Count(strings.TrimSpace(f.out.String()), "\n") != 0 {
		t.Fatalf("应只有一行: %q", f.out.String())
	}
}

func TestStatusJSONNotRunning(t *testing.T) {
	f := newFixture(t)
	f.writeInstalled(t, svc.ModeUser, "default")
	code := statusCmd(f.env, parse(t, "status", "--json"))
	if code != 3 {
		t.Fatalf("code=%d", code)
	}
	if got := strings.TrimSpace(f.out.String()); got != `{"installed":true,"running":false,"mode":"user"}` {
		t.Fatalf("out=%q", got)
	}
}

func TestStatusNotInstalledExit4(t *testing.T) {
	f := newFixture(t)
	code := statusCmd(f.env, parse(t, "status", "--json", "--profile", "default"))
	if code != 4 || strings.TrimSpace(f.out.String()) != `{"installed":false}` {
		t.Fatalf("code=%d out=%q", code, f.out.String())
	}
	f.out.Reset()
	if code := statusCmd(f.env, parse(t, "status")); code != 4 || f.out.Len() != 0 {
		t.Fatalf("无 --json 时退出码应为 4 且 stdout 无 JSON: code=%d out=%q", code, f.out.String())
	}
}

func TestSetNameCmd(t *testing.T) {
	f := newFixture(t)
	p := f.writeInstalled(t, svc.ModeUser, "default")
	reqs := fakeControl(t, p.ControlAddr, tunnel.ControlResponse{OK: true})
	if code := setNameCmd(f.env, parse(t, "set-name", "nas2")); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if req := <-reqs; req.Op != "set-name" || req.Name != "nas2" {
		t.Fatalf("req=%+v", req)
	}
}

func TestSetNameCmdErrors(t *testing.T) {
	f := newFixture(t)
	p := f.writeInstalled(t, svc.ModeUser, "default")
	taken := &tunnel.NameTakenError{Name: "nas2", Owner: tunnel.DeviceInfo{Hostname: "other"}}
	fakeControl(t, p.ControlAddr, tunnel.ControlResponse{Error: taken.Error()})
	var errBuf bytes.Buffer
	f.env.logger = slog.New(slog.NewTextHandler(&errBuf, nil))
	if code := setNameCmd(f.env, parse(t, "set-name", "nas2")); code != 1 || !strings.Contains(errBuf.String(), "已被另一台设备") {
		t.Fatalf("code=%d log=%s", code, errBuf.String())
	}
	// 不合法的名字不用发给服务
	if code := setNameCmd(f.env, parse(t, "set-name", "bad name!")); code != 1 {
		t.Fatalf("不合法名字退出码应为 1,got %d", code)
	}
}

func TestResolveTargetAutoProfile(t *testing.T) {
	f := newFixture(t)
	f.writeInstalled(t, svc.ModeUser, "home")
	tg, err := resolveTarget(f.env, parse(t, "status"))
	if err != nil || tg.profile != "home" || tg.mode != svc.ModeUser {
		t.Fatalf("只装了 home 时应自动选中: %+v %v", tg, err)
	}
	f.writeInstalled(t, svc.ModeUser, "work")
	_, err = resolveTarget(f.env, parse(t, "status"))
	if err == nil || !strings.Contains(err.Error(), "home") || !strings.Contains(err.Error(), "work") ||
		!strings.Contains(err.Error(), "--profile") || errors.Is(err, errNotInstalled) {
		t.Fatalf("多个服务应报错并列出: %v", err)
	}
	// 显式 --profile 不受影响
	if tg, err := resolveTarget(f.env, parse(t, "status", "--profile", "work")); err != nil || tg.profile != "work" {
		t.Fatalf("%+v %v", tg, err)
	}
	if _, err := resolveTarget(f.env, parse(t, "status", "--profile", "none")); !errors.Is(err, errNotInstalled) {
		t.Fatalf("应为 errNotInstalled: %v", err)
	}
}
