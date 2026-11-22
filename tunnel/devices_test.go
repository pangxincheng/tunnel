package tunnel

import (
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
