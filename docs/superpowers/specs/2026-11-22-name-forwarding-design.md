# tunnel · 按名字转发(Unix socket)设计

日期:2026-11-22
状态:待评审

## 1. 意图

现状 server 在 VPS loopback 上占一个 TCP 端口作为汇合点:

- **固定端口**:断线后 VPS 的 sshd 迟迟不释放旧会话的转发端口,重连撞车;
- **随机端口**(`remote_listen_port = 0`):不撞车,但端口号记不住,外网连接时找不到机器。

本质需求是**按名字、稳定地找到某台内网机器**。SSH 登进内网机器后,其他 TCP 服务由 SSH 自身的 `-L` / `-D` 承载,不在本次范围。

做法:汇合点从 VPS 上的 TCP 端口换成 VPS 上的 Unix socket 文件,文件名即服务名(`nas` → `<socket 目录>/nas.sock`)。VPS 上仍然只需要 sshd。

## 2. 目标 / 非目标 / 反目标

**目标**
- server/client 新增 `--name NAME`(TOML `name`),与 `--remote-listen-port` 二选一。
- 断线重连在名字模式下不撞车:依赖 VPS sshd `StreamLocalBindUnlink yes`,重新绑定前先删除残留的 socket 文件。
- 端口模式(含自动分配)行为保持不变。

**非目标(后续另议)**
- `tunnel list`(列出在线名字)、`tunnel connect NAME`(给 ssh 的 `ProxyCommand` 用的 stdio 模式)、VPS 上常驻 manager。
- 同名冲突检测(见 §5.4)。

**反目标**
- 用户需要记忆或手写 socket 完整路径。
- 为此在 VPS 上部署任何自研进程。
- 破坏现有端口模式部署。

## 3. 方案选择

| 方案 | 取舍 | 结论 |
|---|---|---|
| A. 新增 `--name`,与端口模式并存 | 可逐台迁移,保留自动分配;CLI 多一组互斥参数 | **采用**(用户选定) |
| B. 名字模式替换端口模式 | 代码最简;现有部署必须一次性切换 | 否 |
| C. 通用"远端地址"参数(端口或路径) | 改动最少;需手写路径,违背初衷 | 否 |

## 4. 前置条件(VPS,一次性)

`/etc/ssh/sshd_config`:

```
StreamLocalBindUnlink yes      # 必需:重连时替换残留 socket
AllowStreamLocalForwarding yes # 默认即 yes,被收紧过的需确认
```

修改后 `systemctl reload ssh`。写入 README「准备」一节。

## 5. 设计

### 5.1 库层(`tunnel/forward.go`)

两个新工厂,与 `ReverseForward` / `LocalForward` 平行,返回同签名的 establish 闭包,监督循环零改动:

```go
// server:VPS 上 socketPath 监听(streamlocal-forward@openssh.com),每条连接代理到 localAddr
func ReverseForwardUnix(socketPath, localAddr string, opts ...ForwardOption) func(*ssh.Client) (net.Listener, error)
// client:本机 localAddr 监听,每条连接经 direct-streamlocal@openssh.com 连到 VPS 的 socketPath
func LocalForwardUnix(localAddr, socketPath string, opts ...ForwardOption) func(*ssh.Client) (net.Listener, error)
```

x/crypto/ssh v0.55 已支持:`sc.Listen("unix", path)` / `sc.Dial("unix", path)`。accept/代理循环与现有工厂共用(抽一个内部 helper,避免四份拷贝)。

### 5.2 socket 目录(决定:实现者的判断)

- 新增配置 `remote_socket_dir` / `--remote-socket-dir`,必须是绝对路径。
- **未配置时**:每次连接建立后,先开一个 SSH session 执行 `mkdir -p -m 700 "$HOME/.tunnel/sockets" && cd "$HOME/.tunnel/sockets" && pwd`,取回绝对路径。用 `$HOME` 而不是相对路径,原因是 sshd 对相对 socket 路径的解析基准不可靠。
- **已配置时**:直接使用,不执行任何命令(适配 shell 为 nologin / 设置了 ForceCommand 的纯转发账户,目录需用户预先创建)。
- 未配置且 exec 失败:报不可恢复错误,提示"该账户不允许执行命令,请配置 remote_socket_dir 并在 VPS 上预先创建目录(chmod 700)"。
- 目录权限为 700:只有 tunnel 账户能连接这些 socket。这比 loopback 端口(VPS 上任何本地用户都能连)更收紧,README 中顺带说明。

### 5.3 名字校验

- 正则 `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`,不允许 `/`,也不允许以 `.` 开头。
- 拼出的完整路径超过 104 字节(macOS / Linux sun_path 上限的保守值)时报错。目录在运行时才确定,因此这项检查在 establish 内进行,并作为不可恢复错误返回。

### 5.4 同名冲突(决定:后写者胜出,只写文档)

开启 `StreamLocalBindUnlink` 后,两台机器用同一个名字时,后连上的一台会静默顶替前一台。不做"先探测是否有人在用"的检测,原因是重连的典型场景下,旧会话还没被 sshd 回收,它的 socket 仍能 accept,探测会把自己的残留误判成他人占用,恰好破坏本次要修的场景。README 注明"名字需全局唯一"。

### 5.5 错误处理

- `streamlocal-forward` 被拒:新增 `RemoteSocketListenError`(Is `ErrPermanent`)。文案提示两个最常见原因:未开启 `StreamLocalBindUnlink` 导致残留文件阻塞绑定,或 `AllowStreamLocalForwarding` 被关闭。同时给出排查命令 `ls -l <path>`、`sshd -T | grep -i streamlocal`。
- client 侧 `Dial("unix")` 失败(对应名字的 server 不在线):仍按单条连接失败处理,记 Warn 并关闭本地连接,与现有 `LocalForward` 一致;日志中带上名字,便于判断是哪台机器不在线。

### 5.6 CLI 与配置

```bash
tunnel server --name nas [--local-ssh-port 22]  --remote-ip ... --remote-ssh-pub ...
tunnel client --name nas  --local-ssh-port 5102 --remote-ip ... --remote-ssh-pub ...
```

- `--name` 与 `--remote-listen-port` 互斥:两者都给出,或两者都没给,均报用法错误(合并 TOML 之后再判断;flag 覆盖 TOML 时,例如 TOML 写了端口、flag 给了 name,以 flag 为准,并忽略 TOML 中的另一项)。
- 名字模式下,client 的 `--local-ssh-port` **必填**(原先它默认跟随汇合端口,名字模式下没有可跟随的端口)。
- `FileConfig` 新增 `name`、`remote_socket_dir`;`tunnel.toml.example` 补充注释示例。
- 启动日志的转发计划增加 `vps_socket=<路径或"$HOME/.tunnel/sockets/nas.sock(连接后解析)">`。

### 5.7 测试

- `testserver_test.go` 的进程内 SSH 服务器补充 `streamlocal-forward@openssh.com` / `direct-streamlocal@openssh.com` 以及 exec session(`mkdir/pwd`)支持。
- 覆盖:名字模式端到端打通;残留 socket 被替换后重连成功(测试服务器模拟 unlink 行为);exec 被拒且未配置目录时报不可恢复错误;名字校验;CLI 互斥规则与 client 必填项。
- `go test ./...` 全绿。

## 6. 留给实现者

- 内部 helper 的命名与拆分方式。
- 测试服务器如何模拟 `StreamLocalBindUnlink`。
- README 的具体结构:新增「按名字转发」一节,并在「准备」一节中加入 sshd 配置。
