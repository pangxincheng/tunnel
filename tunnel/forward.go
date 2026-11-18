package tunnel

import (
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// 本文件承载全部转发能力:两个工厂函数返回可直接传给 NewClient 的
// establish 闭包(签名 func(*ssh.Client) (net.Listener, error)),
// 内含 listener 创建与 accept/代理循环,调用方零胶水。

// ReverseForward 返回反向转发(server 方向)的 establish 闭包:
// 请求 VPS 的 sshd 在 remoteAddr 监听(端口写 0 时由服务器分配),
// 每条外部连接代理到 localAddr。
//
// 协议视角:发送全局请求 "tcpip-forward";sshd 每接受一个连接,
// 经此 SSH 连接反向开 "forwarded-tcpip" 通道;client.Listen 把这两步
// 封装成 net.Listener。
//
//	c := tunnel.NewClient(cfg, tunnel.ReverseForward("127.0.0.1:5102", "127.0.0.1:22"))
func ReverseForward(remoteAddr string, localAddr string) func(*ssh.Client) (net.Listener, error) {
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
		go func() {
			for {
				remote, err := l.Accept()
				if err != nil {
					return // 监听器随连接断开失效,重连由监督循环负责
				}
				go func() {
					local, err := net.DialTimeout("tcp", localAddr, 5*time.Second)
					if err != nil {
						remote.Close()
						return
					}
					proxyBidirectional(remote, local)
				}()
			}
		}()
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
func LocalForward(localAddr string, remoteAddr string) func(*ssh.Client) (net.Listener, error) {
	return func(sc *ssh.Client) (net.Listener, error) {
		listener, err := net.Listen("tcp", localAddr)
		if err != nil {
			return nil, &LocalListenError{Addr: localAddr, Cause: err}
		}
		go func() {
			for {
				local, err := listener.Accept()
				if err != nil {
					return
				}
				go func() {
					// Dial 在 SSH 连接上开 direct-tcpip 通道,由 sshd 代连 remoteAddr
					remote, err := sc.Dial("tcp", remoteAddr)
					if err != nil {
						local.Close() // 快速失败,不挂起调用方
						return
					}
					proxyBidirectional(local, remote)
				}()
			}
		}()
		return listener, nil
	}
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
