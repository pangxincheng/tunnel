# tunnel(x/crypto/ssh 库 + 薄 CLI)Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现 `tunnel` Go 库(gomobile 可调用)+ 薄 CLI:`server` 守护反向转发、`client` 守护正向转发。

**Architecture:** 库包 `tunnel`(模块根)承载全部核心:Dial(私钥认证 + 主机公钥钉住)、ReverseForward(tcpip-forward 绑 loopback)、LocalForward(direct-tcpip)、Client 监督循环(keepalive + 指数退避)。`cmd/tunnel` 只做参数解析与接线。测试用进程内 SSH 服务器(x/crypto/ssh),支持反向/正向协议与多类型主机密钥。

**Tech Stack:** Go ≥ 1.22 · `golang.org/x/crypto/ssh` · 仅标准库其他

**Spec:** `docs/superpowers/specs/2026-08-14-tunnel-design.md`

## Global Constraints

- 模块路径 `tunnel`;库包在模块根(package tunnel),CLI 在 `cmd/tunnel`
- 依赖白名单:仅 `golang.org/x/crypto`
- 每次提交同改 `CHANGELOG.md`(新条目置顶);提交信息结尾 `Co-Authored-By: Claude <noreply@anthropic.com>`
- 注释中文,解释机制与决策

## 文件结构

```
go.mod                      — 模块定义(Task 1)
backoff.go                  — 指数退避(Task 1)
ssh.go                      — Config + Dial(Task 2)
forward.go                  — ReverseForward / LocalForward / ProxyToLocal / proxyBidirectional(Task 3/4)
client.go                   — Client 监督循环(Task 5)
testserver_test.go          — 进程内测试 SSH 服务器(Task 2 基础版,Task 3/4 扩展)
cmd/tunnel/main.go          — CLI 薄壳(Task 6)
```

---

### Task 1: 脚手架 + 指数退避

**Files:**
- Create: `go.mod`、`.gitignore`
- Create: `backoff.go`、`backoff_test.go`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Produces(Task 5 依赖):
  ```go
  type Backoff struct { /* 不导出 */ }
  func NewBackoff(min, max time.Duration) *Backoff
  func (b *Backoff) Next() time.Duration
  func (b *Backoff) Reset()
  ```

- [ ] **Step 1: 脚手架**

```bash
cd /Users/xincheng/workspaces/llm-project/src/remote-agent
go mod init tunnel
go get golang.org/x/crypto@latest
printf '/tunnel\n*.test\n' > .gitignore
```

- [ ] **Step 2: 写失败测试**

```go
package tunnel

import (
	"testing"
	"time"
)

func TestBackoffGrowsAndCaps(t *testing.T) {
	b := NewBackoff(time.Second, 8*time.Second)
	wantBase := []float64{1, 2, 4, 8, 8, 8}
	for i, w := range wantBase {
		d := b.Next()
		lo := time.Duration(w * 0.8 * float64(time.Second))
		hi := time.Duration(w * 1.2 * float64(time.Second))
		if d < lo || d > hi {
			t.Fatalf("第 %d 次 = %v,期望 [%v, %v]", i, d, lo, hi)
		}
	}
	b.Reset()
	if d := b.Next(); d > 1200*time.Millisecond {
		t.Fatalf("Reset 后 = %v,期望 ~1s", d)
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./... -v`
Expected: 编译失败 `undefined: NewBackoff`

- [ ] **Step 4: 实现 backoff.go**

```go
package tunnel

import (
	"math/rand"
	"time"
)

// Backoff 实现带抖动的指数退避,用于断线后的重连节奏控制。
// 抖动(±20%)避免服务端恢复瞬间所有客户端在同一时刻重连形成惊群。
type Backoff struct {
	min, max time.Duration
	cur      time.Duration
	rng      *rand.Rand
}

func NewBackoff(min, max time.Duration) *Backoff {
	return &Backoff{min: min, max: max, cur: min, rng: rand.New(rand.NewSource(time.Now().UnixNano()))}
}

// Next 返回本次应等待的时长(基准值 ±20% 抖动),随后基准值翻倍,封顶 max。
func (b *Backoff) Next() time.Duration {
	base := b.cur
	b.cur *= 2
	if b.cur > b.max {
		b.cur = b.max
	}
	return time.Duration(float64(base) * (0.8 + 0.4*b.rng.Float64()))
}

// Reset 在连接曾稳定存活后调用,把基准值降回 min。
func (b *Backoff) Reset() { b.cur = b.min }
```

- [ ] **Step 5: 运行确认通过并提交**

Run: `go test ./... -v` Expected: PASS

`CHANGELOG.md` 顶部加条目(不存在则创建):

```markdown
# Changelog

## 2026-08-14(实现)

- 新增 `backoff.go`:带 ±20% 抖动的指数退避。
```

```bash
git add -A && git commit -m "feat: 指数退避

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 2: SSH 拨号(私钥认证 + 主机公钥钉住)

**Files:**
- Create: `ssh.go`
- Create: `testserver_test.go`(基础版:公钥认证 + 多类型主机密钥 + keepalive 应答)
- Test: `ssh_test.go`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Produces(Task 3/4/5 依赖):
  ```go
  type Config struct {
      SSHAddr string // VPS host:port(--remote-ip + --remote-ssh-port 组装),必填
      User    string // 必填
      KeyPath string // --local-ssh-key 私钥路径,与 KeyPEM 二选一
      KeyPEM  []byte // 私钥内容(移动端注入用,优先级高于 KeyPath)
      HostPub string // --remote-ssh-pub:主机公钥的文件路径或内联 authorized_keys 内容,必填
  }
  func Dial(cfg *Config) (*ssh.Client, error)
  ```

- [ ] **Step 1: 写测试服务器(基础版)**

`testserver_test.go`:

```go
package tunnel

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

// testSSHServer 是进程内 SSH 服务器:公钥认证、keepalive 应答,
// 同时持有 Ed25519 与 ECDSA 两把主机密钥(模拟真实 sshd 多密钥)。
// Task 3/4 将扩展 tcpip-forward 与 direct-tcpip 支持。
type testSSHServer struct {
	t           *testing.T
	listener    net.Listener
	config      *ssh.ServerConfig
	HostKey     ssh.PublicKey // Ed25519 主机公钥,供客户端钉住
	mu          sync.Mutex
	clientConns []*ssh.ServerConn
	totalConns  int

	forwardsMu sync.Mutex
	forwards   map[uint32]net.Listener // Task 3 用
}

func startTestSSHServer(t *testing.T, authorized ssh.PublicKey) *testSSHServer {
	t.Helper()
	_, edKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	edSigner, _ := ssh.NewSignerFromKey(edKey)
	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecSigner, _ := ssh.NewSignerFromKey(ecKey)

	s := &testSSHServer{t: t, HostKey: edSigner.PublicKey()}
	s.config = &ssh.ServerConfig{
		PublicKeyCallback: func(_ ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			if string(key.Marshal()) == string(authorized.Marshal()) {
				return nil, nil
			}
			return nil, fmt.Errorf("denied")
		},
	}
	s.config.AddHostKey(edSigner)
	s.config.AddHostKey(ecSigner)

	s.listener, err = net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go s.acceptLoop()
	t.Cleanup(func() { s.listener.Close() })
	return s
}

func (s *testSSHServer) Addr() string { return s.listener.Addr().String() }

func (s *testSSHServer) acceptLoop() {
	for {
		c, err := s.listener.Accept()
		if err != nil {
			return
		}
		go s.handleConn(c)
	}
}

func (s *testSSHServer) handleConn(c net.Conn) {
	conn, chans, reqs, err := ssh.NewServerConn(c, s.config)
	if err != nil {
		return
	}
	s.mu.Lock()
	s.clientConns = append(s.clientConns, conn)
	s.totalConns++
	s.mu.Unlock()
	go s.handleGlobalRequests(conn, reqs)
	for ch := range chans {
		s.handleChannel(conn, ch)
	}
}

func (s *testSSHServer) handleGlobalRequests(conn *ssh.ServerConn, reqs <-chan *ssh.Request) {
	for req := range reqs {
		req.Reply(req.Type == "keepalive@openssh.com", nil)
	}
}

// handleChannel 在 Task 4 中扩展 direct-tcpip;基础版全部拒绝。
func (s *testSSHServer) handleChannel(conn *ssh.ServerConn, ch ssh.NewChannel) {
	ch.Reject(ssh.UnknownChannelType, "unsupported")
}

// CloseAllClients 强制断开所有已接入的客户端(重连测试用)。
func (s *testSSHServer) CloseAllClients() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.clientConns {
		c.Close()
	}
	s.clientConns = nil
}

var _ = io.EOF // Task 3 扩展后保留(io 用于转发拷贝)
```

- [ ] **Step 2: 写失败的拨号测试**

`ssh_test.go`:

```go
package tunnel

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/ssh"
)

// writeClientKey 生成客户端密钥对,返回私钥路径与公钥。
func writeClientKey(t *testing.T) (privPath string, pub ssh.PublicKey) {
	t.Helper()
	edPub, priv, _ := ed25519.GenerateKey(rand.Reader)
	sshPub, _ := ssh.NewPublicKey(edPub)
	block, _ := ssh.MarshalPrivateKey(priv, "")
	privPath = filepath.Join(t.TempDir(), "id")
	if err := os.WriteFile(privPath, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return privPath, sshPub
}

// writeHostPub 把主机公钥写成 .pub 文件(authorized_keys 格式),返回路径。
func writeHostPub(t *testing.T, key ssh.PublicKey) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "host.pub")
	if err := os.WriteFile(p, ssh.MarshalAuthorizedKey(key), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDialSuccess(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	cfg := &Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, server.HostKey),
	}
	client, err := Dial(cfg)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()
	if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
		t.Fatalf("keepalive: %v", err)
	}
}

func TestDialSuccessInlinePub(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	cfg := &Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: string(ssh.MarshalAuthorizedKey(server.HostKey)), // 内联形态
	}
	client, err := Dial(cfg)
	if err != nil {
		t.Fatalf("Dial(内联公钥): %v", err)
	}
	defer client.Close()
}

func TestDialRejectsWrongHostKey(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	_, otherKey, _ := ed25519.GenerateKey(rand.Reader)
	otherSigner, _ := ssh.NewSignerFromKey(otherKey)
	cfg := &Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, otherSigner.PublicKey()), // 张冠李戴
	}
	if _, err := Dial(cfg); err == nil {
		t.Fatal("主机公钥不匹配应被拒绝")
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./... -run TestDial -v`
Expected: 编译失败 `undefined: Dial`

- [ ] **Step 4: 实现 ssh.go**

```go
package tunnel

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/crypto/ssh"
)

// Config 是建立一条 SSH 连接所需的全部参数。
// 字段同时服务 CLI(从 flags 组装)与移动端 gomobile(直接注入)。
type Config struct {
	SSHAddr string // VPS host:port(--remote-ip + --remote-ssh-port 组装),必填
	User    string // 必填
	KeyPath string // --local-ssh-key 私钥路径,与 KeyPEM 二选一
	KeyPEM  []byte // 私钥内容(移动端注入用,优先级高于 KeyPath)
	HostPub string // --remote-ssh-pub:主机公钥的文件路径或内联 authorized_keys 内容,必填
}

// Dial 建立一条 SSH 连接。
// 认证:KeyPEM/KeyPath 私钥签名。
// 主机校验:钉住 HostPub 给出的公钥,逐字节比对,不匹配即拒绝 ——
// 无 TOFU、无 known_hosts 依赖,确定性模型(详见 spec §2)。
// HostKeyAlgorithms 收敛到钉住公钥类型:真实 sshd 同时持有
// RSA/ECDSA/Ed25519 多把主机密钥,x/crypto 默认偏好 ECDSA 先于
// Ed25519,不限定算法会在多密钥服务器上误判不匹配。
func Dial(cfg *Config) (*ssh.Client, error) {
	signer, err := loadSigner(cfg)
	if err != nil {
		return nil, err
	}
	hostKey, err := loadHostPub(cfg.HostPub)
	if err != nil {
		return nil, err
	}
	client, err := ssh.Dial("tcp", cfg.SSHAddr, &ssh.ClientConfig{
		User:              cfg.User,
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		Timeout:           10 * time.Second, // 仅覆盖拨号+握手
		HostKeyAlgorithms: []string{hostKey.Type()},
		HostKeyCallback:   pinnedHostKeyCallback(hostKey),
	})
	if err != nil {
		return nil, fmt.Errorf("连接 %s: %w", cfg.SSHAddr, err)
	}
	return client, nil
}

// loadSigner 加载私钥:KeyPEM 优先,其次读 KeyPath 文件。
// 加密私钥不在 MVP 范围:给出明确指引而不是卡住等密码。
func loadSigner(cfg *Config) (ssh.Signer, error) {
	pemBytes := cfg.KeyPEM
	if len(pemBytes) == 0 {
		b, err := os.ReadFile(cfg.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("读取私钥 %s: %w", cfg.KeyPath, err)
		}
		pemBytes = b
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		if _, ok := err.(*ssh.PassphraseMissingError); ok {
			return nil, fmt.Errorf("私钥已加密:请提供未加密私钥(库层密码认证预留给移动端)")
		}
		return nil, fmt.Errorf("解析私钥: %w", err)
	}
	return signer, nil
}

// loadHostPub 解析钉住的主机公钥:存在对应文件则读文件,
// 否则按内联 authorized_keys 内容解析。
func loadHostPub(hostPub string) (ssh.PublicKey, error) {
	content := hostPub
	if b, err := os.ReadFile(hostPub); err == nil {
		content = string(b)
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(content))
	if err != nil {
		return nil, fmt.Errorf("解析主机公钥(应为 .pub 文件路径或内联 authorized_keys 内容): %w", err)
	}
	return key, nil
}

// pinnedHostKeyCallback 生成只接受指定公钥的主机校验回调。
// 比对用 Marshal 后的 wire 格式字节,与密钥注释等表示层细节无关。
func pinnedHostKeyCallback(want ssh.PublicKey) ssh.HostKeyCallback {
	wantBytes := want.Marshal()
	return func(_ string, _ net.Addr, key ssh.PublicKey) error {
		if !bytes.Equal(key.Marshal(), wantBytes) {
			return fmt.Errorf("主机公钥不匹配(期望 %s)", ssh.FingerprintSHA256(want))
		}
		return nil
	}
}
```

- [ ] **Step 5: 运行确认通过并提交**

Run: `go test ./... -run TestDial -v` Expected: 3 个测试 PASS

`CHANGELOG.md` 顶部加条目(`新增 ssh.go:Dial —— 私钥认证 + 主机公钥钉住(文件/内联双形态,算法收敛)`):

```bash
git add -A && git commit -m "feat: SSH 拨号(主机公钥钉住)

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 3: 反向转发(server 核心)

**Files:**
- Create: `forward.go`(ReverseForward + ProxyToLocal + proxyBidirectional)
- Modify: `testserver_test.go`(扩展 tcpip-forward / forwarded-tcpip / cancel-tcpip-forward)
- Test: `forward_test.go`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Produces(Task 5/6 依赖):
  ```go
  func ReverseForward(client *ssh.Client, bindAddr string, remotePort int, localTarget string) (net.Listener, error)
  func LocalForward(client *ssh.Client, listenAddr, target string) (net.Listener, error) // Task 4
  func ProxyToLocal(remote net.Conn, localTarget string)
  func proxyBidirectional(a, b net.Conn)
  ```

- [ ] **Step 1: 扩展测试服务器支持 tcpip-forward**

在 `testserver_test.go` 中替换 `handleGlobalRequests` 并追加:

```go
func (s *testSSHServer) handleGlobalRequests(conn *ssh.ServerConn, reqs <-chan *ssh.Request) {
	for req := range reqs {
		switch req.Type {
		case "keepalive@openssh.com":
			req.Reply(true, nil)
		case "tcpip-forward":
			s.handleForwardRequest(conn, req)
		case "cancel-tcpip-forward":
			var p tcpipForwardRequest
			if ssh.Unmarshal(req.Payload, &p) == nil {
				s.forwardsMu.Lock()
				if l, ok := s.forwards[p.BindPort]; ok {
					l.Close()
					delete(s.forwards, p.BindPort)
				}
				s.forwardsMu.Unlock()
			}
			req.Reply(true, nil)
		default:
			req.Reply(false, nil)
		}
	}
}

type tcpipForwardRequest struct {
	BindAddr string
	BindPort uint32
}

type forwardedTCPIPPayload struct {
	ConnectedAddr string
	ConnectedPort uint32
	OriginAddr    string
	OriginPort    uint32
}

func (s *testSSHServer) handleForwardRequest(conn *ssh.ServerConn, req *ssh.Request) {
	var p tcpipForwardRequest
	if ssh.Unmarshal(req.Payload, &p) != nil {
		req.Reply(false, nil)
		return
	}
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", p.BindPort))
	if err != nil {
		req.Reply(false, nil)
		return
	}
	bound := uint32(l.Addr().(*net.TCPAddr).Port)
	s.forwardsMu.Lock()
	if s.forwards == nil {
		s.forwards = make(map[uint32]net.Listener)
	}
	s.forwards[bound] = l
	s.forwardsMu.Unlock()
	if p.BindPort == 0 {
		req.Reply(true, ssh.Marshal(struct{ Port uint32 }{bound}))
	} else {
		req.Reply(true, nil)
	}
	go s.forwardAcceptLoop(conn, l, p.BindAddr, bound)
}

func (s *testSSHServer) forwardAcceptLoop(conn *ssh.ServerConn, l net.Listener, bindAddr string, bound uint32) {
	for {
		c, err := l.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			origin := c.RemoteAddr().(*net.TCPAddr)
			ch, reqs, err := conn.OpenChannel("forwarded-tcpip", ssh.Marshal(forwardedTCPIPPayload{
				ConnectedAddr: bindAddr, ConnectedPort: bound,
				OriginAddr: origin.IP.String(), OriginPort: uint32(origin.Port),
			}))
			if err != nil {
				return
			}
			go ssh.DiscardRequests(reqs)
			go io.Copy(ch, c)
			io.Copy(c, ch)
			ch.Close()
		}()
	}
}
```

注意:`ConnectedAddr` 必须回显客户端请求的 bindAddr,否则 x/crypto 客户端按地址分发不到监听器会拒绝通道。

- [ ] **Step 2: 写失败的端到端测试**

`forward_test.go`:

```go
package tunnel

import (
	"bufio"
	"fmt"
	"net"
	"testing"
)

// startEchoServer 模拟内网服务:回显一行。
func startEchoServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				line, _ := bufio.NewReader(c).ReadString('\n')
				fmt.Fprintf(c, "echo: %s", line)
			}()
		}
	}()
	return l.Addr().String()
}

func TestReverseForwardEndToEnd(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	echo := startEchoServer(t)

	client, err := Dial(&Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, server.HostKey),
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	listener, err := ReverseForward(client, "127.0.0.1", 0, echo)
	if err != nil {
		t.Fatalf("ReverseForward: %v", err)
	}
	defer listener.Close()
	go func() {
		for {
			remote, err := listener.Accept()
			if err != nil {
				return
			}
			go ProxyToLocal(remote, echo)
		}
	}()

	vpsPort := listener.Addr().(*net.TCPAddr).Port
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", vpsPort))
	if err != nil {
		t.Fatalf("dial forwarded: %v", err)
	}
	defer conn.Close()
	fmt.Fprintln(conn, "hello")
	got, _ := bufio.NewReader(conn).ReadString('\n')
	if got != "echo: hello\n" {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./... -run TestReverseForward -v`
Expected: 编译失败 `undefined: ReverseForward / ProxyToLocal`

- [ ] **Step 4: 实现 forward.go**

```go
package tunnel

import (
	"fmt"
	"io"
	"net"
	"time"

	"golang.org/x/crypto/ssh"
)

// ReverseForward 请求 VPS 的 sshd 在 bindAddr:remotePort 监听
// (remotePort 为 0 时由服务器分配),即 ssh -R 的程序化等价物。
// 返回 listener 的生命周期绑定 SSH 连接;调用方负责 Accept 并 ProxyToLocal。
func ReverseForward(client *ssh.Client, bindAddr string, remotePort int, localTarget string) (net.Listener, error) {
	listener, err := client.Listen("tcp", fmt.Sprintf("%s:%d", bindAddr, remotePort))
	if err != nil {
		return nil, fmt.Errorf("请求远程转发 %s:%d: %w", bindAddr, remotePort, err)
	}
	return listener, nil
}

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
```

- [ ] **Step 5: 运行确认通过并提交**

Run: `go test ./... -v -count=1` Expected: 全部 PASS

`CHANGELOG.md` 顶部加条目(`新增 forward.go:ReverseForward(ssh -R 程序化);测试服务器扩展 tcpip-forward 协议`):

```bash
git add -A && git commit -m "feat: 反向转发核心

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 4: 正向转发(client 核心)

**Files:**
- Modify: `forward.go`(追加 LocalForward)
- Modify: `testserver_test.go`(handleChannel 支持 direct-tcpip)
- Modify: `forward_test.go`(追加正向端到端测试)
- Modify: `CHANGELOG.md`

- [ ] **Step 1: 扩展测试服务器支持 direct-tcpip**

把 `handleChannel` 替换为:

```go
type directTCPIPPayload struct {
	DestAddr   string
	DestPort   uint32
	OriginAddr string
	OriginPort uint32
}

// handleChannel 支持 direct-tcpip:sshd 代客户端连接目标并双向转发。
func (s *testSSHServer) handleChannel(conn *ssh.ServerConn, ch ssh.NewChannel) {
	if ch.ChannelType() != "direct-tcpip" {
		ch.Reject(ssh.UnknownChannelType, "unsupported")
		return
	}
	var p directTCPIPPayload
	if ssh.Unmarshal(ch.ExtraData(), &p) != nil {
		ch.Reject(ssh.ConnectionFailed, "bad payload")
		return
	}
	target, err := net.Dial("tcp", fmt.Sprintf("%s:%d", p.DestAddr, p.DestPort))
	if err != nil {
		ch.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	channel, reqs, err := ch.Accept()
	if err != nil {
		target.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		defer channel.Close()
		defer target.Close()
		go io.Copy(channel, target)
		io.Copy(target, channel)
	}()
}
```

- [ ] **Step 2: 写失败的正向端到端测试**

`forward_test.go` 追加:

```go
func TestLocalForwardEndToEnd(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	echo := startEchoServer(t) // 扮演 VPS loopback 上的服务

	client, err := Dial(&Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, server.HostKey),
	})
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer client.Close()

	listener, err := LocalForward(client, "127.0.0.1:0", echo)
	if err != nil {
		t.Fatalf("LocalForward: %v", err)
	}
	defer listener.Close()

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial local: %v", err)
	}
	defer conn.Close()
	fmt.Fprintln(conn, "world")
	got, _ := bufio.NewReader(conn).ReadString('\n')
	if got != "echo: world\n" {
		t.Fatalf("got %q", got)
	}
}
```

- [ ] **Step 3: 运行确认失败**

Run: `go test ./... -run TestLocalForward -v`
Expected: 编译失败 `undefined: LocalForward`

- [ ] **Step 4: 实现 LocalForward(追加到 forward.go)**

```go
// LocalForward 在 listenAddr 监听,把每条接入连接经 SSH direct-tcpip 通道
// 转发到 target —— 即 ssh -L 的程序化等价物。
// accept/代理循环已在内部启动;返回 listener 由调用方持有与关闭。
func LocalForward(client *ssh.Client, listenAddr, target string) (net.Listener, error) {
	listener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("本地监听 %s: %w", listenAddr, err)
	}
	go func() {
		for {
			local, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				ch, err := client.Dial("tcp", target)
				if err != nil {
					local.Close() // 快速失败,不挂起调用方
					return
				}
				proxyBidirectional(local, ch)
			}()
		}
	}()
	return listener, nil
}
```

- [ ] **Step 5: 运行确认通过并提交**

Run: `go test ./... -v -count=1` Expected: 全部 PASS

`CHANGELOG.md` 顶部加条目(`新增 LocalForward(ssh -L 程序化,direct-tcpip 通道)`):

```bash
git add -A && git commit -m "feat: 正向转发核心

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 5: 监督循环(keepalive + 重连)

**Files:**
- Create: `client.go`
- Test: `client_test.go`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Produces(Task 6 依赖):
  ```go
  type Client struct { /* 不导出 */ }
  func NewClient(cfg *Config, establish func(*ssh.Client) (net.Listener, error)) *Client
  func (c *Client) Run(ctx context.Context) error // ctx 取消返回 nil
  ```

- [ ] **Step 1: 写失败测试**

`client_test.go`:

```go
package tunnel

import (
	"context"
	"net"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestClientReconnectsAfterServerDrop(t *testing.T) {
	privPath, pub := writeClientKey(t)
	server := startTestSSHServer(t, pub)
	echo := startEchoServer(t)

	cfg := &Config{
		SSHAddr: server.Addr(), User: "u", KeyPath: privPath,
		HostPub: writeHostPub(t, server.HostKey),
	}
	c := NewClient(cfg, func(sc *ssh.Client) (net.Listener, error) {
		l, err := ReverseForward(sc, "127.0.0.1", 0, echo)
		if err != nil {
			return nil, err
		}
		go func() {
			for {
				remote, err := l.Accept()
				if err != nil {
					return
				}
				go ProxyToLocal(remote, echo)
			}
		}()
		return l, nil
	})
	c.keepaliveInterval = 50 * time.Millisecond
	c.reconnectMin = 10 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- c.Run(ctx) }()

	waitFor(t, 3*time.Second, func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return server.totalConns >= 1
	})
	server.CloseAllClients()
	waitFor(t, 3*time.Second, func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return server.totalConns >= 2
	})

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel 后未退出")
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("条件未在超时内满足")
}
```

注意:`keepaliveInterval`/`reconnectMin` 为 Client 不导出字段,默认 15s/1s。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./... -run TestClientReconnects -v`
Expected: 编译失败 `undefined: NewClient`

- [ ] **Step 3: 实现 client.go**

```go
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
```

- [ ] **Step 4: 运行确认通过并提交**

Run: `go test ./... -v -count=1` Expected: 全部 PASS

`CHANGELOG.md` 顶部加条目(`新增 client.go:监督循环,keepalive 探活 + 退避重连 + ctx 优雅退出`):

```bash
git add -A && git commit -m "feat: 监督循环(keepalive + 重连)

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

### Task 6: CLI 薄壳(server / client)

**Files:**
- Create: `cmd/tunnel/main.go`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes:`Config`、`ReverseForward`、`LocalForward`、`ProxyToLocal`、`NewClient`

- [ ] **Step 1: 实现 cmd/tunnel/main.go**

```go
// tunnel CLI:库包 tunnel 的薄壳。
//   tunnel server [--local-ssh-port 22]   [--remote-listen-port 5102] [公共参数...]
//   tunnel client [--local-ssh-port 5102] [--remote-listen-port 5102] [公共参数...]
// 公共参数:--remote-ip / --remote-ssh-port / --remote-ssh-pub / --local-ssh-key / --user
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"golang.org/x/crypto/ssh"

	"tunnel"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "server":
		err = cmdServer(os.Args[2:])
	case "client":
		err = cmdClient(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Printf("tunnel: %v", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `用法:
  tunnel server [--local-ssh-port 22]   [--remote-listen-port 5102] [公共参数...]
  tunnel client [--local-ssh-port 5102] [--remote-listen-port 5102] [公共参数...]

公共参数:
  --remote-ip IP           VPS 地址(必填)
  --remote-ssh-port N      VPS sshd 端口(默认 22)
  --remote-ssh-pub 公钥    VPS 主机公钥(钉住;.pub 文件路径或内联 authorized_keys 内容,必填)
  --local-ssh-key 路径     本机私钥(默认 ~/.ssh/id_ed25519)
  --user 用户              VPS 登录用户(默认 $USER)`)
}

// common 承载公共 flag 并组装 tunnel.Config。
type common struct {
	remoteIP       *string
	remoteSSHPort  *int
	remoteSSHPub   *string
	localSSHKey    *string
	user           *string
}

func (c *common) register(fs *flag.FlagSet) {
	home, _ := os.UserHomeDir()
	c.remoteIP = fs.String("remote-ip", "", "VPS 地址(必填)")
	c.remoteSSHPort = fs.Int("remote-ssh-port", 22, "VPS sshd 端口")
	c.remoteSSHPub = fs.String("remote-ssh-pub", "", "VPS 主机公钥(.pub 路径或内联,必填)")
	c.localSSHKey = fs.String("local-ssh-key", filepath.Join(home, ".ssh", "id_ed25519"), "本机私钥路径")
	c.user = fs.String("user", os.Getenv("USER"), "VPS 登录用户")
}

func (c *common) config() (*tunnel.Config, error) {
	if *c.remoteIP == "" {
		return nil, fmt.Errorf("缺少 --remote-ip")
	}
	if *c.remoteSSHPub == "" {
		return nil, fmt.Errorf("缺少 --remote-ssh-pub")
	}
	return &tunnel.Config{
		SSHAddr: fmt.Sprintf("%s:%d", *c.remoteIP, *c.remoteSSHPort),
		User:    *c.user,
		KeyPath: *c.localSSHKey,
		HostPub: *c.remoteSSHPub,
	}, nil
}

func signalCtx() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

func cmdServer(args []string) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	lsp := fs.Int("local-ssh-port", 22, "内网 sshd 端口")
	rlp := fs.Int("remote-listen-port", 5102, "VPS loopback 监听端口")
	var cm common
	cm.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := cm.config()
	if err != nil {
		return err
	}
	local := fmt.Sprintf("127.0.0.1:%d", *lsp)
	c := tunnel.NewClient(cfg, func(sc *ssh.Client) (net.Listener, error) {
		l, err := tunnel.ReverseForward(sc, "127.0.0.1", *rlp, local)
		if err != nil {
			return nil, err
		}
		go func() {
			for {
				remote, err := l.Accept()
				if err != nil {
					return
				}
				go tunnel.ProxyToLocal(remote, local)
			}
		}()
		return l, nil
	})
	ctx, stop := signalCtx()
	defer stop()
	return c.Run(ctx)
}

func cmdClient(args []string) error {
	fs := flag.NewFlagSet("client", flag.ContinueOnError)
	lsp := fs.Int("local-ssh-port", 5102, "本机监听端口")
	rlp := fs.Int("remote-listen-port", 5102, "VPS loopback 汇合端口")
	var cm common
	cm.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := cm.config()
	if err != nil {
		return err
	}
	target := fmt.Sprintf("127.0.0.1:%d", *rlp)
	c := tunnel.NewClient(cfg, func(sc *ssh.Client) (net.Listener, error) {
		return tunnel.LocalForward(sc, fmt.Sprintf("127.0.0.1:%d", *lsp), target)
	})
	ctx, stop := signalCtx()
	defer stop()
	return c.Run(ctx)
}
```

- [ ] **Step 2: 构建与静态检查**

Run: `go build ./... && go vet ./... && go test ./... -count=1`
Expected: 全部通过

- [ ] **Step 3: 本机冒烟(手动,需本机 sshd 扮演 VPS,macOS 打开"远程登录")**

```bash
go build -o /tmp/tunnel ./cmd/tunnel

# 终端 1(server):
/tmp/tunnel server --remote-ip 127.0.0.1 --remote-ssh-port 22 \
  --remote-ssh-pub /etc/ssh/ssh_host_ed25519_key.pub \
  --remote-listen-port 15102

# 终端 2(client 守护):
/tmp/tunnel client --remote-ip 127.0.0.1 --remote-ssh-port 22 \
  --remote-ssh-pub /etc/ssh/ssh_host_ed25519_key.pub \
  --local-ssh-port 15103 --remote-listen-port 15102

# 终端 3:经两层转发登入本机
ssh -p 15103 $(whoami)@127.0.0.1
```

Expected: 终端 3 登入成功;Ctrl-C 终端 1 的 sshd(系统设置里关开"远程登录")观察 server/client 的退避重连日志。

- [ ] **Step 4: 提交**

`CHANGELOG.md` 顶部加条目(`新增 cmd/tunnel:CLI 薄壳,server/client 双子命令,公共参数 --remote-ip/--remote-ssh-port/--remote-ssh-pub/--local-ssh-key/--user`),然后:

```bash
git add -A && git commit -m "feat: CLI 双子命令,tunnel 可用

Co-Authored-By: Claude <noreply@anthropic.com>"
```

---

## Self-Review 记录

- **Spec 覆盖**:§2 库路线(模块根 package tunnel,KeyPEM/内联公钥注入)→ Task 2;§3 链路 → Task 3/4;§4 双命令与公共参数 → Task 6;§5 错误处理 → Dial(钉住拒绝)、runOnce(ctx 断开)、keepalive(假死);§6 测试策略 → 进程内测试服务器(多密钥/正反向)、重连测试、手动冒烟。
- **Placeholder 扫描**:全部代码步骤含完整代码;冒烟为手动步骤(需真实 sshd,已给逐条命令)。
- **类型一致性**:`Config{SSHAddr,User,KeyPath,KeyPEM,HostPub}` 在 Task 2 定义,Task 3/4/5 测试与 Task 6 CLI 使用一致;`ReverseForward`/`LocalForward`/`ProxyToLocal` 签名 Task 3/4 定义、Task 5/6 调用一致;`NewClient(cfg, establish)` Task 5 定义、Task 6 使用一致。
