package tunnel

import (
	"bufio"
	"fmt"
	"net"
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

	listener, err := ReverseForward(client, "127.0.0.1", 0, echo)
	if err != nil {
		t.Fatalf("ReverseForward: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			remote, err := listener.Accept()
			if err != nil {
				return
			}
			go ProxyToLocal(remote, echo)
		}
	}()

	vpsPort := listener.Addr().(*net.TCPAddr).Port
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", vpsPort))
	if err != nil {
		t.Fatalf("dial forwarded: %v", err)
	}
	defer conn.Close()
	fmt.Fprintln(conn, "hello")
	got, _ := bufio.NewReader(conn).ReadString('\n')
	if got != "echo: hello\n" {
		t.Fatalf("got %q", got)
	}
}
