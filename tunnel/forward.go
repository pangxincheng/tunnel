package tunnel

import (
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// ReverseForward 请求 VPS 的 sshd 在 bindAddr:remotePort 监听
// (remotePort 为 0 时由服务器分配),即 `ssh -R` 的程序化等价物。
//
// 协议视角(tcpip-forward):
//  1. 客户端向 sshd 发送全局请求 "tcpip-forward",指明希望监听的地址与端口;
//  2. sshd 在该端口上每接受一个外部连接,就通过这条 SSH 连接反向开一条
//     "forwarded-tcpip" 通道通知客户端;
//  3. x/crypto/ssh 的 client.Listen 把这两步封装成一个 net.Listener:
//     每次 Accept 得到的就是一条来自 VPS 侧外部连接的通道。
//
// 数据通路:外部设备 → VPS:bindAddr:remotePort → (SSH 加密隧道)
// → 本 listener → ProxyToLocal → 内网目标。
//
// 返回 listener 的生命周期绑定 SSH 连接;连接断开即失效,
// 重连后必须重新调用本函数(见 client.go 的监督循环)。
func ReverseForward(client *ssh.Client, bindAddr string, remotePort int, localTarget string) (net.Listener, error) {
	listener, err := client.Listen("tcp", fmt.Sprintf("%s:%d", bindAddr, remotePort))
	if err != nil {
		return nil, fmt.Errorf("请求远程转发 %s:%d: %w", bindAddr, remotePort, err)
	}
	return listener, nil
}

// LocalForward 在 listenAddr 监听,把每条接入连接经 SSH 通道转发到
// target —— 即 `ssh -L` 的程序化等价物。
//
// 协议视角(direct-tcpip):每接受一条本地连接,就在 SSH 连接上开一条
// "direct-tcpip" 通道,请求 sshd 代为连接 target;sshd 连上后,这条通道
// 就成了"本地连接 ↔ 目标"之间的透明管道。
//
// 典型用法(target = "127.0.0.1:5102"):VPS 上的反向转发只绑了 loopback,
// 公网摸不到;本函数让客户端用自己的 SSH 通道"拐进"VPS 的 localhost,
// 打通 客户端 → VPS:5102 → 内网服务 的完整链路。
//
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
