# tunnel · 设计文档

日期:2026-08-14(修订 2:实现路线改为 x/crypto/ssh 库,支持移动端 gomobile)
状态:已评审

通用 SSH 端口转发工具/库。Go 包 `tunnel` 承载全部核心逻辑(可被移动端 gomobile 调用),CLI `tunnel` 是薄壳,两个子命令:`server`(内网侧反向转发守护)、`client`(守护正向转发)。组合效果:客户端架起转发后用系统 ssh 登进内网,公网 VPS 只暴露一个 SSH 端口。

## 1. 范围

**做**:一条端口映射(VPS loopback:PORT ↔ 内网 sshd);server/client 守护(掉线退避重连);核心全部在 Go 包内,移动端经 gomobile 复用。

**不做**:配置文件、多映射、公网直连模式、终端复用、移动端 UI、`connect` 便利命令(= client + exec 系统 ssh,纯组合无新能力,后续需要再加)。

## 2. 实现路线:x/crypto/ssh 库

核心逻辑用 `golang.org/x/crypto/ssh` 实现为 Go 包 —— 2026-08-14 修订:此前定的"编排系统 ssh"路线在移动端不成立(手机没有 ssh 进程可编排,gomobile 需要纯 Go 库)。

- **认证**:私钥文件(CLI flag 或库层注入 PEM 字节);agent 不作为默认路径(显式优于隐式);密码认证库层预留(移动端)
- **主机公钥校验**:**钉住** `--remote-ssh-pub` 给出的 VPS 主机公钥,逐字节比对,不匹配即拒绝;无 TOFU、无 known_hosts 依赖 —— 确定性模型,移动端注入友好。`HostKeyAlgorithms` 收敛到钉住公钥类型(x/crypto 默认偏好 ECDSA 先于 Ed25519,不限定会误判)
- **保活**:应用层 `keepalive@openssh.com` 心跳(15s),探测失败主动断开触发重连
- **重连**:指数退避(1s→30s,±20% 抖动),连接稳定 60s 归零;ctx 取消优雅退出

## 3. 架构与链路

```
内网服务器                          公网 VPS(仅暴露 :22)
┌──────────────────┐         ┌──────────────────────┐
│ sshd :22         │         │ sshd                 │
│   ▲              │         │  ├─ :22(SSH 接入)     │
│   │  -R 反向转发  │         │  └─ localhost:PORT    │
│ tunnel server ───┼─ssh────►│    (默认仅 loopback)  │
└──────────────────┘         └──────────▲───────────┘
                                        │ -L / direct-tcpip
                              ┌─────────┴──────────┐
                              │ tunnel client      │
                              │  外层转发 + 内层登录 │
                              └────────────────────┘
```

## 4. 命令接口

```bash
tunnel server [--local-ssh-port 22]   [--remote-listen-port 5102] \
              --remote-ip IP [--remote-ssh-port 22] [--remote-ssh-pub PUB] [--local-ssh-key KEY] [--user U]

tunnel client [--local-ssh-port 5102] [--remote-listen-port 5102] \
              --remote-ip IP [--remote-ssh-port 22] [--remote-ssh-pub PUB] [--local-ssh-key KEY] [--user U]
```

公共参数(用库后显式给出,系统 ssh 的隐式行为全部参数化):

| 参数 | 含义 | 默认 |
|---|---|---|
| `--remote-ip` | VPS 地址 | 必填 |
| `--remote-ssh-port` | VPS sshd 端口 | 22 |
| `--remote-ssh-pub` | VPS 主机公钥(钉住;接受文件路径或内联 base64) | 必填 |
| `--local-ssh-key` | 本机私钥路径(向 VPS 认证) | `~/.ssh/id_ed25519` |
| `--user` | VPS 登录用户 | `$USER` |

- `--local-ssh-port`:含义随子命令 —— server 上是"内网 sshd 端口"(转发目标),client 上是"本机监听端口"(插座口)
- `--remote-listen-port`:VPS loopback 上的汇合端口,server/client 必须一致

### server 行为(守护反向转发)

经 SSH 向 VPS sshd 请求 `tcpip-forward`(绑 loopback),把 VPS localhost:PORT 的每个连接代理到 `127.0.0.1:<local-ssh-port>`。掉线退避重连(1s→30s,稳定 60s 归零);Ctrl-C 优雅退出。

### client 行为(守护正向转发)

本机 `127.0.0.1:<local-ssh-port>` 监听,每个连接经 SSH `direct-tcpip` 通道请 VPS 代连 localhost:PORT。架好后该端口即内网 sshd 的映射口,随时 `ssh -p <local-ssh-port> <user>@127.0.0.1`(系统 ssh 的 agent/known_hosts 自动生效)。监督逻辑与 server 相同。

## 5. 错误处理

| 场景 | 行为 |
|---|---|
| 参数缺失/无法解析 | 打印用法,退出码 2 |
| 主机公钥与钉住值不符(疑似攻击) | 拒绝连接,打印期望指纹;不自动放行 |
| VPS 端口被占 / 认证失败 / 转发请求被拒 | 报错并退避重连 |
| 连接假死 | keepalive 探测失败,主动断开进入重连 |
| SIGINT/SIGTERM | 主动断开连接,优雅退出 |

## 6. 测试策略

- 不依赖真实 VPS:进程内测试 SSH 服务器(`x/crypto/ssh` 实现,支持 `tcpip-forward`/`forwarded-tcpip` 反向与 `direct-tcpip` 正向、多类型主机密钥)
- 拨号:成功路径;known_hosts 不匹配被拒
- 反向/正向转发端到端:echo 服务经两跳连通
- 重连:服务器强制踢客户端,断言自动重连且 ctx 取消后优雅退出
- 集成冒烟(手动):本机 sshd 扮演 VPS,server + connect 登入;client 守护 + `ssh -p` 验证
