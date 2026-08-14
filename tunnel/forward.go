package tunnel

import (
	"io"
	"net"
	"time"
)

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
