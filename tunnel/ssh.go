package tunnel

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// Config 是建立一条 SSH 连接所需的全部参数。
// 字段同时服务 CLI(从 flags 组装)与移动端 gomobile(直接注入)。
type Config struct {
	SSHAddr string // VPS host:port(--remote-ip + --remote-ssh-port 组装),必填
	User    string // 必填
	KeyPath string // --local-ssh-key 私钥路径,与 KeyPEM 二选一
	KeyPEM  []byte // 私钥内容(移动端注入用,优先级高于 KeyPath)
	HostPub string // --remote-ssh-pub:主机公钥的文件路径或内联 authorized_keys 内容,必填
}

// Dial 建立一条 SSH 连接。
// 认证:KeyPEM/KeyPath 私钥签名。
// 主机校验:钉住 HostPub 给出的公钥,逐字节比对,不匹配即拒绝 ——
// 无 TOFU、无 known_hosts 依赖,确定性模型(详见 spec §2)。
// HostKeyAlgorithms 收敛到钉住公钥类型:真实 sshd 同时持有
// RSA/ECDSA/Ed25519 多把主机密钥,x/crypto 默认偏好 ECDSA 先于
// Ed25519,不限定算法会在多密钥服务器上误判不匹配。
func Dial(cfg *Config) (*ssh.Client, error) {
	signer, err := loadSigner(cfg)
	if err != nil {
		return nil, err
	}
	hostKey, err := loadHostPub(cfg.HostPub)
	if err != nil {
		return nil, err
	}
	client, err := ssh.Dial("tcp", cfg.SSHAddr, &ssh.ClientConfig{
		User:              cfg.User,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		Timeout:           10 * time.Second, // 仅覆盖拨号+握手
		HostKeyAlgorithms: []string{hostKey.Type()},
		HostKeyCallback:   pinnedHostKeyCallback(hostKey),
	})
	if err != nil {
		// 认证失败不会因重试而自愈(公钥没配、用户不存在),直接终止。
		if strings.Contains(err.Error(), "unable to authenticate") {
			return nil, permanent(fmt.Errorf(
				"SSH 认证失败(%s,用户 %s):请确认私钥对应的公钥已加入 VPS 的 authorized_keys:%w",
				cfg.SSHAddr, cfg.User, err,
			))
		}
		return nil, fmt.Errorf("连接 %s: %w", cfg.SSHAddr, err)
	}
	return client, nil
}

// loadSigner 加载私钥:KeyPEM 优先,其次读 KeyPath 文件。
// 加密私钥不在 MVP 范围:给出明确指引而不是卡住等密码。
func loadSigner(cfg *Config) (ssh.Signer, error) {
	pemBytes := cfg.KeyPEM
	if len(pemBytes) == 0 {
		b, err := os.ReadFile(cfg.KeyPath)
		if err != nil {
			return nil, permanent(fmt.Errorf("读取私钥 %s: %w", cfg.KeyPath, err))
		}
		pemBytes = b
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		if _, ok := err.(*ssh.PassphraseMissingError); ok {
			return nil, permanent(fmt.Errorf("私钥已加密:请提供未加密私钥(库层密码认证预留给移动端)"))
		}
		return nil, permanent(fmt.Errorf("解析私钥: %w", err))
	}
	return signer, nil
}

// loadHostPub 解析钉住的主机公钥:存在对应文件则读文件,
// 否则按内联 authorized_keys 内容解析。
func loadHostPub(hostPub string) (ssh.PublicKey, error) {
	content := hostPub
	if b, err := os.ReadFile(hostPub); err == nil {
		content = string(b)
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(content))
	if err != nil {
		return nil, permanent(fmt.Errorf("解析主机公钥(应为 .pub 文件路径或内联 authorized_keys 内容): %w", err))
	}
	return key, nil
}

// pinnedHostKeyCallback 生成只接受指定公钥的主机校验回调。
// 比对用 Marshal 后的 wire 格式字节,与密钥注释等表示层细节无关。
func pinnedHostKeyCallback(want ssh.PublicKey) ssh.HostKeyCallback {
	wantBytes := want.Marshal()
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if !bytes.Equal(key.Marshal(), wantBytes) {
			return permanent(fmt.Errorf(
				"主机公钥不匹配(期望 %s,实际 %s):VPS 主机密钥可能已变更,确认后更新 --remote-ssh-pub",
				ssh.FingerprintSHA256(want), ssh.FingerprintSHA256(key),
			))
		}
		return nil
	}
}
