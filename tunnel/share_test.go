package tunnel

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testDeviceID = "dev-local"

type shareEnv struct {
	*sessionEnv
	dir       string // VPS 上的 socket 目录(测试服务器的假 $HOME 下)
	statePath string
}

func newShareEnv(t *testing.T) *shareEnv {
	t.Helper()
	e := newSessionEnv(t)
	dir := filepath.Join(e.server.Home, ".tunnel", "sockets")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return &shareEnv{sessionEnv: e, dir: dir, statePath: filepath.Join(t.TempDir(), "state", "state.json")}
}

func (e *shareEnv) newShare(t *testing.T, name string, opts ...SessionOption) *Share {
	t.Helper()
	s, err := NewShare(ShareConfig{
		Conn: *e.cfg, Name: name, Target: e.echo, StatePath: e.statePath, DeviceID: testDeviceID,
	}, opts...)
	if err != nil {
		t.Fatalf("NewShare: %v", err)
	}
	s.keepaliveInterval = 50 * time.Millisecond
	s.reconnectMin = 10 * time.Millisecond
	s.reconnectMax = 50 * time.Millisecond
	return s
}

func (e *shareEnv) sock(name string) string { return filepath.Join(e.dir, name+".sock") }
func (e *shareEnv) info(name string) string { return filepath.Join(e.dir, name+".json") }

func (e *shareEnv) readInfo(t *testing.T, name string) (DeviceInfo, bool) {
	t.Helper()
	data, err := os.ReadFile(e.info(name))
	if err != nil {
		return DeviceInfo{}, false
	}
	var info DeviceInfo
	if json.Unmarshal(data, &info) != nil {
		return DeviceInfo{}, false
	}
	return info, true
}

// runShare 在后台运行 s,测试结束时取消并确认 Run 返回 nil。
func runShare(t *testing.T, s *Share) <-chan error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan error, 1)
	go func() { ch <- s.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-ch:
			if err != nil {
				t.Errorf("Run: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("cancel 后 Run 未退出")
		}
	})
	return ch
}

func waitSharePhase(t *testing.T, s *Share, p Phase) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.Status().Phase == p {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("等待 phase=%s 超时,当前 %+v", p, s.Status())
}

// echoViaUnix 经 VPS 上的 socket 发一行并期望收到回显。
func echoViaUnix(sock string) bool {
	conn, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	fmt.Fprintln(conn, "ping")
	got, _ := bufio.NewReader(conn).ReadString('\n')
	return got == "echo: ping\n"
}

func TestShareWritesInfoAndForwards(t *testing.T) {
	e := newShareEnv(t)
	var hooked []Phase
	var hookMu = make(chan struct{}, 1)
	hookMu <- struct{}{}
	s := e.newShare(t, "nas", WithStateHook(func(st ConnState) {
		<-hookMu
		hooked = append(hooked, st.Phase)
		hookMu <- struct{}{}
	}))
	ch := runShare(t, s)
	waitSharePhase(t, s, PhaseReady)

	info, ok := e.readInfo(t, "nas")
	if !ok || info.DeviceID != testDeviceID || info.Name != "nas" {
		t.Fatalf("nas.json = %+v (ok=%v)", info, ok)
	}
	st := s.Status()
	if info.StartedAt.IsZero() || !info.StartedAt.Equal(st.Since) {
		t.Fatalf("StartedAt %v 应等于 Since %v", info.StartedAt, st.Since)
	}
	if st.Name != "nas" || st.Socket != e.sock("nas") || st.Paused || st.Version != Version {
		t.Fatalf("Status = %+v", st)
	}
	if !echoViaUnix(e.sock("nas")) {
		t.Fatal("经 socket 未收到回显")
	}
	<-hookMu
	if len(hooked) < 2 || hooked[0] != PhaseConnecting || hooked[len(hooked)-1] != PhaseReady {
		t.Errorf("hook phases = %v", hooked)
	}
	hookMu <- struct{}{}

	// 断线后自动重连并恢复
	e.server.CloseAllClients()
	waitFor(t, 3*time.Second, func() bool { return s.Status().Phase == PhaseReady && echoViaUnix(e.sock("nas")) })
	select {
	case err := <-ch:
		t.Fatalf("Run 提前返回: %v", err)
	default:
	}
}

func TestShareStatusJSON(t *testing.T) {
	st := ShareStatus{ConnState: ConnState{Phase: PhaseReady, Attempt: 1}, Name: "nas", Socket: "/s/nas.sock", Paused: false, Version: "v1"}
	data, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(data, &m)
	for _, k := range []string{"phase", "err", "attempt", "name", "socket", "since", "paused", "version"} {
		if _, ok := m[k]; !ok {
			t.Errorf("JSON 缺少键 %q: %s", k, data)
		}
	}
	if _, ok := m["retry_at"]; ok {
		t.Errorf("零值 retry_at 不应输出: %s", data)
	}
}

func TestShareNameTakenByOtherDevice(t *testing.T) {
	e := newShareEnv(t)
	other := DeviceInfo{DeviceID: "other", Name: "nas", Hostname: "other-host"}
	data, _ := json.Marshal(other)
	if err := os.WriteFile(e.info("nas"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	s := e.newShare(t, "nas")
	ch := runShare(t, s)
	waitSharePhase(t, s, PhaseFailed)
	st := s.Status()
	if !strings.Contains(st.Err, "已被另一台设备") || !strings.Contains(st.Err, "other-host") {
		t.Fatalf("Err = %q", st.Err)
	}
	if got, _ := e.readInfo(t, "nas"); got.DeviceID != "other" {
		t.Fatalf("他人的 nas.json 被覆盖: %+v", got)
	}
	time.Sleep(100 * time.Millisecond)
	select {
	case err := <-ch:
		t.Fatalf("Run 不应因不可恢复错误返回: %v", err)
	default:
	}
	if s.Status().Phase != PhaseFailed {
		t.Fatalf("应停在 failed,当前 %+v", s.Status())
	}
}

func TestNameTakenErrorIsPermanent(t *testing.T) {
	err := error(&NameTakenError{Name: "nas", Owner: DeviceInfo{Hostname: "h"}})
	if !IsPermanent(err) {
		t.Fatal("NameTakenError 应为不可恢复错误")
	}
	if want := "「nas」已被另一台设备(h)使用,换一个设备名字"; err.Error() != want {
		t.Fatalf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestShareNameCheckIgnoresUnownedInfo(t *testing.T) {
	for name, content := range map[string]string{
		"corrupt":  "{",
		"noDevice": `{"name":"nas","hostname":"old"}`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newShareEnv(t)
			if err := os.WriteFile(e.info("nas"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			s := e.newShare(t, "nas")
			runShare(t, s)
			waitSharePhase(t, s, PhaseReady)
			if info, ok := e.readInfo(t, "nas"); !ok || info.DeviceID != testDeviceID {
				t.Fatalf("应覆盖为本机信息: %+v (ok=%v)", info, ok)
			}
		})
	}
}

func TestSharePausePersists(t *testing.T) {
	e := newShareEnv(t)
	s := e.newShare(t, "nas")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	waitSharePhase(t, s, PhaseReady)

	if err := s.Pause(); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if !s.Status().Paused {
		t.Fatal("Pause 后 Status().Paused 应立即为 true")
	}
	waitSharePhase(t, s, PhasePaused)
	waitFor(t, 3*time.Second, func() bool { return !echoViaUnix(e.sock("nas")) })
	fi, err := os.Stat(e.statePath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("状态文件权限 = %v", fi.Mode().Perm())
	}
	// 暂停不删除本机信息文件
	if _, ok := e.readInfo(t, "nas"); !ok {
		t.Fatal("暂停不应删除 nas.json")
	}
	// Retry 在暂停时无效
	s.Retry()
	time.Sleep(50 * time.Millisecond)
	if p := s.Status().Phase; p != PhasePaused {
		t.Fatalf("暂停时 Retry 不应连接,phase=%s", p)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 同一 StatePath 重建:仍然暂停,不连接
	s2 := e.newShare(t, "nas")
	if st := s2.Status(); !st.Paused || st.Phase != PhasePaused {
		t.Fatalf("新建后应为 paused: %+v", st)
	}
	runShare(t, s2)
	time.Sleep(100 * time.Millisecond)
	if st := s2.Status(); st.Phase != PhasePaused {
		t.Fatalf("重建后应保持 paused: %+v", st)
	}
	if echoViaUnix(e.sock("nas")) {
		t.Fatal("暂停时 socket 不应可用")
	}
	if err := s2.Resume(); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitSharePhase(t, s2, PhaseReady)
	if s2.Status().Paused {
		t.Fatal("Resume 后 Paused 应为 false")
	}
	if !echoViaUnix(e.sock("nas")) {
		t.Fatal("Resume 后经 socket 未收到回显")
	}
	data, _ := os.ReadFile(e.statePath)
	var ps struct{ Paused bool }
	if json.Unmarshal(data, &ps) != nil || ps.Paused {
		t.Fatalf("状态文件 = %s", data)
	}
}

func TestSharePauseResumeRapid(t *testing.T) {
	e := newShareEnv(t)
	s := e.newShare(t, "nas")
	runShare(t, s)
	waitSharePhase(t, s, PhaseReady)
	for i := 0; i < 5; i++ {
		if err := s.Pause(); err != nil {
			t.Fatal(err)
		}
		if err := s.Resume(); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 3*time.Second, func() bool { return s.Status().Phase == PhaseReady && echoViaUnix(e.sock("nas")) })
	if err := s.Pause(); err != nil {
		t.Fatal(err)
	}
	waitSharePhase(t, s, PhasePaused)
}

func TestShareRetryAfterFailure(t *testing.T) {
	e := newShareEnv(t)
	data, _ := json.Marshal(DeviceInfo{DeviceID: "other", Name: "nas", Hostname: "h"})
	if err := os.WriteFile(e.info("nas"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	s := e.newShare(t, "nas")
	runShare(t, s)
	waitSharePhase(t, s, PhaseFailed)
	if err := os.Remove(e.info("nas")); err != nil {
		t.Fatal(err)
	}
	s.Retry()
	waitSharePhase(t, s, PhaseReady)
	if !echoViaUnix(e.sock("nas")) {
		t.Fatal("Retry 后经 socket 未收到回显")
	}
}

func TestShareSetNameRemovesOldInfo(t *testing.T) {
	e := newShareEnv(t)
	s := e.newShare(t, "nas")
	runShare(t, s)
	waitSharePhase(t, s, PhaseReady)
	if _, ok := e.readInfo(t, "nas"); !ok {
		t.Fatal("nas.json 未写入")
	}
	if err := s.SetName("bad/name"); err == nil {
		t.Fatal("非法名字应报错")
	}
	if err := s.SetName("nas2"); err != nil {
		t.Fatalf("SetName: %v", err)
	}
	if got := s.Status().Name; got != "nas2" {
		t.Fatalf("Status().Name = %q", got)
	}
	waitFor(t, 3*time.Second, func() bool {
		_, ok := e.readInfo(t, "nas2")
		return s.Status().Phase == PhaseReady && ok && echoViaUnix(e.sock("nas2"))
	})
	if _, err := os.Stat(e.info("nas")); !os.IsNotExist(err) {
		t.Fatalf("nas.json 应被删除: %v", err)
	}
	if st := s.Status(); st.Socket != e.sock("nas2") {
		t.Fatalf("Socket = %q", st.Socket)
	}
}

func TestShareSetNameWhilePaused(t *testing.T) {
	e := newShareEnv(t)
	s := e.newShare(t, "nas")
	runShare(t, s)
	waitSharePhase(t, s, PhaseReady)
	if err := s.Pause(); err != nil {
		t.Fatal(err)
	}
	waitSharePhase(t, s, PhasePaused)
	if err := s.SetName("nas2"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if p := s.Status().Phase; p != PhasePaused {
		t.Fatalf("暂停时改名不应连接,phase=%s", p)
	}
	if err := s.Resume(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool {
		_, ok := e.readInfo(t, "nas2")
		return s.Status().Phase == PhaseReady && ok
	})
	if _, err := os.Stat(e.info("nas")); !os.IsNotExist(err) {
		t.Fatalf("nas.json 应被删除: %v", err)
	}
}

func TestShareSetNameDoesNotRemoveOthersInfo(t *testing.T) {
	e := newShareEnv(t)
	s := e.newShare(t, "nas")
	runShare(t, s)
	waitSharePhase(t, s, PhaseReady)
	// 模拟期间别的设备接管了 nas
	data, _ := json.Marshal(DeviceInfo{DeviceID: "other", Name: "nas", Hostname: "h"})
	if err := os.WriteFile(e.info("nas"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.SetName("nas2"); err != nil {
		t.Fatal(err)
	}
	waitSharePhase(t, s, PhaseReady)
	waitFor(t, 3*time.Second, func() bool { _, ok := e.readInfo(t, "nas2"); return ok })
	if got, ok := e.readInfo(t, "nas"); !ok || got.DeviceID != "other" {
		t.Fatalf("他人的 nas.json 不应被删除: %+v", got)
	}
}

func TestNewShareValidates(t *testing.T) {
	if _, err := NewShare(ShareConfig{Name: "bad/name", Target: "127.0.0.1:22", DeviceID: "x"}); err == nil {
		t.Fatal("非法名字应报错")
	}
	if _, err := NewShare(ShareConfig{Name: "nas", DeviceID: "x"}); err == nil {
		t.Fatal("缺少 Target 应报错")
	}
	// StatePath 不存在 = 未暂停
	s, err := NewShare(ShareConfig{Name: "nas", Target: "127.0.0.1:22", DeviceID: "x",
		StatePath: filepath.Join(t.TempDir(), "none.json")})
	if err != nil {
		t.Fatal(err)
	}
	if st := s.Status(); st.Paused || st.Phase != PhaseConnecting {
		t.Fatalf("Status = %+v", st)
	}
}

// 旧名字残留的 socket(真实 sshd 撤销转发时不删除 socket 文件)在改名后应被删除。
func TestShareSetNameRemovesStaleOldSocket(t *testing.T) {
	e := newShareEnv(t)
	if err := os.MkdirAll(filepath.Dir(e.statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.statePath, []byte(`{"paused":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mine, _ := json.Marshal(DeviceInfo{DeviceID: testDeviceID, Name: "nas"})
	if err := os.WriteFile(e.info("nas"), mine, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.sock("nas"), nil, 0o600); err != nil { // 残留的旧 socket
		t.Fatal(err)
	}
	s := e.newShare(t, "nas")
	runShare(t, s)
	waitSharePhase(t, s, PhasePaused)
	if err := s.SetName("nas2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Resume(); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool {
		_, ok := e.readInfo(t, "nas2")
		return s.Status().Phase == PhaseReady && ok
	})
	if _, err := os.Stat(e.info("nas")); !os.IsNotExist(err) {
		t.Fatalf("nas.json 应被删除: %v", err)
	}
	if _, err := os.Stat(e.sock("nas")); !os.IsNotExist(err) {
		t.Fatalf("残留的 nas.sock 应被删除: %v", err)
	}
}

func TestShareKeepaliveReplyTimeout(t *testing.T) {
	e := newShareEnv(t)
	s := e.newShare(t, "nas")
	s.keepaliveInterval = 100 * time.Millisecond
	runShare(t, s)
	waitSharePhase(t, s, PhaseReady)
	e.server.IgnoreKeepalive.Store(true)
	start := time.Now()
	waitFor(t, 2*time.Second, func() bool { return s.Status().Phase != PhaseReady })
	if d := time.Since(start); d > time.Second {
		t.Fatalf("离开 ready 用时 %v", d)
	}
}

func TestShareRetryInReadyReconnects(t *testing.T) {
	e := newShareEnv(t)
	s := e.newShare(t, "nas")
	s.reconnectMin = 10 * time.Second
	s.reconnectMax = 10 * time.Second
	runShare(t, s)
	waitSharePhase(t, s, PhaseReady)
	n := e.server.conns()
	// 模拟半开连接:撤销远端监听的请求也不会有回复,强制重连不能因此卡住
	e.server.IgnoreKeepalive.Store(true)
	start := time.Now()
	s.Retry()
	waitFor(t, time.Second, func() bool { return e.server.conns() > n })
	e.server.IgnoreKeepalive.Store(false)
	waitSharePhase(t, s, PhaseReady)
	if d := time.Since(start); d > 1500*time.Millisecond {
		t.Fatalf("Retry 后重新就绪用时 %v", d)
	}
}
