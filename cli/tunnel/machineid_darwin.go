package tunnel

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
)

var platformUUIDRe = regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([^"]+)"`)

// machineID 解析 ioreg 输出中的 IOPlatformUUID。
func machineID() (string, error) {
	out, err := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return "", fmt.Errorf("执行 ioreg 失败: %w", err)
	}
	m := platformUUIDRe.FindSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("ioreg 输出中未找到 IOPlatformUUID")
	}
	return string(m[1]), nil
}

func osVersion() string {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
