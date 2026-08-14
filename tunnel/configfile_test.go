package tunnel

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadTOML(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tunnel.toml")
	os.WriteFile(p, []byte(`
remote_ip = "1.2.3.4"
remote_ssh_port = 2222
remote_ssh_pub = "~/.ssh/vps.pub"
local_ssh_key = "~/.ssh/custom_key"
user = "tunnel"
local_ssh_port = 9999
remote_listen_port = 5102
`), 0o600)

	fc, err := LoadTOML(p)
	if err != nil {
		t.Fatalf("LoadTOML: %v", err)
	}
	if fc.RemoteIP != "1.2.3.4" || fc.RemoteSSHPort != 2222 || fc.User != "tunnel" {
		t.Fatalf("基础字段: %+v", fc)
	}
	if fc.LocalSSHPort != 9999 || fc.RemoteListenPort != 5102 {
		t.Fatalf("端口字段: %+v", fc)
	}
	home, _ := os.UserHomeDir()
	if fc.RemoteSSHPub != filepath.Join(home, ".ssh/vps.pub") {
		t.Fatalf("~ 未展开: %q", fc.RemoteSSHPub)
	}
	if fc.LocalSSHKey != filepath.Join(home, ".ssh/custom_key") {
		t.Fatalf("~ 未展开: %q", fc.LocalSSHKey)
	}
}

func TestLoadTOMLNotFound(t *testing.T) {
	if _, err := LoadTOML("/nonexistent/tunnel.toml"); err == nil {
		t.Fatal("文件不存在应报错")
	}
}

func TestLoadTOMLPartial(t *testing.T) {
	p := filepath.Join(t.TempDir(), "tunnel.toml")
	os.WriteFile(p, []byte(`remote_ip = "1.2.3.4"`), 0o600)
	fc, err := LoadTOML(p)
	if err != nil {
		t.Fatalf("LoadTOML: %v", err)
	}
	if fc.RemoteIP != "1.2.3.4" || fc.RemoteSSHPort != 0 {
		t.Fatalf("部分字段: %+v", fc)
	}
}
