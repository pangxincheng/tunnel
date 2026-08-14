// tunnel CLI:库包 remote-agent/tunnel 的薄壳(urfave/cli 实现)。
//
// 子命令:
//
//	tunnel server  内网侧守护:反向转发(VPS loopback:PORT → 内网 sshd)
//	tunnel client  客户端守护:正向转发(本机 LISTEN → VPS loopback:PORT)
//	tunnel version 打印版本
//
// 参数优先级:内置默认值 < TOML 配置文件 < 命令行 flag。
// 实现说明:-c/--config 需要在构建 flag 默认值之前知道(预扫描一次参数),
// 之后 TOML 值作为 flag 的默认注册,flag 显式给出时自然覆盖。
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/urfave/cli/v3"

	"remote-agent/tunnel"
)

func main() {
	cmd := &cli.Command{
		Name:  "tunnel",
		Usage: "通用 SSH 端口转发工具(反向/正向转发,断线自动重连)",
		Commands: []*cli.Command{
			serverCmd(),
			clientCmd(),
			versionCmd(),
		},
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Printf("tunnel: %v", err)
		os.Exit(1)
	}
}

// findConfigPath 预扫描参数里的 -c/--config(分离与 = 两种写法);
// 未指定且 ./tunnel.toml 存在时自动使用。
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

// loadFileConfig 加载 TOML;path 为空返回全零配置(纯 flag 模式)。
// 显式指定了 -c 但读不到属用法错误,立即终止。
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

// commonFlags 生成 server/client 共用的公共 flag 列表,默认值来自配置文件。
func commonFlags(fc *tunnel.FileConfig) []cli.Flag {
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
	return []cli.Flag{
		&cli.StringFlag{Name: "config", Aliases: []string{"c"}, Usage: "TOML 配置文件(默认 ./tunnel.toml)"},
		&cli.StringFlag{Name: "remote-ip", Value: fc.RemoteIP, Usage: "VPS 地址(必填)"},
		&cli.IntFlag{Name: "remote-ssh-port", Value: defaultSSHPort, Usage: "VPS sshd 端口"},
		&cli.StringFlag{Name: "remote-ssh-pub", Value: fc.RemoteSSHPub, Usage: "VPS 主机公钥(.pub 路径或内联,必填)"},
		&cli.StringFlag{Name: "local-ssh-key", Value: defaultKey, Usage: "本机私钥路径"},
		&cli.StringFlag{Name: "user", Value: defaultUser, Usage: "VPS 登录用户"},
	}
}

// portFlags 生成两个端口 flag,默认值随子命令角色与配置文件。
func portFlags(fc *tunnel.FileConfig, defaultLocal int) []cli.Flag {
	lsp, rlp := defaultLocal, 5102
	if fc.LocalSSHPort != 0 {
		lsp = fc.LocalSSHPort
	}
	if fc.RemoteListenPort != 0 {
		rlp = fc.RemoteListenPort
	}
	return []cli.Flag{
		&cli.IntFlag{Name: "local-ssh-port", Value: lsp, Usage: "本地端口(server: 内网 sshd;client: 本机监听)"},
		&cli.IntFlag{Name: "remote-listen-port", Value: rlp, Usage: "VPS loopback 汇合端口"},
	}
}

// buildConfig 把合并后的参数组装为库层 Config 并校验必填。
func buildConfig(cmd *cli.Command) (*tunnel.Config, error) {
	if cmd.String("remote-ip") == "" {
		return nil, fmt.Errorf("缺少 --remote-ip(或配置文件 remote_ip)")
	}
	if cmd.String("remote-ssh-pub") == "" {
		return nil, fmt.Errorf("缺少 --remote-ssh-pub(或配置文件 remote_ssh_pub)")
	}
	return &tunnel.Config{
		SSHAddr: fmt.Sprintf("%s:%d", cmd.String("remote-ip"), cmd.Int("remote-ssh-port")),
		User:    cmd.String("user"),
		KeyPath: cmd.String("local-ssh-key"),
		HostPub: cmd.String("remote-ssh-pub"),
	}, nil
}

// run 把库层守护循环接上信号取消:SIGINT/SIGTERM 时优雅退出。
func run(c *tunnel.Client) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return c.Run(ctx)
}

func serverCmd() *cli.Command {
	fc := loadFileConfig(findConfigPath(os.Args[2:]))
	return &cli.Command{
		Name:  "server",
		Usage: "内网侧守护:反向转发(VPS loopback:PORT → 内网 sshd)",
		Flags: append(commonFlags(fc), portFlags(fc, 22)...),
		Action: func(_ context.Context, cmd *cli.Command) error {
			cfg, err := buildConfig(cmd)
			if err != nil {
				return err
			}
			// 反向:VPS loopback:remote-listen-port → 内网 local-ssh-port
			remote := fmt.Sprintf("127.0.0.1:%d", cmd.Int("remote-listen-port"))
			local := fmt.Sprintf("127.0.0.1:%d", cmd.Int("local-ssh-port"))
			return run(tunnel.NewClient(cfg, tunnel.ReverseForward(remote, local)))
		},
	}
}

func clientCmd() *cli.Command {
	fc := loadFileConfig(findConfigPath(os.Args[2:]))
	return &cli.Command{
		Name:  "client",
		Usage: "客户端守护:正向转发(本机 LISTEN → VPS loopback:PORT)",
		Flags: append(commonFlags(fc), portFlags(fc, 5102)...),
		Action: func(_ context.Context, cmd *cli.Command) error {
			cfg, err := buildConfig(cmd)
			if err != nil {
				return err
			}
			// 正向:本机 local-ssh-port → VPS loopback:remote-listen-port
			listen := fmt.Sprintf("127.0.0.1:%d", cmd.Int("local-ssh-port"))
			target := fmt.Sprintf("127.0.0.1:%d", cmd.Int("remote-listen-port"))
			return run(tunnel.NewClient(cfg, tunnel.LocalForward(listen, target)))
		},
	}
}

func versionCmd() *cli.Command {
	return &cli.Command{
		Name:  "version",
		Usage: "打印版本",
		Action: func(_ context.Context, _ *cli.Command) error {
			fmt.Printf("tunnel %s\n", tunnel.Version)
			return nil
		},
	}
}
