//go:build !windows

package tunnel

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// ControlListen 在 Unix socket addr 上监听:创建父目录(0700),仅当残留文件是 socket 时删除,
// 监听后 chmod 0600;ownerUID >= 0 时把 socket 的属主改为该用户。
func ControlListen(addr string, ownerUID int) (net.Listener, error) {
	dir := filepath.Dir(addr)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("创建控制通道目录失败:%w", err)
	}
	if ownerUID >= 0 {
		if err := os.Chown(dir, ownerUID, -1); err != nil {
			return nil, fmt.Errorf("设置控制通道目录属主失败:%w", err)
		}
	}
	if fi, err := os.Lstat(addr); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s 已存在且不是 socket,拒绝覆盖", addr)
		}
		if err := os.Remove(addr); err != nil {
			return nil, fmt.Errorf("删除残留 socket 失败:%w", err)
		}
	}
	l, err := net.Listen("unix", addr)
	if err != nil {
		return nil, fmt.Errorf("监听控制通道失败:%w", err)
	}
	if err := os.Chmod(addr, 0o600); err != nil {
		l.Close()
		return nil, fmt.Errorf("设置控制通道权限失败:%w", err)
	}
	if ownerUID >= 0 {
		if err := os.Chown(addr, ownerUID, -1); err != nil {
			l.Close()
			return nil, fmt.Errorf("设置控制通道属主失败:%w", err)
		}
	}
	return l, nil
}

func dialControl(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", addr, timeout)
}
