package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mkSockDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("/tmp", "tunnel-dev-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

func TestListDevicesOnlineAndOffline(t *testing.T) {
	_, client := dialTestServer(t)
	dir := mkSockDir(t)
	echo := startEchoServer(t)
	if _, err := ReverseForwardUnix(dir, "macmini", echo)(client); err != nil {
		t.Fatal(err)
	}
	if err := WriteDeviceInfo(client, dir, DeviceInfo{DeviceID: "id1", Name: "macmini"}); err != nil {
		t.Fatal(err)
	}
	if err := WriteDeviceInfo(client, dir, DeviceInfo{DeviceID: "id2", Name: "pi"}); err != nil {
		t.Fatal(err)
	}
	got, err := ListDevices(t.Context(), client, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "macmini" || !got[0].Online || got[0].DeviceID != "id1" ||
		got[1].Name != "pi" || got[1].Online {
		t.Fatalf("got %+v", got)
	}
}

func TestListDevicesSocketWithoutInfo(t *testing.T) {
	_, client := dialTestServer(t)
	dir := mkSockDir(t)
	if _, err := ReverseForwardUnix(dir, "lab", startEchoServer(t))(client); err != nil {
		t.Fatal(err)
	}
	got, err := ListDevices(t.Context(), client, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "lab" || !got[0].Online || got[0].DeviceID != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestListDevicesStaleSocket(t *testing.T) {
	_, client := dialTestServer(t)
	dir := mkSockDir(t)
	if err := os.WriteFile(filepath.Join(dir, "old.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	got, err := ListDevices(t.Context(), client, dir)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("耗时 %v", time.Since(start))
	}
	if len(got) != 1 || got[0].Name != "old" || got[0].Online {
		t.Fatalf("got %+v", got)
	}
}

func TestListDevicesFilenameIsAuthoritative(t *testing.T) {
	_, client := dialTestServer(t)
	dir := mkSockDir(t)
	if _, err := ReverseForwardUnix(dir, "x", startEchoServer(t))(client); err != nil {
		t.Fatal(err)
	}
	// x.json 内的 name 与文件名不一致;empty.json 的 name 为空
	if err := os.WriteFile(filepath.Join(dir, "x.json"), []byte(`{"device_id":"d1","name":"other"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "empty.json"), []byte(`{"device_id":"d2"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ListDevices(t.Context(), client, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "empty" || got[0].Online || got[0].DeviceID != "d2" ||
		got[1].Name != "x" || !got[1].Online || got[1].DeviceID != "d1" {
		t.Fatalf("got %+v", got)
	}
}

// ctx 已取消时应返回 ctx.Err(),而不是「全部离线」且 err 为 nil。
func TestListDevicesCancelledContext(t *testing.T) {
	_, client := dialTestServer(t)
	dir := mkSockDir(t)
	if _, err := ReverseForwardUnix(dir, "macmini", startEchoServer(t))(client); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err := ListDevices(ctx, client, dir)
	if !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestDeviceJSONKeys(t *testing.T) {
	data, err := json.Marshal(Device{DeviceInfo: DeviceInfo{Name: "a"}, Online: true})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	if m["online"] != true {
		t.Fatalf("Online 应序列化为 online: %s", data)
	}
	if _, ok := m["Online"]; ok {
		t.Fatalf("不应出现 Online 键: %s", data)
	}
}

func TestRemoveOfflineDevice(t *testing.T) {
	_, client := dialTestServer(t)
	dir := mkSockDir(t)
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	// 离线:json 与残留 socket 都删除,不论属于哪台设备
	if err := WriteDeviceInfo(client, dir, DeviceInfo{DeviceID: "other", Name: "old"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "old.sock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveOfflineDevice(t.Context(), client, dir, "old"); err != nil {
		t.Fatal(err)
	}
	if exists("old.json") || exists("old.sock") {
		t.Fatal("离线设备的记录应被删除")
	}
	// 只有 json、没有 socket 也能删;都不存在不报错
	if err := WriteDeviceInfo(client, dir, DeviceInfo{DeviceID: "x", Name: "nosock"}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveOfflineDevice(t.Context(), client, dir, "nosock"); err != nil || exists("nosock.json") {
		t.Fatalf("err=%v", err)
	}
	if err := RemoveOfflineDevice(t.Context(), client, dir, "gone"); err != nil {
		t.Fatal(err)
	}
	// 在线:拒绝,什么都不删
	if _, err := ReverseForwardUnix(dir, "live", startEchoServer(t))(client); err != nil {
		t.Fatal(err)
	}
	if err := WriteDeviceInfo(client, dir, DeviceInfo{DeviceID: "y", Name: "live"}); err != nil {
		t.Fatal(err)
	}
	if err := RemoveOfflineDevice(t.Context(), client, dir, "live"); !errors.Is(err, ErrDeviceOnline) {
		t.Fatalf("err=%v, want ErrDeviceOnline", err)
	}
	if !exists("live.json") || !exists("live.sock") {
		t.Fatal("在线设备的记录不应被删")
	}
	// 非法名字(防路径穿越)
	if err := RemoveOfflineDevice(t.Context(), client, dir, "../x"); err == nil {
		t.Fatal("非法名字应报错")
	}
}
