// tunnel CLI:库包 tunnel 的薄壳。
//   tunnel server [--local-ssh-port 22]   [--remote-listen-port 5102] [公共参数...]
//   tunnel client [--local-ssh-port 5102] [--remote-listen-port 5102] [公共参数...]
// 公共参数:--remote-ip / --remote-ssh-port / --remote-ssh-pub / --local-ssh-key / --user
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
	"syscall"

	"golang.org/x/crypto/ssh"

	"tunnel"
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
  --remote-ip IP           VPS 地址(必填)
  --remote-ssh-port N      VPS sshd 端口(默认 22)
  --remote-ssh-pub 公钥    VPS 主机公钥(钉住;.pub 文件路径或内联 authorized_keys 内容,必填)
  --local-ssh-key 路径     本机私钥(默认 ~/.ssh/id_ed25519)
  --user 用户              VPS 登录用户(默认 $USER)`)
}

// common 承载公共 flag 并组装 tunnel.Config。
type common struct {
	remoteIP      *string
	remoteSSHPort *int
	remoteSSHPub  *string
	localSSHKey   *string
	user          *string
}

func (c *common) register(fs *flag.FlagSet) {
	home, _ := os.UserHomeDir()
	c.remoteIP = fs.String("remote-ip", "", "VPS 地址(必填)")
	c.remoteSSHPort = fs.Int("remote-ssh-port", 22, "VPS sshd 端口")
	c.remoteSSHPub = fs.String("remote-ssh-pub", "", "VPS 主机公钥(.pub 路径或内联,必填)")
	c.localSSHKey = fs.String("local-ssh-key", filepath.Join(home, ".ssh", "id_ed25519"), "本机私钥路径")
	c.user = fs.String("user", os.Getenv("USER"), "VPS 登录用户")
}

func (c *common) config() (*tunnel.Config, error) {
	if *c.remoteIP == "" {
		return nil, fmt.Errorf("缺少 --remote-ip")
	}
	if *c.remoteSSHPub == "" {
		return nil, fmt.Errorf("缺少 --remote-ssh-pub")
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

func cmdServer(args []string) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	lsp := fs.Int("local-ssh-port", 22, "内网 sshd 端口")
	rlp := fs.Int("remote-listen-port", 5102, "VPS loopback 监听端口")
	var cm common
	cm.register(fs)
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
	fs := flag.NewFlagSet("client", flag.ContinueOnError)
	lsp := fs.Int("local-ssh-port", 5102, "本机监听端口")
	rlp := fs.Int("remote-listen-port", 5102, "VPS loopback 汇合端口")
	var cm common
	cm.register(fs)
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
