// tunnel CLI:库包 github.com/pangxincheng/tunnel/tunnel 的薄壳(docopt 实现,与 pTunnel 同款)。
//
// 子命令:
//
//	tunnel server  内网侧守护:反向转发(VPS loopback:PORT → 内网 sshd)
//	tunnel client  客户端守护:正向转发(本机 LISTEN → VPS loopback:PORT)
//	tunnel version 打印版本
//
// 参数解析采用 docopt:usage 文本即语法定义,ParseArgs 产出 map;
// 合并规则(与 pTunnel 一致):flag 已给出(map 值非 nil)则优先,
// 否则回落到 TOML 配置文件,再回落到内置默认值。
// 日志走 slog:启动时打印配置来源与转发计划,失败时给出可执行的排查提示。
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/docopt/docopt-go"

	"github.com/pangxincheng/tunnel/tunnel"
)

const (
	defaultRemoteSSHPort = 22 // VPS sshd 端口
	defaultInnerSSHPort  = 22 // server: 内网 sshd 端口
)

const usageText = `tunnel —— 通用 SSH 端口转发工具(反向/正向转发,断线自动重连)

Usage:
  tunnel server [options]
  tunnel client [options]
  tunnel version

Options:
  -h --help                帮助
  -c --config=PATH         TOML 配置文件(默认自动读取 ./tunnel.toml)
  --remote-ip=IP           VPS 地址(必填,可在配置文件中给出)
  --remote-ssh-port=N      VPS sshd 端口(默认 22)
  --remote-ssh-pub=PUB     VPS 主机公钥(钉住;.pub 文件路径或内联 authorized_keys 内容,必填)
  --local-ssh-key=PATH     本机私钥(默认 ~/.ssh/id_ed25519)
  --user=USER              VPS 登录用户(默认 $USER)
  --local-ssh-port=N       本地端口(server: 内网 sshd,默认 22;client: 本机监听,默认跟随汇合端口)
  --remote-listen-port=N   VPS loopback 汇合端口(必填)
`

func main() {
	args, err := docopt.ParseArgs(usageText, os.Args[1:], "tunnel "+tunnel.Version)
	if err != nil {
		os.Exit(2) // docopt 已打印用法或错误信息
	}

	logger := newLogger()
	switch {
	case args["version"].(bool):
		fmt.Printf("tunnel %s\n", tunnel.Version)
	case args["server"].(bool):
		runCmd(args, "server", logger)
	case args["client"].(bool):
		runCmd(args, "client", logger)
	}
}

// newLogger 构造 CLI 日志器;时间格式与旧 log 包一致,便于沿用现有日志习惯。
func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.String(slog.TimeKey, a.Value.Time().Format("2006/01/02 15:04:05"))
			}
			return a
		},
	}))
}

// runCmd 完成 server/client 的合并、校验与启动;出错即记录并退出。
func runCmd(args map[string]interface{}, role string, logger *slog.Logger) {
	path, source := configPath(args)
	fc, err := loadFileConfig(path)
	if err != nil {
		fatal(logger, err)
	}
	p, err := merge(args, fc, role)
	if err != nil {
		fatal(logger, err)
	}
	cfg, err := p.config()
	if err != nil {
		fatal(logger, err)
	}

	remote := fmt.Sprintf("127.0.0.1:%d", p.remoteListenPort)
	local := fmt.Sprintf("127.0.0.1:%d", p.localSSHPort)

	logger.Info("tunnel 启动", "version", tunnel.Version, "role", role)
	if path != "" {
		logger.Info("配置来源", "source", source, "file", path)
	} else {
		logger.Info("配置来源", "source", source)
	}
	logger.Info("SSH 目标", "addr", cfg.SSHAddr, "user", cfg.User, "key", cfg.KeyPath)
	if role == "server" {
		logger.Info("反向转发计划", "vps_listen", remote, "inner_target", local)
		err = run(tunnel.NewClient(cfg, tunnel.ReverseForward(remote, local), tunnel.WithLogger(logger)))
	} else {
		logger.Info("正向转发计划", "local_listen", local, "vps_target", remote)
		err = run(tunnel.NewClient(cfg, tunnel.LocalForward(local, remote), tunnel.WithLogger(logger)))
	}
	if err != nil {
		fatal(logger, err)
	}
	logger.Info("tunnel 已停止")
}

// params 是合并后的全部参数。
type params struct {
	remoteIP         string
	remoteSSHPort    int
	remoteSSHPub     string
	localSSHKey      string
	user             string
	localSSHPort     int
	remoteListenPort int
}

func (p *params) config() (*tunnel.Config, error) {
	if p.remoteIP == "" {
		return nil, fmt.Errorf("缺少 --remote-ip(或配置文件 remote_ip)")
	}
	if p.remoteSSHPub == "" {
		return nil, fmt.Errorf("缺少 --remote-ssh-pub(或配置文件 remote_ssh_pub)")
	}
	if p.remoteListenPort == 0 {
		return nil, fmt.Errorf("缺少 --remote-listen-port(或配置文件 remote_listen_port)")
	}
	return &tunnel.Config{
		SSHAddr: net.JoinHostPort(p.remoteIP, strconv.Itoa(p.remoteSSHPort)),
		User:    p.user,
		KeyPath: p.localSSHKey,
		HostPub: p.remoteSSHPub,
	}, nil
}

// merge 按 docopt map → TOML → 内置默认的优先级合并参数。
// role 决定 local-ssh-port 的最终兜底:server 为 22,client 跟随汇合端口。
//
// 注意:usage 里的选项不能再写 [default: N] —— docopt 会把该默认值当成
// "用户显式给出",导致 TOML 中的同名端口被静默覆盖。默认值统一在这里兜底。
func merge(args map[string]interface{}, fc *tunnel.FileConfig, role string) (*params, error) {
	home, _ := os.UserHomeDir()

	remoteSSHPort, err := intArg(args, "--remote-ssh-port", orDefaultInt(fc.RemoteSSHPort, defaultRemoteSSHPort))
	if err != nil {
		return nil, err
	}
	localSSHPort, err := intArg(args, "--local-ssh-port", fc.LocalSSHPort)
	if err != nil {
		return nil, err
	}
	remoteListenPort, err := intArg(args, "--remote-listen-port", fc.RemoteListenPort)
	if err != nil {
		return nil, err
	}

	p := &params{
		remoteIP:         strArg(args, "--remote-ip", fc.RemoteIP),
		remoteSSHPort:    remoteSSHPort,
		remoteSSHPub:     strArg(args, "--remote-ssh-pub", fc.RemoteSSHPub),
		localSSHKey:      strArg(args, "--local-ssh-key", orDefaultStr(fc.LocalSSHKey, filepath.Join(home, ".ssh", "id_ed25519"))),
		user:             strArg(args, "--user", orDefaultStr(fc.User, os.Getenv("USER"))),
		localSSHPort:     localSSHPort,
		remoteListenPort: remoteListenPort,
	}
	// local-ssh-key 的 ~ 展开(docopt 默认值不会展开)
	p.localSSHKey = expandHome(p.localSSHKey)

	if p.localSSHPort == 0 {
		if role == "server" {
			p.localSSHPort = defaultInnerSSHPort
		} else {
			p.localSSHPort = p.remoteListenPort
		}
	}
	return p, nil
}

// strArg/intArg:docopt 已给出(非 nil/非空)则用之,否则回落 fallback。
// docopt 中未出现在命令行里的选项在 map 中为 nil。
func strArg(args map[string]interface{}, key, fallback string) string {
	if v, ok := args[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

func intArg(args map[string]interface{}, key string, fallback int) (int, error) {
	if v, ok := args[key].(string); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("参数 %s 不是合法数字: %q", key, v)
		}
		return n, nil
	}
	return fallback, nil
}

// configPath 取 -c/--config;未指定且 ./tunnel.toml 存在时自动使用。
// 同时返回来源说明,启动日志用它回答"配置到底从哪来的"。
func configPath(args map[string]interface{}) (path, source string) {
	if v := strArg(args, "--config", ""); v != "" {
		return v, "命令行 -c"
	}
	if _, err := os.Stat("tunnel.toml"); err == nil {
		return "tunnel.toml", "自动发现 ./tunnel.toml"
	}
	return "", "未使用配置文件(命令行参数/内置默认值)"
}

// loadFileConfig 加载 TOML;path 为空返回全零配置(纯 flag 模式)。
// 显式指定了 -c 但读不到属用法错误,返回错误由调用方终止。
func loadFileConfig(path string) (*tunnel.FileConfig, error) {
	if path == "" {
		return &tunnel.FileConfig{}, nil
	}
	return tunnel.LoadTOML(path)
}

func orDefaultStr(v, def string) string {
	if v != "" {
		return v
	}
	return def
}

func orDefaultInt(v, def int) int {
	if v != 0 {
		return v
	}
	return def
}

func expandHome(p string) string {
	if p == "~" {
		home, _ := os.UserHomeDir()
		return home
	}
	if len(p) > 1 && p[:2] == "~/" {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

// fatal 记录错误并以非零码退出:slog 没有 Fatal,退出码由 CLI 自己决定。
func fatal(logger *slog.Logger, err error) {
	logger.Error("tunnel 退出", "err", err)
	os.Exit(1)
}

// run 把库层守护循环接上信号取消:SIGINT/SIGTERM 时优雅退出。
func run(c *tunnel.Client) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return c.Run(ctx)
}
