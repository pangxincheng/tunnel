package tunnel

import (
	"context"
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
