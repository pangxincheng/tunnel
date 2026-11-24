package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestClientReconnectsAfterServerDrop(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	echo := startEchoServer(t)

	cfg := &Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, server.HostKey),
	}
	c := NewClient(cfg, ReverseForward("127.0.0.1:0", echo))
	c.keepaliveInterval = 50 * time.Millisecond
	c.reconnectMin = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- c.Run(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return server.totalConns >= 1
	})
	server.CloseAllClients()
	waitFor(t, 3*time.Second, func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return server.totalConns >= 2
	})

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel 后未退出")
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("条件未在超时内满足")
}

// TestRunFailsFastOnPermanentError:VPS 端口占用属于不可恢复错误,
// Run 应立即返回而不是按退避无限重连。
func TestRunFailsFastOnPermanentError(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	occupy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupy.Close()

	cfg := &Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, server.HostKey),
	}
	c := NewClient(cfg, ReverseForward(occupy.Addr().String(), "127.0.0.1:22"))

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	start := time.Now()
	err = c.Run(ctx)
	if err == nil {
		t.Fatal("端口占用应返回错误")
	}
	if !IsPermanent(err) {
		t.Fatalf("应为不可恢复错误: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("未快速失败,耗时 %v", elapsed)
	}
}

// TestClientReconnectsWithNameForwarding:名字模式下断线重连后,
// 同一 socket 路径重新可用(旧 socket 残留被替换)。
func TestClientReconnectsWithNameForwarding(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	echo := startEchoServer(t)

	cfg := &Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, server.HostKey),
	}
	c := NewClient(cfg, ReverseForwardUnix("", "nas", echo))
	c.keepaliveInterval = 50 * time.Millisecond
	c.reconnectMin = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	sock := server.Home + "/.tunnel/sockets/nas.sock"
	reachable := func() bool {
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return false
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		fmt.Fprintln(conn, "ping")
		got, _ := bufio.NewReader(conn).ReadString('\n')
		return got == "echo: ping\n"
	}
	waitFor(t, 3*time.Second, reachable)
	server.CloseAllClients()
	waitFor(t, 3*time.Second, func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return server.totalConns >= 2
	})
	waitFor(t, 3*time.Second, reachable)
}
