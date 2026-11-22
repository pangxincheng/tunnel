package tunnel

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

// 远端文件:设备信息文件 <socket目录>/<name>.json 经 SFTP 读写,
// 纯转发账户(禁止 exec)也能用。远端始终是 POSIX,故用 path 拼接。

const infoSuffix = ".json"

// ResolveSocketDir 返回 VPS 上的 socket 目录。dir 非空直接返回;
// 否则先走 exec(DefaultSocketDirCommand),被拒时改用 SFTP:
// 取家目录并创建 <home>/.tunnel/sockets(0700)。两者都失败返回原 *SocketDirError。
func ResolveSocketDir(sc *ssh.Client, dir string) (string, error) {
	if dir != "" {
		return dir, nil
	}
	d, err := remoteSocketDir(sc)
	if err == nil {
		return d, nil
	}
	var sde *SocketDirError
	if !errors.As(err, &sde) {
		return "", err
	}
	if d, ferr := sftpSocketDir(sc); ferr == nil {
		return d, nil
	}
	return "", err
}

func sftpSocketDir(sc *ssh.Client) (string, error) {
	c, err := sftp.NewClient(sc)
	if err != nil {
		return "", err
	}
	defer c.Close()
	home, err := c.Getwd()
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(home, "/") {
		return "", fmt.Errorf("SFTP 返回的家目录不是绝对路径:%q", home)
	}
	dir := path.Join(home, ".tunnel", "sockets")
	if err := c.MkdirAll(dir); err != nil {
		return "", err
	}
	if err := c.Chmod(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// WriteDeviceInfo 原子写入 dir/<info.Name>.json:先写 .json.tmp,再 PosixRename 覆盖。
func WriteDeviceInfo(sc *ssh.Client, dir string, info DeviceInfo) error {
	if err := ValidateName(info.Name); err != nil {
		return err
	}
	data, err := json.Marshal(info)
	if err != nil {
		return err
	}
	c, err := sftp.NewClient(sc)
	if err != nil {
		return fmt.Errorf("打开 SFTP: %w", err)
	}
	defer c.Close()
	final := path.Join(dir, info.Name+infoSuffix)
	tmp := final + ".tmp"
	f, err := c.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
	if err != nil {
		return fmt.Errorf("写设备信息 %s: %w", tmp, err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		c.Remove(tmp)
		return fmt.Errorf("写设备信息 %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		c.Remove(tmp)
		return fmt.Errorf("写设备信息 %s: %w", tmp, err)
	}
	if err := c.PosixRename(tmp, final); err != nil {
		c.Remove(tmp)
		return fmt.Errorf("重命名设备信息 %s: %w", final, err)
	}
	return nil
}

// ReadDeviceInfos 读取 dir 下所有 *.json,键为去掉 .json 的文件名。
// 无法读取或解析的文件以 DeviceInfo{Name: 键} 收录,不报错。
func ReadDeviceInfos(sc *ssh.Client, dir string) (map[string]DeviceInfo, error) {
	c, err := sftp.NewClient(sc)
	if err != nil {
		return nil, fmt.Errorf("打开 SFTP: %w", err)
	}
	defer c.Close()
	entries, err := c.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("列出 %s: %w", dir, err)
	}
	out := make(map[string]DeviceInfo)
	for _, e := range entries {
		fn := e.Name()
		if e.IsDir() || !strings.HasSuffix(fn, infoSuffix) {
			continue
		}
		key := strings.TrimSuffix(fn, infoSuffix)
		info := DeviceInfo{Name: key}
		if data, err := readRemoteFile(c, path.Join(dir, fn)); err == nil {
			var parsed DeviceInfo
			if json.Unmarshal(data, &parsed) == nil {
				info = parsed
			}
		}
		out[key] = info
	}
	return out, nil
}

// 设备信息文件很小;限制读取量,避免异常大文件拖垮列表。
const maxInfoSize = 64 << 10

func readRemoteFile(c *sftp.Client, p string) ([]byte, error) {
	f, err := c.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxInfoSize))
}

// RemoveDeviceInfo 仅当 dir/<name>.json 存在且其 device_id 等于 deviceID 时删除;
// 文件不存在不算错误。
func RemoveDeviceInfo(sc *ssh.Client, dir, name, deviceID string) error {
	if err := ValidateName(name); err != nil {
		return err
	}
	c, err := sftp.NewClient(sc)
	if err != nil {
		return fmt.Errorf("打开 SFTP: %w", err)
	}
	defer c.Close()
	p := path.Join(dir, name+infoSuffix)
	data, err := readRemoteFile(c, p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("读设备信息 %s: %w", p, err)
	}
	var info DeviceInfo
	if json.Unmarshal(data, &info) != nil || info.DeviceID != deviceID {
		return nil
	}
	if err := c.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除设备信息 %s: %w", p, err)
	}
	return nil
}
