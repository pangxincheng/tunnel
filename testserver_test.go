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
		switch req.Type {
		case "keepalive@openssh.com":
			req.Reply(true, nil)
		case "tcpip-forward":
			s.handleForwardRequest(conn, req)
		case "cancel-tcpip-forward":
			var p tcpipForwardRequest
			if ssh.Unmarshal(req.Payload, &p) == nil {
				s.forwardsMu.Lock()
				if l, ok := s.forwards[p.BindPort]; ok {
					l.Close()
					delete(s.forwards, p.BindPort)
				}
				s.forwardsMu.Unlock()
			}
			req.Reply(true, nil)
		default:
			req.Reply(false, nil)
		}
	}
}

type tcpipForwardRequest struct {
	BindAddr string
	BindPort uint32
}

type forwardedTCPIPPayload struct {
	ConnectedAddr string
	ConnectedPort uint32
	OriginAddr    string
	OriginPort    uint32
}

func (s *testSSHServer) handleForwardRequest(conn *ssh.ServerConn, req *ssh.Request) {
	var p tcpipForwardRequest
	if ssh.Unmarshal(req.Payload, &p) != nil {
		req.Reply(false, nil)
		return
	}
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p.BindPort))
	if err != nil {
		req.Reply(false, nil)
		return
	}
	bound := uint32(l.Addr().(*net.TCPAddr).Port)
	s.forwardsMu.Lock()
	if s.forwards == nil {
		s.forwards = make(map[uint32]net.Listener)
	}
	s.forwards[bound] = l
	s.forwardsMu.Unlock()
	if p.BindPort == 0 {
		req.Reply(true, ssh.Marshal(struct{ Port uint32 }{bound}))
	} else {
		req.Reply(true, nil)
	}
	go s.forwardAcceptLoop(conn, l, p.BindAddr, bound)
}

func (s *testSSHServer) forwardAcceptLoop(conn *ssh.ServerConn, l net.Listener, bindAddr string, bound uint32) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			origin := c.RemoteAddr().(*net.TCPAddr)
			// ConnectedAddr 必须回显客户端请求的 bindAddr,
			// 否则 x/crypto 客户端按地址分发不到监听器会拒绝通道
			ch, reqs, err := conn.OpenChannel("forwarded-tcpip", ssh.Marshal(forwardedTCPIPPayload{
				ConnectedAddr: bindAddr, ConnectedPort: bound,
				OriginAddr: origin.IP.String(), OriginPort: uint32(origin.Port),
			}))
			if err != nil {
				return
			}
			go ssh.DiscardRequests(reqs)
			go io.Copy(ch, c)
			io.Copy(c, ch)
			ch.Close()
		}()
	}
}

type directTCPIPPayload struct {
	DestAddr   string
	DestPort   uint32
	OriginAddr string
	OriginPort uint32
}

// handleChannel 支持 direct-tcpip:sshd 代客户端连接目标并双向转发。
func (s *testSSHServer) handleChannel(conn *ssh.ServerConn, ch ssh.NewChannel) {
	if ch.ChannelType() != "direct-tcpip" {
		ch.Reject(ssh.UnknownChannelType, "unsupported")
		return
	}
	var p directTCPIPPayload
	if ssh.Unmarshal(ch.ExtraData(), &p) != nil {
		ch.Reject(ssh.ConnectionFailed, "bad payload")
		return
	}
	target, err := net.Dial("tcp", net.JoinHostPort(p.DestAddr, fmt.Sprint(p.DestPort)))
	if err != nil {
		ch.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	channel, reqs, err := ch.Accept()
	if err != nil {
		target.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		defer channel.Close()
		defer target.Close()
		go io.Copy(channel, target)
		io.Copy(target, channel)
	}()
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
