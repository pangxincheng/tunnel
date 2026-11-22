package tunnel

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"golang.org/x/crypto/ssh"
)

// 按名字转发:VPS 上的汇合点是 <socket目录>/<name>.sock,名字即服务名。

// DefaultSocketDirCommand 在未配置 remote_socket_dir 时,于每次连接后执行,
// 创建并取回 VPS 上 socket 目录的绝对路径。用 $HOME 而不是相对路径:
// sshd 对相对 socket 路径的解析基准不可靠。
const DefaultSocketDirCommand = `mkdir -p -m 700 "$HOME/.tunnel/sockets" && cd "$HOME/.tunnel/sockets" && pwd`

// maxSocketPathLen 是 socket 完整路径的长度上限:sun_path 在 macOS 上为
// 104 字节(含结尾 NUL),Linux 为 108,取保守值。
const maxSocketPathLen = 103

var nameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ValidateName 校验服务名:字母数字开头,仅含字母数字与 . _ -,最长 64。
func ValidateName(name string) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("名字 %q 不合法:须以字母或数字开头,只含字母、数字、. _ -,最长 64 个字符", name)
	}
	return nil
}

// ResolveSocketPath 返回 VPS 上 name 对应的 socket 完整路径。
// dir 非空时直接使用(不执行任何远程命令);为空时经 exec 执行
// DefaultSocketDirCommand 解析 $HOME/.tunnel/sockets。
// 远端始终是 POSIX,故用 path 而非 filepath 拼接。
func ResolveSocketPath(sc *ssh.Client, dir, name string) (string, error) {
	if dir == "" {
		d, err := remoteSocketDir(sc)
		if err != nil {
			return "", err
		}
		dir = d
	}
	p := path.Join(dir, name+".sock")
	if len(p) > maxSocketPathLen {
		return "", permanent(fmt.Errorf(
			"VPS socket 路径过长(%d 字节,上限 %d):%s;请缩短名字或用 remote_socket_dir 指定更短的目录",
			len(p), maxSocketPathLen, p))
	}
	return p, nil
}

// remoteSocketDir 在 VPS 上执行 DefaultSocketDirCommand 并校验输出。
// 账户不允许执行命令(会话/exec 被拒、命令失败)属不可恢复错误;
// 其余 I/O 错误原样返回,交给监督循环重连。
func remoteSocketDir(sc *ssh.Client) (string, error) {
	session, err := sc.NewSession()
	if err != nil {
		var oce *ssh.OpenChannelError
		if errors.As(err, &oce) {
			return "", &SocketDirError{Cause: err}
		}
		return "", fmt.Errorf("打开 SSH 会话: %w", err)
	}
	defer session.Close()
	out, err := session.Output(DefaultSocketDirCommand)
	if err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) || strings.HasPrefix(err.Error(), "ssh: command ") {
			return "", &SocketDirError{Cause: err}
		}
		return "", fmt.Errorf("解析 VPS socket 目录: %w", err)
	}
	dir := strings.TrimSpace(string(out))
	if !strings.HasPrefix(dir, "/") {
		return "", &SocketDirError{Cause: fmt.Errorf("命令输出不是绝对路径:%q", dir)}
	}
	return dir, nil
}
