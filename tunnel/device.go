package tunnel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"
)

// DeviceInfo 是写到 VPS 上的本机设备信息,字段名属于对外规范。
type DeviceInfo struct {
	DeviceID      string    `json:"device_id"`
	Name          string    `json:"name"`
	Hostname      string    `json:"hostname"`
	OS            string    `json:"os"`
	OSVersion     string    `json:"os_version"`
	Arch          string    `json:"arch"`
	TunnelVersion string    `json:"tunnel_version"`
	StartedAt     time.Time `json:"started_at"`
}

// deriveDeviceID 由原始机器标识派生设备 ID:hex(sha256("tunnel-device-id:"+raw)[:16])。
func deriveDeviceID(raw string) string {
	sum := sha256.Sum256([]byte("tunnel-device-id:" + strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:16])
}

// DeviceID 返回本机稳定的设备 ID。
func DeviceID() (string, error) {
	raw, err := machineID()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("机器标识为空")
	}
	return deriveDeviceID(raw), nil
}

// LocalDeviceInfo 收集本机设备信息。OSVersion 与 Hostname 尽力获取,取不到留空。
func LocalDeviceInfo(name string, startedAt time.Time) (DeviceInfo, error) {
	id, err := DeviceID()
	if err != nil {
		return DeviceInfo{}, fmt.Errorf("读取设备 ID 失败: %w", err)
	}
	hostname, _ := os.Hostname()
	osName := runtime.GOOS
	switch runtime.GOOS {
	case "darwin":
		osName = "macOS"
	case "linux":
		osName = "Linux"
	case "windows":
		osName = "Windows"
	}
	return DeviceInfo{
		DeviceID:      id,
		Name:          name,
		Hostname:      hostname,
		OS:            osName,
		OSVersion:     osVersion(),
		Arch:          runtime.GOARCH,
		TunnelVersion: Version,
		StartedAt:     startedAt,
	}, nil
}
