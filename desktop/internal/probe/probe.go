// Package probe 实现"测试连接":连上服务器、读取主机指纹、解析 socket 目录。
package probe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pangxincheng/tunnel/tunnel"
	"golang.org/x/crypto/ssh"
)

const timeout = 10 * time.Second

type Params struct {
	Host                     string
	Port                     int
	User, KeyPath, SocketDir string
}

type Result struct {
	Fingerprint string // ssh.FingerprintSHA256,如 "SHA256:abc…"
	HostKey     string // authorized_keys 格式的一行
	SocketDir   string // ResolveSocketDir 解析出的绝对路径
}

var ErrPassphrase = errors.New("passphrase-protected key")

// AuthError:服务器拒绝了这把私钥。
type AuthError struct{ Err error }

func (e *AuthError) Error() string { return "ssh authentication failed: " + e.Err.Error() }
func (e *AuthError) Unwrap() error { return e.Err }

// DialError:网络不可达、握手失败或超时。
type DialError struct{ Err error }

func (e *DialError) Error() string { return "ssh connection failed: " + e.Err.Error() }
func (e *DialError) Unwrap() error { return e.Err }

// SocketDirError:认证成功但无法解析远端 socket 目录。
type SocketDirError struct{ Err error }

func (e *SocketDirError) Error() string { return "resolve socket dir failed: " + e.Err.Error() }
func (e *SocketDirError) Unwrap() error { return e.Err }

// Test 连接服务器并返回主机指纹与 socket 目录。主机密钥此处直接放行,
// 仅用于展示指纹,信任须待用户确认之后。
func Test(ctx context.Context, p Params) (Result, error) {
	var res Result
	keyData, err := os.ReadFile(p.KeyPath)
	if err != nil {
		return res, fmt.Errorf("read private key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		var pm *ssh.PassphraseMissingError
		if errors.As(err, &pm) {
			return res, ErrPassphrase
		}
		return res, fmt.Errorf("parse private key: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var hostKey ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User: p.User,
		Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			hostKey = key
			return nil
		},
		Timeout: timeout,
	}

	addr := net.JoinHostPort(p.Host, strconv.Itoa(p.Port))
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return res, &DialError{Err: err}
	}
	// 握手期间 ctx 取消或超时时关闭连接以中断。
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		stop()
		conn.Close()
		if ctx.Err() != nil {
			return res, &DialError{Err: ctx.Err()}
		}
		if strings.Contains(err.Error(), "unable to authenticate") {
			return res, &AuthError{Err: err}
		}
		return res, &DialError{Err: err}
	}
	stop()
	sc := ssh.NewClient(sshConn, chans, reqs)
	defer sc.Close()

	dir, err := tunnel.ResolveSocketDir(sc, p.SocketDir)
	if err != nil {
		return res, &SocketDirError{Err: err}
	}
	return Result{
		Fingerprint: ssh.FingerprintSHA256(hostKey),
		HostKey:     strings.TrimSpace(string(ssh.MarshalAuthorizedKey(hostKey))),
		SocketDir:   dir,
	}, nil
}
