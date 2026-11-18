package main

import (
	"testing"

	"github.com/pangxincheng/tunnel/tunnel"
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
