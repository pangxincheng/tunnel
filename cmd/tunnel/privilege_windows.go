//go:build windows

package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/pangxincheng/tunnel/internal/svc"
)

// checkPrivilege 在 Windows 上不检查:system 模式已由 svc.NewManager 拒绝,
// user 模式以当前用户运行即可。
func checkPrivilege(mode svc.Mode, euid int, isTerminal bool, args []string) ([]string, error) {
	return nil, nil
}

func execReplace([]string) error { return errors.New("Windows 不支持自动提权") }

// detachConsole 释放控制台:任务计划程序以控制台程序启动 tunnel.exe 时,
// 不让登录任务一直挂着一个控制台窗口(启动瞬间可能仍会闪一下)。
func detachConsole() {
	syscall.NewLazyDLL("kernel32.dll").NewProc("FreeConsole").Call()
}

// serviceLogOutput:任务计划程序不重定向输出,且 FreeConsole 后 stderr 已失效,
// 因此 run-service 自己把日志追加写入 logPath。
func serviceLogOutput(logPath string) (io.Writer, func(), error) {
	if logPath == "" {
		return io.Discard, func() {}, nil
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return f, func() { f.Close() }, nil
}
