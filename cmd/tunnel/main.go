// tunnel CLI:库包 github.com/pangxincheng/tunnel/tunnel 的薄壳(docopt 实现,与 pTunnel 同款)。
//
// 子命令:
//
//	tunnel server  内网侧守护:反向转发(VPS loopback:PORT 或 VPS socket NAME.sock → 内网 sshd)
//	tunnel client  客户端守护:正向转发(本机 LISTEN → VPS loopback:PORT 或 VPS socket NAME.sock)
//	tunnel install / uninstall    以系统服务方式开机自启或登录后运行(按名字共享本机 sshd)
//	tunnel status / pause / resume / retry  经本机控制通道查看或操作已安装的服务
//	tunnel run-service            服务进程入口(由服务管理器启动)
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
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/docopt/docopt-go"

	"github.com/pangxincheng/tunnel/internal/svc"
	"github.com/pangxincheng/tunnel/tunnel"
)

const (
	defaultRemoteSSHPort = 22 // VPS sshd 端口
	defaultInnerSSHPort  = 22 // server: 内网 sshd 端口
)

const usageText = `tunnel —— 通用 SSH 端口转发工具(反向/正向转发,断线自动重连)

Usage:
  tunnel server [options] [--name=NAME]
  tunnel client [options] [--name=NAME]
  tunnel install --mode=MODE [--profile=P] [--name=NAME] [--for-user=USER] [options]
  tunnel uninstall [--mode=MODE] [--profile=P]
  tunnel status [--mode=MODE] [--profile=P] [--json]
  tunnel (pause|resume|retry) [--mode=MODE] [--profile=P]
  tunnel run-service --mode=MODE --profile=P
  tunnel version

Options:
  -h --help                帮助
  -c --config=PATH         TOML 配置文件(默认自动读取 ./tunnel.toml)
  --log-level=LEVEL        日志级别:debug/info/warn/error(默认 info)
  --remote-ip=IP           VPS 地址(必填,可在配置文件中给出)
  --remote-ssh-port=N      VPS sshd 端口(默认 22)
  --remote-ssh-pub=PUB     VPS 主机公钥(钉住;.pub 文件路径或内联 authorized_keys 内容,必填)
  --local-ssh-key=PATH     本机私钥(默认 ~/.ssh/id_ed25519)
  --user=USER              VPS 登录用户(默认 $USER)
  --local-ssh-port=N       本地端口(server: 内网 sshd,默认 22;client: 本机监听,默认跟随汇合端口)
  --remote-listen-port=N   VPS loopback 汇合端口(与 --name 二选一;server 传 0 = 由 VPS 自动分配,实际端口见启动日志)
  --name=NAME              按名字转发:VPS 上 <socket目录>/NAME.sock(与 --remote-listen-port 二选一)
  --remote-socket-dir=DIR  VPS 上的 socket 目录(绝对路径;默认连接后执行 mkdir 解析 $HOME/.tunnel/sockets)
  --mode=MODE              服务模式:system(开机即运行,需 root,Windows 不支持)/ user(登录后运行)
  --profile=P              服务器 profile(默认 default 或配置文件 profile;服务名 tunnel-<profile>,每个 profile 一个服务)
  --for-user=USER          install --mode system:服务属于哪个用户(默认 SUDO_USER,否则当前用户)
  --json                   status 以 JSON 输出

服务命令(install 等)只支持按名字转发,共享本机 127.0.0.1:<local-ssh-port>;
--name 是设备名字,只保存在服务配置中;同一 profile 再次 install 即更新(可改名字)。
其余服务命令按 --profile(默认 default)定位服务,--mode 缺省时自动查找。status 退出码 3 = 已安装但未运行。
`

func main() {
	args, err := docopt.ParseArgs(usageText, os.Args[1:], "tunnel "+tunnel.Version)
	if err != nil {
		os.Exit(2) // docopt 已打印用法或错误信息
	}

	logger, err := newLogger(strArg(args, "--log-level", "info"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "tunnel: %v\n", err)
		os.Exit(2) // 与 docopt 的用法错误保持一致
	}
	switch {
	case boolArg(args, "version"):
		fmt.Printf("tunnel %s\n", tunnel.Version)
	case boolArg(args, "server"):
		runCmd(args, "server", logger)
	case boolArg(args, "client"):
		runCmd(args, "client", logger)
	case boolArg(args, "install"):
		reexecOrExit(installFlow(defaultEnv(logger), args, os.Args[1:]))
	case boolArg(args, "uninstall"):
		reexecOrExit(uninstallFlow(defaultEnv(logger), args, os.Args[1:]))
	case boolArg(args, "status"):
		os.Exit(statusCmd(defaultEnv(logger), args))
	case boolArg(args, "pause"):
		os.Exit(controlCmd(defaultEnv(logger), args, "pause"))
	case boolArg(args, "resume"):
		os.Exit(controlCmd(defaultEnv(logger), args, "resume"))
	case boolArg(args, "retry"):
		os.Exit(controlCmd(defaultEnv(logger), args, "retry"))
	case boolArg(args, "run-service"):
		runServiceMain(args)
	}
}

// reexecOrExit 处理 install / uninstall 的结果:需要提权时以 sudo 替换当前进程,出错退出码 1。
func reexecOrExit(reexec []string, err error) {
	if err == nil && reexec != nil {
		fmt.Fprintln(os.Stderr, "需要 root 权限,正在通过 sudo 重新执行……")
		err = execReplace(reexec) // 成功时不返回
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "tunnel: %v\n", err)
		os.Exit(1)
	}
}

// runServiceMain 是服务进程入口:Windows 先释放控制台并改写日志文件,随后运行到 SIGINT/SIGTERM。
func runServiceMain(args map[string]interface{}) {
	detachConsole()
	mode, err := parseMode(strArg(args, "--mode", ""))
	if err != nil {
		fmt.Fprintf(os.Stderr, "tunnel: %v\n", err)
		os.Exit(2)
	}
	env := defaultEnv(nil)
	profile := strArg(args, "--profile", "")
	out, closeOut, err := serviceLogOutput(env.paths(mode, svc.ServiceName(profile)).LogPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tunnel: 打开日志文件失败: %v\n", err)
		os.Exit(1)
	}
	defer closeOut()
	env.logger, _ = newLoggerTo(out, "info")
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	err = runService(ctx, env, mode, profile)
	stop()
	if err != nil {
		env.logger.Error("共享服务退出", "err", err)
		closeOut()
		os.Exit(1)
	}
}

// newLogger 构造 CLI 日志器;level 接受 debug/info/warn/error(slog 原生解析)。
// 时间格式与旧 log 包一致,便于沿用现有日志习惯。
func newLogger(levelName string) (*slog.Logger, error) {
	return newLoggerTo(os.Stderr, levelName)
}

// newLoggerTo 同 newLogger,输出到 w。
func newLoggerTo(w io.Writer, levelName string) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(levelName)); err != nil {
		return nil, fmt.Errorf("无效的 --log-level %q(可选 debug/info/warn/error)", levelName)
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.String(slog.TimeKey, a.Value.Time().Format("2006/01/02 15:04:05"))
			}
			return a
		},
	})), nil
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
	cfg, err := p.config(role)
	if err != nil {
		fatal(logger, err)
	}

	local := fmt.Sprintf("127.0.0.1:%d", p.localSSHPort)

	logger.Info("tunnel 启动", "version", tunnel.Version, "role", role)
	if path != "" {
		logger.Info("配置来源", "source", source, "file", path)
	} else {
		logger.Info("配置来源", "source", source)
	}
	logger.Info("SSH 目标", "addr", cfg.SSHAddr, "user", cfg.User, "key", cfg.KeyPath)
	if p.name != "" {
		err = runNameMode(p, cfg, role, local, logger)
	} else {
		err = runPortMode(p, cfg, role, local, logger)
	}
	if err != nil {
		fatal(logger, err)
	}
	logger.Info("tunnel 已停止")
}

// runNameMode 按名字转发:汇合点是 VPS 上的 <socket目录>/<name>.sock。
func runNameMode(p *params, cfg *tunnel.Config, role, local string, logger *slog.Logger) error {
	sock := path.Join(p.remoteSocketDir, p.name+".sock")
	if p.remoteSocketDir == "" {
		sock = "$HOME/.tunnel/sockets/" + p.name + ".sock(连接后解析)"
	}
	fl := tunnel.WithForwardLogger(logger)
	if role == "server" {
		logger.Info("反向转发计划", "name", p.name, "vps_socket", sock, "inner_target", local)
		return run(tunnel.NewClient(cfg, tunnel.ReverseForwardUnix(p.remoteSocketDir, p.name, local, fl), tunnel.WithLogger(logger)))
	}
	logger.Info("正向转发计划", "name", p.name, "local_listen", local, "vps_socket", sock)
	return run(tunnel.NewClient(cfg, tunnel.LocalForwardUnix(local, p.remoteSocketDir, p.name, fl), tunnel.WithLogger(logger)))
}

// runPortMode 按端口转发:汇合点是 VPS loopback 上的 TCP 端口。
func runPortMode(p *params, cfg *tunnel.Config, role, local string, logger *slog.Logger) error {
	var err error
	remote := fmt.Sprintf("127.0.0.1:%d", p.remoteListenPort)
	vpsListen := remote
	if p.remoteListenPort == 0 {
		vpsListen = "自动分配(VPS 选择)"
	}
	if role == "server" {
		logger.Info("反向转发计划", "vps_listen", vpsListen, "inner_target", local)
		if p.remoteListenPort == 0 {
			logger.Info("已启用 VPS 自动分配端口:实际端口见「隧道就绪」日志;重连后会重新分配,需要固定端口时请改用具体值")
		}
		err = run(tunnel.NewClient(cfg, tunnel.ReverseForward(remote, local, tunnel.WithForwardLogger(logger)), tunnel.WithLogger(logger)))
	} else {
		logger.Info("正向转发计划", "local_listen", local, "vps_target", remote)
		err = run(tunnel.NewClient(cfg, tunnel.LocalForward(local, remote, tunnel.WithForwardLogger(logger)), tunnel.WithLogger(logger)))
	}
	return err
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
	// remoteListenPortSet 区分"未配置"(false)与"显式 0"(true,server 自动分配)。
	remoteListenPortSet bool
	// name 非空 = 名字模式(与端口模式互斥,二者由 merge 选定其一)。
	name            string
	remoteSocketDir string
}

func (p *params) config(role string) (*tunnel.Config, error) {
	if p.remoteIP == "" {
		return nil, fmt.Errorf("缺少 --remote-ip(或配置文件 remote_ip)")
	}
	if p.remoteSSHPub == "" {
		return nil, fmt.Errorf("缺少 --remote-ssh-pub(或配置文件 remote_ssh_pub)")
	}
	if p.name != "" {
		if err := p.checkNameMode(role); err != nil {
			return nil, err
		}
	} else if err := p.checkPortMode(role); err != nil {
		return nil, err
	}
	if p.remoteSSHPort < 1 || p.remoteSSHPort > 65535 {
		return nil, fmt.Errorf("--remote-ssh-port 超出范围(1-65535):%d", p.remoteSSHPort)
	}
	return &tunnel.Config{
		SSHAddr: net.JoinHostPort(p.remoteIP, strconv.Itoa(p.remoteSSHPort)),
		User:    p.user,
		KeyPath: p.localSSHKey,
		HostPub: p.remoteSSHPub,
	}, nil
}

func (p *params) checkNameMode(role string) error {
	if err := tunnel.ValidateName(p.name); err != nil {
		return err
	}
	if p.remoteSocketDir != "" && !strings.HasPrefix(p.remoteSocketDir, "/") {
		return fmt.Errorf("--remote-socket-dir 必须是绝对路径:%q", p.remoteSocketDir)
	}
	if role == "client" && p.localSSHPort == 0 {
		return fmt.Errorf("名字模式下 client 必须指定 --local-ssh-port(本机监听端口)")
	}
	if p.localSSHPort < 1 || p.localSSHPort > 65535 {
		return fmt.Errorf("--local-ssh-port 超出范围(1-65535):%d", p.localSSHPort)
	}
	return nil
}

func (p *params) checkPortMode(role string) error {
	if !p.remoteListenPortSet {
		return fmt.Errorf("缺少 --name 或 --remote-listen-port(或配置文件 name / remote_listen_port;server 传端口 0 表示由 VPS 自动分配)")
	}
	if p.remoteListenPort < 0 || p.remoteListenPort > 65535 {
		return fmt.Errorf("--remote-listen-port 超出范围(0-65535):%d", p.remoteListenPort)
	}
	if role == "client" && p.remoteListenPort == 0 {
		return fmt.Errorf("client 模式必须指定 VPS 上的实际端口;自动分配仅限 server,分配结果见 server 的「隧道就绪」日志")
	}
	return nil
}

// merge 按 docopt map → TOML → 内置默认的优先级合并参数。
// role 决定 local-ssh-port 的最终兜底:server 为 22,client 跟随汇合端口。
//
// 注意:usage 里的选项不能再写 [default: N] —— docopt 会把该默认值当成
// "用户显式给出",导致 TOML 中的同名端口被静默覆盖。默认值统一在这里兜底。
func merge(args map[string]interface{}, fc *tunnel.FileConfig, role string) (*params, error) {
	home, _ := os.UserHomeDir()
	return mergeWith(args, fc, role, os.Getenv("USER"), home)
}

// mergeWith 同 merge,但 VPS 登录用户的默认值与 ~ 展开所用的家目录由调用方给出:
// sudo 运行的 install 用发起 sudo 的用户(SUDO_USER)而不是 root。
func mergeWith(args map[string]interface{}, fc *tunnel.FileConfig, role, defaultUser, home string) (*params, error) {

	remoteSSHPort, err := intArg(args, "--remote-ssh-port", orDefaultInt(fc.RemoteSSHPort, defaultRemoteSSHPort))
	if err != nil {
		return nil, err
	}
	localSSHPort, err := intArg(args, "--local-ssh-port", fc.LocalSSHPort)
	if err != nil {
		return nil, err
	}
	remoteListenPort, remoteListenPortSet, err := portArg(args, "--remote-listen-port", fc.RemoteListenPort)
	if err != nil {
		return nil, err
	}
	name, err := selectMode(args, fc)
	if err != nil {
		return nil, err
	}
	if name != "" { // 名字模式:端口(只可能来自被 flag 压过的 TOML)忽略
		remoteListenPort, remoteListenPortSet = 0, false
	}

	p := &params{
		remoteIP:            strArg(args, "--remote-ip", fc.RemoteIP),
		remoteSSHPort:       remoteSSHPort,
		remoteSSHPub:        strArg(args, "--remote-ssh-pub", fc.RemoteSSHPub),
		localSSHKey:         strArg(args, "--local-ssh-key", orDefaultStr(fc.LocalSSHKey, filepath.Join(home, ".ssh", "id_ed25519"))),
		user:                strArg(args, "--user", orDefaultStr(fc.User, defaultUser)),
		localSSHPort:        localSSHPort,
		remoteListenPort:    remoteListenPort,
		remoteListenPortSet: remoteListenPortSet,
		name:                name,
		remoteSocketDir:     strArg(args, "--remote-socket-dir", fc.RemoteSocketDir),
	}
	// local-ssh-key 的 ~ 展开(docopt 默认值不会展开)
	p.localSSHKey = expandHomeIn(p.localSSHKey, home)

	if p.localSSHPort == 0 {
		switch {
		case role == "server":
			p.localSSHPort = defaultInnerSSHPort
		case p.name == "":
			p.localSSHPort = p.remoteListenPort
		}
		// 名字模式的 client 没有可跟随的汇合端口:保持 0,由 config 报必填
	}
	return p, nil
}

// selectMode 在名字模式与端口模式间选定其一,返回名字(空 = 端口模式)。
// 规则:同一来源(flag 或 TOML)两者都给 → 互斥错误;flag 胜过 TOML,
// 被压过的另一项忽略。都没给时返回空,由 config 报缺少参数。
func selectMode(args map[string]interface{}, fc *tunnel.FileConfig) (string, error) {
	flagName := strArg(args, "--name", "")
	flagPort := strArg(args, "--remote-listen-port", "") != ""
	switch {
	case flagName != "" && flagPort:
		return "", fmt.Errorf("--name 与 --remote-listen-port 只能二选一")
	case flagName != "":
		return flagName, nil
	case flagPort:
		return "", nil
	case fc.Name != "" && fc.RemoteListenPort != nil:
		return "", fmt.Errorf("配置文件中 name 与 remote_listen_port 只能二选一")
	default:
		return fc.Name, nil
	}
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

// portArg 处理"未配置"与"显式 0"语义不同的端口(remote-listen-port)。
// flag 出现过(哪怕是 0)即视为显式;否则看 TOML 是否定义了该键(指针非 nil)。
// 返回 (值, 是否显式给出, error)。
func portArg(args map[string]interface{}, key string, file *int) (int, bool, error) {
	if v, ok := args[key].(string); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return 0, false, fmt.Errorf("参数 %s 不是合法数字: %q", key, v)
		}
		return n, true, nil
	}
	if file != nil {
		return *file, true, nil
	}
	return 0, false, nil
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

// expandHomeIn 用给定家目录展开开头的 ~。
func expandHomeIn(p, home string) string {
	if p == "~" {
		return home
	}
	if len(p) > 1 && p[:2] == "~/" {
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
