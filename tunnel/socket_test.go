package tunnel

import (
	"errors"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// dialTestServer 启动进程内 SSH 服务器并建立一条已认证连接。
func dialTestServer(t *testing.T) (*testSSHServer, *ssh.Client) {
	t.Helper()
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	client, err := Dial(&Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, server.HostKey),
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	return server, client
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"nas", "a", "home-pc_1.lan", strings.Repeat("a", 64)} {
		if err := ValidateName(ok); err != nil {
			t.Errorf("%q 应合法: %v", ok, err)
		}
	}
	for _, bad := range []string{"", ".hidden", "-x", "a/b", "a b", strings.Repeat("a", 65), "名字"} {
		if ValidateName(bad) == nil {
			t.Errorf("%q 应非法", bad)
		}
	}
}

func TestResolveSocketPathExplicitDir(t *testing.T) {
	server, client := dialTestServer(t)
	server.DenyExec = true
	got, err := ResolveSocketPath(client, "/srv/tn", "nas")
	if err != nil {
		t.Fatalf("ResolveSocketPath: %v", err)
	}
	if got != "/srv/tn/nas.sock" {
		t.Fatalf("got %q", got)
	}
	if n := server.execCount(); n != 0 {
		t.Fatalf("显式目录不应执行远程命令,实际 %d 次", n)
	}
}

func TestResolveSocketPathDefaultViaExec(t *testing.T) {
	server, client := dialTestServer(t)
	got, err := ResolveSocketPath(client, "", "nas")
	if err != nil {
		t.Fatalf("ResolveSocketPath: %v", err)
	}
	want := server.Home + "/.tunnel/sockets/nas.sock"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	st, err := os.Stat(server.Home + "/.tunnel/sockets")
	if err != nil {
		t.Fatalf("目录未创建: %v", err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Fatalf("目录权限 %v, want 0700", st.Mode().Perm())
	}
}

func TestResolveSocketPathExecDenied(t *testing.T) {
	server, client := dialTestServer(t)
	server.DenyExec = true
	_, err := ResolveSocketPath(client, "", "nas")
	if err == nil {
		t.Fatal("exec 被拒应报错")
	}
	var sde *SocketDirError
	if !IsPermanent(err) || !errors.As(err, &sde) {
		t.Fatalf("应为不可恢复的 SocketDirError: %v", err)
	}
	if !strings.Contains(err.Error(), "remote_socket_dir") {
		t.Fatalf("缺少配置提示: %v", err)
	}
}

func TestResolveSocketPathRejectsRelativeOutput(t *testing.T) {
	server, client := dialTestServer(t)
	server.ExecOutput = func(string) (string, uint32) { return "relative/dir\n", 0 }
	if _, err := ResolveSocketPath(client, "", "nas"); err == nil {
		t.Fatal("非绝对路径输出应报错")
	}
}

func TestResolveSocketPathTooLong(t *testing.T) {
	_, client := dialTestServer(t)
	_, err := ResolveSocketPath(client, "/"+strings.Repeat("d", 95), "nas")
	if err == nil || !IsPermanent(err) {
		t.Fatalf("超长路径应为不可恢复错误: %v", err)
	}
	if !strings.Contains(err.Error(), "103") {
		t.Fatalf("缺少长度上限提示: %v", err)
	}
}
