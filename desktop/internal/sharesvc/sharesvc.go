// Package sharesvc 通过附带的 tunnel CLI(子进程)控制"共享本机"服务。
package sharesvc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/pangxincheng/tunnel/cli/tunnel"
)

// Runner 运行外部命令。实现应在一个全新的空工作目录中运行,
// 使 CLI 不会自动读取 ./tunnel.toml。退出码非 0 不算 err。
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, exit int, err error)
}

// ExecRunner 用 exec.CommandContext 运行命令,工作目录为运行后即删除的临时空目录。
type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, name string, args ...string) ([]byte, []byte, int, error) {
	dir, err := os.MkdirTemp("", "tunnel-app-run-")
	if err != nil {
		return nil, nil, -1, err
	}
	defer os.RemoveAll(dir)
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	hideWindow(cmd)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err = cmd.Run()
	if cerr := ctx.Err(); cerr != nil {
		return so.Bytes(), se.Bytes(), -1, cerr
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return so.Bytes(), se.Bytes(), ee.ExitCode(), nil
	}
	if err != nil {
		return so.Bytes(), se.Bytes(), -1, err
	}
	return so.Bytes(), se.Bytes(), 0, nil
}

// ErrCancelled 表示用户取消了系统授权。
var ErrCancelled = errors.New("authorization cancelled")

// ErrNotRunning 表示服务已安装但没有在运行(CLI 退出码 3,控制通道连不上)。
var ErrNotRunning = errors.New("share service is not running")

// exitNotRunning 是 CLI 在服务已安装、控制通道连不上时的退出码。
const exitNotRunning = 3

// State 是共享服务的当前状态。
type State struct {
	Installed, Running bool
	Mode               string              // "user"|"system",未安装时为空
	Status             *tunnel.ShareStatus // 只在 Running 时非空
}

// InstallOpts 是安装共享服务的参数。
type InstallOpts struct {
	Profile, Name, Host, User, KeyPath, HostKey, SocketDir string
	Port, LocalPort                                        int // LocalPort:本机 sshd 端口
	System                                                 bool
}

// Service 控制共享服务。
type Service struct {
	bin, goos, currentUser string
	runner                 Runner
}

// New 创建 Service。bin 是 tunnel CLI 路径,goos 如 runtime.GOOS,currentUser 用于 --for-user。
func New(bin string, r Runner, goos, currentUser string) *Service {
	return &Service{bin: bin, runner: r, goos: goos, currentUser: currentUser}
}

type statusJSON struct {
	tunnel.ShareStatus
	Mode string `json:"mode"`
}

var errAttr = regexp.MustCompile(`err="((?:[^"\\]|\\.)*)"`)

// cliError 把 CLI 的 stderr 整理成错误信息:优先取 slog 行里 err="..." 的值。
func cliError(what string, exit int, stderr []byte) error {
	msg := strings.TrimSpace(string(stderr))
	if m := errAttr.FindAllStringSubmatch(msg, -1); len(m) > 0 {
		last := m[len(m)-1][1]
		if u, err := strconv.Unquote(`"` + last + `"`); err == nil {
			last = u
		}
		msg = last
	}
	if msg == "" {
		return fmt.Errorf("%s: exit status %d", what, exit)
	}
	return fmt.Errorf("%s: %s", what, msg)
}

// Status 查询共享服务状态。
func (s *Service) Status(ctx context.Context, profile string) (State, error) {
	if err := validateProfile(profile); err != nil {
		return State{}, err
	}
	out, errOut, exit, err := s.runner.Run(ctx, s.bin, "status", "--json", "--profile", profile)
	if err != nil {
		return State{}, err
	}
	switch exit {
	case 0:
		var j statusJSON
		if err := json.Unmarshal(out, &j); err != nil {
			return State{}, fmt.Errorf("parse tunnel status: %w", err)
		}
		st := j.ShareStatus
		return State{Installed: true, Running: true, Mode: j.Mode, Status: &st}, nil
	case exitNotRunning:
		var j struct {
			Mode string `json:"mode"`
		}
		_ = json.Unmarshal(out, &j)
		return State{Installed: true, Mode: j.Mode}, nil
	case 4:
		return State{}, nil
	}
	return State{}, cliError("tunnel status", exit, errOut)
}

func validateProfile(p string) error {
	if err := tunnel.ValidateName(p); err != nil {
		return fmt.Errorf("invalid profile: %w", err)
	}
	return nil
}

func (s *Service) simple(ctx context.Context, what string, args ...string) error {
	_, errOut, exit, err := s.runner.Run(ctx, s.bin, args...)
	if err != nil {
		return err
	}
	if exit == exitNotRunning {
		return fmt.Errorf("%s: %w", what, ErrNotRunning)
	}
	if exit != 0 {
		return cliError(what, exit, errOut)
	}
	return nil
}

func (s *Service) Pause(ctx context.Context, profile string) error {
	if err := validateProfile(profile); err != nil {
		return err
	}
	return s.simple(ctx, "tunnel pause", "pause", "--profile", profile)
}

func (s *Service) Resume(ctx context.Context, profile string) error {
	if err := validateProfile(profile); err != nil {
		return err
	}
	return s.simple(ctx, "tunnel resume", "resume", "--profile", profile)
}

func (s *Service) Retry(ctx context.Context, profile string) error {
	if err := validateProfile(profile); err != nil {
		return err
	}
	return s.simple(ctx, "tunnel retry", "retry", "--profile", profile)
}

func (s *Service) SetName(ctx context.Context, profile, name string) error {
	if err := validateProfile(profile); err != nil {
		return err
	}
	if err := tunnel.ValidateName(name); err != nil {
		return fmt.Errorf("invalid name: %w", err)
	}
	return s.simple(ctx, "tunnel set-name", "set-name", name, "--profile", profile)
}

// Install 安装并启动共享服务;System 时经系统授权提权。
func (s *Service) Install(ctx context.Context, o InstallOpts) error {
	if err := validateProfile(o.Profile); err != nil {
		return err
	}
	if err := tunnel.ValidateName(o.Name); err != nil {
		return fmt.Errorf("invalid name: %w", err)
	}
	if o.System && s.currentUser == "" {
		return errors.New("system mode requires the current user name")
	}
	mode := "user"
	if o.System {
		mode = "system"
	}
	args := []string{"install", "--mode", mode, "--profile", o.Profile, "--name", o.Name,
		"--remote-ip", o.Host, "--remote-ssh-port", strconv.Itoa(o.Port), "--user", o.User,
		"--local-ssh-key", o.KeyPath, "--remote-ssh-pub", o.HostKey,
		"--local-ssh-port", strconv.Itoa(o.LocalPort)}
	if o.SocketDir != "" {
		args = append(args, "--remote-socket-dir", o.SocketDir)
	}
	if o.System {
		args = append(args, "--for-user", s.currentUser)
		return s.elevated(ctx, "tunnel install", args)
	}
	return s.simple(ctx, "tunnel install", args...)
}

// Uninstall 卸载共享服务;system 时经系统授权提权。
func (s *Service) Uninstall(ctx context.Context, profile string, system bool) error {
	if err := validateProfile(profile); err != nil {
		return err
	}
	if system && s.currentUser == "" {
		return errors.New("system mode requires the current user name")
	}
	if system {
		return s.elevated(ctx, "tunnel uninstall", []string{"uninstall", "--mode", "system", "--profile", profile})
	}
	return s.simple(ctx, "tunnel uninstall", "uninstall", "--profile", profile)
}

// userHome 可在测试中替换。
var userHome = os.UserHomeDir

// LogPath 返回服务日志路径;Linux 返回 ""(用 journalctl)。
func (s *Service) LogPath(profile string, system bool) string {
	switch s.goos {
	case "darwin":
		if system {
			return "/var/log/tunnel/tunnel-" + profile + ".log"
		}
		h, err := userHome()
		if err != nil {
			return ""
		}
		return filepath.Join(h, "Library", "Application Support", "tunnel", "tunnel-"+profile, "tunnel.log")
	case "windows":
		h, err := userHome()
		if err != nil {
			return ""
		}
		return filepath.Join(h, "AppData", "Roaming", "tunnel", "tunnel-"+profile, "tunnel.log")
	}
	return ""
}
