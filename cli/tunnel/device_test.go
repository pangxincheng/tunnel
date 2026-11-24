package tunnel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestDeriveDeviceIDStable(t *testing.T) {
	a := deriveDeviceID("abc\n")
	if a != deriveDeviceID("abc") {
		t.Fatal("应忽略首尾空白")
	}
	if len(a) != 32 {
		t.Fatalf("应为 32 位十六进制,got %q", a)
	}
	if a == deriveDeviceID("abd") {
		t.Fatal("不同输入应得到不同 ID")
	}
	sum := sha256.Sum256([]byte("tunnel-device-id:abc"))
	if a != hex.EncodeToString(sum[:16]) {
		t.Fatal("派生公式与规范不符")
	}
}

func TestDeviceIDOnThisMachine(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("该平台不支持读取机器标识")
	}
	a, err := DeviceID()
	if err != nil {
		t.Fatalf("DeviceID: %v", err)
	}
	b, err := DeviceID()
	if err != nil {
		t.Fatalf("DeviceID 第二次: %v", err)
	}
	if a != b || len(a) != 32 {
		t.Fatalf("两次结果应相同且为 32 位: %q vs %q", a, b)
	}
}

func TestLocalDeviceInfo(t *testing.T) {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
	default:
		t.Skip("该平台不支持读取机器标识")
	}
	started := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	info, err := LocalDeviceInfo("nas", started)
	if err != nil {
		t.Fatalf("LocalDeviceInfo: %v", err)
	}
	want, _ := DeviceID()
	if info.DeviceID != want || info.Name != "nas" || info.Arch != runtime.GOARCH ||
		info.TunnelVersion != Version || info.OS == "" || !info.StartedAt.Equal(started) {
		t.Fatalf("字段不正确: %+v", info)
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	exp := "arch,device_id,hostname,name,os,os_version,started_at,tunnel_version"
	if strings.Join(keys, ",") != exp {
		t.Fatalf("JSON 键集合不符: %v", keys)
	}
	if m["started_at"] != "2026-11-22T10:00:00Z" {
		t.Fatalf("started_at 应为 RFC 3339: %v", m["started_at"])
	}
}
