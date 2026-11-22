package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

// Phase 是连接监督循环所处的阶段。
type Phase string

const (
	PhaseConnecting Phase = "connecting" // 正在拨号 / 建立
	PhaseReady      Phase = "ready"      // 连接可用
	PhaseWaiting    Phase = "waiting"    // 可恢复错误后的退避等待
	PhaseFailed     Phase = "failed"     // 不可恢复错误,等待 RetryNow
	PhasePaused     Phase = "paused"     // 已暂停(由 Share 使用)
)

// ConnState 是监督循环的状态快照。
//   - Err:最近一次失败的原因;进入 ready 时清空,connecting 期间保留上一次的错误;
//   - RetryAt:仅 waiting 时有值,为计划的下次重连时间;
//   - Attempt:自上次稳定连接(存活超一分钟)以来的连接尝试次数,RetryNow 不清零。
type ConnState struct {
	Phase   Phase     `json:"phase"`
	Err     string    `json:"err"`
	RetryAt time.Time `json:"retry_at"`
	Attempt int       `json:"attempt"`
}

// ErrNotConnected 表示当前没有可用的 SSH 连接。
var ErrNotConnected = errors.New("未连接 VPS")

// supervisor 是与业务无关的连接监督状态机:connecting → ready → (断开)
// waiting(退避)/ failed(不可恢复,阻塞等 RetryNow)→ connecting ……
// 只在 ctx 取消时返回。Session 与 Share 共用。
type supervisor struct {
	logger *slog.Logger
	hook   func(ConnState)

	mu    sync.Mutex
	state ConnState
	retry chan struct{} // 容量 1,RetryNow 的信号
}

func newSupervisor(logger *slog.Logger, hook func(ConnState)) *supervisor {
	return &supervisor{
		logger: logger,
		hook:   hook,
		state:  ConnState{Phase: PhaseConnecting},
		retry:  make(chan struct{}, 1),
	}
}

func (sv *supervisor) State() ConnState {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	return sv.state
}

// set 更新状态并在锁外调用钩子(钩子可安全调用 State)。
// 只由 run 所在协程调用,钩子调用顺序与状态变化顺序一致。
func (sv *supervisor) set(st ConnState) {
	sv.mu.Lock()
	sv.state = st
	sv.mu.Unlock()
	if sv.hook != nil {
		sv.hook(st)
	}
}

// RetryNow 只在 waiting / failed 时生效:立即开始下一次连接并重置退避。
// 其他阶段调用为无操作(不会让下一次断线跳过退避)。
func (sv *supervisor) RetryNow() {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.state.Phase != PhaseWaiting && sv.state.Phase != PhaseFailed {
		return
	}
	select {
	case sv.retry <- struct{}{}:
	default:
	}
}

// run 阻塞执行监督循环直到 ctx 取消(返回 nil)。connect 完成一次连接
// 生命周期:建立成功时(在 connect 自己的协程里)调用 ready,随后阻塞到
// 连接结束并返回原因。不可恢复错误(IsPermanent)进入 failed 并等待
// RetryNow;其余错误按 [min, max] 指数退避重连,连接存活超一分钟才重置退避。
func (sv *supervisor) run(ctx context.Context, min, max time.Duration,
	connect func(ctx context.Context, ready func()) error) error {
	backoff := NewBackoff(min, max)
	attempt := 0
	lastErr := ""
	for {
		if ctx.Err() != nil {
			return nil
		}
		attempt++
		sv.set(ConnState{Phase: PhaseConnecting, Err: lastErr, Attempt: attempt})
		var readyAt time.Time
		err := connect(ctx, func() {
			readyAt = time.Now()
			sv.set(ConnState{Phase: PhaseReady, Attempt: attempt})
		})
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			err = errors.New("连接结束")
		}
		lastErr = err.Error()
		if !readyAt.IsZero() && time.Since(readyAt) > time.Minute {
			backoff.Reset()
			attempt = 0
		}

		// 丢弃过期的重试信号(例如退避定时器与 RetryNow 同时触发时留下的)
		select {
		case <-sv.retry:
		default:
		}

		if IsPermanent(err) {
			sv.set(ConnState{Phase: PhaseFailed, Err: lastErr, Attempt: attempt})
			sv.logger.Error("不可恢复错误,等待手动重试", "err", err, "attempt", attempt)
			select {
			case <-ctx.Done():
				return nil
			case <-sv.retry:
				sv.logger.Info("收到重试请求,立即重连")
				backoff.Reset()
			}
			continue
		}

		wait := backoff.Next()
		sv.set(ConnState{Phase: PhaseWaiting, Err: lastErr, RetryAt: time.Now().Add(wait), Attempt: attempt})
		sv.logger.Warn("连接未就绪,准备重试",
			"err", err, "retry_in", wait.Round(time.Millisecond), "attempt", attempt)
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		case <-sv.retry:
			timer.Stop()
			sv.logger.Info("收到重试请求,跳过退避立即重连")
			backoff.Reset()
		}
	}
}

// Session 让一条 SSH 连接承载多个按名字的正向转发(连接端使用):
// 每个转发在本机 127.0.0.1:<port> 监听,接入连接经当前 SSH 连接拨到
// VPS 上的 <socketDir>/<name>.sock。本机监听独立于 SSH 连接 —— 断线
// 期间保持监听(接入连接被立即关闭),重连后自动恢复。
//
// Run 只应同时运行一个;Run 返回时关闭全部本机监听并清空转发表。
type Session struct {
	cfg       *Config
	socketDir string
	logger    *slog.Logger
	hook      func(ConnState)

	keepaliveInterval time.Duration                                    // 默认 15s,测试可缩短
	reconnectMin      time.Duration                                    // 默认 1s
	reconnectMax      time.Duration                                    // 默认 30s
	listen            func(network, addr string) (net.Listener, error) // 默认 net.Listen,测试可替换

	sv *supervisor

	mu       sync.Mutex
	sc       *ssh.Client // 当前连接,未就绪时为 nil
	dir      string      // 当前连接下解析出的 socket 目录
	forwards map[string]*sessionForward
}

// SessionOption 定制 Session(Share 复用同一类型)。
type SessionOption func(*Session)

// WithSessionLogger 注入 slog 日志器;nil 忽略。默认 slog.Default()。
func WithSessionLogger(l *slog.Logger) SessionOption {
	return func(s *Session) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithStateHook 在每次状态变化时回调(在监督协程中同步调用,勿阻塞)。
func WithStateHook(f func(ConnState)) SessionOption {
	return func(s *Session) { s.hook = f }
}

// NewSession 创建会话;socketDir 为空时每次连上后经 ResolveSocketDir 解析。
func NewSession(cfg *Config, socketDir string, opts ...SessionOption) *Session {
	s := &Session{
		cfg:               cfg,
		socketDir:         socketDir,
		logger:            slog.Default(),
		keepaliveInterval: 15 * time.Second,
		reconnectMin:      time.Second,
		reconnectMax:      30 * time.Second,
		listen:            net.Listen,
		forwards:          make(map[string]*sessionForward),
	}
	for _, opt := range opts {
		opt(s)
	}
	s.sv = newSupervisor(s.logger, s.hook)
	return s
}

// Run 阻塞运行监督循环,ctx 取消时返回 nil。不可恢复错误不返回,
// 而是进入 PhaseFailed 等待 RetryNow。返回前关闭全部本机监听。
func (s *Session) Run(ctx context.Context) error {
	defer s.closeAllForwards()
	s.logger.Debug("监督循环参数",
		"reconnect_min", s.reconnectMin, "reconnect_max", s.reconnectMax, "keepalive", s.keepaliveInterval)
	return s.sv.run(ctx, s.reconnectMin, s.reconnectMax, s.connectOnce)
}

// RetryNow 在退避等待或 Failed 时立即开始下一次连接,并重置退避。
func (s *Session) RetryNow() { s.sv.RetryNow() }

// State 返回当前状态快照。
func (s *Session) State() ConnState { return s.sv.State() }

// connectOnce 完成一次连接生命周期:拨号 → 解析目录 → 就绪 → 保活,
// 断开时返回原因;返回前关闭连接并等待保活协程退出。
func (s *Session) connectOnce(ctx context.Context, ready func()) error {
	s.logger.Info("正在连接 VPS", "addr", s.cfg.SSHAddr, "attempt", s.sv.State().Attempt)
	sc, err := Dial(s.cfg)
	if err != nil {
		return err
	}
	defer sc.Close()
	// ctx 取消时主动断开,使目录解析或 Conn.Wait 返回
	stopWatch := context.AfterFunc(ctx, func() { sc.Close() })
	defer stopWatch()

	dir, err := ResolveSocketDir(sc, s.socketDir)
	if err != nil {
		return err
	}

	kaCtx, stopKA := context.WithCancel(ctx)
	kaDone := make(chan struct{})
	go func() {
		defer close(kaDone)
		keepalive(kaCtx, sc, s.keepaliveInterval, s.logger)
	}()

	s.mu.Lock()
	s.sc, s.dir = sc, dir
	s.mu.Unlock()
	s.logger.Info("会话就绪", "addr", s.cfg.SSHAddr, "socket_dir", dir)
	ready()

	waitErr := sc.Conn.Wait()

	s.mu.Lock()
	if s.sc == sc {
		s.sc, s.dir = nil, ""
	}
	s.mu.Unlock()
	stopKA()
	sc.Close()
	<-kaDone

	if waitErr == nil {
		return fmt.Errorf("连接被对端关闭")
	}
	return waitErr
}

// current 返回当前连接与 socket 目录;未就绪返回 ErrNotConnected。
func (s *Session) current() (*ssh.Client, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sc == nil {
		return nil, "", ErrNotConnected
	}
	return s.sc, s.dir, nil
}

// Devices 用当前连接列出服务器上的设备;未连接返回 ErrNotConnected。
func (s *Session) Devices(ctx context.Context) ([]Device, error) {
	sc, dir, err := s.current()
	if err != nil {
		return nil, err
	}
	return ListDevices(ctx, sc, dir)
}

// sessionForward 是一个本机监听及其进行中的接入连接。
type sessionForward struct {
	name string
	port int
	l    net.Listener

	mu     sync.Mutex
	closed bool
	conns  map[net.Conn]struct{}
}

// track 登记接入连接;转发已关闭时返回 false。
func (f *sessionForward) track(c net.Conn) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return false
	}
	f.conns[c] = struct{}{}
	return true
}

func (f *sessionForward) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *sessionForward) untrack(c net.Conn) {
	f.mu.Lock()
	delete(f.conns, c)
	f.mu.Unlock()
}

// close 关闭监听与全部进行中的接入连接(代理的另一端随之关闭)。
func (f *sessionForward) close() {
	f.l.Close()
	f.mu.Lock()
	f.closed = true
	conns := f.conns
	f.conns = nil
	f.mu.Unlock()
	for c := range conns {
		c.Close()
	}
}

// AddForward 立即在 127.0.0.1:localPort 监听名字 name 的转发(与 SSH 连接
// 状态无关)。端口被占用返回 *LocalListenError。同名重复调用:端口相同为
// 无操作;不同则先关旧再开新(新端口监听失败时旧转发不再保留)。
func (s *Session) AddForward(name string, localPort int) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	if localPort < 1 || localPort > 65535 {
		return fmt.Errorf("本机端口 %d 超出范围(1-65535)", localPort)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if old, ok := s.forwards[name]; ok {
		if old.port == localPort {
			return nil
		}
		old.close()
		delete(s.forwards, name)
	}
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(localPort))
	l, err := s.listen("tcp", addr)
	if err != nil {
		return &LocalListenError{Addr: addr, Cause: err}
	}
	f := &sessionForward{name: name, port: localPort, l: l, conns: make(map[net.Conn]struct{})}
	s.forwards[name] = f
	s.logger.Info("本机转发已监听", "name", name, "listen", addr)
	go s.serveForward(f)
	return nil
}

// RemoveForward 关闭名字 name 的本机监听及其进行中的连接;不存在时无操作。
func (s *Session) RemoveForward(name string) {
	s.mu.Lock()
	f, ok := s.forwards[name]
	delete(s.forwards, name)
	s.mu.Unlock()
	if ok {
		f.close()
		s.logger.Info("本机转发已关闭", "name", name)
	}
}

// Forwards 返回当前转发表:名字 → 本机端口。
func (s *Session) Forwards() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	m := make(map[string]int, len(s.forwards))
	for name, f := range s.forwards {
		m[name] = f.port
	}
	return m
}

func (s *Session) closeAllForwards() {
	s.mu.Lock()
	fs := s.forwards
	s.forwards = make(map[string]*sessionForward)
	s.mu.Unlock()
	for _, f := range fs {
		f.close()
	}
}

// serveForward 是单个转发的 accept 循环:每条接入连接经当前 SSH 连接
// 拨到 VPS 上的 socket 并双向代理;未连接或拨号失败立即关闭接入连接。
// 只在监听被关闭(RemoveForward / Run 返回)时退出;监听跨重连长期存活,
// 瞬时 Accept 错误(如 EMFILE)记 Warn 后短暂退避(5ms 起翻倍,封顶 1s)重试,
// 不能让转发永久失效。
func (s *Session) serveForward(f *sessionForward) {
	var delay time.Duration
	for {
		in, err := f.l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || f.isClosed() {
				return
			}
			if delay == 0 {
				delay = 5 * time.Millisecond
			} else if delay *= 2; delay > time.Second {
				delay = time.Second
			}
			s.logger.Warn("本机转发接受连接失败,稍后重试", "name", f.name, "err", err, "retry_in", delay)
			time.Sleep(delay)
			continue
		}
		delay = 0
		if !f.track(in) {
			in.Close()
			continue
		}
		go func() {
			defer f.untrack(in)
			s.logger.Debug("收到本机连接", "name", f.name, "remote", remoteAddrString(in))
			sc, dir, err := s.current()
			if err != nil {
				s.logger.Warn("未连接 VPS,关闭本机连接", "name", f.name)
				in.Close()
				return
			}
			sock := path.Join(dir, f.name+sockSuffix)
			out, err := sc.Dial("unix", sock)
			if err != nil {
				s.logger.Warn("经 SSH 连接目标失败", "name", f.name, "target", sock, "err", err)
				in.Close()
				return
			}
			proxyBidirectional(in, out)
		}()
	}
}
