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

func TestMergePrecedence(t *testing.T) {
	tests := []struct {
		name           string
		fc             tunnel.FileConfig
		flags          map[string]string
		role           string
		wantSSHPort    int
		wantLocalPort  int
		wantListenPort int
	}{
		{
			name:           "TOML 覆盖内置默认",
			fc:             tunnel.FileConfig{RemoteSSHPort: 2222, LocalSSHPort: 9999, RemoteListenPort: 5102},
			role:           "server",
			wantSSHPort:    2222,
			wantLocalPort:  9999,
			wantListenPort: 5102,
		},
		{
			name:          "flag 覆盖 TOML",
			fc:            tunnel.FileConfig{RemoteSSHPort: 2222, LocalSSHPort: 9999, RemoteListenPort: 5102},
			flags:         map[string]string{"--remote-ssh-port": "3333", "--local-ssh-port": "8888"},
			role:          "server",
			wantSSHPort:   3333,
			wantLocalPort: 8888,
		},
		{
			name:           "server 兜底端口",
			fc:             tunnel.FileConfig{RemoteListenPort: 5102},
			role:           "server",
			wantSSHPort:    defaultRemoteSSHPort,
			wantLocalPort:  defaultInnerSSHPort,
			wantListenPort: 5102,
		},
		{
			name:           "client 本机端口跟随汇合端口",
			fc:             tunnel.FileConfig{RemoteListenPort: 6000},
			role:           "client",
			wantSSHPort:    defaultRemoteSSHPort,
			wantLocalPort:  6000,
			wantListenPort: 6000,
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
			if tt.wantListenPort != 0 && p.remoteListenPort != tt.wantListenPort {
				t.Errorf("remote-listen-port = %d, want %d", p.remoteListenPort, tt.wantListenPort)
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
