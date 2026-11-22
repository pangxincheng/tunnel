package tunnel

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
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

	// exec 模拟:Home 是假 $HOME(放 /tmp 下,避免 macOS 上 socket 路径超长);
	// DenyExec 模拟 nologin/ForceCommand 账户;ExecOutput 为 nil 时
	// 只认 DefaultSocketDirCommand。
	Home       string
	DenyExec   bool
	ExecOutput func(cmd string) (stdout string, exit uint32)
	execMu     sync.Mutex
	execs      int

	// BindUnlink 模拟 sshd 的 StreamLocalBindUnlink(默认开启):绑定前删除残留 socket。
	BindUnlink   bool
	unixForwards map[string]net.Listener
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

	s := &testSSHServer{t: t, HostKey: edSigner.PublicKey(), BindUnlink: true}
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

	s.Home, err = os.MkdirTemp("/tmp", "tn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(s.Home) })

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
		case "streamlocal-forward@openssh.com":
			s.handleStreamLocalForward(conn, req)
		case "cancel-streamlocal-forward@openssh.com":
			var p struct{ SocketPath string }
			if ssh.Unmarshal(req.Payload, &p) == nil {
				s.forwardsMu.Lock()
				if l, ok := s.unixForwards[p.SocketPath]; ok {
					l.Close()
					delete(s.unixForwards, p.SocketPath)
				}
				s.forwardsMu.Unlock()
			}
			req.Reply(true, nil)
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

// handleStreamLocalForward 模拟 sshd 的 streamlocal-forward@openssh.com:
// 在 VPS(本机)上监听 unix socket,每条连接经 forwarded-streamlocal 通道交给客户端。
func (s *testSSHServer) handleStreamLocalForward(conn *ssh.ServerConn, req *ssh.Request) {
	var p struct{ SocketPath string }
	if ssh.Unmarshal(req.Payload, &p) != nil {
		req.Reply(false, nil)
		return
	}
	if s.BindUnlink {
		os.Remove(p.SocketPath)
	}
	l, err := net.Listen("unix", p.SocketPath)
	if err != nil {
		req.Reply(false, nil)
		return
	}
	s.forwardsMu.Lock()
	if s.unixForwards == nil {
		s.unixForwards = make(map[string]net.Listener)
	}
	s.unixForwards[p.SocketPath] = l
	s.forwardsMu.Unlock()
	req.Reply(true, nil)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				ch, reqs, err := conn.OpenChannel("forwarded-streamlocal@openssh.com",
					ssh.Marshal(struct{ SocketPath, Reserved0 string }{p.SocketPath, ""}))
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				go io.Copy(ch, c)
				io.Copy(c, ch)
				ch.Close()
			}()
		}
	}()
}

type directTCPIPPayload struct {
	DestAddr   string
	DestPort   uint32
	OriginAddr string
	OriginPort uint32
}

// handleChannel 支持 direct-tcpip:sshd 代客户端连接目标并双向转发。
func (s *testSSHServer) handleChannel(conn *ssh.ServerConn, ch ssh.NewChannel) {
	if ch.ChannelType() == "session" {
		go s.handleSession(ch)
		return
	}
	var target net.Conn
	var err error
	switch ch.ChannelType() {
	case "direct-tcpip":
		var p directTCPIPPayload
		if ssh.Unmarshal(ch.ExtraData(), &p) != nil {
			ch.Reject(ssh.ConnectionFailed, "bad payload")
			return
		}
		target, err = net.Dial("tcp", net.JoinHostPort(p.DestAddr, fmt.Sprint(p.DestPort)))
	case "direct-streamlocal@openssh.com":
		var p struct {
			SocketPath, Reserved0 string
			Reserved1             uint32
		}
		if ssh.Unmarshal(ch.ExtraData(), &p) != nil {
			ch.Reject(ssh.ConnectionFailed, "bad payload")
			return
		}
		target, err = net.Dial("unix", p.SocketPath)
	default:
		ch.Reject(ssh.UnknownChannelType, "unsupported")
		return
	}
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

func (s *testSSHServer) execCount() int {
	s.execMu.Lock()
	defer s.execMu.Unlock()
	return s.execs
}

// defaultExec 只认 DefaultSocketDirCommand:在 Home 下建目录并输出绝对路径。
func (s *testSSHServer) defaultExec(cmd string) (string, uint32) {
	if cmd != DefaultSocketDirCommand {
		return "", 127
	}
	dir := filepath.Join(s.Home, ".tunnel", "sockets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", 1
	}
	os.Chmod(dir, 0o700)
	return dir + "\n", 0
}

// handleSession 支持 session 通道上的 exec 请求(其余请求一律拒绝)。
func (s *testSSHServer) handleSession(nc ssh.NewChannel) {
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	for req := range reqs {
		if req.Type != "exec" {
			req.Reply(false, nil)
			continue
		}
		var p struct{ Command string }
		if ssh.Unmarshal(req.Payload, &p) != nil || s.DenyExec {
			req.Reply(false, nil)
			return
		}
		s.execMu.Lock()
		s.execs++
		s.execMu.Unlock()
		req.Reply(true, nil)
		run := s.ExecOutput
		if run == nil {
			run = s.defaultExec
		}
		out, status := run(p.Command)
		io.WriteString(ch, out)
		ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
		return
	}
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
