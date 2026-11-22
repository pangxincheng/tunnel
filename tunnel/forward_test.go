package tunnel

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// startEchoServer 模拟内网服务:回显一行。
func startEchoServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				fmt.Fprintf(c, "echo: %s", line)
			}()
		}
	}()
	return l.Addr().String()
}

// TestReverseForwardEndToEnd 验证工厂返回的闭包开箱即用:
// 不需要调用方再写 accept/代理循环。
func TestReverseForwardEndToEnd(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	echo := startEchoServer(t)

	client, err := Dial(&Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, server.HostKey),
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	listener, err := ReverseForward("127.0.0.1:0", echo)(client)
	if err != nil {
		t.Fatalf("ReverseForward: %v", err)
	}
	defer listener.Close()

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", listener.Addr().(*net.TCPAddr).Port))
	if err != nil {
		t.Fatalf("dial forwarded: %v", err)
	}
	defer conn.Close()
	fmt.Fprintln(conn, "factory")
	got, _ := bufio.NewReader(conn).ReadString('\n')
	if got != "echo: factory\n" {
		t.Fatalf("got %q", got)
	}
}

func TestLocalForwardEndToEnd(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	echo := startEchoServer(t)

	client, err := Dial(&Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, server.HostKey),
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	listener, err := LocalForward("127.0.0.1:0", echo)(client)
	if err != nil {
		t.Fatalf("LocalForward: %v", err)
	}
	defer listener.Close()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial local: %v", err)
	}
	defer conn.Close()
	fmt.Fprintln(conn, "factory")
	got, _ := bufio.NewReader(conn).ReadString('\n')
	if got != "echo: factory\n" {
		t.Fatalf("got %q", got)
	}
}

// TestReverseForwardOccupiedPort:VPS 侧端口被占用时 sshd 只回通用拒绝,
// 必须转成带排查提示的不可恢复错误,否则 CLI 只会无限重连。
func TestReverseForwardOccupiedPort(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	occupy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupy.Close()

	client, err := Dial(&Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, server.HostKey),
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	_, err = ReverseForward(occupy.Addr().String(), "127.0.0.1:22")(client)
	if err == nil {
		t.Fatal("端口被占用应返回错误")
	}
	if !IsPermanent(err) {
		t.Fatalf("端口占用应为不可恢复错误: %v", err)
	}
	if !strings.Contains(err.Error(), "已被占用") || !strings.Contains(err.Error(), "ss -ltnp") {
		t.Fatalf("缺少端口占用排查提示: %v", err)
	}
}

func TestLocalForwardOccupiedPort(t *testing.T) {
	occupy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupy.Close()

	_, err = LocalForward(occupy.Addr().String(), "127.0.0.1:22")(nil)
	if err == nil {
		t.Fatal("本机端口被占用应返回错误")
	}
	if !IsPermanent(err) {
		t.Fatalf("本机端口占用应为不可恢复错误: %v", err)
	}
	if !strings.Contains(err.Error(), "已被占用") {
		t.Fatalf("缺少端口占用提示: %v", err)
	}
}

// echoRoundTrip 经 conn 发一行,断言收到回显。
func echoRoundTrip(t *testing.T, conn net.Conn, msg string) {
	t.Helper()
	defer conn.Close()
	fmt.Fprintln(conn, msg)
	got, _ := bufio.NewReader(conn).ReadString('\n')
	if got != "echo: "+msg+"\n" {
		t.Fatalf("got %q", got)
	}
}

// dialSecond 对同一测试服务器再建一条 SSH 连接(模拟另一端)。
func dialSecond(t *testing.T, server *testSSHServer, privPath string) *ssh.Client {
	t.Helper()
	c, err := Dial(&Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, server.HostKey),
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestReverseForwardUnixEndToEnd(t *testing.T) {
	server, client := dialTestServer(t)
	echo := startEchoServer(t)

	l, err := ReverseForwardUnix("", "nas", echo)(client)
	if err != nil {
		t.Fatalf("ReverseForwardUnix: %v", err)
	}
	defer l.Close()
	want := server.Home + "/.tunnel/sockets/nas.sock"
	if l.Addr().String() != want {
		t.Fatalf("listen = %q, want %q", l.Addr().String(), want)
	}
	conn, err := net.Dial("unix", want)
	if err != nil {
		t.Fatalf("dial socket: %v", err)
	}
	echoRoundTrip(t, conn, "unix")
}

func TestLocalForwardUnixEndToEnd(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	echo := startEchoServer(t)

	rl, err := ReverseForwardUnix("", "nas", echo)(dialSecond(t, server, privPath))
	if err != nil {
		t.Fatalf("ReverseForwardUnix: %v", err)
	}
	defer rl.Close()

	ll, err := LocalForwardUnix("127.0.0.1:0", "", "nas")(dialSecond(t, server, privPath))
	if err != nil {
		t.Fatalf("LocalForwardUnix: %v", err)
	}
	defer ll.Close()
	conn, err := net.Dial("tcp", ll.Addr().String())
	if err != nil {
		t.Fatalf("dial local: %v", err)
	}
	echoRoundTrip(t, conn, "by-name")
}

// TestReverseForwardUnixReplacesStaleSocket:断线后旧会话的 socket 残留,
// 同名重新绑定必须成功(StreamLocalBindUnlink)—— 本特性要修的核心场景。
func TestReverseForwardUnixReplacesStaleSocket(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	echo := startEchoServer(t)

	if _, err := ReverseForwardUnix("", "nas", echo)(dialSecond(t, server, privPath)); err != nil {
		t.Fatalf("first ReverseForwardUnix: %v", err)
	}
	server.CloseAllClients()

	l, err := ReverseForwardUnix("", "nas", echo)(dialSecond(t, server, privPath))
	if err != nil {
		t.Fatalf("重连后同名绑定失败: %v", err)
	}
	defer l.Close()
	conn, err := net.Dial("unix", l.Addr().String())
	if err != nil {
		t.Fatalf("dial socket: %v", err)
	}
	echoRoundTrip(t, conn, "again")
}

func TestReverseForwardUnixStaleWithoutUnlink(t *testing.T) {
	server, client := dialTestServer(t)
	server.BindUnlink = false
	occupy, err := net.Listen("unix", server.Home+"/nas.sock")
	if err != nil {
		t.Fatal(err)
	}
	defer occupy.Close()

	_, err = ReverseForwardUnix(server.Home, "nas", "127.0.0.1:22")(client)
	if err == nil || !IsPermanent(err) {
		t.Fatalf("残留 socket 阻塞绑定应为不可恢复错误: %v", err)
	}
	if !strings.Contains(err.Error(), "StreamLocalBindUnlink") {
		t.Fatalf("缺少 StreamLocalBindUnlink 提示: %v", err)
	}
}

// TestLocalForwardUnixTargetOffline:对应名字的 server 不在线时,
// 本机连接应被立即关闭而不是挂起。
func TestLocalForwardUnixTargetOffline(t *testing.T) {
	_, client := dialTestServer(t)
	l, err := LocalForwardUnix("127.0.0.1:0", "", "nas")(client)
	if err != nil {
		t.Fatalf("LocalForwardUnix: %v", err)
	}
	defer l.Close()
	conn, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatalf("dial local: %v", err)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Read(make([]byte, 1)); err != io.EOF {
		t.Fatalf("应收到 EOF(连接被关闭),got %v", err)
	}
}
