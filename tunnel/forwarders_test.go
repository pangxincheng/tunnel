package tunnel

import (
	"bufio"
	"fmt"
	"net"
	"testing"
)

// TestReverseForwarderEndToEnd 验证工厂返回的闭包开箱即用:
// 不需要调用方再写 accept/代理循环。
func TestReverseForwarderEndToEnd(t *testing.T) {
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

	listener, err := ReverseForwarder("127.0.0.1", 0, echo)(client)
	if err != nil {
		t.Fatalf("ReverseForwarder: %v", err)
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

func TestLocalForwarderEndToEnd(t *testing.T) {
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

	listener, err := LocalForwarder("127.0.0.1:0", echo)(client)
	if err != nil {
		t.Fatalf("LocalForwarder: %v", err)
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
