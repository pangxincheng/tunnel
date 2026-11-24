//go:build !darwin && !linux && !windows

package tunnel

import "fmt"

func machineID() (string, error) {
	return "", fmt.Errorf("不支持的平台")
}

func osVersion() string { return "" }
