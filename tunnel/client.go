package tunnel

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// Client 是隧道监督循环,职责是"让转发永远在线":
// 拨号 → 建立转发 → keepalive 保活;断开按指数退避重连,直到 ctx 取消。
// 转发方向(正向/反向)封装在 establish 里,监督循环对方向无感。
//
// 错误处理分两类:网络抖动/对端断开按退避重连;配置、认证、主机公钥、
// 监听端口占用等不可恢复错误(IsPermanent)立即返回给调用方。
type Client struct {
	cfg       *Config
	establish func(*ssh.Client) (net.Listener, error)
	logger    *slog.Logger

	keepaliveInterval time.Duration // 默认 15s,测试可缩短
	reconnectMin      time.Duration // 默认 1s
	reconnectMax      time.Duration // 默认 30s
}

// Option 定制 Client;目前只有日志器。
type Option func(*Client)

// WithLogger 注入 slog 日志器;传 nil 时保留默认(slog.Default())。
// 移动端可传 slog.New(slog.NewTextHandler(io.Discard, nil)) 静默。
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) {
		if l != nil {
			c.logger = l
		}
	}
}

func NewClient(cfg *Config, establish func(*ssh.Client) (net.Listener, error), opts ...Option) *Client {
	c := &Client{
		cfg:               cfg,
		establish:         establish,
		logger:            slog.Default(),
		keepaliveInterval: 15 * time.Second,
		reconnectMin:      time.Second,
		reconnectMax:      30 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Run 阻塞运行直到 ctx 取消(返回 nil)。网络类错误退避重连;
// 不可恢复错误立即返回,由调用方报告并退出。
func (c *Client) Run(ctx context.Context) error {
	backoff := NewBackoff(c.reconnectMin, c.reconnectMax)
	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		attempt++
		c.logger.Info("正在连接 VPS", "addr", c.cfg.SSHAddr, "attempt", attempt)
		connectedAt := time.Now()
		established, err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if err == nil {
			err = errors.New("连接结束")
		}
		if IsPermanent(err) {
			return err
		}
		// 连接曾稳定存活超一分钟才重置退避;一拨上就断保持增长,避免 tight loop
		if established && time.Since(connectedAt) > time.Minute {
			backoff.Reset()
		}
		wait := backoff.Next()
		c.logger.Warn("隧道未就绪,准备重试",
			"err", err, "retry_in", wait.Round(time.Millisecond), "attempt", attempt)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// runOnce 完成一次连接生命周期:拨号 → 建立转发 → 保活,断开时返回原因。
// established 表示转发监听是否建立成功,用于判断退避是否该重置。
func (c *Client) runOnce(ctx context.Context) (established bool, err error) {
	sshClient, err := Dial(c.cfg)
	if err != nil {
		return false, err
	}
	defer sshClient.Close()
	c.logger.Info("SSH 连接已建立", "addr", c.cfg.SSHAddr)

	// ctx 取消时主动断开,使下方 Conn.Wait 返回
	stopWatch := context.AfterFunc(ctx, func() { sshClient.Close() })
	defer stopWatch()

	listener, err := c.establish(sshClient)
	if err != nil {
		return false, err
	}
	defer listener.Close()
	c.logger.Info("隧道就绪", "listen", listener.Addr().String())

	// keepalive:应用层请求,能发现 TCP 层无法感知的对端假死;
	// 探测失败主动断开,触发重连
	kaDone := make(chan struct{})
	go func() {
		ticker := time.NewTicker(c.keepaliveInterval)
		defer ticker.Stop()
		for {
			select {
			case <-kaDone:
				return
			case <-ticker.C:
				if _, _, err := sshClient.SendRequest("keepalive@openssh.com", true, nil); err != nil {
					// ctx 取消导致的断开是正常退出,不刷告警
					if ctx.Err() == nil {
						c.logger.Warn("keepalive 探测失败,主动断开", "err", err)
					}
					sshClient.Close()
					return
				}
			}
		}
	}()
	defer close(kaDone)

	waitErr := sshClient.Conn.Wait()
	if waitErr == nil {
		return true, fmt.Errorf("连接被对端关闭")
	}
	return true, waitErr
}
