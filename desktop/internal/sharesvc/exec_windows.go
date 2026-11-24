package sharesvc

import (
	"os/exec"
	"syscall"
)

const createNoWindow = 0x08000000

// hideWindow 避免 GUI 应用每次调用 CLI 都闪出控制台窗口。
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
