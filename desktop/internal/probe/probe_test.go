package probe

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pangxincheng/tunnel/tunnel"
	"golang.org/x/crypto/ssh"
)

type testServer struct {
	ln      net.Listener
	hostPub ssh.PublicKey
	home    string
}

func newSigner(t *testing.T) (ssh.Signer, ed25519.PrivateKey) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	s, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return s, priv
}

func writeKey(t *testing.T, priv ed25519.PrivateKey, pass string) string {
	t.Helper()
	var blk *pem.Block
	var err error
	if pass == "" {
		blk, err = ssh.MarshalPrivateKey(priv, "")
	} else {
		blk, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "", []byte(pass))
	}
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(p, pem.EncodeToMemory(blk), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func startServer(t *testing.T, authorized ssh.PublicKey) *testServer {
	t.Helper()
	hostSigner, _ := newSigner(t)
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
			if string(k.Marshal()) == string(authorized.Marshal()) {
				return nil, nil
			}
			return nil, errors.New("denied")
		},
	}
	cfg.AddHostKey(hostSigner)
	home, err := os.MkdirTemp("/tmp", "pr")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &testServer{ln: ln, hostPub: hostSigner.PublicKey(), home: home}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c, cfg)
		}
	}()
	return s
}

func (s *testServer) serve(c net.Conn, cfg *ssh.ServerConfig) {
	conn, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	defer conn.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "no")
			continue
		}
		ch, rq, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			defer ch.Close()
			for r := range rq {
				if r.Type != "exec" {
					r.Reply(false, nil)
					continue
				}
				var p struct{ Command string }
				ssh.Unmarshal(r.Payload, &p)
				r.Reply(true, nil)
				status := uint32(127)
				if p.Command == tunnel.DefaultSocketDirCommand {
					dir := filepath.Join(s.home, ".tunnel", "sockets")
					os.MkdirAll(dir, 0o700)
					io.WriteString(ch, dir+"\n")
					status = 0
				}
				ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{status}))
				return
			}
		}()
	}
}

func (s *testServer) params(keyPath string) Params {
	addr := s.ln.Addr().(*net.TCPAddr)
	return Params{Host: addr.IP.String(), Port: addr.Port, User: "u", KeyPath: keyPath}
}

func TestProbeOK(t *testing.T) {
	cs, cpriv := newSigner(t)
	srv := startServer(t, cs.PublicKey())
	res, err := Test(t.Context(), srv.params(writeKey(t, cpriv, "")))
	if err != nil {
		t.Fatal(err)
	}
	if res.Fingerprint != ssh.FingerprintSHA256(srv.hostPub) {
		t.Errorf("fingerprint %q", res.Fingerprint)
	}
	want := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(srv.hostPub)))
	if res.HostKey != want {
		t.Errorf("hostkey %q want %q", res.HostKey, want)
	}
	if !filepath.IsAbs(res.SocketDir) || !strings.HasSuffix(res.SocketDir, filepath.Join(".tunnel", "sockets")) {
		t.Errorf("socketdir %q", res.SocketDir)
	}
}

func TestProbeExplicitSocketDir(t *testing.T) {
	cs, cpriv := newSigner(t)
	srv := startServer(t, cs.PublicKey())
	p := srv.params(writeKey(t, cpriv, ""))
	p.SocketDir = srv.home
	res, err := Test(t.Context(), p)
	if err != nil {
		t.Fatal(err)
	}
	if res.SocketDir != srv.home {
		t.Errorf("socketdir %q", res.SocketDir)
	}
}

func TestProbePassphrase(t *testing.T) {
	cs, cpriv := newSigner(t)
	srv := startServer(t, cs.PublicKey())
	_, err := Test(t.Context(), srv.params(writeKey(t, cpriv, "secret")))
	if !errors.Is(err, ErrPassphrase) {
		t.Fatalf("err = %v", err)
	}
}

func TestProbeAuthRejected(t *testing.T) {
	cs, _ := newSigner(t)
	_, other := newSigner(t)
	srv := startServer(t, cs.PublicKey())
	_, err := Test(t.Context(), srv.params(writeKey(t, other, "")))
	var ae *AuthError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v (%T)", err, err)
	}
}

func TestProbeDialFail(t *testing.T) {
	_, cpriv := newSigner(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	_, err := Test(t.Context(), Params{Host: "127.0.0.1", Port: port, User: "u", KeyPath: writeKey(t, cpriv, "")})
	var de *DialError
	if !errors.As(err, &de) {
		t.Fatalf("err = %v (%T)", err, err)
	}
}

func TestProbeKeyFileMissing(t *testing.T) {
	_, err := Test(t.Context(), Params{Host: "127.0.0.1", Port: 1, User: "u", KeyPath: "/nonexistent/key"})
	if err == nil {
		t.Fatal("want error")
	}
	var de *DialError
	if errors.As(err, &de) {
		t.Fatalf("unexpected DialError: %v", err)
	}
}
