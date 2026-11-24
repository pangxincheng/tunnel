package tunnel

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// writeClientKey 生成客户端密钥对,返回私钥路径与公钥。
func writeClientKey(t *testing.T) (privPath string, pub ssh.PublicKey) {
	t.Helper()
	edPub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(edPub)
	block, _ := ssh.MarshalPrivateKey(priv, "")
	privPath = filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(privPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return privPath, sshPub
}

// writeHostPub 把主机公钥写成 .pub 文件(authorized_keys 格式),返回路径。
func writeHostPub(t *testing.T, key ssh.PublicKey) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "host.pub")
	if err := os.WriteFile(p, ssh.MarshalAuthorizedKey(key), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDialSuccess(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	cfg := &Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, server.HostKey),
	}
	client, err := Dial(cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
		t.Fatalf("keepalive: %v", err)
	}
}

func TestDialSuccessInlinePub(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	cfg := &Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: string(ssh.MarshalAuthorizedKey(server.HostKey)), // 内联形态
	}
	client, err := Dial(cfg)
	if err != nil {
		t.Fatalf("Dial(内联公钥): %v", err)
	}
	defer client.Close()
}

func TestDialRejectsWrongHostKey(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(otherKey)
	cfg := &Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, otherSigner.PublicKey()), // 张冠李戴
	}
	if _, err := Dial(cfg); err == nil {
		t.Fatal("主机公钥不匹配应被拒绝")
	} else if !IsPermanent(err) {
		t.Fatalf("主机公钥不匹配应为不可恢复错误: %v", err)
	}
}

// TestDialAuthFailureIsPermanent:公钥未授权不会因重试而自愈。
func TestDialAuthFailureIsPermanent(t *testing.T) {
	_, authorized := writeClientKey(t)
	server := startTestSSHServer(t, authorized)
	otherPriv, _ := writeClientKey(t) // 未加入 authorized_keys
	cfg := &Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: otherPriv,
		HostPub: writeHostPub(t, server.HostKey),
	}
	_, err := Dial(cfg)
	if err == nil {
		t.Fatal("未授权公钥应被拒绝")
	}
	if !IsPermanent(err) {
		t.Fatalf("认证失败应为不可恢复错误: %v", err)
	}
	if !strings.Contains(err.Error(), "authorized_keys") {
		t.Fatalf("缺少排查提示: %v", err)
	}
}
