package tunnel

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"testing"
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
