# 按名字转发(Unix socket)Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** server/client 支持 `--name NAME`,以 VPS 上的 Unix socket `<dir>/<name>.sock` 代替 loopback TCP 端口作汇合点;端口模式不变。

**Architecture:** 库层新增 socket 路径解析(`socket.go`)与两个 establish 工厂(`ReverseForwardUnix` / `LocalForwardUnix`),签名与现有工厂一致,监督循环 `Client` 零改动。CLI 新增 `--name` / `--remote-socket-dir`,与 `--remote-listen-port` 互斥。

**Tech Stack:** Go ≥ 1.22,`golang.org/x/crypto/ssh` v0.55(`sc.Listen("unix", p)` / `sc.Dial("unix", p)`),docopt,BurntSushi/toml。

**Spec:** `docs/superpowers/specs/2026-11-22-name-forwarding-design.md`

## Global Constraints

- 名字正则:`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`
- socket 文件:`<dir>/<name>.sock`;完整路径长度上限 103 字节(sun_path 104 含结尾 NUL,取 macOS 保守值),超过即不可恢复错误。
- 默认目录解析命令(逐字):`mkdir -p -m 700 "$HOME/.tunnel/sockets" && cd "$HOME/.tunnel/sockets" && pwd`
- `remote_socket_dir` 必须是绝对路径(以 `/` 开头);配置后不执行任何远程命令。
- 所有用户可见文案用中文,与现有日志/错误风格一致。
- 每个 commit 同步更新 `CHANGELOG.md` 顶部当日条目(`## 2026-11-22(按名字转发)`,首个任务新建,后续任务追加 bullet)。
- 提交身份:`pangxincheng <pangxincheng@foxmail.com>`(仓库已配置)。
- 端口模式(含 `remote_listen_port = 0`)的现有测试必须原样通过。

## Review Focus

1. **macOS 上测试 socket 路径过长**:`t.TempDir()` 在 macOS 下形如 `/var/folders/.../001`,拼上 `.tunnel/sockets/x.sock` 很容易超过 103 字节。测试服务器的假 `$HOME` 必须用 `os.MkdirTemp("/tmp", "tn")`(并在 Cleanup 中删除)→ Task 1 的测试服务器实现中落实。
2. **重连时旧 socket 残留**:同一名字第二次 Listen(上一个 SSH 连接已断但文件仍在)必须成功 → Task 2 `TestReverseForwardUnixReplacesStaleSocket`。
3. **TOML 写端口、flag 给名字(或反之)**:flag 胜出、另一项被忽略,不得报互斥错误 → Task 3 `TestMergeModeSelection` 表格用例。
4. **exec 返回的路径带换行或非绝对路径**:必须 trim;非绝对路径报错而不是拼出相对 socket 路径 → Task 1 `TestResolveSocketPathRejectsRelativeOutput`。
5. **对应名字的 server 不在线时 client 收到连接**:本地连接应被立即关闭(不挂起),日志带名字 → Task 2 `TestLocalForwardUnixTargetOffline`。

---

### Task 1: socket 路径解析 + 测试服务器 exec 支持

**Files:**
- Create: `tunnel/socket.go`
- Create: `tunnel/socket_test.go`
- Modify: `tunnel/errors.go`(新增 `SocketDirError`)
- Modify: `tunnel/testserver_test.go`(session/exec 支持)
- Modify: `CHANGELOG.md`

**Interfaces:**
- Produces:
  - `func ValidateName(name string) error`:不合法返回普通 error(CLI 用法错误用)。
  - `func ResolveSocketPath(sc *ssh.Client, dir, name string) (string, error)`:`dir == ""` 时经 exec 解析 `$HOME/.tunnel/sockets`;返回 `path.Join(dir, name+".sock")`(用标准库 `path` 而非 `filepath`:远端始终是 POSIX);路径超长返回 `permanent(...)`。
  - `const DefaultSocketDirCommand = `mkdir -p -m 700 "$HOME/.tunnel/sockets" && cd "$HOME/.tunnel/sockets" && pwd``
  - `type SocketDirError struct{ Cause error }`:`Is(ErrPermanent) == true`;文案含「该账户不允许执行命令」「remote_socket_dir」「chmod 700」。
  - 测试服务器新字段:`Home string`(启动时 `os.MkdirTemp("/tmp", "tn")`)、`DenyExec bool`、`ExecCount int`(收到的 exec 请求数,加锁读写)、`ExecOutput func(cmd string) (stdout string, exit uint32)`(nil 时默认实现:命令等于 `DefaultSocketDirCommand` 则在 `Home` 下建 `.tunnel/sockets`(0700)并输出其绝对路径 + `\n`、exit 0;其他命令 exit 127)。

- [ ] **Step 1: 写失败测试 `tunnel/socket_test.go`**

```go
func TestValidateName(t *testing.T) {
	for _, ok := range []string{"nas", "a", "home-pc_1.lan", strings.Repeat("a", 64)} {
		if err := ValidateName(ok); err != nil { t.Errorf("%q 应合法: %v", ok, err) }
	}
	for _, bad := range []string{"", ".hidden", "-x", "a/b", "a b", strings.Repeat("a", 65), "名字"} {
		if ValidateName(bad) == nil { t.Errorf("%q 应非法", bad) }
	}
}
func TestResolveSocketPathExplicitDir(t *testing.T)   // DenyExec=true,dir="/srv/tn" → "/srv/tn/nas.sock",且 server.ExecCount == 0
func TestResolveSocketPathDefaultViaExec(t *testing.T) // dir="" → server.Home+"/.tunnel/sockets/nas.sock",目录存在且权限 0700
func TestResolveSocketPathExecDenied(t *testing.T)     // DenyExec=true,dir="" → IsPermanent(err) && errors.As(err, *SocketDirError) && 文案含 "remote_socket_dir"
func TestResolveSocketPathRejectsRelativeOutput(t *testing.T) // ExecOutput 返回 "relative/dir\n" → err != nil
func TestResolveSocketPathTooLong(t *testing.T)        // dir="/"+strings.Repeat("d", 95),name="nas" → IsPermanent(err),文案含 "103"
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./tunnel/ -run 'TestValidateName|TestResolveSocketPath' -v`
Expected: FAIL(`undefined: ValidateName` 等)

- [ ] **Step 3: 测试服务器支持 `session` 通道 + `exec` 请求**

`handleChannel` 按 `ChannelType()` 分派:`"session"` 走新函数 `handleSession`:接受通道,等待 `exec` 请求(payload `struct{ Command string }`);`DenyExec` 为 true 时 `req.Reply(false, nil)` 并关闭通道;否则 Reply true,写 stdout,发送 `exit-status`(payload `struct{ Status uint32 }`),关闭通道。其余类型维持现状。`Home` 在 `startTestSSHServer` 中创建,`t.Cleanup(os.RemoveAll)`。

- [ ] **Step 4: 实现 `tunnel/socket.go` 与 `SocketDirError`**

exec 用 `sc.NewSession()` + `session.Output(DefaultSocketDirCommand)`。`NewSession` 返回 `*ssh.OpenChannelError`,或者 `Output` 返回 `*ssh.ExitError` / 请求被拒(`ssh: command ... failed`)时,包成 `&SocketDirError{Cause: err}`;其他 I/O 错误原样返回(可重连)。输出 `strings.TrimSpace` 后不以 `/` 开头即 `SocketDirError`。

- [ ] **Step 5: 运行确认通过 + 全量**

Run: `go test ./... `
Expected: PASS(含原有全部测试)

- [ ] **Step 6: Commit**

```bash
git add tunnel/socket.go tunnel/socket_test.go tunnel/errors.go tunnel/testserver_test.go CHANGELOG.md
git commit -m "feat: socket 名字校验与 VPS socket 目录解析"
```

---

### Task 2: Unix socket 转发工厂

**Files:**
- Modify: `tunnel/forward.go`
- Modify: `tunnel/errors.go`(新增 `RemoteSocketListenError`)
- Modify: `tunnel/forward_test.go`
- Modify: `tunnel/client_test.go`(重连用例)
- Modify: `tunnel/testserver_test.go`(streamlocal 支持)
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: `ResolveSocketPath`(Task 1)。
- Produces:
  - `func ReverseForwardUnix(dir, name, localAddr string, opts ...ForwardOption) func(*ssh.Client) (net.Listener, error)`
  - `func LocalForwardUnix(localAddr, dir, name string, opts ...ForwardOption) func(*ssh.Client) (net.Listener, error)`
  - 返回的 listener:server 侧 `Addr().String()` 为 socket 完整路径(x/crypto 的 unixListener 已如此),client 侧为本机 TCP 地址。
  - `type RemoteSocketListenError struct{ Path string; Cause error }`:`Is(ErrPermanent) == true`;文案含 `StreamLocalBindUnlink`、`AllowStreamLocalForwarding`、`ls -l <Path>`、`sshd -T | grep -i streamlocal`。
  - 测试服务器新字段:`BindUnlink bool`(`startTestSSHServer` 默认 true)。

> 注:spec §5.1 写的签名是 `(socketPath, localAddr)`;因为目录要在每次连接后才能解析(§5.2),工厂改为接收 `dir, name`,在闭包内调用 `ResolveSocketPath`。行为与 spec 一致。

- [ ] **Step 1: 测试服务器支持 streamlocal**

全局请求 `streamlocal-forward@openssh.com`(payload `struct{ SocketPath string }`):`BindUnlink` 为 true 时先 `os.Remove(path)`;`net.Listen("unix", path)` 失败则 Reply false;成功后记录到 `unixForwards map[string]net.Listener` 并启动 accept 循环,对每条连接 `conn.OpenChannel("forwarded-streamlocal@openssh.com", ssh.Marshal(struct{ SocketPath, Reserved0 string }{path, ""}))`,然后双向拷贝。`cancel-streamlocal-forward@openssh.com`:关闭并删除记录,Reply true。通道 `direct-streamlocal@openssh.com`(payload `struct{ SocketPath, Reserved0 string; Reserved1 uint32 }`):`net.Dial("unix", path)`,失败 Reject,成功后双向拷贝(复用 direct-tcpip 的拷贝逻辑)。

- [ ] **Step 2: 写失败测试(`forward_test.go`)**

```go
func TestReverseForwardUnixEndToEnd(t *testing.T)  // dir="" → listener.Addr().String() == server.Home+"/.tunnel/sockets/nas.sock";net.Dial("unix", 该路径) 发 "unix\n" 收到 "echo: unix\n"
func TestLocalForwardUnixEndToEnd(t *testing.T)    // 先 ReverseForwardUnix(第一条 SSH 连接),再用第二条 SSH 连接 LocalForwardUnix("127.0.0.1:0", "", "nas", ...);TCP 连本机 listener → "echo: ..."
func TestReverseForwardUnixReplacesStaleSocket(t *testing.T) // 第一条连接 ReverseForwardUnix 后 server.CloseAllClients()(文件残留);新连接再次 ReverseForwardUnix 同名 → err == nil 且端到端可用
func TestReverseForwardUnixStaleWithoutUnlink(t *testing.T)  // BindUnlink=false,预先在路径上 net.Listen("unix") 占住 → IsPermanent(err) && 文案含 "StreamLocalBindUnlink"
func TestLocalForwardUnixTargetOffline(t *testing.T) // 无 server 侧;LocalForwardUnix 建立成功;本机 TCP 连接后 2s 内读到 EOF(连接被关闭)
```

`client_test.go` 增加 `TestClientReconnectsWithNameForwarding`:`NewClient(cfg, ReverseForwardUnix("", "nas", echo))` 跑起来,确认 socket 可用后 `server.CloseAllClients()`,等待重连(缩短 `reconnectMin`/`keepaliveInterval`,做法同现有重连测试),再次经 socket 收到 echo。

- [ ] **Step 3: 运行确认失败**

Run: `go test ./tunnel/ -run 'Unix|NameForwarding' -v`
Expected: FAIL(`undefined: ReverseForwardUnix`)

- [ ] **Step 4: 实现**

把现有两个工厂里的 accept → 拨号 → `proxyBidirectional` 循环抽成内部 helper `serve(l net.Listener, dial func() (net.Conn, error), target string, fc forwardConfig, logMsg string)`,四个工厂共用;现有工厂的日志文案与行为保持不变。`ReverseForwardUnix`:`ResolveSocketPath` → `sc.Listen("unix", path)`,错误含 `"streamlocal-forward@openssh.com request denied by peer"` 时包成 `RemoteSocketListenError`。`LocalForwardUnix`:先 `ResolveSocketPath`,再在本机监听(失败沿用 `LocalListenError`),每条连接 `sc.Dial("unix", path)`,失败记 `Warn("经 SSH 连接目标失败", "name", name, "target", path, "err", err)`。

- [ ] **Step 5: 运行确认通过 + 全量**

Run: `go test ./... -race`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add tunnel/ CHANGELOG.md
git commit -m "feat: ReverseForwardUnix/LocalForwardUnix 按名字经 VPS Unix socket 转发"
```

---

### Task 3: CLI、配置文件与文档

**Files:**
- Modify: `tunnel/configfile.go`(`Name string \`toml:"name"\``、`RemoteSocketDir string \`toml:"remote_socket_dir"\``)
- Modify: `cmd/tunnel/main.go`
- Modify: `cmd/tunnel/main_test.go`
- Modify: `tunnel/configfile_test.go`
- Modify: `tunnel.toml.example`、`README.md`、`CHANGELOG.md`

**Interfaces:**
- Consumes: `ValidateName`(Task 1)、`ReverseForwardUnix` / `LocalForwardUnix`(Task 2)。
- Produces(`cmd/tunnel` 内部):`params` 新增 `name string`、`remoteSocketDir string`;`merge` 负责模式选择,`params.config(role)` 负责校验。

模式选择规则(`merge` 内,按来源判断):

| flag `--name` | flag `--remote-listen-port` | TOML `name` | TOML `remote_listen_port` | 结果 |
|---|---|---|---|---|
| 有 | 有 | * | * | 错误:互斥 |
| 有 | 无 | * | * | 名字模式(忽略 TOML 端口) |
| 无 | 有 | * | * | 端口模式(忽略 TOML name) |
| 无 | 无 | 有 | 有 | 错误:互斥 |
| 无 | 无 | 有 | 无 | 名字模式 |
| 无 | 无 | 无 | 有 | 端口模式 |
| 无 | 无 | 无 | 无 | `config()` 报错:缺少 `--name` 或 `--remote-listen-port` |

名字模式下:server 的 `localSSHPort` 兜底 22;client 的 `localSSHPort` 未给出时 `config("client")` 报错,文案为「名字模式下 client 必须指定 --local-ssh-port(本机监听端口)」。`remoteSocketDir` 非空且不以 `/` 开头时报错。名字不合法时用 `ValidateName` 的错误。

- [ ] **Step 1: 写失败测试**

`main_test.go`:`nilArgs()` 增加 `"--name": nil, "--remote-socket-dir": nil`。新增:

```go
func TestMergeModeSelection(t *testing.T) // 表格覆盖上表 7 行;断言 p.name / p.remoteListenPortSet / err
func TestConfigNameModeClientRequiresLocalPort(t *testing.T) // TOML{Name:"nas"} role=client 无 --local-ssh-port → config 报错且文案含 "--local-ssh-port";给 "--local-ssh-port": "5102" 后通过
func TestConfigNameModeServerDefaults(t *testing.T) // TOML{Name:"nas"} role=server → localSSHPort == 22,config 通过
func TestConfigRejectsInvalidName(t *testing.T)     // --name "a/b" → config 报错
func TestConfigRejectsRelativeSocketDir(t *testing.T) // --remote-socket-dir "sockets" → 报错
```

`configfile_test.go`:TOML 含 `name = "nas"`、`remote_socket_dir = "/srv/tn"` 能被读出。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./... -run 'Mode|NameMode|InvalidName|SocketDir|TOML' -v`
Expected: FAIL

- [ ] **Step 3: 实现 CLI**

usage 的 Options 增加(不写 `[default: ...]`,原因见 `merge` 注释):

```
  --name=NAME              按名字转发:VPS 上 <socket目录>/NAME.sock(与 --remote-listen-port 二选一)
  --remote-socket-dir=DIR  VPS 上的 socket 目录(绝对路径;默认连接后执行 mkdir 解析 $HOME/.tunnel/sockets)
```

`--remote-listen-port` 的说明补「与 --name 二选一」。`runCmd` 按模式分支:名字模式下转发计划日志为 server `logger.Info("反向转发计划", "name", name, "vps_socket", sock, "inner_target", local)`,client `logger.Info("正向转发计划", "name", name, "local_listen", local, "vps_socket", sock)`。其中 `sock` 在配置了目录时为 `dir/name.sock`,否则为 `"$HOME/.tunnel/sockets/" + name + ".sock(连接后解析)"`。端口模式分支保持原样。

- [ ] **Step 4: 运行确认通过 + 全量 + 构建**

Run: `go test ./... -race && go vet ./... && mkdir -p dist && go build -o dist/tunnel ./cmd/tunnel && ./dist/tunnel --help`
Expected: PASS;help 中出现 `--name` 与 `--remote-socket-dir`

- [ ] **Step 5: 文档**

- `tunnel.toml.example`:增加注释掉的 `# name = "nas"` 与 `# remote_socket_dir = "/home/tunnel/.tunnel/sockets"`,并说明与 `remote_listen_port` 二选一。
- `README.md`:
  - 特性列表加「按名字转发」。
  - 「准备」一节加 sshd 配置(`StreamLocalBindUnlink yes`、`AllowStreamLocalForwarding`、`systemctl reload ssh`)。
  - 新增「按名字转发(推荐)」一节,包含 server/client 示例,以及以下注意事项:名字需全局唯一(后连者顶替);nologin 账户需配置 `remote_socket_dir` 并预建目录(chmod 700);socket 目录 700 只有 tunnel 账户能连接;client 必须给 `--local-ssh-port`。
- `CHANGELOG.md`:在当日条目中补充用户可见变化。

- [ ] **Step 6: Commit**

```bash
git add tunnel/configfile.go tunnel/configfile_test.go cmd/tunnel/ tunnel.toml.example README.md CHANGELOG.md
git commit -m "feat: CLI 支持 --name / --remote-socket-dir 按名字转发"
```
