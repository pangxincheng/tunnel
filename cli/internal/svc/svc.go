// Package svc 渲染并安装系统服务(systemd / launchd / 任务计划程序)。
// 只做模板渲染与命令编排;真实的命令执行与文件写入通过 Runner / FS 注入。
package svc

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Mode string

const (
	ModeSystem Mode = "system"
	ModeUser   Mode = "user"
)

// ServiceName 返回服务名 tunnel-<profile>:每个服务器 profile 在本机只有一个共享服务,
// 设备名字只保存在服务配置里,改名不改变服务身份。
func ServiceName(profile string) string { return "tunnel-" + profile }

// Spec 描述要安装的服务。Name 为 ServiceName 的结果。
type Spec struct {
	Name    string
	Mode    Mode
	Exec    string
	Args    []string
	LogPath string
	User    string // Windows:任务所属账户(DOMAIN\user),登录触发器与 Principal 都限定为它
}

// Runner 执行外部命令。
type Runner interface {
	Run(name string, args ...string) error
}

// ExecRunner 是基于 exec.Command 的真实 Runner。
type ExecRunner struct{}

func (ExecRunner) Run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg != "" {
			return errors.New(name + " " + strings.Join(args, " ") + " 失败: " + err.Error() + ": " + msg)
		}
		return errors.New(name + " " + strings.Join(args, " ") + " 失败: " + err.Error())
	}
	return nil
}

// FS 抽象服务定义文件的读写,便于测试。
type FS interface {
	WriteFile(path string, data []byte) error
	Remove(path string) error
	Exists(path string) bool
}

type osFS struct{}

func (osFS) WriteFile(p string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o644)
}
func (osFS) Remove(p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
func (osFS) Exists(p string) bool { _, err := os.Stat(p); return err == nil }

type Manager interface {
	Install(Spec) error
	Uninstall(name string) error
	Installed(name string) (bool, error)
	// Stop 结束正在运行的服务进程,便于随后替换其二进制;只有 Windows 需要
	// (运行中的 exe 不能被覆盖),其他平台为无操作。未在运行时可能返回错误,调用方可忽略。
	Stop(name string) error
}

// Option 调整 NewManager 的可注入依赖。
type Option func(*manager)

// WithFS 替换文件读写(默认真实文件系统)。
func WithFS(fs FS) Option { return func(m *manager) { m.fs = fs } }

// WithUID 替换 launchd 用户域所用的 uid 来源(默认 os.Getuid)。
func WithUID(f func() int) Option { return func(m *manager) { m.uid = f } }

// WithSleep 替换等待所用的 sleep(默认 time.Sleep),测试用。
func WithSleep(f func(time.Duration)) Option { return func(m *manager) { m.sleep = f } }

type manager struct {
	mode  Mode
	goos  string
	home  string
	r     Runner
	fs    FS
	uid   func() int
	sleep func(time.Duration)
}

// NewManager 返回对应平台的 Manager。goos/home 以参数传入,便于在任意平台测试。
func NewManager(mode Mode, goos string, home string, r Runner, opts ...Option) (Manager, error) {
	if mode != ModeSystem && mode != ModeUser {
		return nil, errors.New("未知的服务模式:" + string(mode))
	}
	switch goos {
	case "windows":
		if mode == ModeSystem {
			return nil, errors.New("Windows 暂不支持开机即运行(--mode system)")
		}
	case "linux", "darwin":
	default:
		return nil, errors.New("不支持的操作系统:" + goos)
	}
	m := &manager{mode: mode, goos: goos, home: home, r: r, fs: osFS{}, uid: os.Getuid, sleep: time.Sleep}
	for _, o := range opts {
		o(m)
	}
	return m, nil
}

func (m *manager) Install(s Spec) error {
	switch m.goos {
	case "linux":
		return m.installSystemd(s)
	case "darwin":
		return m.installLaunchd(s)
	default:
		return m.installTask(s)
	}
}

func (m *manager) Uninstall(name string) error {
	switch m.goos {
	case "linux":
		return m.uninstallSystemd(name)
	case "darwin":
		return m.uninstallLaunchd(name)
	default:
		return m.uninstallTask(name)
	}
}

func (m *manager) Stop(name string) error {
	if m.goos != "windows" {
		return nil
	}
	return m.r.Run("schtasks", "/End", "/TN", name)
}

func (m *manager) Installed(name string) (bool, error) {
	switch m.goos {
	case "linux":
		return m.fs.Exists(m.unitPath(name)), nil
	case "darwin":
		return m.fs.Exists(m.plistPath(name)), nil
	default:
		return m.fs.Exists(m.taskXMLPath(name)), nil
	}
}
