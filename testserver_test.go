package tunnel

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// testSSHServer 是进程内 SSH 服务器:公钥认证、keepalive 应答,
// 同时持有 Ed25519 与 ECDSA 两把主机密钥(模拟真实 sshd 多密钥)。
type testSSHServer struct {
	t           *testing.T
	listener    net.Listener
	config      *ssh.ServerConfig
	HostKey     ssh.PublicKey // Ed25519 主机公钥,供客户端钉住
	mu          sync.Mutex
	clientConns []*ssh.ServerConn
	totalConns  int

	forwardsMu sync.Mutex
	forwards   map[uint32]net.Listener
}

func startTestSSHServer(t *testing.T, authorized ssh.PublicKey) *testSSHServer {
	t.Helper()
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edSigner, _ := ssh.NewSignerFromKey(edKey)
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecSigner, _ := ssh.NewSignerFromKey(ecKey)

	s := &testSSHServer{t: t, HostKey: edSigner.PublicKey()}
	s.config = &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(authorized.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("denied")
		},
	}
	s.config.AddHostKey(edSigner)
	s.config.AddHostKey(ecSigner)

	s.listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.acceptLoop()
	t.Cleanup(func() { s.listener.Close() })
	return s
}

func (s *testSSHServer) Addr() string { return s.listener.Addr().String() }

func (s *testSSHServer) acceptLoop() {
	for {
		c, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(c)
	}
}

func (s *testSSHServer) handleConn(c net.Conn) {
	conn, chans, reqs, err := ssh.NewServerConn(c, s.config)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.clientConns = append(s.clientConns, conn)
	s.totalConns++
	s.mu.Unlock()
	go s.handleGlobalRequests(conn, reqs)
	for ch := range chans {
		s.handleChannel(conn, ch)
	}
}

func (s *testSSHServer) handleGlobalRequests(conn *ssh.ServerConn, reqs <-chan *ssh.Request) {
	for req := range reqs {
		req.Reply(req.Type == "keepalive@openssh.com", nil)
	}
}

// handleChannel 在 Task 4 中扩展 direct-tcpip;基础版全部拒绝。
func (s *testSSHServer) handleChannel(conn *ssh.ServerConn, ch ssh.NewChannel) {
	ch.Reject(ssh.UnknownChannelType, "unsupported")
}

// CloseAllClients 强制断开所有已接入的客户端(重连测试用)。
func (s *testSSHServer) CloseAllClients() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.clientConns {
		c.Close()
	}
	s.clientConns = nil
}

var _ = io.EOF // Task 3 扩展转发后使用
