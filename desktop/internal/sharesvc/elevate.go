package sharesvc

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

var safeArg = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)

// shellQuote 对 argv 元素做 shell 转义:安全字符原样保留,否则用单引号包裹(' → '\”)。
func shellQuote(a string) string {
	if safeArg.MatchString(a) {
		return a
	}
	return "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
}

// appleScriptString 把 s 嵌入 AppleScript 字符串字面量(转义 \ 和 ")。
func appleScriptString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

// elevated 以管理员权限运行 CLI(args 不含可执行文件)。
func (s *Service) elevated(ctx context.Context, what string, args []string) error {
	var name string
	var full []string
	switch s.goos {
	case "darwin":
		parts := make([]string, 0, len(args)+1)
		for _, a := range append([]string{s.bin}, args...) {
			parts = append(parts, shellQuote(a))
		}
		script := "do shell script " + appleScriptString(strings.Join(parts, " ")) + " with administrator privileges"
		name, full = "osascript", []string{"-e", script}
	case "linux":
		name, full = "pkexec", append([]string{s.bin}, args...)
	default:
		return fmt.Errorf("system mode is not supported on %s", s.goos)
	}
	_, errOut, exit, err := s.runner.Run(ctx, name, full...)
	if err != nil {
		return err
	}
	if exit == 0 {
		return nil
	}
	if s.goos == "darwin" && strings.Contains(string(errOut), "(-128)") {
		return ErrCancelled
	}
	if s.goos == "linux" && (exit == 126 || exit == 127) {
		return ErrCancelled
	}
	return cliError(what, exit, errOut)
}
