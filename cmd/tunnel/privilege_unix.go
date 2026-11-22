//go:build !windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"

	"github.com/pangxincheng/tunnel/internal/svc"
)

// checkPrivilege 在任何副作用之前检查运行身份:
//   - system 且非 root:有终端时返回经 sudo 重新执行自身的命令(参数原样),否则报错;
//   - user 且是 root:报错(会装进 root 自己的登录项,永远不会启动)。
func checkPrivilege(mode svc.Mode, euid int, isTerminal bool, args []string) ([]string, error) {
	switch {
	case mode == svc.ModeSystem && euid != 0:
		if !isTerminal {
			return nil, errors.New("--mode system 需要 root 权限,请用 sudo 运行")
		}
		// 用绝对路径:sudo 的 secure_path 里未必能找到 os.Args[0]
		exe, err := os.Executable()
		if err != nil {
			exe = os.Args[0]
		}
		return append([]string{"sudo", "--", exe}, args...), nil
	case mode == svc.ModeUser && euid == 0:
		return nil, errors.New("--mode user 不能以 root 运行(会装进 root 自己的登录项,永远不会启动)")
	}
	return nil, nil
}

// execReplace 用 argv 替换当前进程(sudo 重新执行)。
func execReplace(argv []string) error {
	p, err := exec.LookPath(argv[0])
	if err != nil {
		return fmt.Errorf("找不到 %s:%w", argv[0], err)
	}
	return syscall.Exec(p, argv, os.Environ())
}

// detachConsole 仅 Windows 需要;Unix 无操作。
func detachConsole() {}

// serviceLogOutput 返回 run-service 的日志输出。Unix 下由 systemd(journal)或
// launchd(StandardErrorPath)接管 stderr,直接写 stderr。
func serviceLogOutput(string) (io.Writer, func(), error) { return os.Stderr, func() {}, nil }
