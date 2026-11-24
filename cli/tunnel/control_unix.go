//go:build !windows

package tunnel

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// controlGeteuid 可在测试中替换。
var controlGeteuid = os.Geteuid

// ControlListen 在 Unix socket addr 上监听:创建父目录(0700),仅当残留文件是 socket 时删除,
// 监听后 chmod 0600;ownerUID >= 0 时把 socket 的属主改为该用户。
// ownerUID < 0(user 模式,目录可能在 /tmp 这类公共位置下)时父目录必须属于当前用户、
// 权限恰为 0700 且不是符号链接,否则拒绝监听,防止其他本机用户预先占位。
func ControlListen(addr string, ownerUID int) (net.Listener, error) {
	dir := filepath.Dir(addr)
	if ownerUID < 0 {
		if err := ensurePrivateDir(dir); err != nil {
			return nil, err
		}
	} else if err := os.MkdirAll(dir, 0o700); err != nil {
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

// ensurePrivateDir 确保 dir 存在、属于当前用户、权限恰为 0700 且不是符号链接;缺失时创建。
func ensurePrivateDir(dir string) error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("创建控制通道目录失败:%w", err)
		}
		if err := os.Chmod(dir, 0o700); err != nil {
			return fmt.Errorf("设置控制通道目录权限失败:%w", err)
		}
		fi, err = os.Lstat(dir)
	}
	if err != nil {
		return fmt.Errorf("检查控制通道目录失败:%w", err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("控制通道目录 %s 不是目录(或是符号链接),拒绝使用", dir)
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); ok && int(st.Uid) != controlGeteuid() {
		return fmt.Errorf("控制通道目录 %s 的属主(uid %d)不是当前用户,拒绝使用", dir, st.Uid)
	}
	if fi.Mode().Perm() != 0o700 {
		return fmt.Errorf("控制通道目录 %s 的权限为 %04o,应为 0700,拒绝使用", dir, fi.Mode().Perm())
	}
	return nil
}

func dialControl(addr string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout("unix", addr, timeout)
}
