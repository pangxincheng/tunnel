package main

import (
	"strings"
	"testing"

	"github.com/pangxincheng/tunnel/cli/tunnel"
)

// nilArgs 模拟 docopt 在选项未出现在命令行时的 map:值为 nil。
func nilArgs() map[string]interface{} {
	return map[string]interface{}{
		"--remote-ip":          nil,
		"--remote-ssh-port":    nil,
		"--remote-ssh-pub":     nil,
		"--local-ssh-key":      nil,
		"--user":               nil,
		"--local-ssh-port":     nil,
		"--remote-listen-port": nil,
		"--name":               nil,
		"--remote-socket-dir":  nil,
	}
}

func intPtr(n int) *int { return &n }

func TestMergePrecedence(t *testing.T) {
	tests := []struct {
		name           string
		fc             tunnel.FileConfig
		flags          map[string]string
		role           string
		wantSSHPort    int
		wantLocalPort  int
		wantListenPort int
		wantListenSet  bool
	}{
		{
			name:           "TOML 覆盖内置默认",
			fc:             tunnel.FileConfig{RemoteSSHPort: 2222, LocalSSHPort: 9999, RemoteListenPort: intPtr(5102)},
			role:           "server",
			wantSSHPort:    2222,
			wantLocalPort:  9999,
			wantListenPort: 5102,
			wantListenSet:  true,
		},
		{
			name:           "flag 覆盖 TOML",
			fc:             tunnel.FileConfig{RemoteSSHPort: 2222, LocalSSHPort: 9999, RemoteListenPort: intPtr(5102)},
			flags:          map[string]string{"--remote-ssh-port": "3333", "--local-ssh-port": "8888"},
			role:           "server",
			wantSSHPort:    3333,
			wantLocalPort:  8888,
			wantListenPort: 5102,
			wantListenSet:  true,
		},
		{
			name:           "server 兜底端口",
			fc:             tunnel.FileConfig{RemoteListenPort: intPtr(5102)},
			role:           "server",
			wantSSHPort:    defaultRemoteSSHPort,
			wantLocalPort:  defaultInnerSSHPort,
			wantListenPort: 5102,
			wantListenSet:  true,
		},
		{
			name:           "client 本机端口跟随汇合端口",
			fc:             tunnel.FileConfig{RemoteListenPort: intPtr(6000)},
			role:           "client",
			wantSSHPort:    defaultRemoteSSHPort,
			wantLocalPort:  6000,
			wantListenPort: 6000,
			wantListenSet:  true,
		},
		{
			name:           "server 显式 0 = VPS 自动分配",
			fc:             tunnel.FileConfig{RemoteListenPort: intPtr(0)},
			role:           "server",
			wantSSHPort:    defaultRemoteSSHPort,
			wantLocalPort:  defaultInnerSSHPort,
			wantListenPort: 0,
			wantListenSet:  true,
		},
		{
			name:           "未配置汇合端口",
			fc:             tunnel.FileConfig{},
			role:           "server",
			wantSSHPort:    defaultRemoteSSHPort,
			wantLocalPort:  defaultInnerSSHPort,
			wantListenPort: 0,
			wantListenSet:  false,
		},
		{
			name:           "flag 显式 0 覆盖 TOML",
			fc:             tunnel.FileConfig{RemoteListenPort: intPtr(5102)},
			flags:          map[string]string{"--remote-listen-port": "0"},
			role:           "server",
			wantSSHPort:    defaultRemoteSSHPort,
			wantLocalPort:  defaultInnerSSHPort,
			wantListenPort: 0,
			wantListenSet:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := nilArgs()
			for k, v := range tt.flags {
				args[k] = v
			}
			p, err := merge(args, &tt.fc, tt.role)
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			if p.remoteSSHPort != tt.wantSSHPort {
				t.Errorf("remote-ssh-port = %d, want %d", p.remoteSSHPort, tt.wantSSHPort)
			}
			if p.localSSHPort != tt.wantLocalPort {
				t.Errorf("local-ssh-port = %d, want %d", p.localSSHPort, tt.wantLocalPort)
			}
			if p.remoteListenPort != tt.wantListenPort || p.remoteListenPortSet != tt.wantListenSet {
				t.Errorf("remote-listen-port = %d(set=%v), want %d(set=%v)",
					p.remoteListenPort, p.remoteListenPortSet, tt.wantListenPort, tt.wantListenSet)
			}
		})
	}
}

func TestMergeRejectsNonNumericPort(t *testing.T) {
	args := nilArgs()
	args["--remote-ssh-port"] = "not-a-number"
	if _, err := merge(args, &tunnel.FileConfig{}, "server"); err == nil {
		t.Fatal("非数字端口应报错")
	}
}

func TestConfigRequiresListenPort(t *testing.T) {
	p, err := merge(nilArgs(), &tunnel.FileConfig{
		RemoteIP: "1.2.3.4", RemoteSSHPub: "ssh-ed25519 AAAA",
	}, "server")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := p.config("server"); err == nil {
		t.Fatal("未配置 remote-listen-port 应报错")
	}
}

func TestConfigAllowsAutoPortForServerOnly(t *testing.T) {
	p, err := merge(nilArgs(), &tunnel.FileConfig{
		RemoteIP: "1.2.3.4", RemoteSSHPub: "ssh-ed25519 AAAA", RemoteListenPort: intPtr(0),
	}, "server")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := p.config("server"); err != nil {
		t.Fatalf("server 应允许自动分配端口: %v", err)
	}
	if _, err := p.config("client"); err == nil {
		t.Fatal("client 不应允许 remote-listen-port=0")
	}
}

func TestConfigRejectsOutOfRangePort(t *testing.T) {
	p, err := merge(nilArgs(), &tunnel.FileConfig{
		RemoteIP: "1.2.3.4", RemoteSSHPub: "ssh-ed25519 AAAA", RemoteListenPort: intPtr(70000),
	}, "server")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := p.config("server"); err == nil {
		t.Fatal("超出 0-65535 的端口应报错")
	}
}

// TestMergeModeSelection 覆盖 --name 与 --remote-listen-port 的互斥/优先级规则:
// 同一来源两者都给 → 互斥错误;flag 胜过 TOML,被压过的另一项忽略。
func TestMergeModeSelection(t *testing.T) {
	tests := []struct {
		name     string
		flags    map[string]string
		fc       tunnel.FileConfig
		wantErr  bool
		wantName string
		wantPort bool
	}{
		{name: "flag 两者都给", flags: map[string]string{"--name": "nas", "--remote-listen-port": "5102"}, wantErr: true},
		{name: "flag name 压过 TOML 端口", flags: map[string]string{"--name": "nas"}, fc: tunnel.FileConfig{RemoteListenPort: intPtr(5102)}, wantName: "nas"},
		{name: "flag 端口压过 TOML name", flags: map[string]string{"--remote-listen-port": "5102"}, fc: tunnel.FileConfig{Name: "nas"}, wantPort: true},
		{name: "TOML 两者都给", fc: tunnel.FileConfig{Name: "nas", RemoteListenPort: intPtr(5102)}, wantErr: true},
		{name: "TOML 只给 name", fc: tunnel.FileConfig{Name: "nas"}, wantName: "nas"},
		{name: "TOML 只给端口", fc: tunnel.FileConfig{RemoteListenPort: intPtr(5102)}, wantPort: true},
		{name: "都没给", wantName: "", wantPort: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := nilArgs()
			for k, v := range tt.flags {
				args[k] = v
			}
			p, err := merge(args, &tt.fc, "server")
			if tt.wantErr {
				if err == nil {
					t.Fatal("应报互斥错误")
				}
				return
			}
			if err != nil {
				t.Fatalf("merge: %v", err)
			}
			if p.name != tt.wantName || p.remoteListenPortSet != tt.wantPort {
				t.Fatalf("name=%q portSet=%v, want name=%q portSet=%v", p.name, p.remoteListenPortSet, tt.wantName, tt.wantPort)
			}
		})
	}
}

func TestConfigRequiresNameOrPort(t *testing.T) {
	p, err := merge(nilArgs(), &tunnel.FileConfig{RemoteIP: "1.2.3.4", RemoteSSHPub: "ssh-ed25519 AAAA"}, "server")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	_, err = p.config("server")
	if err == nil || !strings.Contains(err.Error(), "--name") {
		t.Fatalf("都未配置时应提示 --name 或 --remote-listen-port: %v", err)
	}
}

func nameModeFile() tunnel.FileConfig {
	return tunnel.FileConfig{RemoteIP: "1.2.3.4", RemoteSSHPub: "ssh-ed25519 AAAA", Name: "nas"}
}

func TestConfigNameModeClientRequiresLocalPort(t *testing.T) {
	fc := nameModeFile()
	p, err := merge(nilArgs(), &fc, "client")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := p.config("client"); err == nil || !strings.Contains(err.Error(), "--local-ssh-port") {
		t.Fatalf("名字模式 client 缺本机端口应报错: %v", err)
	}
	args := nilArgs()
	args["--local-ssh-port"] = "5102"
	p, err = merge(args, &fc, "client")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := p.config("client"); err != nil {
		t.Fatalf("给出本机端口后应通过: %v", err)
	}
}

func TestConfigNameModeServerDefaults(t *testing.T) {
	fc := nameModeFile()
	p, err := merge(nilArgs(), &fc, "server")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if p.localSSHPort != defaultInnerSSHPort {
		t.Fatalf("local-ssh-port = %d, want %d", p.localSSHPort, defaultInnerSSHPort)
	}
	if _, err := p.config("server"); err != nil {
		t.Fatalf("config: %v", err)
	}
}

func TestConfigRejectsInvalidName(t *testing.T) {
	fc := nameModeFile()
	args := nilArgs()
	args["--name"] = "a/b"
	p, err := merge(args, &fc, "server")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := p.config("server"); err == nil {
		t.Fatal("非法名字应报错")
	}
}

func TestConfigRejectsRelativeSocketDir(t *testing.T) {
	fc := nameModeFile()
	args := nilArgs()
	args["--remote-socket-dir"] = "sockets"
	p, err := merge(args, &fc, "server")
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if _, err := p.config("server"); err == nil {
		t.Fatal("相对路径的 socket 目录应报错")
	}
}
