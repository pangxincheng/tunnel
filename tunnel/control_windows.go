//go:build windows

package tunnel

import (
	"fmt"
	"net"
	"time"

	"github.com/Microsoft/go-winio"
)

// 安全描述符:关闭继承的 DACL,只允许 SYSTEM(SY)与当前创建者(OW,即当前用户)完全访问。
const controlPipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;OW)"

// ControlListen 在命名管道 addr(\\.\pipe\...)上监听;ownerUID 在 Windows 上被忽略。
func ControlListen(addr string, ownerUID int) (net.Listener, error) {
	l, err := winio.ListenPipe(addr, &winio.PipeConfig{SecurityDescriptor: controlPipeSDDL})
	if err != nil {
		return nil, fmt.Errorf("监听控制通道失败:%w", err)
	}
	return l, nil
}

func dialControl(addr string, timeout time.Duration) (net.Conn, error) {
	return winio.DialPipe(addr, &timeout)
}
