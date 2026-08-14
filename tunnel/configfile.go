package tunnel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// FileConfig 是 tunnel.toml 配置文件的全部字段,均为可选;
// CLI 合并优先级:默认值 < 配置文件 < 命令行 flag。
//
// 字段与 flag 同名(下划线风格):
//
//	remote_ip / remote_ssh_port / remote_ssh_pub / local_ssh_key / user
//	local_ssh_port      server: 内网 sshd 端口;client: 本机监听端口
//	remote_listen_port  VPS loopback 汇合端口
type FileConfig struct {
	RemoteIP         string `toml:"remote_ip"`
	RemoteSSHPort    int    `toml:"remote_ssh_port"`
	RemoteSSHPub     string `toml:"remote_ssh_pub"`
	LocalSSHKey      string `toml:"local_ssh_key"`
	User             string `toml:"user"`
	LocalSSHPort     int    `toml:"local_ssh_port"`
	RemoteListenPort int    `toml:"remote_listen_port"`
}

// LoadTOML 读取 TOML 配置文件;路径类字段做 ~ 展开。
// 所有字段可选,缺失保持零值,由调用方(CLI)决定默认与必填校验。
func LoadTOML(path string) (*FileConfig, error) {
	fc := &FileConfig{}
	if _, err := toml.DecodeFile(path, fc); err != nil {
		return nil, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	fc.RemoteSSHPub = expandHome(fc.RemoteSSHPub)
	fc.LocalSSHKey = expandHome(fc.LocalSSHKey)
	return fc, nil
}

// expandHome 把路径开头的 ~ 展开为用户主目录。
func expandHome(p string) string {
	if p == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}
