// tunnel CLI:库包 remote-agent/tunnel 的薄壳。
//
// 子命令:
//
//	tunnel server  内网侧守护:反向转发(VPS loopback:PORT → 内网 sshd)
//	tunnel client  客户端守护:正向转发(本机 LISTEN → VPS loopback:PORT)
//
// 参数优先级:内置默认值 < TOML 配置文件 < 命令行 flag。
//
// 架构说明:server/client 两个子命令在 SSH 协议层面都是"客户端"
// (都主动向 VPS 发起连接),区别只在转发方向。因此二者复用同一个
// tunnel.Client 监督循环,方向差异通过 establish 函数注入 ——
// 详见 cmdServer/cmdClient 中各自的闭包。
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
//
// 为什么要预扫描:标准库 flag 的默认值必须在注册时给出,
// 而"配置文件充当中间层默认值"要求先知道配置文件路径才能注册 flag。
// 所以流程是两阶段:先在原始 args 里把 -c 挑出来 → 加载 TOML →
// 用 TOML 值注册 flag 默认值 → 再正常 Parse(flag 值自然覆盖 TOML)。
// 未指定 -c 且 ./tunnel.toml 存在时,自动使用该默认路径。
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
// 显式指定了 -c 但读不到属于用法错误,立即终止(不静默忽略)。
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

// register 把公共 flag 注册进 fs,默认值依次取:flag > TOML > 内置。
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

// config 把合并后的参数组装成库层 Config,并做必填校验。
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

// signalCtx 把 SIGINT/SIGTERM 转化为 ctx 取消;
// Client.Run 检测到取消后会主动断开 SSH 连接并退出。
func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

// configFlag 把 -c/--config 注册进 FlagSet,使其通过 Parse 校验。
// 值本身已在预扫描阶段(findConfigPath)消费,这里只为"占位防报错"。
func configFlag(fs *flag.FlagSet) {
	fs.String("c", "", "TOML 配置文件")
	fs.String("config", "", "TOML 配置文件")
}

// cmdServer 内网侧守护:VPS loopback:PORT → 内网 sshd(反向转发)。
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

	// establish 闭包(反向转发策略):每条 SSH 连接建立后,
	// 请求 VPS sshd 在 loopback:PORT 监听,每条外部连接代理到内网 sshd。
	// 重连时监督循环重新调用本闭包,转发随新连接复活。
	c := tunnel.NewClient(cfg, func(sc *ssh.Client) (net.Listener, error) {
		l, err := tunnel.ReverseForward(sc, "127.0.0.1", *rlp, local)
		if err != nil {
			return nil, err
		}
		go func() {
			for {
				remote, err := l.Accept()
				if err != nil {
					return // 监听器随连接断开失效,重连由监督循环负责
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

// cmdClient 客户端守护:本机 LISTEN → VPS loopback:PORT(正向转发)。
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

	// establish 闭包(正向转发策略):本机监听,每条连接经
	// direct-tcpip 通道请 VPS 代连汇合端口。LocalForward 内部
	// 自带 accept/代理循环,无需外层再 Accept。
	c := tunnel.NewClient(cfg, func(sc *ssh.Client) (net.Listener, error) {
		return tunnel.LocalForward(sc, fmt.Sprintf("127.0.0.1:%d", *lsp), target)
	})
	ctx, stop := signalCtx()
	defer stop()
	return c.Run(ctx)
}
