// 开机自启与后台运行:install / uninstall / status / pause / resume / retry / run-service。
//
// 所有系统相关的依赖(服务管理器、路径、SSH 拨号、设备 ID、运行身份)都经 svcEnv
// 注入:测试只使用假的 Manager 与临时目录,从不接触真实的系统服务。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	osuser "os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/crypto/ssh"

	"github.com/pangxincheng/tunnel/internal/svc"
	"github.com/pangxincheng/tunnel/tunnel"
)

const (
	defaultProfile = "default"
	serviceConfig  = "tunnel.toml" // ConfigDir 内的服务配置
	serviceKey     = "id_key"      // system 模式复制的私钥
	serviceOwner   = "owner"       // system 模式记录的安装用户 UID

	exitNotRunning = 3 // 服务已安装但控制通道连不上
)

// svcEnv 是服务子命令的全部外部依赖。
type svcEnv struct {
	goos       string
	home       string // 安装用户的家目录(sudo 下为 SUDO_USER 的家目录)
	user       string // 安装用户名(VPS 登录用户的默认值)
	ownerUID   int    // system 模式控制通道的属主(安装用户 UID)
	euid       int
	isTerminal bool
	exe        string // 当前可执行文件
	stdout     io.Writer
	logger     *slog.Logger
	paths      func(mode svc.Mode, name string) svc.Paths
	manager    func(mode svc.Mode) (svc.Manager, error)
	dial       func(*tunnel.Config) (*ssh.Client, error)
	deviceID   func() (string, error)
}

// defaultEnv 返回真实环境。以 root 运行且存在 SUDO_USER 时,默认 VPS 用户、
// ~ 展开与控制通道属主都取发起 sudo 的用户,而不是 root。
func defaultEnv(logger *slog.Logger) *svcEnv {
	home, _ := os.UserHomeDir()
	userName := os.Getenv("USER")
	euid := os.Geteuid()
	owner := os.Getuid()
	if euid == 0 {
		if su := os.Getenv("SUDO_USER"); su != "" {
			if u, err := osuser.Lookup(su); err == nil {
				userName, home = su, u.HomeDir
			}
		}
		if n, err := strconv.Atoi(os.Getenv("SUDO_UID")); err == nil {
			owner = n
		}
	}
	exe, err := os.Executable()
	if err == nil {
		if r, err := filepath.EvalSymlinks(exe); err == nil {
			exe = r
		}
	}
	isTerminal := false
	if fi, err := os.Stdin.Stat(); err == nil {
		isTerminal = fi.Mode()&os.ModeCharDevice != 0
	}
	goos := runtime.GOOS
	return &svcEnv{
		goos:       goos,
		home:       home,
		user:       userName,
		ownerUID:   owner,
		euid:       euid,
		isTerminal: isTerminal,
		exe:        exe,
		stdout:     os.Stdout,
		logger:     logger,
		paths:      func(m svc.Mode, n string) svc.Paths { return svc.PathsFor(m, n, goos, home) },
		manager: func(m svc.Mode) (svc.Manager, error) {
			return svc.NewManager(m, goos, home, svc.ExecRunner{})
		},
		dial:     tunnel.Dial,
		deviceID: tunnel.DeviceID,
	}
}

// serviceFile 是写入 ConfigDir/tunnel.toml 的内容(键与 tunnel.toml 相同)。
// 私钥只以路径引用,主机公钥内联。
type serviceFile struct {
	Profile         string `toml:"profile"`
	Name            string `toml:"name"`
	RemoteIP        string `toml:"remote_ip"`
	RemoteSSHPort   int    `toml:"remote_ssh_port"`
	RemoteSSHPub    string `toml:"remote_ssh_pub"`
	LocalSSHKey     string `toml:"local_ssh_key"`
	User            string `toml:"user"`
	LocalSSHPort    int    `toml:"local_ssh_port"`
	RemoteSocketDir string `toml:"remote_socket_dir,omitempty"`
}

func parseMode(s string) (svc.Mode, error) {
	switch m := svc.Mode(s); m {
	case svc.ModeSystem, svc.ModeUser:
		return m, nil
	}
	return "", fmt.Errorf("--mode 只能是 system 或 user:%q", s)
}

func boolArg(args map[string]interface{}, key string) bool {
	v, _ := args[key].(bool)
	return v
}

// ---------- install ----------

// installFlow 实现 tunnel install。返回非 nil 的 reexec 时调用方应以该命令
// 替换当前进程(sudo 重新执行),此时尚未产生任何副作用。
func installFlow(env *svcEnv, args map[string]interface{}, raw []string) ([]string, error) {
	mode, err := parseMode(strArg(args, "--mode", ""))
	if err != nil {
		return nil, err
	}
	if re, err := checkPrivilege(mode, env.euid, env.isTerminal, raw); err != nil || re != nil {
		return re, err
	}
	mgr, err := env.manager(mode)
	if err != nil {
		return nil, err
	}

	cfgFile, source := configPath(args)
	fc, err := loadFileConfigHome(cfgFile, env.home)
	if err != nil {
		return nil, err
	}
	p, err := mergeWith(args, fc, "server", env.user, env.home)
	if err != nil {
		return nil, err
	}
	if p.name == "" {
		return nil, errors.New("tunnel install 只支持 --name 按名字转发(或配置文件 name)")
	}
	profile := strArg(args, "--profile", orDefaultStr(fc.Profile, defaultProfile))
	if err := tunnel.ValidateName(profile); err != nil {
		return nil, fmt.Errorf("profile 不合法:%w", err)
	}
	cfg, err := p.config("server")
	if err != nil {
		return nil, err
	}
	keyPath, err := filepath.Abs(p.localSSHKey)
	if err != nil {
		return nil, err
	}
	cfg.KeyPath = keyPath

	// 先实际连一次 VPS:配置有误时立刻报错,不留下装好却永远连不上的服务
	if cfgFile != "" {
		env.logger.Info("配置来源", "source", source, "file", cfgFile)
	}
	env.logger.Info("验证 VPS 连接", "addr", cfg.SSHAddr, "user", cfg.User, "key", cfg.KeyPath)
	sc, err := env.dial(cfg)
	if err != nil {
		return nil, fmt.Errorf("连接 VPS 失败,未安装:%w", err)
	}
	dir, err := tunnel.ResolveSocketDir(sc, p.remoteSocketDir)
	sc.Close()
	if err != nil {
		return nil, fmt.Errorf("解析 VPS socket 目录失败,未安装:%w", err)
	}
	env.logger.Info("VPS 连接正常", "socket_dir", dir)

	name := svc.ServiceName(profile, p.name)
	paths := env.paths(mode, name)
	dirPerm := os.FileMode(0o700)
	if mode == svc.ModeSystem {
		dirPerm = 0o755 // 非 root 的 status 需要能看到服务;配置与私钥本身仍为 0600
	}
	if err := os.MkdirAll(paths.ConfigDir, dirPerm); err != nil {
		return nil, fmt.Errorf("创建配置目录失败:%w", err)
	}
	if err := os.Chmod(paths.ConfigDir, dirPerm); err != nil {
		return nil, err
	}

	if mode == svc.ModeSystem {
		// 服务以 root 运行,不依赖安装用户家目录里的私钥
		key, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("读取私钥失败:%w", err)
		}
		keyPath = filepath.Join(paths.ConfigDir, serviceKey)
		if err := writeFileAtomic(keyPath, key, 0o600); err != nil {
			return nil, fmt.Errorf("复制私钥失败:%w", err)
		}
		if err := writeFileAtomic(filepath.Join(paths.ConfigDir, serviceOwner), []byte(strconv.Itoa(env.ownerUID)+"\n"), 0o644); err != nil {
			return nil, fmt.Errorf("记录安装用户失败:%w", err)
		}
	}
	sf := serviceFile{
		Profile:         profile,
		Name:            p.name,
		RemoteIP:        p.remoteIP,
		RemoteSSHPort:   p.remoteSSHPort,
		RemoteSSHPub:    inlineHostPub(p.remoteSSHPub),
		LocalSSHKey:     keyPath,
		User:            p.user,
		LocalSSHPort:    p.localSSHPort,
		RemoteSocketDir: p.remoteSocketDir,
	}
	cfgPath := filepath.Join(paths.ConfigDir, serviceConfig)
	if err := writeServiceConfig(cfgPath, sf); err != nil {
		return nil, fmt.Errorf("写入服务配置失败:%w", err)
	}
	if err := copySelf(env.exe, paths.BinPath); err != nil {
		return nil, fmt.Errorf("复制 tunnel 到 %s 失败:%w", paths.BinPath, err)
	}
	if paths.LogPath != "" {
		// launchd 不会自动创建日志目录,目录缺失时任务无法启动
		if err := os.MkdirAll(filepath.Dir(paths.LogPath), 0o755); err != nil {
			return nil, fmt.Errorf("创建日志目录失败:%w", err)
		}
	}
	spec := svc.Spec{
		Name:    name,
		Mode:    mode,
		Exec:    paths.BinPath,
		Args:    []string{"run-service", "--mode", string(mode), "--profile", profile, "--name", p.name},
		LogPath: paths.LogPath,
	}
	if err := mgr.Install(spec); err != nil {
		return nil, fmt.Errorf("安装服务失败(可修正后重新执行 tunnel install):%w", err)
	}

	sel := fmt.Sprintf("--mode %s --profile %s --name %s", mode, profile, p.name)
	sudo := ""
	if mode == svc.ModeSystem {
		sudo = "sudo "
	}
	w := env.stdout
	fmt.Fprintf(w, "已安装并启动服务 %s(%s 模式)\n", name, mode)
	fmt.Fprintf(w, "  共享名字:%s → 本机 127.0.0.1:%d\n", p.name, p.localSSHPort)
	fmt.Fprintf(w, "  服务配置:%s\n", cfgPath)
	fmt.Fprintf(w, "  查看状态:tunnel status %s\n", sel)
	fmt.Fprintf(w, "  查看日志:%s\n", logHint(env.goos, mode, name, paths))
	fmt.Fprintf(w, "  卸载:    %stunnel uninstall %s\n", sudo, sel)
	return nil, nil
}

// loadFileConfigHome 同 loadFileConfig,但 ~ 按给定家目录展开(sudo 下为发起用户)。
func loadFileConfigHome(path, home string) (*tunnel.FileConfig, error) {
	fc := &tunnel.FileConfig{}
	if path == "" {
		return fc, nil
	}
	if _, err := toml.DecodeFile(path, fc); err != nil {
		return nil, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	fc.RemoteSSHPub = expandHomeIn(fc.RemoteSSHPub, home)
	fc.LocalSSHKey = expandHomeIn(fc.LocalSSHKey, home)
	return fc, nil
}

// inlineHostPub 把 .pub 文件路径换成文件内容(与 Dial 的解析规则一致),
// 服务配置不再依赖安装用户的文件。
func inlineHostPub(v string) string {
	if b, err := os.ReadFile(v); err == nil {
		return strings.TrimSpace(string(b))
	}
	return v
}

func writeServiceConfig(path string, sf serviceFile) error {
	var b strings.Builder
	b.WriteString("# 由 tunnel install 生成;修改后请重新执行 tunnel install。\n")
	if err := toml.NewEncoder(&b).Encode(sf); err != nil {
		return err
	}
	return writeFileAtomic(path, []byte(b.String()), 0o600)
}

// setConfigName 把服务配置中的 name 改写为新名字(set-name 持久化)。
func setConfigName(path, name string) error {
	fc, err := tunnel.LoadTOML(path)
	if err != nil {
		return err
	}
	return writeServiceConfig(path, serviceFile{
		Profile:         fc.Profile,
		Name:            name,
		RemoteIP:        fc.RemoteIP,
		RemoteSSHPort:   fc.RemoteSSHPort,
		RemoteSSHPub:    fc.RemoteSSHPub,
		LocalSSHKey:     fc.LocalSSHKey,
		User:            fc.User,
		LocalSSHPort:    fc.LocalSSHPort,
		RemoteSocketDir: fc.RemoteSocketDir,
	})
}

// writeFileAtomic 写同目录临时文件(以 0600 创建,写完再 chmod)后重命名。
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, perm)
	}
	if werr == nil {
		werr = os.Rename(tmp, path)
	}
	if werr != nil {
		os.Remove(tmp)
	}
	return werr
}

// copySelf 把当前可执行文件复制到 dst(0755)。先写临时文件再重命名:
// 覆盖正在运行的旧版本也安全;dst 就是自身时跳过。
func copySelf(exe, dst string) error {
	if exe == "" {
		return errors.New("无法确定当前可执行文件路径")
	}
	if a, err := os.Stat(exe); err == nil {
		if b, err := os.Stat(dst); err == nil && os.SameFile(a, b) {
			return nil
		}
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		// 运行中的 exe 不能被覆盖,但可以改名
		os.Remove(dst + ".old")
		os.Rename(dst, dst+".old")
	}
	return writeFileAtomic(dst, data, 0o755)
}

func logHint(goos string, mode svc.Mode, name string, p svc.Paths) string {
	switch {
	case goos == "linux" && mode == svc.ModeSystem:
		return "journalctl -u " + name + " -f"
	case goos == "linux":
		return "journalctl --user -u " + name + " -f"
	case p.LogPath != "":
		return p.LogPath
	}
	return "(无)"
}

// ---------- 定位已安装的服务 ----------

type target struct {
	mode    svc.Mode
	profile string
	name    string
}

func (t target) service() string { return svc.ServiceName(t.profile, t.name) }

// installedNames 列出 mode 下 profile 的已安装服务(以 ConfigDir/tunnel.toml 为准)的名字。
func installedNames(env *svcEnv, mode svc.Mode, profile string) []string {
	prefix := svc.ServiceName(profile, "")
	parent := filepath.Dir(env.paths(mode, prefix+"x").ConfigDir)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		n := strings.TrimPrefix(e.Name(), prefix)
		if !e.IsDir() || n == e.Name() || tunnel.ValidateName(n) != nil {
			continue
		}
		cfg := filepath.Join(env.paths(mode, e.Name()).ConfigDir, serviceConfig)
		if _, err := os.Stat(cfg); err != nil {
			continue
		}
		// profile 含 "-" 时目录名有歧义,能读到配置就按其中的 profile 核对
		// (system 模式的配置非 root 不可读,此时按目录名接受)
		if fc, err := tunnel.LoadTOML(cfg); err == nil && fc.Profile != "" && fc.Profile != profile {
			continue
		}
		names = append(names, n)
	}
	return names
}

// resolveTarget 由 --mode / --profile / --name 定位一个已安装的服务:
// 未给 --mode 时依次查找 user、system(都有则要求指定);未给 --name 时
// 该 profile 下只有一个服务才自动选中。
func resolveTarget(env *svcEnv, args map[string]interface{}) (target, error) {
	profile := strArg(args, "--profile", defaultProfile)
	if err := tunnel.ValidateName(profile); err != nil {
		return target{}, fmt.Errorf("profile 不合法:%w", err)
	}
	name := strArg(args, "--name", "")
	var modes []svc.Mode
	if m := strArg(args, "--mode", ""); m != "" {
		mode, err := parseMode(m)
		if err != nil {
			return target{}, err
		}
		modes = []svc.Mode{mode}
	} else {
		modes = []svc.Mode{svc.ModeUser}
		if env.goos != "windows" {
			modes = append(modes, svc.ModeSystem)
		}
	}
	var found []target
	seenMode := map[svc.Mode]bool{}
	for _, mode := range modes {
		for _, n := range installedNames(env, mode, profile) {
			if name == "" || n == name {
				found = append(found, target{mode, profile, n})
				seenMode[mode] = true
			}
		}
	}
	switch {
	case len(found) == 0 && name != "":
		return target{}, fmt.Errorf("未找到已安装的服务 %s", svc.ServiceName(profile, name))
	case len(found) == 0:
		return target{}, fmt.Errorf("profile %s 下没有已安装的服务(先运行 tunnel install,或用 --profile 指定)", profile)
	case len(seenMode) > 1:
		return target{}, errors.New("user 与 system 模式下都有已安装的服务,请用 --mode 指定")
	case len(found) > 1:
		var ns []string
		for _, t := range found {
			ns = append(ns, t.name)
		}
		return target{}, fmt.Errorf("profile %s 下有多个服务(%s),请用 --name 指定", profile, strings.Join(ns, "、"))
	}
	return found[0], nil
}

// ---------- status / pause / resume / retry ----------

func statusCmd(env *svcEnv, args map[string]interface{}) int {
	tg, err := resolveTarget(env, args)
	if err != nil {
		env.logger.Error("tunnel status 失败", "err", err)
		return 1
	}
	p := env.paths(tg.mode, tg.service())
	resp, err := tunnel.ControlCall(p.ControlAddr, tunnel.ControlRequest{Op: "status"})
	if err != nil {
		return controlUnreachable(env, tg, p, err)
	}
	if resp.Status == nil {
		env.logger.Error("tunnel status 失败", "err", resp.Error)
		return 1
	}
	if boolArg(args, "--json") {
		data, _ := json.Marshal(resp.Status)
		fmt.Fprintln(env.stdout, string(data))
		return 0
	}
	printStatus(env.stdout, tg, *resp.Status)
	return 0
}

// controlCmd 对应 pause / resume / retry。
func controlCmd(env *svcEnv, args map[string]interface{}, op string) int {
	tg, err := resolveTarget(env, args)
	if err != nil {
		env.logger.Error("tunnel "+op+" 失败", "err", err)
		return 1
	}
	p := env.paths(tg.mode, tg.service())
	resp, err := tunnel.ControlCall(p.ControlAddr, tunnel.ControlRequest{Op: op})
	if err != nil {
		return controlUnreachable(env, tg, p, err)
	}
	if !resp.OK {
		env.logger.Error("tunnel "+op+" 失败", "err", resp.Error)
		return 1
	}
	done := map[string]string{"pause": "已暂停共享", "resume": "已恢复共享", "retry": "已请求立即重连"}[op]
	fmt.Fprintf(env.stdout, "%s:%s\n", tg.service(), done)
	return 0
}

// controlUnreachable 处理控制通道连不上:无权限时说明原因(退出码 1),否则视为未运行(退出码 3)。
func controlUnreachable(env *svcEnv, tg target, p svc.Paths, err error) int {
	if errors.Is(err, fs.ErrPermission) {
		env.logger.Error("无权访问控制通道(只有安装该服务的用户与 root 可以操作)", "control", p.ControlAddr, "err", err)
		return 1
	}
	printNotRunning(env, tg, p, err)
	return exitNotRunning
}

func printNotRunning(env *svcEnv, tg target, p svc.Paths, err error) {
	fmt.Fprintf(env.stdout, "%s(%s 模式):已安装,未运行\n", tg.service(), tg.mode)
	fmt.Fprintf(env.stdout, "  控制通道 %s 连不上:%v\n", p.ControlAddr, err)
	fmt.Fprintf(env.stdout, "  查看日志:%s\n", logHint(env.goos, tg.mode, tg.service(), p))
}

func phaseText(st tunnel.ShareStatus) string {
	switch st.Phase {
	case tunnel.PhaseConnecting:
		return "连接中"
	case tunnel.PhaseReady:
		if !st.Since.IsZero() {
			return "已就绪(自 " + st.Since.Local().Format("2006-01-02 15:04:05") + ")"
		}
		return "已就绪"
	case tunnel.PhaseWaiting:
		if !st.RetryAt.IsZero() {
			return "等待重连(" + st.RetryAt.Local().Format("15:04:05") + " 重试,可用 tunnel retry 立即重连)"
		}
		return "等待重连"
	case tunnel.PhaseFailed:
		return "失败(修正后用 tunnel retry 重试)"
	case tunnel.PhasePaused:
		return "已暂停(tunnel resume 恢复)"
	}
	return string(st.Phase)
}

func printStatus(w io.Writer, tg target, st tunnel.ShareStatus) {
	fmt.Fprintf(w, "服务:%s(%s 模式)\n", tg.service(), tg.mode)
	fmt.Fprintf(w, "名字:%s\n", st.Name)
	fmt.Fprintf(w, "状态:%s\n", phaseText(st))
	if st.Err != "" {
		fmt.Fprintf(w, "原因:%s\n", st.Err)
	}
	if st.Socket != "" {
		fmt.Fprintf(w, "VPS socket:%s\n", st.Socket)
	}
	fmt.Fprintf(w, "版本:%s\n", st.Version)
}

// ---------- uninstall ----------

// uninstallFlow 实现 tunnel uninstall:停止并删除服务 → 尽力删除 VPS 上本机的
// 设备信息与 socket(连不上只警告)→ 删除配置目录与状态文件。
func uninstallFlow(env *svcEnv, args map[string]interface{}, raw []string) ([]string, error) {
	tg, err := resolveTarget(env, args)
	if err != nil {
		return nil, err
	}
	if re, err := checkPrivilege(tg.mode, env.euid, env.isTerminal, raw); err != nil || re != nil {
		return re, err
	}
	mgr, err := env.manager(tg.mode)
	if err != nil {
		return nil, err
	}
	name := tg.service()
	p := env.paths(tg.mode, name)
	fc, cfgErr := tunnel.LoadTOML(filepath.Join(p.ConfigDir, serviceConfig)) // 删除前读出,清理 VPS 用

	// 安装半途失败时只有配置、没有服务登记(Windows 的 schtasks /Delete 对不存在的
	// 任务会报错):此时跳过 Uninstall,仍继续清理,避免永远卸不掉。
	registered, err := mgr.Installed(name)
	if err != nil {
		return nil, fmt.Errorf("检查服务是否已安装失败:%w", err)
	}
	if registered {
		if err := mgr.Uninstall(name); err != nil {
			return nil, fmt.Errorf("停止并删除服务失败(配置已保留,可重新执行 tunnel uninstall):%w", err)
		}
	} else {
		env.logger.Warn("系统中没有该服务的登记(可能是安装中途失败),只清理配置与 VPS 上的残留", "service", name)
	}
	if cfgErr != nil {
		env.logger.Warn("读取服务配置失败,跳过 VPS 上的清理", "err", cfgErr)
	} else if err := removeServerDevice(env, fc); err != nil {
		env.logger.Warn("未能删除 VPS 上本机的设备信息(不影响卸载;其他设备会看到它离线)", "err", err)
	}

	removeServiceDir(p.ConfigDir, name)
	if d := filepath.Dir(p.StatePath); filepath.Base(d) == name {
		removeServiceDir(d, name)
	} else {
		os.Remove(p.StatePath)
	}
	if env.goos != "windows" {
		if fi, err := os.Lstat(p.ControlAddr); err == nil && fi.Mode()&os.ModeSocket != 0 {
			os.Remove(p.ControlAddr)
		}
	}
	fmt.Fprintf(env.stdout, "已卸载服务 %s(%s 模式)\n", name, tg.mode)
	return nil, nil
}

// removeServiceDir 只删除以服务名命名的目录,防止路径异常时误删上级目录。
func removeServiceDir(dir, name string) {
	if filepath.Base(dir) == name {
		os.RemoveAll(dir)
	}
}

func removeServerDevice(env *svcEnv, fc *tunnel.FileConfig) error {
	p, err := mergeWith(map[string]interface{}{}, fc, "server", env.user, env.home)
	if err != nil {
		return err
	}
	if p.name == "" {
		return errors.New("服务配置缺少 name")
	}
	cfg, err := p.config("server")
	if err != nil {
		return err
	}
	id, err := env.deviceID()
	if err != nil {
		return fmt.Errorf("读取设备 ID 失败:%w", err)
	}
	sc, err := env.dial(cfg)
	if err != nil {
		return err
	}
	defer sc.Close()
	dir, err := tunnel.ResolveSocketDir(sc, p.remoteSocketDir)
	if err != nil {
		return err
	}
	if err := tunnel.RemoveDevice(sc, dir, p.name, id); err != nil {
		return err
	}
	env.logger.Info("已删除 VPS 上本机的设备信息与 socket", "name", p.name, "dir", dir)
	return nil
}

// ---------- run-service ----------

// runService 是服务进程的入口:读 ConfigDir/tunnel.toml → NewShare → 控制通道 →
// Share.Run,直到 ctx 取消。set-name 改写配置中的 name,服务名不变。
func runService(ctx context.Context, env *svcEnv, mode svc.Mode, profile, name string) error {
	if err := tunnel.ValidateName(profile); err != nil {
		return fmt.Errorf("profile 不合法:%w", err)
	}
	if err := tunnel.ValidateName(name); err != nil {
		return err
	}
	svcName := svc.ServiceName(profile, name)
	p := env.paths(mode, svcName)
	cfgPath := filepath.Join(p.ConfigDir, serviceConfig)
	fc, err := tunnel.LoadTOML(cfgPath)
	if err != nil {
		return err
	}
	sp, err := mergeWith(map[string]interface{}{}, fc, "server", env.user, env.home)
	if err != nil {
		return err
	}
	if sp.name == "" {
		return fmt.Errorf("服务配置 %s 缺少 name", cfgPath)
	}
	cfg, err := sp.config("server")
	if err != nil {
		return err
	}
	owner := -1
	if mode == svc.ModeSystem {
		if owner, err = readOwner(p.ConfigDir); err != nil {
			return err
		}
	}
	id, err := env.deviceID()
	if err != nil {
		return fmt.Errorf("读取设备 ID 失败:%w", err)
	}
	target := fmt.Sprintf("127.0.0.1:%d", sp.localSSHPort)
	share, err := tunnel.NewShare(tunnel.ShareConfig{
		Conn:      *cfg,
		SocketDir: sp.remoteSocketDir,
		Name:      sp.name,
		Target:    target,
		StatePath: p.StatePath,
		DeviceID:  id,
	}, tunnel.WithSessionLogger(env.logger))
	if err != nil {
		return err
	}
	l, err := tunnel.ControlListen(p.ControlAddr, owner)
	if err != nil {
		return err
	}
	env.logger.Info("共享服务启动", "version", tunnel.Version, "service", svcName, "name", sp.name,
		"target", target, "addr", cfg.SSHAddr, "control", p.ControlAddr)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	served := make(chan struct{})
	go func() {
		defer close(served)
		err := tunnel.ServeControl(ctx, l, share, func(n string) error { return setConfigName(cfgPath, n) })
		if err != nil {
			env.logger.Error("控制通道已停止", "err", err)
		}
	}()
	err = share.Run(ctx)
	cancel()
	<-served
	env.logger.Info("共享服务已停止", "service", svcName)
	return err
}

// readOwner 读取 system 模式记录的安装用户 UID;文件缺失时控制通道保持 root 所有。
func readOwner(dir string) (int, error) {
	b, err := os.ReadFile(filepath.Join(dir, serviceOwner))
	if errors.Is(err, fs.ErrNotExist) {
		return -1, nil
	}
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0, fmt.Errorf("%s 内容不是 UID:%q", filepath.Join(dir, serviceOwner), b)
	}
	return n, nil
}
