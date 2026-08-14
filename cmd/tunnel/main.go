// tunnel CLI:库包 tunnel 的薄壳。
//   tunnel server [--local-ssh-port 22]   [--remote-listen-port 5102] [公共参数...]
//   tunnel client [--local-ssh-port 5102] [--remote-listen-port 5102] [公共参数...]
// 公共参数:-c/--config(TOML 配置)、--remote-ip / --remote-ssh-port /
// --remote-ssh-pub / --local-ssh-key / --user
//
// 参数优先级:默认值 < 配置文件 < 命令行 flag。
// 未指定 -c 时,若 ./tunnel.toml 存在则自动加载。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/crypto/ssh"

	"remote-agent/tunnel"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "server":
		err = cmdServer(os.Args[2:])
	case "client":
		err = cmdClient(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Printf("tunnel: %v", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `用法:
  tunnel server [--local-ssh-port 22]   [--remote-listen-port 5102] [公共参数...]
  tunnel client [--local-ssh-port 5102] [--remote-listen-port 5102] [公共参数...]

公共参数:
  -c, --config 路径        TOML 配置文件(默认自动读取 ./tunnel.toml)
  --remote-ip IP           VPS 地址(必填,可在配置文件中给出)
  --remote-ssh-port N      VPS sshd 端口(默认 22)
  --remote-ssh-pub 公钥    VPS 主机公钥(钉住;.pub 文件路径或内联 authorized_keys 内容,必填)
  --local-ssh-key 路径     本机私钥(默认 ~/.ssh/id_ed25519)
  --user 用户              VPS 登录用户(默认 $USER)

参数优先级:默认值 < 配置文件 < 命令行 flag`)
}

// findConfigPath 预扫描参数里的 -c/--config(支持分离与 = 两种写法)。
// 未指定且 ./tunnel.toml 存在时返回该默认路径;都没有返回空串。
func findConfigPath(args []string) string {
	for i, a := range args {
		if (a == "-c" || a == "--config") && i+1 < len(args) {
			return args[i+1]
		}
		if v, ok := strings.CutPrefix(a, "-c="); ok {
			return v
		}
		if v, ok := strings.CutPrefix(a, "--config="); ok {
			return v
		}
	}
	if _, err := os.Stat("tunnel.toml"); err == nil {
		return "tunnel.toml"
	}
	return ""
}

// loadFileConfig 加载 TOML 配置;path 为空返回全零配置(纯 flag 模式)。
func loadFileConfig(path string) *tunnel.FileConfig {
	if path == "" {
		return &tunnel.FileConfig{}
	}
	fc, err := tunnel.LoadTOML(path)
	if err != nil {
		log.Fatalf("tunnel: %v", err)
	}
	return fc
}

// common 承载公共 flag;默认值来自配置文件(再往下才是内置默认)。
type common struct {
	remoteIP      *string
	remoteSSHPort *int
	remoteSSHPub  *string
	localSSHKey   *string
	user          *string
}

func (c *common) register(fs *flag.FlagSet, fc *tunnel.FileConfig) {
	home, _ := os.UserHomeDir()
	defaultKey := fc.LocalSSHKey
	if defaultKey == "" {
		defaultKey = filepath.Join(home, ".ssh", "id_ed25519")
	}
	defaultSSHPort := fc.RemoteSSHPort
	if defaultSSHPort == 0 {
		defaultSSHPort = 22
	}
	defaultUser := fc.User
	if defaultUser == "" {
		defaultUser = os.Getenv("USER")
	}
	c.remoteIP = fs.String("remote-ip", fc.RemoteIP, "VPS 地址(必填)")
	c.remoteSSHPort = fs.Int("remote-ssh-port", defaultSSHPort, "VPS sshd 端口")
	c.remoteSSHPub = fs.String("remote-ssh-pub", fc.RemoteSSHPub, "VPS 主机公钥(.pub 路径或内联,必填)")
	c.localSSHKey = fs.String("local-ssh-key", defaultKey, "本机私钥路径")
	c.user = fs.String("user", defaultUser, "VPS 登录用户")
}

func (c *common) config() (*tunnel.Config, error) {
	if *c.remoteIP == "" {
		return nil, fmt.Errorf("缺少 --remote-ip(或配置文件 remote_ip)")
	}
	if *c.remoteSSHPub == "" {
		return nil, fmt.Errorf("缺少 --remote-ssh-pub(或配置文件 remote_ssh_pub)")
	}
	return &tunnel.Config{
		SSHAddr: fmt.Sprintf("%s:%d", *c.remoteIP, *c.remoteSSHPort),
		User:    *c.user,
		KeyPath: *c.localSSHKey,
		HostPub: *c.remoteSSHPub,
	}, nil
}

func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

// configFlag 把 -c/--config 注册进 FlagSet(仅用于通过解析,值已在预扫描阶段消费)。
func configFlag(fs *flag.FlagSet) {
	fs.String("c", "", "TOML 配置文件")
	fs.String("config", "", "TOML 配置文件")
}

func cmdServer(args []string) error {
	fc := loadFileConfig(findConfigPath(args))
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	lspDefault, rlpDefault := 22, 5102
	if fc.LocalSSHPort != 0 {
		lspDefault = fc.LocalSSHPort
	}
	if fc.RemoteListenPort != 0 {
		rlpDefault = fc.RemoteListenPort
	}
	lsp := fs.Int("local-ssh-port", lspDefault, "内网 sshd 端口")
	rlp := fs.Int("remote-listen-port", rlpDefault, "VPS loopback 监听端口")
	var cm common
	cm.register(fs, fc)
	configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := cm.config()
	if err != nil {
		return err
	}
	local := fmt.Sprintf("127.0.0.1:%d", *lsp)
	c := tunnel.NewClient(cfg, func(sc *ssh.Client) (net.Listener, error) {
		l, err := tunnel.ReverseForward(sc, "127.0.0.1", *rlp, local)
		if err != nil {
			return nil, err
		}
		go func() {
			for {
				remote, err := l.Accept()
				if err != nil {
					return
				}
				go tunnel.ProxyToLocal(remote, local)
			}
		}()
		return l, nil
	})
	ctx, stop := signalCtx()
	defer stop()
	return c.Run(ctx)
}

func cmdClient(args []string) error {
	fc := loadFileConfig(findConfigPath(args))
	fs := flag.NewFlagSet("client", flag.ContinueOnError)
	lspDefault, rlpDefault := 5102, 5102
	if fc.LocalSSHPort != 0 {
		lspDefault = fc.LocalSSHPort
	}
	if fc.RemoteListenPort != 0 {
		rlpDefault = fc.RemoteListenPort
	}
	lsp := fs.Int("local-ssh-port", lspDefault, "本机监听端口")
	rlp := fs.Int("remote-listen-port", rlpDefault, "VPS loopback 汇合端口")
	var cm common
	cm.register(fs, fc)
	configFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := cm.config()
	if err != nil {
		return err
	}
	target := fmt.Sprintf("127.0.0.1:%d", *rlp)
	c := tunnel.NewClient(cfg, func(sc *ssh.Client) (net.Listener, error) {
		return tunnel.LocalForward(sc, fmt.Sprintf("127.0.0.1:%d", *lsp), target)
	})
	ctx, stop := signalCtx()
	defer stop()
	return c.Run(ctx)
}
