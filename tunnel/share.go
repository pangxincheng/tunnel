package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// ShareConfig 是 Share 的配置。
type ShareConfig struct {
	Conn      Config // VPS 连接参数
	SocketDir string // VPS 上的 socket 目录,空则每次连上后经 ResolveSocketDir 解析
	Name      string // 共享名字(设备名)
	Target    string // 本机被共享的目标,如 127.0.0.1:22
	StatePath string // 持久化暂停状态的 JSON 文件;空表示不持久化
	DeviceID  string // 本机设备 ID;空则用 DeviceID()
}

// ShareStatus 是 Share 的状态快照。
//   - Socket:VPS 上的 socket 路径,目录尚未解析时为空;
//   - Since:本次进入 ready 的时间(即设备信息的 started_at),未就绪时为零值;
//   - Paused:是否处于暂停(Pause 后立即为 true,Phase 稍后变为 paused)。
type ShareStatus struct {
	ConnState
	Name    string    `json:"name"`
	Socket  string    `json:"socket"`
	Since   time.Time `json:"since"`
	Paused  bool      `json:"paused"`
	Version string    `json:"version"`
}

// shareState 是 StatePath 文件的内容。
type shareState struct {
	Paused bool `json:"paused"`
}

// Share 是共享端运行时:连上 VPS → 名字检查 → 写设备信息文件 → 把 Target
// 反向转发到 <socketDir>/<name>.sock。断线按退避重连;不可恢复错误(如名字
// 被另一台设备占用)停在 PhaseFailed 等待 Retry;暂停时断开且不再连接,
// 暂停状态持久化到 StatePath。暂停不删除设备信息文件(其他设备据此显示
// 「离线 / 最后在线」)。
//
// Run 只应同时运行一个;其余方法可在任意协程调用。
type Share struct {
	cfg    ShareConfig
	logger *slog.Logger

	keepaliveInterval time.Duration // 默认 15s,测试可缩短
	reconnectMin      time.Duration // 默认 1s
	reconnectMax      time.Duration // 默认 30s

	sv   *supervisor
	wake chan struct{} // 容量 1,Resume 唤醒暂停中的 Run

	mu        sync.Mutex
	name      string
	paused    bool
	cancelRun context.CancelFunc  // 当前一轮监督循环的取消函数;暂停或未运行时为 nil
	pending   map[string]struct{} // 改名后待删除的旧名字信息文件
	dir       string              // 最近一次解析出的 socket 目录
	since     time.Time
}

// NewShare 校验配置并读取 StatePath(文件不存在 = 未暂停)。
// 选项与 Session 共用:WithSessionLogger、WithStateHook。
func NewShare(cfg ShareConfig, opts ...SessionOption) (*Share, error) {
	if err := ValidateName(cfg.Name); err != nil {
		return nil, err
	}
	if cfg.Target == "" {
		return nil, fmt.Errorf("未指定共享目标(如 127.0.0.1:22)")
	}
	if cfg.DeviceID == "" {
		id, err := DeviceID()
		if err != nil {
			return nil, fmt.Errorf("读取设备 ID 失败: %w", err)
		}
		cfg.DeviceID = id
	}
	// 选项作用于 Session 类型,借一个临时 Session 取出日志器与钩子
	tmp := &Session{logger: slog.Default()}
	for _, opt := range opts {
		opt(tmp)
	}
	s := &Share{
		cfg:               cfg,
		logger:            tmp.logger,
		keepaliveInterval: 15 * time.Second,
		reconnectMin:      time.Second,
		reconnectMax:      30 * time.Second,
		wake:              make(chan struct{}, 1),
		name:              cfg.Name,
		pending:           make(map[string]struct{}),
		dir:               cfg.SocketDir,
	}
	paused, err := s.readState()
	if err != nil {
		return nil, err
	}
	s.paused = paused
	s.sv = newSupervisor(s.logger, tmp.hook)
	if paused {
		s.sv.state = ConnState{Phase: PhasePaused}
	}
	return s, nil
}

// readState 读取持久化的暂停状态;文件不存在视为未暂停,内容损坏记警告后视为未暂停。
func (s *Share) readState() (bool, error) {
	if s.cfg.StatePath == "" {
		return false, nil
	}
	data, err := os.ReadFile(s.cfg.StatePath)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("读取状态文件 %s: %w", s.cfg.StatePath, err)
	}
	var st shareState
	if err := json.Unmarshal(data, &st); err != nil {
		s.logger.Warn("状态文件损坏,按未暂停处理", "path", s.cfg.StatePath, "err", err)
		return false, nil
	}
	return st.Paused, nil
}

// writeState 原子写入暂停状态(先写同目录临时文件再重命名),权限 0600。
func (s *Share) writeState(paused bool) error {
	p := s.cfg.StatePath
	if p == "" {
		return nil
	}
	data, err := json.Marshal(shareState{Paused: paused})
	if err != nil {
		return err
	}
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".state-*.tmp") // CreateTemp 以 0600 创建
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Sync()
	}
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Chmod(tmp, 0o600)
	}
	if werr == nil {
		werr = os.Rename(tmp, p)
	}
	if werr != nil {
		os.Remove(tmp)
		return werr
	}
	return nil
}

// Run 阻塞运行直到 ctx 取消(返回 nil);不可恢复错误不返回。
func (s *Share) Run(ctx context.Context) error {
	s.logger.Debug("监督循环参数",
		"reconnect_min", s.reconnectMin, "reconnect_max", s.reconnectMax, "keepalive", s.keepaliveInterval)
	for {
		if ctx.Err() != nil {
			return nil
		}
		s.mu.Lock()
		if s.paused {
			// 丢弃过期的唤醒信号:此后的信号必然来自检查之后的 Resume
			select {
			case <-s.wake:
			default:
			}
			s.mu.Unlock()
			if s.sv.State().Phase != PhasePaused {
				s.sv.set(ConnState{Phase: PhasePaused})
				s.logger.Info("共享已暂停")
			}
			select {
			case <-ctx.Done():
				return nil
			case <-s.wake:
			}
			continue
		}
		runCtx, cancel := context.WithCancel(ctx)
		s.cancelRun = cancel
		s.mu.Unlock()

		s.sv.run(runCtx, s.reconnectMin, s.reconnectMax, s.connectOnce)

		s.mu.Lock()
		s.cancelRun = nil
		s.mu.Unlock()
		cancel()
	}
}

// Pause 持久化暂停状态并立即断开;写状态文件失败时不改变状态。
func (s *Share) Pause() error { return s.setPaused(true) }

// Resume 持久化恢复状态并立即开始连接;写状态文件失败时不改变状态。
func (s *Share) Resume() error { return s.setPaused(false) }

func (s *Share) setPaused(paused bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.writeState(paused); err != nil {
		return fmt.Errorf("保存暂停状态到 %s 失败: %w", s.cfg.StatePath, err)
	}
	s.paused = paused
	if paused {
		if s.cancelRun != nil {
			s.cancelRun()
		}
		s.logger.Info("收到暂停请求")
	} else {
		select {
		case s.wake <- struct{}{}:
		default:
		}
		s.logger.Info("收到恢复请求")
	}
	return nil
}

// Retry 在退避等待或 failed 时立即重连并重置退避;其他阶段(含暂停)无操作。
func (s *Share) Retry() { s.sv.RetryNow() }

// SetName 改用新名字共享:断开当前连接,下次连上后删除旧名字的本机信息
// 文件与残留 socket(仅当信息文件仍属于本机),再以新名字重连。暂停中只改名字,恢复后生效。
// 持久化由调用方负责。
func (s *Share) SetName(name string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if name == s.name {
		return nil
	}
	old := s.name
	s.pending[old] = struct{}{}
	delete(s.pending, name)
	s.name = name
	if s.cancelRun != nil {
		s.cancelRun()
	}
	s.logger.Info("共享名字已修改,重新连接", "old", old, "name", name)
	return nil
}

// Status 返回当前状态快照。
func (s *Share) Status() ShareStatus {
	st := ShareStatus{ConnState: s.sv.State(), Version: Version}
	s.mu.Lock()
	defer s.mu.Unlock()
	st.Name = s.name
	st.Paused = s.paused
	st.Since = s.since
	if s.dir != "" {
		st.Socket = path.Join(s.dir, s.name+sockSuffix)
	}
	return st
}

// connectOnce 完成一次共享连接生命周期:拨号 → 解析目录 → 清理旧名字 →
// 名字检查 → 写设备信息 → 反向转发 → 就绪 → 保活,断开时返回原因。
func (s *Share) connectOnce(ctx context.Context, ready func()) error {
	s.mu.Lock()
	name := s.name
	s.mu.Unlock()

	s.logger.Info("正在连接 VPS", "addr", s.cfg.Conn.SSHAddr, "name", name, "attempt", s.sv.State().Attempt)
	sc, err := Dial(&s.cfg.Conn)
	if err != nil {
		return err
	}
	defer sc.Close()

	// ctx 取消(停止 / 暂停 / 改名)时主动断开:已建立转发时先撤销远端监听
	// (限时,避免网络已断时卡住),再关闭连接,使各步骤或 Conn.Wait 返回。
	var lmu sync.Mutex
	var fwd net.Listener
	stopWatch := context.AfterFunc(ctx, func() {
		lmu.Lock()
		l := fwd
		lmu.Unlock()
		if l != nil {
			done := make(chan struct{})
			go func() { l.Close(); close(done) }()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		}
		sc.Close()
	})
	defer stopWatch()

	dir, err := ResolveSocketDir(sc, s.cfg.SocketDir)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.dir = dir
	s.mu.Unlock()

	s.removePending(sc, dir, name)

	infos, err := ReadDeviceInfos(sc, dir)
	if err != nil {
		return fmt.Errorf("读取 VPS 上的设备信息: %w", err)
	}
	if owner, ok := infos[name]; ok && owner.DeviceID != "" && owner.DeviceID != s.cfg.DeviceID {
		return &NameTakenError{Name: name, Owner: owner}
	}

	startedAt := time.Now()
	if err := WriteDeviceInfo(sc, dir, localDeviceInfo(s.cfg.DeviceID, name, startedAt)); err != nil {
		return err
	}

	l, err := ReverseForwardUnix(dir, name, s.cfg.Target, WithForwardLogger(s.logger))(sc)
	if err != nil {
		return err
	}
	lmu.Lock()
	fwd = l
	lmu.Unlock()
	defer l.Close()

	kaCtx, stopKA := context.WithCancel(ctx)
	kaDone := make(chan struct{})
	go func() {
		defer close(kaDone)
		keepalive(kaCtx, sc, s.keepaliveInterval, s.logger)
	}()

	s.mu.Lock()
	s.since = startedAt
	s.mu.Unlock()
	s.logger.Info("共享就绪", "addr", s.cfg.Conn.SSHAddr, "name", name,
		"vps_socket", path.Join(dir, name+sockSuffix), "target", s.cfg.Target)
	ready()

	waitErr := sc.Conn.Wait()

	s.mu.Lock()
	s.since = time.Time{}
	s.mu.Unlock()
	stopKA()
	sc.Close()
	<-kaDone

	if waitErr == nil {
		return fmt.Errorf("连接被对端关闭")
	}
	return waitErr
}

// removePending 删除改名前旧名字的本机信息文件及残留 socket(RemoveDevice
// 只在 json 属于本机时删除);失败记警告并留到下次连接再试,不影响本次共享。
func (s *Share) removePending(sc *ssh.Client, dir, current string) {
	s.mu.Lock()
	var names []string
	for n := range s.pending {
		if n != current {
			names = append(names, n)
		}
	}
	s.mu.Unlock()
	for _, n := range names {
		if err := RemoveDevice(sc, dir, n, s.cfg.DeviceID); err != nil {
			s.logger.Warn("删除旧名字的设备信息失败,下次连接重试", "name", n, "err", err)
			continue
		}
		s.mu.Lock()
		delete(s.pending, n)
		s.mu.Unlock()
		s.logger.Info("已删除旧名字的设备信息与 socket", "name", n)
	}
}
