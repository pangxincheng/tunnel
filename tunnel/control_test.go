//go:build !windows

package tunnel

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// startControl 起 Share + 控制通道,返回 socket 地址与 onSetName 调用计数。
func startControl(t *testing.T, name string) (addr string, calls *atomic.Int32, s *Share) {
	t.Helper()
	e := newShareEnv(t)
	s = e.newShare(t, name)
	runShare(t, s)
	waitSharePhase(t, s, PhaseReady)

	dir, err := os.MkdirTemp("/tmp", "ctl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	addr = filepath.Join(dir, "sub", "control.sock")
	l, err := ControlListen(addr, -1)
	if err != nil {
		t.Fatalf("ControlListen: %v", err)
	}
	calls = new(atomic.Int32)
	ctx := t.Context()
	done := make(chan error, 1)
	go func() {
		done <- ServeControl(ctx, l, s, func(string) error { calls.Add(1); return nil })
	}()
	t.Cleanup(func() {
		// t.Context 在 Cleanup 之前已取消
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("ServeControl 未在 ctx 取消后退出")
		}
	})
	return addr, calls, s
}

func TestControlStatusPauseResume(t *testing.T) {
	addr, _, _ := startControl(t, "nas")
	resp, err := ControlCall(addr, ControlRequest{Op: "status"})
	if err != nil || !resp.OK || resp.Status == nil || resp.Status.Phase != PhaseReady || resp.Status.Name != "nas" {
		t.Fatalf("status = %+v, %v", resp, err)
	}
	resp, err = ControlCall(addr, ControlRequest{Op: "pause"})
	if err != nil || !resp.OK || resp.Status == nil || !resp.Status.Paused {
		t.Fatalf("pause = %+v, %v", resp, err)
	}
	resp, err = ControlCall(addr, ControlRequest{Op: "resume"})
	if err != nil || !resp.OK || resp.Status == nil || resp.Status.Paused {
		t.Fatalf("resume = %+v, %v", resp, err)
	}
	waitFor(t, 3*time.Second, func() bool {
		r, err := ControlCall(addr, ControlRequest{Op: "status"})
		return err == nil && r.Status != nil && r.Status.Phase == PhaseReady
	})
	resp, err = ControlCall(addr, ControlRequest{Op: "retry"})
	if err != nil || !resp.OK {
		t.Fatalf("retry = %+v, %v", resp, err)
	}
}

func TestControlSetName(t *testing.T) {
	addr, calls, s := startControl(t, "nas")
	resp, err := ControlCall(addr, ControlRequest{Op: "set-name", Name: "nas2"})
	if err != nil || !resp.OK || resp.Status == nil || resp.Status.Name != "nas2" {
		t.Fatalf("set-name = %+v, %v", resp, err)
	}
	if calls.Load() != 1 || s.Status().Name != "nas2" {
		t.Fatalf("calls=%d name=%s", calls.Load(), s.Status().Name)
	}
}

func TestControlSetNameInvalid(t *testing.T) {
	addr, calls, _ := startControl(t, "nas")
	resp, err := ControlCall(addr, ControlRequest{Op: "set-name", Name: "a/b"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK || !strings.Contains(resp.Error, "不合法") {
		t.Fatalf("resp = %+v", resp)
	}
	if calls.Load() != 0 {
		t.Fatal("非法名字不应调用 onSetName")
	}
}

func TestControlUnknownOp(t *testing.T) {
	addr, _, _ := startControl(t, "nas")
	resp, err := ControlCall(addr, ControlRequest{Op: "reboot"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK || !strings.Contains(resp.Error, "未知操作") {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestControlListenPermissions(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "ctl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	addr := filepath.Join(dir, "sub", "c.sock")
	l, err := ControlListen(addr, -1)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(addr)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode = %v, %v", fi, err)
	}
	if di, _ := os.Stat(filepath.Dir(addr)); di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v", di.Mode().Perm())
	}
	l.Close()
	// 残留 socket 文件可被替换
	if l2, err := ControlListen(addr, -1); err != nil {
		t.Fatalf("残留 socket 应被删除后重新监听: %v", err)
	} else {
		l2.Close()
	}
	// 残留的非 socket 文件不得被删除
	reg := filepath.Join(dir, "regular")
	os.WriteFile(reg, []byte("x"), 0o600)
	if _, err := ControlListen(reg, -1); err == nil {
		t.Fatal("普通文件不应被当作残留 socket 删除")
	}
	if _, err := os.Stat(reg); err != nil {
		t.Fatal("普通文件被删除了")
	}
}

func TestControlServerSurvivesStuckClient(t *testing.T) {
	addr, _, _ := startControl(t, "nas")
	c, err := net.Dial("unix", addr) // 连上但不发请求
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	resp, err := ControlCall(addr, ControlRequest{Op: "status"})
	if err != nil || !resp.OK {
		t.Fatalf("卡住的客户端不应阻塞其他请求: %+v, %v", resp, err)
	}
}

// user 模式(ownerUID < 0)的控制通道目录(如 /tmp/tunnel-<uid>)必须属于当前用户且权限恰为 0700:
// 缺失时以 0700 创建;属于他人、权限更宽或是符号链接时拒绝监听。
func TestControlListenPrivateDir(t *testing.T) {
	base, err := os.MkdirTemp("/tmp", "ctl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })

	// 缺失 → 创建为 0700
	addr := filepath.Join(base, "tunnel-1", "a.sock")
	l, err := ControlListen(addr, -1)
	if err != nil {
		t.Fatal(err)
	}
	l.Close()
	if fi, err := os.Lstat(filepath.Dir(addr)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("目录应为 0700: %v %v", fi.Mode(), err)
	}

	// 权限更宽 → 拒绝
	wide := filepath.Join(base, "wide")
	os.Mkdir(wide, 0o700)
	os.Chmod(wide, 0o755)
	if _, err := ControlListen(filepath.Join(wide, "a.sock"), -1); err == nil || !strings.Contains(err.Error(), "0700") {
		t.Fatalf("权限 0755 的目录应被拒绝: %v", err)
	}

	// 符号链接 → 拒绝
	link := filepath.Join(base, "link")
	os.Symlink(filepath.Dir(addr), link)
	if _, err := ControlListen(filepath.Join(link, "b.sock"), -1); err == nil {
		t.Fatal("符号链接目录应被拒绝")
	}

	// 属于其他用户 → 拒绝
	old := controlGeteuid
	controlGeteuid = func() int { return os.Geteuid() + 1 }
	t.Cleanup(func() { controlGeteuid = old })
	if _, err := ControlListen(filepath.Join(filepath.Dir(addr), "c.sock"), -1); err == nil || !strings.Contains(err.Error(), "属主") {
		t.Fatalf("属于其他用户的目录应被拒绝: %v", err)
	}
}
