# tunnel · 设计文档

日期:2026-08-14
状态:已评审

通用 SSH 端口转发工具。单 binary 双子命令:`tunnel server`(内网侧反向转发)与 `tunnel client`(客户端侧正向转发)。组合效果:客户端 SSH 到本地监听端口即落到内网 sshd,公网 VPS 只暴露一个 SSH 端口。

## 1. 范围

**做**:一条端口映射(VPS loopback:PORT ↔ 内网单个本地地址);私钥认证 + 主机公钥钉住;keepalive 探活 + 指数退避重连;TOML 配置 + flag 覆盖。

**不做**:多映射、公网直连模式(`--remote-bind 0.0.0.0` 虽保留 flag 但非主场景)、配置文件热加载、Web 管理端、终端复用(ra/rad 已从仓库范围移除)。

## 2. 架构与链路

```
内网服务器                          公网 VPS(仅暴露 :22)
┌──────────────────┐         ┌──────────────────────┐
│ sshd :22         │         │ sshd                 │
│   ▲              │         │  ├─ :22(SSH 接入)     │
│   │ reverse fwd  │         │  └─ localhost:PORT    │
│ tunnel server ───┼─SSH────►│    (反向转发,仅 loopback)│
└──────────────────┘         └──────────▲───────────┘
                                        │ direct-tcpip(经 SSH 通道)
                              ┌─────────┴──────────┐
                              │ tunnel client      │
                              │  本地 :LISTEN 监听  │
                              └────────────────────┘
客户端: ssh -p LISTEN user@127.0.0.1 → 落到内网 sshd:22
```

- `tunnel server`:经 SSH 向 VPS sshd 请求 `tcpip-forward`(绑定 loopback),把 VPS localhost:PORT 的每个连接代理到内网 LOCAL(默认 `127.0.0.1:22`)
- `tunnel client`:本地监听 LISTEN,每个连接经 SSH `direct-tcpip` 通道请 VPS sshd 代连 localhost:PORT
- VPS 侧只是配置好的 sshd(建议纯转发低权限账户、仅密钥登录),零自研服务端
- 安全:私钥认证;主机公钥钉住(逐字节比对 + 算法收敛到钉住类型,无 TOFU);两段 SSH 上层的应用流量(如客户端到内网的 ssh)对 VPS 不可见

## 3. 命令接口

```bash
tunnel server [-c tunnel.toml] [--ssh-ip IP] [--ssh-port 22] [--ssh-user USER]
              [--ssh-key PATH] [--host-pub PATH] [--port 5102]
              [--local 127.0.0.1:22] [--remote-bind 127.0.0.1]

tunnel client [-c tunnel.toml] [--ssh-ip IP] [--ssh-port 22] [--ssh-user USER]
              [--ssh-key PATH] [--host-pub PATH] [--port 5102]
              [--listen 127.0.0.1:5102]
```

同名 flag 覆盖配置文件值;未给 `-c` 时读取默认路径 `./tunnel.toml`(不存在则纯 flag)。

## 4. 配置文件(TOML)

```toml
ssh_ip = "vps.example.com"
ssh_port = 22
ssh_user = "tunnel"
ssh_key = "~/.ssh/id_ed25519"
host_pub = "~/.ssh/vps_host.pub"   # 钉住 VPS 主机公钥

# server 侧
port = 5102                        # VPS loopback 监听端口
local = "127.0.0.1:22"             # 内网转发目标
remote_bind = "127.0.0.1"          # VPS 侧绑定地址

# client 侧
listen = "127.0.0.1:5102"          # 客户端本地监听
```

server/client 各取所需字段。必填:`ssh_ip`、`ssh_user`、`ssh_key`、`host_pub`、`port`。`~` 路径展开。

## 5. 关键决策

1. **VPS 侧默认绑 loopback**:反向转发公网不可见,客户端必须走 SSH 跳板(`tunnel client`),公网攻击面 = VPS 的 22 端口
2. **单映射**:一个进程一条映射;多条就开多个进程(配多个配置文件)
3. **重连语义**:断线(含 keepalive 发现的对端假死)→ 退避 1s→30s(±20% 抖动)→ 重连;连接稳定 60s 退避归零;Ctrl-C 优雅退出
4. **主机公钥不匹配 = 拒绝**:打印期望指纹,不自动放行;算法收敛到钉住公钥类型(x/crypto 默认偏好 ECDSA 先于 Ed25519,不限定会误判)

## 6. 错误处理

| 场景 | 行为 |
|---|---|
| 缺必填配置 | 启动即报错退出,指出缺哪项 |
| 私钥/主机公钥文件不可读或解析失败 | 启动即报错 |
| 拨号失败 / VPS 端口被占 / 转发建立失败 | 退避重连 |
| 连接断开(TCP 层或 keepalive 探测失败) | 退避重连;稳定 60s 退避归零 |
| 主机公钥不匹配 | 拒绝连接,打印期望指纹;同样走退避重连(不静默放行) |
| SIGINT/SIGTERM | 主动断开 SSH 连接,优雅退出 |

## 7. 测试策略

- 不依赖真实 VPS:进程内测试 SSH 服务器(`x/crypto/ssh` 实现,支持 `tcpip-forward`/`cancel-tcpip-forward`/`forwarded-tcpip` 与 `direct-tcpip`)
- 拨号:成功路径;钉住公钥不匹配被拒
- 反向转发端到端:echo 服务扮演内网服务,经 VPS 转发端口连通
- 正向转发端到端:经 direct-tcpip 通道连通 VPS loopback 上的 echo 服务
- 重连:服务器强制踢客户端,断言自动重连且 ctx 取消后优雅退出
- 配置:TOML 加载、默认值、flag 覆盖优先级、缺必填项报错
