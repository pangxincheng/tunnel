package tunnel

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeviceInfoRoundTrip(t *testing.T) {
	server, client := dialTestServer(t)
	dir, err := ResolveSocketDir(client, "")
	if err != nil {
		t.Fatal(err)
	}
	want := DeviceInfo{
		DeviceID: "abc", Name: "nas", Hostname: "h", OS: "linux", OSVersion: "6.1",
		Arch: "amd64", TunnelVersion: "1.2.3", StartedAt: time.Now().Truncate(time.Second),
	}
	if err := WriteDeviceInfo(client, dir, want); err != nil {
		t.Fatalf("WriteDeviceInfo: %v", err)
	}
	// 覆盖写一次,确认重命名可替换已有文件
	if err := WriteDeviceInfo(client, dir, want); err != nil {
		t.Fatalf("WriteDeviceInfo(覆盖): %v", err)
	}
	got, err := ReadDeviceInfos(client, dir)
	if err != nil {
		t.Fatalf("ReadDeviceInfos: %v", err)
	}
	g, ok := got["nas"]
	if !ok || len(got) != 1 {
		t.Fatalf("map = %+v", got)
	}
	if g.DeviceID != want.DeviceID || g.Name != want.Name || g.Hostname != want.Hostname ||
		g.OS != want.OS || g.OSVersion != want.OSVersion || g.Arch != want.Arch ||
		g.TunnelVersion != want.TunnelVersion || !g.StartedAt.Equal(want.StartedAt) {
		t.Fatalf("got %+v want %+v", g, want)
	}
	if tmps, _ := filepath.Glob(filepath.Join(server.Home, ".tunnel", "sockets", "*.tmp")); len(tmps) != 0 {
		t.Fatalf("残留临时文件: %v", tmps)
	}
}

func TestReadDeviceInfosCorruptFile(t *testing.T) {
	server, client := dialTestServer(t)
	dir, err := ResolveSocketDir(client, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = server
	got, err := ReadDeviceInfos(client, dir)
	if err != nil {
		t.Fatalf("ReadDeviceInfos: %v", err)
	}
	b, ok := got["bad"]
	if !ok || b.DeviceID != "" || b.Name != "bad" {
		t.Fatalf("got %+v", got)
	}
}

func TestRemoveDeviceInfoOnlyOwn(t *testing.T) {
	_, client := dialTestServer(t)
	dir, err := ResolveSocketDir(client, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteDeviceInfo(client, dir, DeviceInfo{DeviceID: "mine", Name: "nas"}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "nas.json")
	if err := RemoveDeviceInfo(client, dir, "nas", "other"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("他人的文件不应被删: %v", err)
	}
	if err := RemoveDeviceInfo(client, dir, "nas", "mine"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("自己的文件应被删: %v", err)
	}
	if err := RemoveDeviceInfo(client, dir, "nas", "mine"); err != nil {
		t.Fatalf("不存在应返回 nil: %v", err)
	}
}

func TestResolveSocketDirFallsBackToSFTP(t *testing.T) {
	server, client := dialTestServer(t)
	server.DenyExec = true
	got, err := ResolveSocketDir(client, "")
	if err != nil {
		t.Fatalf("ResolveSocketDir: %v", err)
	}
	want := server.Home + "/.tunnel/sockets"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	st, err := os.Stat(want)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("stat = %v, %v", st, err)
	}
	if d, err := ResolveSocketDir(client, "/x/y"); err != nil || d != "/x/y" {
		t.Fatalf("显式目录: %q %v", d, err)
	}
}

func TestResolveSocketDirBothDenied(t *testing.T) {
	server, client := dialTestServer(t)
	server.DenyExec = true
	server.DenySFTP = true
	_, err := ResolveSocketDir(client, "")
	var sde *SocketDirError
	if !errors.As(err, &sde) || !IsPermanent(err) {
		t.Fatalf("err = %v", err)
	}
}

// SFTP 兜底因连接中断失败属瞬时错误:不得被当作不可恢复的 SocketDirError,
// 否则监督循环会停止重试。
func TestResolveSocketDirSFTPTransientFailure(t *testing.T) {
	server, client := dialTestServer(t)
	server.DenyExec = true
	server.DropSFTP = true
	_, err := ResolveSocketDir(client, "")
	if err == nil {
		t.Fatal("应报错")
	}
	var sde *SocketDirError
	if IsPermanent(err) || errors.As(err, &sde) {
		t.Fatalf("瞬时失败不应为不可恢复错误: %v", err)
	}
}

func TestRemoveDeviceOnlyOwn(t *testing.T) {
	_, client := dialTestServer(t)
	dir, err := ResolveSocketDir(client, "")
	if err != nil {
		t.Fatal(err)
	}
	touch := func(name string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	// 他人的:json 与 sock 都保留
	if err := WriteDeviceInfo(client, dir, DeviceInfo{DeviceID: "other", Name: "a"}); err != nil {
		t.Fatal(err)
	}
	touch("a.sock")
	if err := RemoveDevice(client, dir, "a", "mine"); err != nil {
		t.Fatal(err)
	}
	if !exists("a.json") || !exists("a.sock") {
		t.Fatal("他人的设备文件不应被删")
	}
	// 无 json(无法确认归属):sock 保留
	touch("b.sock")
	if err := RemoveDevice(client, dir, "b", "mine"); err != nil {
		t.Fatal(err)
	}
	if !exists("b.sock") {
		t.Fatal("无法确认归属的 socket 不应被删")
	}
	// 自己的:json 与 sock 都删除;sock 不存在也不报错
	if err := WriteDeviceInfo(client, dir, DeviceInfo{DeviceID: "mine", Name: "c"}); err != nil {
		t.Fatal(err)
	}
	touch("c.sock")
	if err := RemoveDevice(client, dir, "c", "mine"); err != nil {
		t.Fatal(err)
	}
	if exists("c.json") || exists("c.sock") {
		t.Fatal("自己的设备文件应被删")
	}
	if err := WriteDeviceInfo(client, dir, DeviceInfo{DeviceID: "mine", Name: "d"}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveDevice(client, dir, "d", "mine"); err != nil || exists("d.json") {
		t.Fatalf("无 sock 时应只删 json: err=%v", err)
	}
}
