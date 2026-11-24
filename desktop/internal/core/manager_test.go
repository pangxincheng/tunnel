package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pangxincheng/tunnel/cli/tunnel"
	"github.com/pangxincheng/tunnel/desktop/internal/config"
	"github.com/pangxincheng/tunnel/desktop/internal/sharesvc"
	"github.com/pangxincheng/tunnel/desktop/internal/sshd"
)

func withForwards(s config.Server, fw map[string]config.Forward) config.Server {
	s.Forwards = fw
	return s
}

func withShare(s config.Server, on bool, name string) config.Server {
	s.Share = config.ShareIntent{On: on, Name: name}
	return s
}

func TestAddServerAllocatesPortsOnDevicesSeen(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.m.AddServer(srvCfg("home")); err != nil {
		t.Fatal(err)
	}
	s := e.ready("home", dev("b", "id-b", false), dev("a", "id-a", true))
	if p := e.device("home", "a").Port; p != 5102 {
		t.Fatalf("a port %d", p)
	}
	if p := e.device("home", "b").Port; p != 5103 {
		t.Fatalf("b port %d", p)
	}
	s.setDevices(dev("a", "id-a", true), dev("b", "id-b", true))
	e.refreshNow("home", s)
	if e.device("home", "a").Port != 5102 || e.device("home", "b").Port != 5103 {
		t.Fatal("ports changed on second refresh")
	}
	fw := e.cfgServer("home").Forwards
	if fw["a"].Port != 5102 || fw["b"].Port != 5103 {
		t.Fatalf("config forwards %+v", fw)
	}
	v := e.view("home")
	if names := []string{v.Devices[0].Name, v.Devices[1].Name}; names[0] != "a" || names[1] != "b" {
		t.Fatalf("not sorted: %v", names)
	}
	if v.DevicesAt.IsZero() || v.Online != 2 {
		t.Fatalf("%+v", v)
	}
}

func TestSetForwardPortBusy(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	s := e.ready("home", dev("a", "id-a", true))
	s.setAddErr("a", &tunnel.LocalListenError{Addr: "127.0.0.1:5102", Cause: errors.New("in use")})
	if err := e.m.SetForward("home", "a", true); err == nil {
		t.Fatal("expected error")
	}
	d := e.device("home", "a")
	if d.On || d.Err != "port_busy" {
		t.Fatalf("%+v", d)
	}
	if e.cfgServer("home").Forwards["a"].On {
		t.Fatal("persisted On")
	}
	s.setAddErr("a", nil)
	if err := e.m.SetForward("home", "a", true); err != nil {
		t.Fatal(err)
	}
	if d := e.device("home", "a"); !d.On || d.Err != "" {
		t.Fatalf("%+v", d)
	}
}

func TestSetForwardOfflineRejected(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	s := e.ready("home", dev("b", "id-b", false))
	if err := e.m.SetForward("home", "b", true); !errors.Is(err, ErrDeviceOffline) {
		t.Fatalf("got %v", err)
	}
	if len(s.Calls()) != 0 {
		t.Fatalf("calls %v", s.Calls())
	}
	if err := e.m.SetForward("home", "zzz", true); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("got %v", err)
	}
	if err := e.m.SetForward("nope", "b", true); !errors.Is(err, ErrUnknownServer) {
		t.Fatalf("got %v", err)
	}
}

func TestForwardSurvivesDeviceOffline(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	s := e.ready("home", dev("a", "id-a", true))
	if err := e.m.SetForward("home", "a", true); err != nil {
		t.Fatal(err)
	}
	if v := e.view("home"); v.Connected != 1 {
		t.Fatalf("connected %d", v.Connected)
	}
	s.setDevices(dev("a", "id-a", false))
	e.refreshNow("home", s)
	v := e.view("home")
	if v.Connected != 0 || v.Online != 0 || !v.Devices[0].On {
		t.Fatalf("%+v", v)
	}
	if contains(s.Calls(), "remove a") {
		t.Fatal("RemoveForward called")
	}
}

func TestConnectedCountsOnlyLive(t *testing.T) {
	c := cfgWith(withForwards(srvCfg("home"), map[string]config.Forward{"c": {Port: 5200, On: true}}))
	e := newEnv(t, c)
	s := e.ready("home", dev("a", "id-a", true), dev("b", "id-b", true), dev("c", "id-c", false))
	if err := e.m.SetForward("home", "a", true); err != nil {
		t.Fatal(err)
	}
	v := e.view("home")
	if v.Connected != 1 || v.Online != 2 || v.Conn != "ready" {
		t.Fatalf("%+v", v)
	}
	s.setState(tunnel.ConnState{Phase: tunnel.PhaseWaiting, Err: "boom", RetryAt: t0})
	v = e.view("home")
	if v.Connected != 0 || v.Conn != "waiting" || v.ConnErr != "boom" || !v.RetryAt.Equal(t0) {
		t.Fatalf("%+v", v)
	}
}

// A forward that is on keeps its port: the UI locks the port while the switch
// is on, and the backend refuses the change too.
func TestSetPortRequiresForwardOff(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	s := e.ready("home", dev("a", "id-a", true))
	if err := e.m.SetForward("home", "a", true); err != nil {
		t.Fatal(err)
	}
	port := e.cfgServer("home").Forwards["a"].Port
	n := len(s.Calls())
	if err := e.m.SetPort("home", "a", 6000); !errors.Is(err, ErrForwardOn) {
		t.Fatalf("got %v, want ErrForwardOn", err)
	}
	if f := e.cfgServer("home").Forwards["a"]; f.Port != port || !f.On {
		t.Fatalf("forward changed: %+v", f)
	}
	if len(s.Calls()) != n {
		t.Fatalf("session touched: %v", s.Calls())
	}
}

func TestSetPortValidatesOffForward(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	s := e.ready("home", dev("a", "id-a", true), dev("b", "id-b", true))
	n := len(s.Calls())
	if err := e.m.SetPort("home", "a", 6000); err != nil {
		t.Fatal(err)
	}
	if f := e.cfgServer("home").Forwards["a"]; f.Port != 6000 || f.On {
		t.Fatalf("%+v", f)
	}
	if err := e.m.SetPort("home", "a", 80); err == nil || e.device("home", "a").Err != "port_invalid" {
		t.Fatalf("invalid: %v %+v", err, e.device("home", "a"))
	}
	if err := e.m.SetPort("home", "a", e.device("home", "b").Port); err == nil || e.device("home", "a").Err != "port_conflict" {
		t.Fatalf("conflict: %v %+v", err, e.device("home", "a"))
	}
	if e.cfgServer("home").Forwards["a"].Port != 6000 {
		t.Fatal("rejected port persisted")
	}
	if len(s.Calls()) != n {
		t.Fatalf("an off forward touched the session: %v", s.Calls())
	}
}

// A port another program already listens on is refused when it is chosen,
// not later when the switch is turned on.
func TestSetPortRejectsBusyPort(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	e.ready("home", dev("a", "id-a", true))
	port := e.device("home", "a").Port
	e.setBusy(7000)
	if err := e.m.SetPort("home", "a", 7000); !errors.Is(err, ErrPortBusy) {
		t.Fatalf("got %v, want ErrPortBusy", err)
	}
	if d := e.device("home", "a"); d.Err != "" || d.Port != port {
		t.Fatalf("%+v", d)
	}
	if err := e.m.SetPort("home", "a", 7001); err != nil {
		t.Fatal(err)
	}
	if d := e.device("home", "a"); d.Err != "" || d.Port != 7001 {
		t.Fatalf("%+v", d)
	}
}

func TestDisconnectPausesAndKeepsSwitches(t *testing.T) {
	c := cfgWith(withShare(withForwards(srvCfg("home"), map[string]config.Forward{"a": {Port: 5102, On: true}}), true, "mac"))
	e := newEnv(t, c, func(e *env) { e.share.installed("home", false, "mac") })
	s1 := e.ready("home", dev("a", "id-a", true))
	eventually(t, "running", s1.Running)
	if err := e.m.SetConnected("home", false); err != nil {
		t.Fatal(err)
	}
	if s1.Running() {
		t.Fatal("session still running")
	}
	cs := e.cfgServer("home")
	if !cs.Disconnected || !cs.Forwards["a"].On || !cs.Share.On {
		t.Fatalf("%+v", cs)
	}
	if !contains(e.share.Calls(), "pause home") {
		t.Fatalf("share calls %v", e.share.Calls())
	}
	v := e.view("home")
	if v.Conn != "disconnected" || v.Connected != 0 || !v.Devices[0].On {
		t.Fatalf("%+v", v)
	}
	if err := e.m.SetConnected("home", true); err != nil {
		t.Fatal(err)
	}
	s2 := e.session("home", 2)
	if !contains(s2.Calls(), "add a 5102") {
		t.Fatalf("calls %v", s2.Calls())
	}
	if !contains(e.share.Calls(), "resume home") {
		t.Fatalf("share calls %v", e.share.Calls())
	}
	if e.cfgServer("home").Disconnected {
		t.Fatal("still disconnected")
	}
}

func TestShareNameTaken(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), false, "mac")))
	e.ready("home", dev("mac", "other", true))
	if err := e.m.SetShare("home", true); !errors.Is(err, ErrNameTaken) {
		t.Fatalf("got %v", err)
	}
	if v := e.view("home"); v.Share.NameErr != "taken" || v.Share.On {
		t.Fatalf("%+v", v.Share)
	}
	for _, c := range e.share.Calls() {
		if strings.HasPrefix(c, "install") {
			t.Fatal("install called")
		}
	}
	// own device with the same name is fine
	if err := e.m.SetShareName("home", "mac2"); err != nil {
		t.Fatal(err)
	}
	if e.view("home").Share.NameErr != "" {
		t.Fatal("NameErr not cleared")
	}
	if err := e.m.SetShareName("home", "-bad"); err == nil || e.view("home").Share.NameErr != "invalid" {
		t.Fatalf("invalid name: %v", err)
	}
}

func TestShareNoSSHD(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), false, "mac")))
	e.setSSHD(sshd.Result{})
	if err := e.m.SetShare("home", true); !errors.Is(err, ErrNoSSHD) {
		t.Fatalf("got %v", err)
	}
	if len(e.share.Calls()) != 0 || e.cfgServer("home").Share.On {
		t.Fatalf("calls %v", e.share.Calls())
	}
	if e.m.State().SSHD.Running {
		t.Fatal("sshd state not updated")
	}
}

func TestShareInstallThenResume(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), false, "mac")))
	e.ready("home")
	if err := e.m.SetShare("home", true); err != nil {
		t.Fatal(err)
	}
	if got := e.share.Calls(); len(got) != 1 || got[0] != "install home name=mac system=false local=22" {
		t.Fatalf("calls %v", got)
	}
	e.waitView("home", "share ready", func(v ServerView) bool { return v.Share.Phase == "ready" && v.Share.On })
	if v := e.view("home"); v.Share.LogPath != "/log/home" || v.Share.Busy {
		t.Fatalf("%+v", v.Share)
	}
	if err := e.m.SetShare("home", false); err != nil {
		t.Fatal(err)
	}
	e.waitView("home", "share paused", func(v ServerView) bool { return v.Share.Phase == "paused" && !v.Share.On })
	if err := e.m.SetShare("home", true); err != nil {
		t.Fatal(err)
	}
	want := []string{"install home name=mac system=false local=22", "pause home", "resume home"}
	if got := e.share.Calls(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v", got)
	}
	if !e.cfgServer("home").Share.On {
		t.Fatal("not persisted")
	}
}

func TestShareOutdated(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), true, "mac")), func(e *env) {
		e.share.installed("home", false, "mac")
		e.share.svc["home"].version = "1.1.0"
	})
	e.waitView("home", "outdated", func(v ServerView) bool { return v.Share.Outdated })
	if err := e.m.UpdateShare("home"); err != nil {
		t.Fatal(err)
	}
	if !contains(e.share.Calls(), "install home name=mac system=false local=22") {
		t.Fatalf("calls %v", e.share.Calls())
	}
	e.waitView("home", "not outdated", func(v ServerView) bool { return !v.Share.Outdated })
}

func TestSetShareSystemCancelled(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), true, "mac")), func(e *env) {
		e.share.installed("home", false, "mac")
		e.share.installErrs = []error{sharesvc.ErrCancelled}
	})
	if err := e.m.SetShareSystem("home", true); !errors.Is(err, sharesvc.ErrCancelled) {
		t.Fatalf("got %v", err)
	}
	want := []string{"uninstall home system=false", "install home name=mac system=true local=22", "install home name=mac system=false local=22"}
	if got := e.share.Calls(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v", got)
	}
	if cs := e.cfgServer("home"); cs.Share.System || !cs.Share.On {
		t.Fatalf("%+v", cs.Share)
	}
	if e.view("home").Share.System {
		t.Fatal("view System changed")
	}
	// success path
	if err := e.m.SetShareSystem("home", true); err != nil {
		t.Fatal(err)
	}
	if !e.cfgServer("home").Share.System {
		t.Fatal("System not persisted")
	}
}

func TestRenameServerMigratesShare(t *testing.T) {
	c := cfgWith(withShare(withForwards(srvCfg("home"), map[string]config.Forward{"a": {Port: 5102, On: true}}), true, "mac"))
	e := newEnv(t, c, func(e *env) { e.share.installed("home", false, "mac") })
	s1 := e.session("home", 1)
	ns := srvCfg("lab")
	ns.Host = "home.example" // same connection params, new name
	if err := e.m.UpdateServer("home", ns); err != nil {
		t.Fatal(err)
	}
	want := []string{"uninstall home system=false", "install lab name=mac system=false local=22"}
	if got := e.share.Calls(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v", got)
	}
	cs := e.cfgServer("lab")
	if !cs.Forwards["a"].On || !cs.Share.On || cs.Share.Name != "mac" {
		t.Fatalf("%+v", cs)
	}
	if _, ok := e.cfg().Server("home"); ok {
		t.Fatal("old name kept")
	}
	if s1.Running() {
		t.Fatal("old session still running")
	}
	s2 := e.session("home", 2) // same host
	if !contains(s2.Calls(), "add a 5102") {
		t.Fatalf("calls %v", s2.Calls())
	}
	if err := e.m.UpdateServer("lab", srvCfg("-bad")); err == nil {
		t.Fatal("invalid name accepted")
	}
}

func TestDeleteServerUninstalls(t *testing.T) {
	c := cfgWith(withShare(srvCfg("home"), true, "mac"), srvCfg("work"))
	c.Selected = "home"
	e := newEnv(t, c, func(e *env) { e.share.installed("home", true, "mac") })
	s := e.session("home", 1)
	eventually(t, "running", s.Running)
	if err := e.m.DeleteServer("home"); err != nil {
		t.Fatal(err)
	}
	if got := e.share.Calls(); len(got) != 1 || got[0] != "uninstall home system=true" {
		t.Fatalf("calls %v", got)
	}
	cc := e.cfg()
	if _, ok := cc.Server("home"); ok || cc.Selected != "work" {
		t.Fatalf("%+v", cc)
	}
	if s.Running() {
		t.Fatal("session still running")
	}
	if len(e.m.State().Servers) != 1 {
		t.Fatal("still in state")
	}
}

func TestRestoreForwardsWhileUnreachable(t *testing.T) {
	c := cfgWith(withForwards(srvCfg("home"), map[string]config.Forward{"a": {Port: 5102, On: true}}))
	e := newEnv(t, c)
	s := e.session("home", 1)
	s.setState(tunnel.ConnState{Phase: tunnel.PhaseWaiting})
	if !contains(s.Calls(), "add a 5102") {
		t.Fatalf("calls %v", s.Calls())
	}

	c.Settings.RestoreForwards = false
	e2 := newEnv(t, c)
	s2 := e2.session("home", 1)
	if len(s2.Calls()) != 0 {
		t.Fatalf("calls %v", s2.Calls())
	}
	if e2.cfgServer("home").Forwards["a"].On {
		t.Fatal("On kept")
	}
}

func TestLastSeen(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	t1 := t0.Add(time.Hour)
	e.clock.Set(t1)
	s := e.ready("home", tunnel.Device{DeviceInfo: tunnel.DeviceInfo{Name: "a", DeviceID: "id-a", StartedAt: t0}, Online: true})
	if ls := e.cfgServer("home").LastSeen["a"]; !ls.Equal(t1) {
		t.Fatalf("last seen %v", ls)
	}
	t3 := t1.Add(30 * time.Minute)
	s.setDevices(
		tunnel.Device{DeviceInfo: tunnel.DeviceInfo{Name: "a", DeviceID: "id-a", StartedAt: t0}},
		tunnel.Device{DeviceInfo: tunnel.DeviceInfo{Name: "b", DeviceID: "id-b", StartedAt: t3}},
	)
	e.refreshNow("home", s) // drain in-flight refreshes that still saw a online
	e.clock.Set(t1.Add(time.Hour))
	e.refreshNow("home", s)
	if ls := e.device("home", "a").LastSeen; !ls.Equal(t1) {
		t.Fatalf("a last seen %v", ls)
	}
	if ls := e.device("home", "b").LastSeen; !ls.Equal(t3) {
		t.Fatalf("b last seen %v", ls)
	}
}

func TestRefreshStopsWhenDisconnected(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	s := e.ready("home", dev("a", "id-a", true))
	if err := e.m.SetConnected("home", false); err != nil {
		t.Fatal(err)
	}
	n := s.DevCalls()
	e.m.Refresh("home")
	time.Sleep(60 * time.Millisecond)
	if s.DevCalls() != n {
		t.Fatalf("devices fetched after disconnect: %d -> %d", n, s.DevCalls())
	}
}

func TestRefreshOnlyWhenReady(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	s := e.session("home", 1)
	time.Sleep(40 * time.Millisecond)
	if s.DevCalls() != 0 {
		t.Fatal("devices fetched while connecting")
	}
}

func TestOwnDeviceHidden(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	e.ready("home", dev("me", "self", true), dev("a", "id-a", true))
	v := e.view("home")
	if len(v.Devices) != 1 || v.Devices[0].Name != "a" || v.Online != 1 {
		t.Fatalf("%+v", v)
	}
	if _, ok := e.cfgServer("home").Forwards["me"]; ok {
		t.Fatal("port allocated for own device")
	}
}

func TestOnChangeCoalesced(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")), func(e *env) { e.refreshEvery, e.shareEvery = time.Hour, time.Hour })
	s := e.session("home", 1)
	eventually(t, "first change", func() bool { e.mu.Lock(); defer e.mu.Unlock(); return e.changes > 0 })
	time.Sleep(3 * coalesce) // let start-up notifications (initial share sync, sshd detect) drain
	e.mu.Lock()
	start := e.changes
	e.mu.Unlock()
	for i := 0; i < 200; i++ {
		s.setState(tunnel.ConnState{Phase: tunnel.PhaseConnecting, Attempt: i})
	}
	s.setState(tunnel.ConnState{Phase: tunnel.PhaseFailed, Err: "last"})
	eventually(t, "latest delivered", func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return len(e.lastSeen.Servers) == 1 && e.lastSeen.Servers[0].ConnErr == "last"
	})
	e.mu.Lock()
	n := e.changes - start
	e.mu.Unlock()
	if n > 5 {
		t.Fatalf("%d OnChange calls for one burst", n)
	}
}

func TestConcurrentOps(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), false, "mac"), srvCfg("work")))
	e.ready("home", dev("a", "id-a", true), dev("b", "id-b", true))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(4)
		go func() { defer wg.Done(); _ = e.m.SetForward("home", "a", i%2 == 0) }()
		go func() { defer wg.Done(); _ = e.m.SetShare("home", i%2 == 0) }()
		go func() { defer wg.Done(); e.m.Refresh("home"); _ = e.m.State() }()
		go func() { defer wg.Done(); _ = e.m.SetConnected("work", i%2 == 0) }()
	}
	wg.Wait()
}

// --- review fixes ---

func TestRenameRacesAddServer(t *testing.T) {
	c := cfgWith(withShare(srvCfg("home"), true, "mac"))
	e := newEnv(t, c, func(e *env) {
		e.share.installed("home", false, "mac")
		e.share.installBlock = make(chan struct{})
		e.share.installStarted = make(chan struct{}, 1)
	})
	done := make(chan error, 1)
	go func() {
		ns := srvCfg("lab")
		ns.Host = "home.example"
		done <- e.m.UpdateServer("home", ns)
	}()
	<-e.share.installStarted // rename is inside the slow reinstall
	if err := e.m.AddServer(srvCfg("lab")); err == nil {
		t.Fatal("AddServer took a name reserved by a pending rename")
	}
	if err := e.m.AddServer(srvCfg("home")); err == nil {
		t.Fatal("AddServer took the name being renamed away from")
	}
	close(e.share.installBlock)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range e.cfg().Servers {
		if s.Name == "lab" {
			n++
		}
	}
	if n != 1 || len(e.cfg().Servers) != 1 {
		t.Fatalf("servers %+v", e.cfg().Servers)
	}
	if got := e.sess.sessions("lab.example"); len(got) != 0 {
		t.Fatalf("orphaned session(s) for the rejected AddServer: %d", len(got))
	}
}

func TestShareReinstallNeedsSSHD(t *testing.T) {
	c := cfgWith(withShare(srvCfg("home"), true, "mac"))
	e := newEnv(t, c, func(e *env) {
		e.share.installed("home", false, "mac")
		e.sshd = sshd.Result{}
	})
	if err := e.m.SetShareSystem("home", true); !errors.Is(err, ErrNoSSHD) {
		t.Fatalf("got %v", err)
	}
	ns := srvCfg("lab")
	ns.Host = "home.example"
	if err := e.m.UpdateServer("home", ns); !errors.Is(err, ErrNoSSHD) {
		t.Fatalf("got %v", err)
	}
	if got := e.share.Calls(); len(got) != 0 {
		t.Fatalf("service touched: %v", got)
	}
	if cs := e.cfgServer("home"); cs.Share.System {
		t.Fatalf("%+v", cs.Share)
	}
}

func TestDeleteServerStatusError(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), true, "mac")), func(e *env) {
		e.share.statusErr = errors.New("tunnel CLI missing")
	})
	if err := e.m.DeleteServer("home"); err != nil {
		t.Fatal(err)
	}
	if len(e.cfg().Servers) != 0 || len(e.share.Calls()) != 0 {
		t.Fatalf("servers %v calls %v", e.cfg().Servers, e.share.Calls())
	}
}

func TestUpdateServerStatusError(t *testing.T) {
	c := cfgWith(withShare(srvCfg("home"), false, "mac"), withShare(srvCfg("work"), true, "mac"))
	e := newEnv(t, c, func(e *env) { e.share.statusErr = errors.New("tunnel CLI missing") })
	if err := e.m.UpdateServer("home", srvCfg("lab")); err != nil {
		t.Fatalf("share never on: %v", err)
	}
	if _, ok := e.cfg().Server("lab"); !ok {
		t.Fatal("rename not applied")
	}
	if err := e.m.UpdateServer("work", srvCfg("lab2")); err == nil {
		t.Fatal("share on: expected the status error")
	}
	if _, ok := e.cfg().Server("work"); !ok {
		t.Fatal("rename applied despite error")
	}
}

func TestDisconnectRetriesFailedPause(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), true, "mac")), func(e *env) {
		e.share.installed("home", false, "mac")
		e.share.pauseErrs = []error{errors.New("pause failed")}
	})
	// let the share loop see the running service first, so it doesn't pause it itself
	e.waitView("home", "share synced", func(v ServerView) bool { return v.Share.Phase == "ready" })
	if err := e.m.SetConnected("home", false); err == nil {
		t.Fatal("expected pause error")
	}
	if !e.cfgServer("home").Disconnected {
		t.Fatal("not disconnected")
	}
	if err := e.m.SetConnected("home", false); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range e.share.Calls() {
		if c == "pause home" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("calls %v", e.share.Calls())
	}
}

func TestParamChangeClearsDevices(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	e.ready("home", dev("a", "id-a", true))
	ns := srvCfg("home")
	ns.Host = "other.example"
	if err := e.m.UpdateServer("home", ns); err != nil {
		t.Fatal(err)
	}
	v := e.view("home")
	if len(v.Devices) != 0 || !v.DevicesAt.IsZero() {
		t.Fatalf("%+v", v)
	}
	if cs := e.cfgServer("home"); len(cs.Devices) != 0 || !cs.DevicesAt.IsZero() {
		t.Fatalf("cached devices kept: %+v", cs)
	}
}

func TestDevicesCachedAcrossRestart(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	t1 := t0.Add(time.Hour)
	e.clock.Set(t1)
	s := e.ready("home", tunnel.Device{DeviceInfo: tunnel.DeviceInfo{Name: "a", DeviceID: "id-a", StartedAt: t0}, Online: true},
		dev("b", "id-b", false), dev("me", "self", true))
	if err := e.m.SetForward("home", "a", true); err != nil {
		t.Fatal(err)
	}
	t2 := t1.Add(time.Minute)
	e.clock.Set(t2)
	e.refreshNow("home", s) // a still online: LastSeen=t2 kept in memory, written at shutdown

	e2 := e.reopen()
	v := e2.view("home")
	if len(v.Devices) != 2 || v.Devices[0].Name != "a" || v.Devices[1].Name != "b" {
		t.Fatalf("cached devices %+v", v.Devices)
	}
	a := v.Devices[0]
	if a.Online || !a.On || a.Port != 5102 || !a.LastSeen.Equal(t2) {
		t.Fatalf("a %+v", a)
	}
	if !v.DevicesAt.Equal(t2) || v.Online != 0 {
		t.Fatalf("%+v", v)
	}
}

func TestRefreshSavesOnlyOnChange(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	s := e.ready("home", dev("a", "id-a", true))
	e.m.mu.Lock()
	n := e.m.saves
	e.m.mu.Unlock()
	t1 := t0.Add(time.Hour)
	e.clock.Set(t1)
	e.refreshNow("home", s)
	e.refreshNow("home", s)
	e.m.mu.Lock()
	same := e.m.saves == n
	e.m.mu.Unlock()
	if !same {
		t.Fatal("config rewritten on an unchanged refresh")
	}
	s.setDevices(dev("a", "id-a", false))
	e.refreshNow("home", s) // drain any in-flight refresh that still saw a online
	e.clock.Set(t1.Add(time.Hour))
	e.refreshNow("home", s)
	if ls := e.cfgServer("home").LastSeen["a"]; !ls.Equal(t1) {
		t.Fatalf("last seen on disk %v, want %v", ls, t1)
	}
}

func TestRefreshAfterStop(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	e.cancel()
	e.m.wg.Wait()
	e.mu.Lock()
	n := e.detects
	e.mu.Unlock()
	e.m.Refresh("home")
	time.Sleep(20 * time.Millisecond)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.detects != n {
		t.Fatal("background work started after stop")
	}
}

// The edit dialog needs the stored connection settings to prefill the form
// and to keep the trusted host key when only the name changes.
func TestServerViewCarriesConnectionSettings(t *testing.T) {
	e := newEnv(t, nil)
	sc := srvCfg("home")
	sc.SocketDir = "/run/tunnel"
	if err := e.m.AddServer(sc); err != nil {
		t.Fatal(err)
	}
	v := e.view("home")
	if v.KeyPath != "/k" || v.HostKey != validHostKey || v.SocketDir != "/run/tunnel" {
		t.Fatalf("view %+v", v)
	}
}

func TestAddServerRejectsBadHostKey(t *testing.T) {
	for _, hk := range []string{"", "   ", "ssh-ed25519 AAA", "not a key"} {
		e := newEnv(t, nil)
		sc := srvCfg("home")
		sc.HostKey = hk
		if err := e.m.AddServer(sc); err == nil || !strings.Contains(err.Error(), "host_key") {
			t.Fatalf("host_key %q: err %v", hk, err)
		}
		if len(e.m.State().Servers) != 0 {
			t.Fatalf("host_key %q: server was added", hk)
		}
	}
}

func TestUpdateServerRejectsBadHostKey(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.m.AddServer(srvCfg("home")); err != nil {
		t.Fatal(err)
	}
	for _, hk := range []string{"", "ssh-ed25519 AAA", "not a key"} {
		ns := srvCfg("home")
		ns.HostKey = hk
		if err := e.m.UpdateServer("home", ns); err == nil || !strings.Contains(err.Error(), "host_key") {
			t.Fatalf("host_key %q: err %v", hk, err)
		}
	}
	if got := e.cfgServer("home").HostKey; got != validHostKey {
		t.Fatalf("stored host key changed: %q", got)
	}
}

// --- final review fixes ---

func TestShareOffWhileNotRunning(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), true, "mac")), func(e *env) {
		e.share.installed("home", false, "mac")
		e.share.setSvc("home", func(s *fakeSvc) { s.stopped = true })
	})
	if err := e.m.SetShare("home", false); err != nil {
		t.Fatalf("SetShare(off) on a stopped service: %v", err)
	}
	if sc := e.cfgServer("home"); sc.Share.On || !sc.Share.Decided {
		t.Fatalf("intent not persisted: %+v", sc.Share)
	}
	if v := e.view("home"); v.Share.On || v.Share.Phase != "not_running" {
		t.Fatalf("%+v", v.Share)
	}
}

func TestDisconnectWhileShareNotRunning(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), true, "mac")), func(e *env) {
		e.share.installed("home", false, "mac")
		e.share.setSvc("home", func(s *fakeSvc) { s.stopped = true })
	})
	if err := e.m.SetConnected("home", false); err != nil {
		t.Fatalf("SetConnected(false): %v", err)
	}
	if !e.cfgServer("home").Disconnected {
		t.Fatal("not disconnected")
	}
}

func TestShareOnWhileNotRunningKeepsIntent(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), false, "mac")), func(e *env) {
		e.share.installed("home", false, "mac")
		e.share.setSvc("home", func(s *fakeSvc) { s.stopped = true })
	})
	e.ready("home")
	if err := e.m.SetShare("home", true); err != nil {
		t.Fatalf("SetShare(on): %v", err)
	}
	if !e.cfgServer("home").Share.On {
		t.Fatal("intent not persisted")
	}
}

func TestSyncPausesServiceBackRunningWhileOff(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), true, "mac")), func(e *env) {
		e.share.installed("home", false, "mac")
		e.share.setSvc("home", func(s *fakeSvc) { s.stopped = true })
	})
	e.waitView("home", "not running", func(v ServerView) bool { return v.Share.Phase == "not_running" })
	if err := e.m.SetShare("home", false); err != nil {
		t.Fatal(err)
	}
	// the service manager restarts it, unpaused
	e.share.mu.Lock()
	e.share.pauseErrs = []error{errors.New("pause failed")}
	e.share.mu.Unlock()
	e.share.setSvc("home", func(s *fakeSvc) { s.stopped = false })
	eventually(t, "pause after coming back", func() bool { return e.share.count("pause home") == 1 })
	time.Sleep(60 * time.Millisecond) // several share ticks
	if n := e.share.count("pause home"); n != 1 {
		t.Fatalf("pause hammered: %d calls %v", n, e.share.Calls())
	}
	// next transition (stopped, then running again) re-applies once
	e.share.setSvc("home", func(s *fakeSvc) { s.stopped = true })
	e.waitView("home", "not running", func(v ServerView) bool { return v.Share.Phase == "not_running" })
	e.share.setSvc("home", func(s *fakeSvc) { s.stopped = false })
	e.waitView("home", "paused", func(v ServerView) bool { return v.Share.Phase == "paused" })
	if n := e.share.count("pause home"); n != 2 {
		t.Fatalf("calls %v", e.share.Calls())
	}
}

func TestSyncPausesWhenDisconnectedAndResumesWhenOn(t *testing.T) {
	c := cfgWith(withShare(srvCfg("home"), true, "mac"))
	c.Servers[0].Disconnected = true
	e := newEnv(t, c, func(e *env) { e.share.installed("home", false, "mac") })
	// running while the server is disconnected: paused on first sight
	e.waitView("home", "paused", func(v ServerView) bool { return v.Share.Phase == "paused" })
	if err := e.m.SetConnected("home", true); err != nil {
		t.Fatal(err)
	}
	e.waitView("home", "ready", func(v ServerView) bool { return v.Share.Phase == "ready" })
	// paused behind the app's back while the intent is on: resumed
	n := e.share.count("resume home")
	e.share.setSvc("home", func(s *fakeSvc) { s.paused = true })
	eventually(t, "resumed", func() bool { return e.share.count("resume home") == n+1 })
}

func TestShareViewFromInstalledService(t *testing.T) {
	c := cfgWith(withShare(srvCfg("home"), true, "old"))
	e := newEnv(t, c, func(e *env) { e.share.installed("home", true, "svcname") })
	e.waitView("home", "service name and mode", func(v ServerView) bool {
		return v.Share.Name == "svcname" && v.Share.System
	})
	eventually(t, "config follows the service", func() bool {
		sc := e.cfgServer("home")
		return sc.Share.Name == "svcname" && sc.Share.System
	})
	// switching back to user mode now reinstalls (config agrees with the service)
	if err := e.m.SetShareSystem("home", false); err != nil {
		t.Fatal(err)
	}
	if !contains(e.share.Calls(), "uninstall home system=true") {
		t.Fatalf("calls %v", e.share.Calls())
	}
}

func TestAdoptRunningServiceUnknownToConfig(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")), func(e *env) { e.share.installed("home", true, "mac") })
	e.waitView("home", "adopted", func(v ServerView) bool {
		return v.Share.On && v.Share.Name == "mac" && v.Share.System && v.Share.Phase == "ready"
	})
	sc := e.cfgServer("home")
	if !sc.Share.On || sc.Share.Name != "mac" || !sc.Share.System || !sc.Share.Decided {
		t.Fatalf("%+v", sc.Share)
	}
	if e.share.count("pause home") != 0 {
		t.Fatalf("calls %v", e.share.Calls())
	}
}

func TestRetryShareNotRunning(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), true, "mac")), func(e *env) {
		e.share.installed("home", false, "mac")
		e.share.setSvc("home", func(s *fakeSvc) { s.stopped = true })
	})
	// Installed but not running (e.g. unloaded by a failed update): retry
	// reinstalls it in its installed mode, which loads and starts it again.
	if err := e.m.RetryShare("home"); err != nil {
		t.Fatal(err)
	}
	if !contains(e.share.Calls(), "install home name=mac system=false local=22") {
		t.Fatalf("calls %v", e.share.Calls())
	}
	if contains(e.share.Calls(), "retry home") {
		t.Fatal("retry sent to a service that is not running")
	}
}

func TestRetryShareNotRunningSystemMode(t *testing.T) {
	e := newEnv(t, cfgWith(withShare(srvCfg("home"), true, "mac")), func(e *env) {
		e.share.installed("home", true, "mac")
		e.share.setSvc("home", func(s *fakeSvc) { s.stopped = true })
	})
	if err := e.m.RetryShare("home"); err != nil {
		t.Fatal(err)
	}
	if !contains(e.share.Calls(), "install home name=mac system=true local=22") {
		t.Fatalf("calls %v", e.share.Calls())
	}
}

func TestSetPortTypedErrors(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	e.ready("home", dev("a", "id-a", true), dev("b", "id-b", true))
	if err := e.m.SetPort("home", "a", 80); !errors.Is(err, config.ErrPortRange) {
		t.Fatalf("range: %v", err)
	}
	if err := e.m.SetPort("home", "a", e.device("home", "b").Port); !errors.Is(err, config.ErrPortConflict) {
		t.Fatalf("conflict: %v", err)
	}
}

func TestDefaultShareName(t *testing.T) {
	cases := map[string]string{
		"Xins-MacBook-Pro.local": "xins-macbook-pro",
		"My Laptop":              "my-laptop",
		"host.example.com":       "host.example.com",
		"-dash":                  "dash",
		"日本":                     "device",
		"":                       "device",
		strings.Repeat("a", 80):  strings.Repeat("a", 64),
	}
	for in, want := range cases {
		if got := defaultShareName(in); got != want {
			t.Errorf("defaultShareName(%q) = %q, want %q", in, got, want)
		}
		if err := tunnel.ValidateName(defaultShareName(in)); err != nil {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestAddServerDefaultsShareName(t *testing.T) {
	e := newEnv(t, nil)
	if err := e.m.AddServer(srvCfg("home")); err != nil {
		t.Fatal(err)
	}
	if n := e.cfgServer("home").Share.Name; n != "test-host" {
		t.Fatalf("share name %q", n)
	}
	s := srvCfg("work")
	s.Share.Name = "mine"
	if err := e.m.AddServer(s); err != nil {
		t.Fatal(err)
	}
	if n := e.cfgServer("work").Share.Name; n != "mine" {
		t.Fatalf("share name %q", n)
	}
}

func TestKeyPathTildeExpanded(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip(err)
	}
	s := withShare(srvCfg("home"), false, "mac")
	s.KeyPath = "~/.ssh/id"
	e := newEnv(t, cfgWith(s))
	want := filepath.Join(home, ".ssh", "id")
	if got := e.session("home", 1).cfg.KeyPath; got != want {
		t.Fatalf("session key %q want %q", got, want)
	}
	e.ready("home")
	if err := e.m.SetShare("home", true); err != nil {
		t.Fatal(err)
	}
	e.share.mu.Lock()
	got := e.share.lastInstall.KeyPath
	e.share.mu.Unlock()
	if got != want {
		t.Fatalf("install key %q want %q", got, want)
	}
	if e.cfgServer("home").KeyPath != "~/.ssh/id" {
		t.Fatal("stored key path changed")
	}
}

func TestWaitPersistsLastSeen(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	s := e.ready("home", dev("a", "id-a", true))
	t2 := t0.Add(2 * time.Hour)
	e.clock.Set(t2)
	e.refreshNow("home", s)
	if ls := e.cfgServer("home").LastSeen["a"]; ls.Equal(t2) {
		t.Fatal("precondition: LastSeen is saved only on shutdown")
	}
	e.cancel()
	e.m.Wait()
	if ls := e.cfgServer("home").LastSeen["a"]; !ls.Equal(t2) {
		t.Fatalf("last seen on disk %v want %v", ls, t2)
	}
}

func TestRemoveDevice(t *testing.T) {
	c := cfgWith(withForwards(srvCfg("home"), map[string]config.Forward{"old": {Port: 5200}}))
	e := newEnv(t, c)
	s := e.ready("home", dev("old", "id-old", false), dev("a", "id-a", true))
	if err := e.m.RemoveDevice("home", "old"); err != nil {
		t.Fatal(err)
	}
	if !contains(s.Calls(), "rmdev old") {
		t.Fatalf("calls %v", s.Calls())
	}
	v := e.view("home")
	if len(v.Devices) != 1 || v.Devices[0].Name != "a" {
		t.Fatalf("devices %+v", v.Devices)
	}
	sc := e.cfgServer("home")
	if _, ok := sc.Forwards["old"]; ok {
		t.Fatal("forward of removed device kept")
	}
	if _, ok := sc.LastSeen["old"]; ok {
		t.Fatal("last seen of removed device kept")
	}
	for _, d := range sc.Devices {
		if d.Name == "old" {
			t.Fatal("cached device list still has it")
		}
	}
}

func TestRemoveDeviceRejectsOnline(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	s := e.ready("home", dev("a", "id-a", true))
	if err := e.m.RemoveDevice("home", "a"); !errors.Is(err, ErrDeviceOnline) {
		t.Fatalf("err %v", err)
	}
	// Came back online after the last refresh: the server-side probe refuses.
	s.setDevices(dev("b", "id-b", false))
	e.refreshNow("home", s)
	s.setDevices(dev("b", "id-b", true))
	if err := e.m.RemoveDevice("home", "b"); !errors.Is(err, ErrDeviceOnline) {
		t.Fatalf("err %v", err)
	}
	if len(e.view("home").Devices) != 1 {
		t.Fatal("device dropped although the server refused")
	}
	if err := e.m.RemoveDevice("home", "nope"); !errors.Is(err, ErrUnknownDevice) {
		t.Fatalf("err %v", err)
	}
}

func TestRemoveDeviceNeedsConnection(t *testing.T) {
	c := cfgWith(srvCfg("home"))
	c.Servers[0].Devices = []tunnel.DeviceInfo{{Name: "old", DeviceID: "id-old"}}
	e := newEnv(t, c)
	if err := e.m.RemoveDevice("home", "old"); !errors.Is(err, tunnel.ErrNotConnected) {
		t.Fatalf("err %v", err)
	}
}

func TestAutoCleanOfflineDevices(t *testing.T) {
	c := cfgWith(withForwards(srvCfg("home"), map[string]config.Forward{"kept": {Port: 5300, On: true}}))
	c.Settings.AutoCleanDays = 30
	old := t0.Add(-31 * 24 * time.Hour)
	at := func(name string, started time.Time, online bool) tunnel.Device {
		return tunnel.Device{DeviceInfo: tunnel.DeviceInfo{Name: name, DeviceID: "id-" + name, StartedAt: started}, Online: online}
	}
	e := newEnv(t, c)
	s := e.ready("home",
		at("stale", old, false),                 // offline > 30 days: removed
		at("recent", t0.Add(-time.Hour), false), // offline briefly: kept
		at("kept", old, false),                  // forward on: kept
		at("unknown", time.Time{}, false),       // no time known: kept
		at("live", old, true),                   // online: kept
	)
	e.waitView("home", "stale removed", func(v ServerView) bool {
		for _, d := range v.Devices {
			if d.Name == "stale" {
				return false
			}
		}
		return true
	})
	if n := len(e.view("home").Devices); n != 4 {
		t.Fatalf("devices %+v", e.view("home").Devices)
	}
	for _, call := range s.Calls() {
		if strings.HasPrefix(call, "rmdev ") && call != "rmdev stale" {
			t.Fatalf("unexpected %s", call)
		}
	}
}

func TestAutoCleanOffByDefault(t *testing.T) {
	e := newEnv(t, cfgWith(srvCfg("home")))
	s := e.ready("home", tunnel.Device{DeviceInfo: tunnel.DeviceInfo{Name: "stale", DeviceID: "x",
		StartedAt: t0.Add(-365 * 24 * time.Hour)}})
	e.refreshNow("home", s)
	if contains(s.Calls(), "rmdev stale") {
		t.Fatal("auto clean ran while off")
	}
}
