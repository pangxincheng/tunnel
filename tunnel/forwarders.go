package tunnel

import (
	"net"

	"golang.org/x/crypto/ssh"
)

// 本文件提供"即插即用"的 establish 工厂:把 ReverseForward/LocalForward
// 与各自的代理循环封装好,返回可直接传给 NewClient 的闭包。
// 调用方(CLI、移动端)不再需要自己写 accept/代理逻辑。

// ReverseForwarder 返回反向转发(server 方向)的 establish 闭包:
// 请求 VPS 在 bindAddr:remotePort 监听,每条外部连接代理到 localTarget。
//
//	c := tunnel.NewClient(cfg, tunnel.ReverseForwarder("127.0.0.1", 5102, "127.0.0.1:22"))
func ReverseForwarder(bindAddr string, remotePort int, localTarget string) func(*ssh.Client) (net.Listener, error) {
	return func(sc *ssh.Client) (net.Listener, error) {
		l, err := ReverseForward(sc, bindAddr, remotePort, localTarget)
		if err != nil {
			return nil, err
		}
		// 反向转发的 listener 需要调用方驱动 accept 循环
		// (与 LocalForward 不同:它返回的是 SSH 通道监听器,
		// 代理目标各异,循环内置在这里)
		go func() {
			for {
				remote, err := l.Accept()
				if err != nil {
					return // 监听器随连接断开失效,重连由监督循环负责
				}
				go ProxyToLocal(remote, localTarget)
			}
		}()
		return l, nil
	}
}

// LocalForwarder 返回正向转发(client 方向)的 establish 闭包:
// 本机 listenAddr 监听,每条连接经 direct-tcpip 通道转发到 target。
//
//	c := tunnel.NewClient(cfg, tunnel.LocalForwarder("127.0.0.1:5102", "127.0.0.1:5102"))
func LocalForwarder(listenAddr, target string) func(*ssh.Client) (net.Listener, error) {
	return func(sc *ssh.Client) (net.Listener, error) {
		return LocalForward(sc, listenAddr, target) // accept/代理循环内置
	}
}
