package tunnel

import (
	"errors"
	"fmt"
	"net"
)

// ErrPermanent 标记"重试也不会成功"的错误:配置/密钥/认证/主机公钥错误,
// 以及监听端口被占用。监督循环遇到它立即返回,而不是无限重连。
// 网络抖动、对端断开等可恢复错误不带此标记。
var ErrPermanent = errors.New("不可恢复错误")

// IsPermanent 报告 err 是否属于不可恢复错误。
func IsPermanent(err error) bool { return errors.Is(err, ErrPermanent) }

// permanentError 给底层错误打上 ErrPermanent 标记,但保持原始文案不变。
type permanentError struct{ err error }

func (e *permanentError) Error() string { return e.err.Error() }
func (e *permanentError) Unwrap() error { return e.err }
func (e *permanentError) Is(target error) bool {
	return target == ErrPermanent
}

// permanent 把 err 标记为不可恢复。
func permanent(err error) error { return &permanentError{err: err} }

// RemoteListenError 表示 VPS 上的 sshd 拒绝了 tcpip-forward 请求。
// OpenSSH 对此只回一个通用的失败状态,常见原因有:
//   - 端口已被其他进程占用;
//   - sshd 关闭了 AllowTcpForwarding;
//   - 绑定特权端口但登录用户无权限。
//
// 错误文案把排查命令直接写给用户,避免只看到 "request denied by peer"。
type RemoteListenError struct {
	Addr  string // 期望在 VPS 上监听的地址,如 127.0.0.1:5102
	Cause error
}

func (e *RemoteListenError) Error() string {
	port := e.Addr
	if _, p, err := net.SplitHostPort(e.Addr); err == nil {
		port = p
	}
	return fmt.Sprintf(
		"VPS 拒绝监听 %s:sshd 未接受 tcpip-forward 请求(%v)。"+
			"常见原因:端口已被占用(在 VPS 上执行 `ss -ltnp | grep ':%s'` 确认),"+
			"或 sshd 未允许端口转发(AllowTcpForwarding)",
		e.Addr, e.Cause, port,
	)
}

func (e *RemoteListenError) Unwrap() error { return e.Cause }
func (e *RemoteListenError) Is(target error) bool {
	return target == ErrPermanent
}

// LocalListenError 表示本机监听失败(最常见的是端口被占用)。
type LocalListenError struct {
	Addr  string
	Cause error
}

func (e *LocalListenError) Error() string {
	return fmt.Sprintf("本机监听 %s 失败(端口可能已被占用):%v", e.Addr, e.Cause)
}

func (e *LocalListenError) Unwrap() error { return e.Cause }
func (e *LocalListenError) Is(target error) bool {
	return target == ErrPermanent
}
