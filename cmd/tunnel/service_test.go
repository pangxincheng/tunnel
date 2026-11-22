package main

import (
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
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/docopt/docopt-go"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"

	"github.com/pangxincheng/tunnel/internal/svc"
	"github.com/pangxincheng/tunnel/tunnel"
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
	installErr  error
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
func (m *fakeManager) Uninstall(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.uninstalled = append(m.uninstalled, name)
	return nil
}
func (m *fakeManager) Installed(name string) (bool, error) { return false, nil }

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
	args := []string{"install", "--mode", "system", "--name", "nas"}
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
		{"uninstall"},
		{"uninstall", "--mode", "user", "--profile", "p", "--name", "n"},
		{"status", "--json"},
		{"pause", "--name", "nas"},
		{"resume"},
		{"retry", "--mode", "system"},
		{"run-service", "--mode", "user", "--profile", "default", "--name", "nas"},
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
	p := f.env.paths(svc.ModeSystem, "tunnel-home-nas")

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
	want := []string{"run-service", "--mode", "system", "--profile", "home", "--name", "nas"}
	if s.Name != "tunnel-home-nas" || s.Mode != svc.ModeSystem || s.Exec != p.BinPath || s.LogPath != p.LogPath ||
		strings.Join(s.Args, " ") != strings.Join(want, " ") {
		t.Fatalf("Spec 不对: %+v", s)
	}
	if !strings.Contains(f.out.String(), "tunnel-home-nas") {
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
	p := f.env.paths(svc.ModeUser, "tunnel-default-nas")
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
	p := f.env.paths(svc.ModeUser, "tunnel-default-nas")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runService(ctx, f.env, svc.ModeUser, "default", "nas") }()

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
	p := f.env.paths(svc.ModeUser, "tunnel-default-nas")
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
	if code := statusCmd(f.env, parse(t, "status")); code != 1 {
		t.Fatalf("未安装时退出码应为 1,got %d", code)
	}
}

func TestResolveTargetAmbiguous(t *testing.T) {
	f := newFixture(t)
	for _, mode := range []svc.Mode{svc.ModeUser, svc.ModeSystem} {
		for _, n := range []string{"a", "b"} {
			p := f.env.paths(mode, svc.ServiceName("default", n))
			os.MkdirAll(p.ConfigDir, 0o700)
			os.WriteFile(filepath.Join(p.ConfigDir, "tunnel.toml"), []byte("profile = \"default\"\n"), 0o600)
		}
	}
	if _, err := resolveTarget(f.env, parse(t, "status", "--name", "a")); err == nil || !strings.Contains(err.Error(), "--mode") {
		t.Fatalf("两种模式都有时应要求 --mode: %v", err)
	}
	if _, err := resolveTarget(f.env, parse(t, "status", "--mode", "user")); err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("多个服务时应要求 --name: %v", err)
	}
	tg, err := resolveTarget(f.env, parse(t, "status", "--mode", "user", "--name", "b"))
	if err != nil || tg.mode != svc.ModeUser || tg.name != "b" || tg.profile != "default" {
		t.Fatalf("got %+v %v", tg, err)
	}
}

// ---------- uninstall ----------

func TestUninstallRemovesEverything(t *testing.T) {
	f := newFixture(t)
	argv := f.installArgv("user", "--name", "nas")
	if _, err := installFlow(f.env, parse(t, argv...), argv); err != nil {
		t.Fatal(err)
	}
	p := f.env.paths(svc.ModeUser, "tunnel-default-nas")
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
	if len(f.mgr.uninstalled) != 1 || f.mgr.uninstalled[0] != "tunnel-default-nas" {
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
	uargv := []string{"uninstall", "--mode", "user", "--name", "nas"}
	if _, err := uninstallFlow(f.env, parse(t, uargv...), uargv); err != nil {
		t.Fatalf("VPS 连不上不应阻止卸载: %v", err)
	}
	if !strings.Contains(logBuf.String(), "WARN") {
		t.Fatalf("应打印警告: %s", logBuf.String())
	}
	p := f.env.paths(svc.ModeUser, "tunnel-default-nas")
	if _, err := os.Stat(p.ConfigDir); !os.IsNotExist(err) {
		t.Fatal("配置目录应被删除")
	}
}

func TestUninstallSystemChecksPrivilege(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip()
	}
	f := newFixture(t)
	p := f.env.paths(svc.ModeSystem, "tunnel-default-nas")
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
