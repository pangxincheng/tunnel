package tunnel

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// sessionEnv 是 Session 测试的公共环境:测试 SSH 服务器、客户端配置与回显服务。
type sessionEnv struct {
	server *testSSHServer
	cfg    *Config
	echo   string
}

func newSessionEnv(t *testing.T) *sessionEnv {
	t.Helper()
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	return &sessionEnv{
		server: server,
		cfg: &Config{
			SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
			HostPub: writeHostPub(t, server.HostKey),
		},
		echo: startEchoServer(t),
	}
}

// publish 用独立的 Client 把回显服务以名字 name 挂到 VPS 上(断线自动重连)。
func (e *sessionEnv) publish(t *testing.T, name string) {
	t.Helper()
	c := NewClient(e.cfg, ReverseForwardUnix("", name, e.echo))
	c.keepaliveInterval = 50 * time.Millisecond
	c.reconnectMin = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	sock := e.server.Home + "/.tunnel/sockets/" + name + ".sock"
	waitFor(t, 3*time.Second, func() bool {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return false
		}
		conn.Close()
		return true
	})
}

// runSession 在后台运行 s,测试结束时取消并确认 Run 返回 nil。
func runSession(t *testing.T, s *Session) (errCh <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	go func() { ch <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-ch:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("cancel 后 Run 未退出")
		}
	})
	return ch
}

func newTestSession(e *sessionEnv, opts ...SessionOption) *Session {
	s := NewSession(e.cfg, "", opts...)
	s.keepaliveInterval = 50 * time.Millisecond
	s.reconnectMin = 10 * time.Millisecond
	return s
}

func waitPhase(t *testing.T, s *Session, p Phase, timeout time.Duration) {
	t.Helper()
	waitFor(t, timeout, func() bool { return s.State().Phase == p })
}

// freePort 取一个当前空闲的本机端口。
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func localAddr(port int) string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(port)) }

// echoVia 经本机端口发一行并期望收到回显。
func echoVia(port int) bool {
	conn, err := net.DialTimeout("tcp", localAddr(port), time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	fmt.Fprintln(conn, "ping")
	got, _ := bufio.NewReader(conn).ReadString('\n')
	return got == "echo: ping\n"
}

func TestSessionForwardEndToEnd(t *testing.T) {
	e := newSessionEnv(t)
	e.publish(t, "nas")
	s := newTestSession(e)
	runSession(t, s)
	waitPhase(t, s, PhaseReady, 3*time.Second)

	port := freePort(t)
	if err := s.AddForward("nas", port); err != nil {
		t.Fatalf("AddForward: %v", err)
	}
	if got := s.Forwards(); len(got) != 1 || got["nas"] != port {
		t.Fatalf("Forwards = %v", got)
	}
	if !echoVia(port) {
		t.Fatal("经本机端口未收到回显")
	}
}

func TestSessionListenerSurvivesReconnect(t *testing.T) {
	e := newSessionEnv(t)
	e.publish(t, "nas")
	s := newTestSession(e)
	s.reconnectMin = 500 * time.Millisecond // 留出观察"断线期间"的窗口
	runSession(t, s)
	waitPhase(t, s, PhaseReady, 3*time.Second)
	port := freePort(t)
	if err := s.AddForward("nas", port); err != nil {
		t.Fatal(err)
	}

	e.server.CloseAllClients()
	waitPhase(t, s, PhaseWaiting, 3*time.Second)

	// 断线期间:本机端口仍在监听,但接入连接被立刻关闭
	conn, err := net.Dial("tcp", localAddr(port))
	if err != nil {
		t.Fatalf("断线期间本机端口应仍可连接: %v", err)
	}
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("断线期间接入连接应被立刻关闭,Read 得到 %v", err)
	}
	conn.Close()

	waitPhase(t, s, PhaseReady, 3*time.Second)
	waitFor(t, 3*time.Second, func() bool { return echoVia(port) })
}

func TestSessionAddForwardPortBusyIsolated(t *testing.T) {
	e := newSessionEnv(t)
	e.publish(t, "b")
	s := newTestSession(e)
	runSession(t, s)
	waitPhase(t, s, PhaseReady, 3*time.Second)

	occupy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupy.Close()
	busy := occupy.Addr().(*net.TCPAddr).Port

	err = s.AddForward("a", busy)
	var lle *LocalListenError
	if !errors.As(err, &lle) {
		t.Fatalf("端口占用应返回 *LocalListenError,得到 %v", err)
	}
	port := freePort(t)
	if err := s.AddForward("b", port); err != nil {
		t.Fatalf("AddForward(b): %v", err)
	}
	if got := s.Forwards(); len(got) != 1 || got["b"] != port {
		t.Fatalf("Forwards = %v", got)
	}
	if !echoVia(port) {
		t.Fatal("转发 b 不可用")
	}
	if p := s.State().Phase; p != PhaseReady {
		t.Fatalf("Phase = %q,期望 ready", p)
	}
}

func TestSessionRetryNowSkipsBackoff(t *testing.T) {
	e := newSessionEnv(t)
	s := newTestSession(e)
	s.reconnectMin = 10 * time.Second
	runSession(t, s)
	waitPhase(t, s, PhaseReady, 3*time.Second)

	e.server.CloseAllClients()
	waitPhase(t, s, PhaseWaiting, 3*time.Second)
	if st := s.State(); st.RetryAt.IsZero() || st.Err == "" {
		t.Fatalf("退避中应带 RetryAt 与 Err: %+v", st)
	}
	s.RetryNow()
	waitPhase(t, s, PhaseReady, time.Second)
}

func TestSessionPermanentErrorWaitsForRetry(t *testing.T) {
	e := newSessionEnv(t)
	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(otherKey)
	e.cfg.HostPub = writeHostPub(t, otherSigner.PublicKey())

	var mu sync.Mutex
	var phases []Phase
	s := newTestSession(e, WithStateHook(func(st ConnState) {
		mu.Lock()
		phases = append(phases, st.Phase)
		mu.Unlock()
	}))
	errCh := runSession(t, s)
	waitPhase(t, s, PhaseFailed, 3*time.Second)
	st := s.State()
	if st.Err == "" {
		t.Fatal("Failed 时 Err 应非空")
	}
	select {
	case err := <-errCh:
		t.Fatalf("不可恢复错误时 Run 不应返回: %v", err)
	case <-time.After(200 * time.Millisecond):
	}

	s.RetryNow()
	waitFor(t, 3*time.Second, func() bool {
		cur := s.State()
		return cur.Phase == PhaseFailed && cur.Attempt > st.Attempt
	})
	mu.Lock()
	defer mu.Unlock()
	if len(phases) < 3 || phases[0] != PhaseConnecting || phases[1] != PhaseFailed || phases[2] != PhaseConnecting {
		t.Fatalf("状态钩子序列 = %v", phases)
	}
}

func TestSessionDevicesNotConnected(t *testing.T) {
	e := newSessionEnv(t)
	s := NewSession(e.cfg, "")
	if _, err := s.Devices(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("未连接时应返回 ErrNotConnected,得到 %v", err)
	}
}

func TestSessionDevicesConnected(t *testing.T) {
	e := newSessionEnv(t)
	e.publish(t, "nas")
	s := newTestSession(e)
	runSession(t, s)
	waitPhase(t, s, PhaseReady, 3*time.Second)
	devs, err := s.Devices(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(devs) != 1 || devs[0].Name != "nas" || !devs[0].Online {
		t.Fatalf("Devices = %+v", devs)
	}
}

// TestSessionRemoveForwardClosesActive:RemoveForward 关闭监听与进行中的连接。
func TestSessionRemoveForwardClosesActive(t *testing.T) {
	e := newSessionEnv(t)
	e.publish(t, "nas")
	s := newTestSession(e)
	runSession(t, s)
	waitPhase(t, s, PhaseReady, 3*time.Second)
	port := freePort(t)
	if err := s.AddForward("nas", port); err != nil {
		t.Fatal(err)
	}
	// 建立一条进行中的连接(回显服务读到换行前不会关闭)
	conn, err := net.Dial("tcp", localAddr(port))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(100 * time.Millisecond) // 等代理接通

	s.RemoveForward("nas")
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("RemoveForward 后进行中的连接应被关闭,Read 得到 %v", err)
	}
	if _, err := net.DialTimeout("tcp", localAddr(port), time.Second); err == nil {
		t.Fatal("RemoveForward 后端口不应再监听")
	}
	if len(s.Forwards()) != 0 {
		t.Fatalf("Forwards = %v", s.Forwards())
	}
	s.RemoveForward("nas") // 重复调用无副作用
}

// TestSessionAddForwardSameNameChangePort:同名同端口无操作;换端口则关旧开新。
func TestSessionAddForwardSameNameChangePort(t *testing.T) {
	e := newSessionEnv(t)
	s := NewSession(e.cfg, "")
	t.Cleanup(func() { s.RemoveForward("nas") })
	p1 := freePort(t)
	if err := s.AddForward("nas", p1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddForward("nas", p1); err != nil {
		t.Fatalf("同名同端口应为无操作: %v", err)
	}
	p2 := freePort(t)
	if err := s.AddForward("nas", p2); err != nil {
		t.Fatal(err)
	}
	if got := s.Forwards(); len(got) != 1 || got["nas"] != p2 {
		t.Fatalf("Forwards = %v", got)
	}
	if _, err := net.DialTimeout("tcp", localAddr(p1), time.Second); err == nil {
		t.Fatal("旧端口应已关闭")
	}
	if err := s.AddForward("bad/name", freePort(t)); err == nil {
		t.Fatal("非法名字应报错")
	}
}

// TestSessionRunExitClosesForwards:Run 返回时关闭全部本机监听,不留泄漏。
func TestSessionRunExitClosesForwards(t *testing.T) {
	e := newSessionEnv(t)
	s := newTestSession(e)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitPhase(t, s, PhaseReady, 3*time.Second)
	port := freePort(t)
	if err := s.AddForward("nas", port); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel 后 Run 未退出")
	}
	if _, err := net.DialTimeout("tcp", localAddr(port), time.Second); err == nil {
		t.Fatal("Run 返回后本机端口不应再监听")
	}
	if len(s.Forwards()) != 0 {
		t.Fatalf("Forwards = %v", s.Forwards())
	}
	if _, err := s.Devices(context.Background()); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("Run 返回后应为未连接: %v", err)
	}
}

// flakyListener 第一次 Accept 返回一个瞬时错误(模拟 EMFILE),之后正常。
type flakyListener struct {
	net.Listener
	once sync.Once
}

func (l *flakyListener) Accept() (net.Conn, error) {
	var fail bool
	l.once.Do(func() { fail = true })
	if fail {
		return nil, fmt.Errorf("accept: %w", errors.New("too many open files"))
	}
	return l.Listener.Accept()
}

// TestSessionForwardSurvivesTransientAcceptError:Accept 的瞬时错误不应让转发
// 永久失效;只有监听被关闭才退出 accept 循环。
func TestSessionForwardSurvivesTransientAcceptError(t *testing.T) {
	e := newSessionEnv(t)
	e.publish(t, "nas")
	s := newTestSession(e)
	s.listen = func(network, addr string) (net.Listener, error) {
		l, err := net.Listen(network, addr)
		if err != nil {
			return nil, err
		}
		return &flakyListener{Listener: l}, nil
	}
	runSession(t, s)
	waitPhase(t, s, PhaseReady, 3*time.Second)
	port := freePort(t)
	if err := s.AddForward("nas", port); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { return echoVia(port) })
}
