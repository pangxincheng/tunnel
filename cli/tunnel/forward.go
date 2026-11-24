package tunnel

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// 本文件承载全部转发能力:两个工厂函数返回可直接传给 NewClient 的
// establish 闭包(签名 func(*ssh.Client) (net.Listener, error)),
// 内含 listener 创建与 accept/代理循环,调用方零胶水。

// ForwardOption 定制转发工厂;目前只有日志器。
type ForwardOption func(*forwardConfig)

type forwardConfig struct {
	logger *slog.Logger
}

// WithForwardLogger 注入 slog 日志器;nil 忽略。默认 slog.Default()。
// 与 Client 的 WithLogger 分开命名,避免两个 Option 类型混淆。
func WithForwardLogger(l *slog.Logger) ForwardOption {
	return func(c *forwardConfig) {
		if l != nil {
			c.logger = l
		}
	}
}

func newForwardConfig(opts []ForwardOption) forwardConfig {
	cfg := forwardConfig{logger: slog.Default()}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

// ReverseForward 返回反向转发(server 方向)的 establish 闭包:
// 请求 VPS 的 sshd 在 remoteAddr 监听(端口写 0 时由服务器分配),
// 每条外部连接代理到 localAddr。
//
// 协议视角:发送全局请求 "tcpip-forward";sshd 每接受一个连接,
// 经此 SSH 连接反向开 "forwarded-tcpip" 通道;client.Listen 把这两步
// 封装成 net.Listener。
//
//	c := tunnel.NewClient(cfg, tunnel.ReverseForward("127.0.0.1:5102", "127.0.0.1:22"))
func ReverseForward(remoteAddr string, localAddr string, opts ...ForwardOption) func(*ssh.Client) (net.Listener, error) {
	fc := newForwardConfig(opts)
	return func(sc *ssh.Client) (net.Listener, error) {
		l, err := sc.Listen("tcp", remoteAddr)
		if err != nil {
			// x/crypto 对 tcpip-forward 被拒只给这一句通用错误,sshd 的真实原因
			// (端口占用/禁止转发)不会回传;把它归为不可恢复并给出排查提示。
			if strings.Contains(err.Error(), "tcpip-forward request denied by peer") {
				return nil, &RemoteListenError{Addr: remoteAddr, Cause: err}
			}
			return nil, fmt.Errorf("请求远程转发 %s: %w", remoteAddr, err)
		}
		go serve(l, dialInner(localAddr), fc.logger, "收到反向连接", "连接内网目标失败", "target", localAddr)
		return l, nil
	}
}

// LocalForward 返回正向转发(client 方向)的 establish 闭包:
// 本机 localAddr 监听,每条连接经 direct-tcpip 通道请 sshd 代连 remoteAddr。
//
// 典型用法(remoteAddr = "127.0.0.1:5102"):VPS 上的反向转发只绑了 loopback,
// 公网摸不到;本函数让客户端用自己的 SSH 通道"拐进"VPS 的 localhost,
// 打通 客户端 → VPS:5102 → 内网服务 的完整链路。
//
//	c := tunnel.NewClient(cfg, tunnel.LocalForward("127.0.0.1:5102", "127.0.0.1:5102"))
func LocalForward(localAddr string, remoteAddr string, opts ...ForwardOption) func(*ssh.Client) (net.Listener, error) {
	fc := newForwardConfig(opts)
	return func(sc *ssh.Client) (net.Listener, error) {
		listener, err := net.Listen("tcp", localAddr)
		if err != nil {
			return nil, &LocalListenError{Addr: localAddr, Cause: err}
		}
		// Dial 在 SSH 连接上开 direct-tcpip 通道,由 sshd 代连 remoteAddr
		dial := func() (net.Conn, error) { return sc.Dial("tcp", remoteAddr) }
		go serve(listener, dial, fc.logger, "收到本机连接", "经 SSH 连接目标失败", "target", remoteAddr)
		return listener, nil
	}
}

// ReverseForwardUnix 返回按名字反向转发(server 方向)的 establish 闭包:
// 每次连接后解析 VPS 上的 <dir>/<name>.sock(dir 为空时经 exec 解析
// $HOME/.tunnel/sockets),请求 sshd 在该 socket 上监听
// (streamlocal-forward@openssh.com),每条连接代理到 localAddr。
// 重连时旧 socket 残留由 VPS sshd 的 StreamLocalBindUnlink 替换。
//
//	c := tunnel.NewClient(cfg, tunnel.ReverseForwardUnix("", "nas", "127.0.0.1:22"))
func ReverseForwardUnix(dir, name, localAddr string, opts ...ForwardOption) func(*ssh.Client) (net.Listener, error) {
	fc := newForwardConfig(opts)
	return func(sc *ssh.Client) (net.Listener, error) {
		sock, err := ResolveSocketPath(sc, dir, name)
		if err != nil {
			return nil, err
		}
		l, err := sc.Listen("unix", sock)
		if err != nil {
			if strings.Contains(err.Error(), "streamlocal-forward@openssh.com request denied by peer") {
				return nil, &RemoteSocketListenError{Path: sock, Cause: err}
			}
			return nil, fmt.Errorf("请求远程 socket 转发 %s: %w", sock, err)
		}
		go serve(l, dialInner(localAddr), fc.logger, "收到反向连接", "连接内网目标失败", "target", localAddr)
		return l, nil
	}
}

// LocalForwardUnix 返回按名字正向转发(client 方向)的 establish 闭包:
// 本机 localAddr 监听,每条连接经 direct-streamlocal@openssh.com 通道
// 连到 VPS 上 name 对应的 socket。
//
//	c := tunnel.NewClient(cfg, tunnel.LocalForwardUnix("127.0.0.1:5102", "", "nas"))
func LocalForwardUnix(localAddr, dir, name string, opts ...ForwardOption) func(*ssh.Client) (net.Listener, error) {
	fc := newForwardConfig(opts)
	return func(sc *ssh.Client) (net.Listener, error) {
		sock, err := ResolveSocketPath(sc, dir, name)
		if err != nil {
			return nil, err
		}
		// 记录实际路径:server 与 client 账户或 remote_socket_dir 不一致时,
		// 对照 server「隧道就绪」日志即可发现两边路径不同
		fc.logger.Info("VPS socket 已解析", "name", name, "path", sock)
		listener, err := net.Listen("tcp", localAddr)
		if err != nil {
			return nil, &LocalListenError{Addr: localAddr, Cause: err}
		}
		dial := func() (net.Conn, error) { return sc.Dial("unix", sock) }
		go serve(listener, dial, fc.logger, "收到本机连接", "经 SSH 连接目标失败", "name", name, "target", sock)
		return listener, nil
	}
}

// dialInner 返回连接内网目标的拨号函数(反向转发的落点)。
func dialInner(addr string) func() (net.Conn, error) {
	return func() (net.Conn, error) { return net.DialTimeout("tcp", addr, 5*time.Second) }
}

// serve 是全部转发工厂共用的 accept/代理循环:每条接入连接调用 dial
// 连到另一端并双向转发;拨号失败记 Warn 并立即关闭接入连接(快速失败,
// 不挂起调用方)。监听器随 SSH 连接断开失效时退出,重连由监督循环负责。
// logAttrs 附加在拨号失败日志上(如 target、name)。
func serve(l net.Listener, dial func() (net.Conn, error), logger *slog.Logger, acceptMsg, failMsg string, logAttrs ...any) {
	for {
		in, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			logger.Debug(acceptMsg, "remote", remoteAddrString(in))
			out, err := dial()
			if err != nil {
				// 复制后再追加:多个协程共享 logAttrs,原地 append 可能竞争底层数组
				attrs := append(append([]any(nil), logAttrs...), "err", err)
				logger.Warn(failMsg, attrs...)
				in.Close()
				return
			}
			proxyBidirectional(in, out)
		}()
	}
}

// remoteAddrString 安全地取对端地址(x/crypto 某些通道的 RemoteAddr 可能为 nil)。
func remoteAddrString(c net.Conn) string {
	if a := c.RemoteAddr(); a != nil {
		return a.String()
	}
	return "unknown"
}

// proxyBidirectional 双向流式拷贝,任一方向结束即同时关闭两端
// (对已关闭连接 Read 立刻报错,叫醒另一方向的拷贝协程)。
// 纯 L4 字节转发:不理解也不修改流过的任何内容。
func proxyBidirectional(a net.Conn, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
	a.Close()
	b.Close()
}
