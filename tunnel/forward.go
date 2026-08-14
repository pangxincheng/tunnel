package tunnel

import (
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// ReverseForward 请求 VPS 的 sshd 在 bindAddr:remotePort 监听
// (remotePort 为 0 时由服务器分配),即 ssh -R 的程序化等价物。
// 返回 listener 的生命周期绑定 SSH 连接;调用方负责 Accept 并 ProxyToLocal。
func ReverseForward(client *ssh.Client, bindAddr string, remotePort int, localTarget string) (net.Listener, error) {
	listener, err := client.Listen("tcp", fmt.Sprintf("%s:%d", bindAddr, remotePort))
	if err != nil {
		return nil, fmt.Errorf("请求远程转发 %s:%d: %w", bindAddr, remotePort, err)
	}
	return listener, nil
}

// LocalForward 在 listenAddr 监听,把每条接入连接经 SSH direct-tcpip 通道
// 转发到 target —— 即 ssh -L 的程序化等价物。
// accept/代理循环已在内部启动;返回 listener 由调用方持有与关闭。
func LocalForward(client *ssh.Client, listenAddr, target string) (net.Listener, error) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("本地监听 %s: %w", listenAddr, err)
	}
	go func() {
		for {
			local, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				ch, err := client.Dial("tcp", target)
				if err != nil {
					local.Close() // 快速失败,不挂起调用方
					return
				}
				proxyBidirectional(local, ch)
			}()
		}
	}()
	return listener, nil
}

// ProxyToLocal 把一条连接代理到本地地址;本地不可达时直接断开(快速失败)。
func ProxyToLocal(remote net.Conn, localTarget string) {
	local, err := net.DialTimeout("tcp", localTarget, 5*time.Second)
	if err != nil {
		remote.Close()
		return
	}
	proxyBidirectional(remote, local)
}

// proxyBidirectional 双向流式拷贝,任一方向结束即同时关闭两端
// (对已关闭连接 Read 立刻报错,叫醒另一方向的拷贝协程)。
// 纯 L4 字节转发:不理解也不修改流过的任何内容。
func proxyBidirectional(a, b net.Conn) {
	done := make(chan struct{}, 2)
	go func() { io.Copy(a, b); done <- struct{}{} }()
	go func() { io.Copy(b, a); done <- struct{}{} }()
	<-done
	a.Close()
	b.Close()
}
