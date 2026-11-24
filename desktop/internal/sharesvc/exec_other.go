//go:build !windows

package sharesvc

import "os/exec"

func hideWindow(*exec.Cmd) {}
