package tunnel

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// Client 是隧道监督循环,职责是"让转发永远在线":
// 拨号 → 建立转发 → keepalive 保活;断开按指数退避重连,直到 ctx 取消。
// 转发方向(正向/反向)封装在 establish 里,监督循环对方向无感。
type Client struct {
	cfg       *Config
	establish func(*ssh.Client) (net.Listener, error)

	keepaliveInterval time.Duration // 默认 15s,测试可缩短
	reconnectMin      time.Duration // 默认 1s
	reconnectMax      time.Duration // 默认 30s
}

func NewClient(cfg *Config, establish func(*ssh.Client) (net.Listener, error)) *Client {
	return &Client{
		cfg:               cfg,
		establish:         establish,
		keepaliveInterval: 15 * time.Second,
		reconnectMin:      time.Second,
		reconnectMax:      30 * time.Second,
	}
}

// Run 阻塞运行直到 ctx 取消(返回 nil)。除取消外的所有错误都退避重连。
func (c *Client) Run(ctx context.Context) error {
	backoff := NewBackoff(c.reconnectMin, c.reconnectMax)
	for {
		if ctx.Err() != nil {
			return nil
		}
		connectedAt := time.Now()
		err := c.runOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		// 连接曾稳定存活超一分钟才重置退避;一拨上就断保持增长,避免 tight loop
		if time.Since(connectedAt) > time.Minute {
			backoff.Reset()
		}
		wait := backoff.Next()
		log.Printf("tunnel: 连接断开(%v),%v 后重连", err, wait.Round(time.Millisecond))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
	}
}

// runOnce 完成一次连接生命周期:拨号 → 建立转发 → 保活,断开时返回原因。
func (c *Client) runOnce(ctx context.Context) error {
	sshClient, err := Dial(c.cfg)
	if err != nil {
		return err
	}
	defer sshClient.Close()

	// ctx 取消时主动断开,使下方 Conn.Wait 返回
	stopWatch := context.AfterFunc(ctx, func() { sshClient.Close() })
	defer stopWatch()

	listener, err := c.establish(sshClient)
	if err != nil {
		return err
	}
	defer listener.Close()
	log.Printf("tunnel: 已建立,转发监听于 %s", listener.Addr())

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
					sshClient.Close()
					return
				}
			}
		}
	}()
	defer close(kaDone)

	waitErr := sshClient.Conn.Wait()
	if waitErr == nil {
		return fmt.Errorf("连接被对端关闭")
	}
	return waitErr
}
