package tunnel

import (
	"fmt"
	"os"
	"strings"
)

// machineID 读取 /etc/machine-id,失败时回退 /var/lib/dbus/machine-id。
func machineID() (string, error) {
	var firstErr error
	for _, p := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		b, err := os.ReadFile(p)
		if err == nil && strings.TrimSpace(string(b)) != "" {
			return strings.TrimSpace(string(b)), nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr == nil {
		firstErr = fmt.Errorf("文件为空")
	}
	return "", fmt.Errorf("读取机器标识失败: %w", firstErr)
}

func osVersion() string {
	b, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "PRETTY_NAME="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
