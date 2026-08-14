# tunnel

通用 SSH 端口转发工具/库。把内网机器的 SSH 服务安全地暴露出来 —— 公网 VPS 上**只需一个 sshd**,不需要部署任何自研服务端。

```
内网服务器                          公网 VPS(仅暴露 :22)
┌──────────────────┐         ┌──────────────────────┐
│ sshd :22         │         │ sshd                 │
│   ▲              │         │  └─ localhost:5102   │
│ tunnel server ───┼─SSH────►│   (反向转发,仅 loopback)│
└──────────────────┘         └──────────▲───────────┘
                                        │ SSH 通道
                              ┌─────────┴──────────┐
                              │ tunnel client      │
                              │  127.0.0.1:5102    │
                              └────────────────────┘
之后: ssh -p 5102 <内网用户>@127.0.0.1  →  登入内网机器
```

特性:

- **双命令**:`tunnel server`(内网侧,反向转发)+ `tunnel client`(客户端侧,正向转发)
- **断线自愈**:应用层 keepalive 探活,指数退避自动重连
- **主机公钥钉住**:不使用 TOFU,配置里钉死 VPS 公钥,不匹配即拒绝
- **纯 Go 库**:核心在 `tunnel/` 包,移动端可经 gomobile 直接调用(无系统 ssh 依赖)
- **TOML 配置 + flag 覆盖**:长期驻留用配置文件,临时调整用 flag

## 编译

需要 Go ≥ 1.22:

```bash
git clone <仓库地址> && cd remote-agent
go build -o tunnel ./cmd/tunnel

# 运行测试(不依赖真实 VPS,内置进程内测试 SSH 服务器)
go test ./...
```

## 使用

### 1. 准备(一次性)

在 VPS 上:

- 确认 sshd 正常运行(默认配置即可,反向转发绑定 loopback 不需要 `GatewayPorts`)
- 建议建一个纯转发低权限账户,并把你的公钥加入其 `~/.ssh/authorized_keys`
- 抄下 VPS 的主机公钥:`cat /etc/ssh/ssh_host_ed25519_key.pub`(或客户端上 `ssh-keyscan -t ed25519 <VPS>`,并核对指纹)

### 2. 内网服务器上启动 server

```bash
tunnel server \
  --remote-ip <VPS地址> \
  --remote-ssh-pub ~/.ssh/vps_host.pub \
  --local-ssh-key ~/.ssh/id_ed25519 \
  --user tunnel \
  --remote-listen-port 5102
# --local-ssh-port 默认 22,即内网 sshd
```

### 3. 客户端启动 client

```bash
tunnel client \
  --remote-ip <VPS地址> \
  --remote-ssh-pub ~/.ssh/vps_host.pub \
  --local-ssh-key ~/.ssh/id_ed25519 \
  --user tunnel \
  --remote-listen-port 5102 \
  --local-ssh-port 5102     # 本机监听口,默认 5102
```

### 4. 连接内网

```bash
ssh -p 5102 <内网用户>@127.0.0.1
```

### 使用配置文件(可选)

在含 `tunnel.toml` 的目录下运行可省略全部参数(参考 `tunnel.toml.example`):

```toml
remote_ip = "vps.example.com"
remote_ssh_port = 22
user = "tunnel"
local_ssh_key = "~/.ssh/id_ed25519"
remote_ssh_pub = "~/.ssh/vps_host.pub"
remote_listen_port = 5102
```

```bash
tunnel server   # 参数全部来自 ./tunnel.toml
tunnel client --local-ssh-port 9999   # flag 覆盖配置
```

优先级:**默认值 < 配置文件 < 命令行 flag**;用 `-c/--config` 指定其他路径。

## 作为库使用(移动端)

```go
import "remote-agent/tunnel"

cfg := &tunnel.Config{
    SSHAddr: "vps.example.com:22",
    User:    "tunnel",
    KeyPEM:  privateKeyBytes,      // 直接注入私钥内容,无文件依赖
    HostPub: "ssh-ed25519 AAAA…",  // 内联主机公钥
}

// 正向转发(client 方向):本机 5102 ← VPS loopback 5102
c := tunnel.NewClient(cfg, tunnel.LocalForward("127.0.0.1:5102", "127.0.0.1:5102"))
c.Run(ctx) // 守护:keepalive + 退避重连

// 反向转发(server 方向):VPS loopback 5102 → 内网 127.0.0.1:22
// tunnel.NewClient(cfg, tunnel.ReverseForward("127.0.0.1", 5102, "127.0.0.1:22"))
```

`ReverseForward`/`LocalForward` 工厂已内置 listener 创建与 accept/代理循环,开箱即用。

经 `gomobile bind` 编译即可供 iOS/Android 调用。

## 安全说明

- 两段 SSH(VPS↔内网、客户端↔VPS)都是标准 OpenSSH 加密通道;经 VPS 转发的应用流量(如客户端到内网的 ssh)对 VPS 不可见
- VPS 主机公钥钉住:与配置不符即拒绝连接 —— 主机密钥变更时应先确认是重装还是攻击
- 反向转发默认绑 VPS loopback,公网无法直接触达内网端口;攻击面 = VPS 的 sshd

## 文档

- 设计文档:`docs/superpowers/specs/2026-08-14-tunnel-design.md`
- 实施计划:`docs/superpowers/plans/2026-08-14-tunnel-orchestrator.md`
- 变更记录:`CHANGELOG.md`

## License

MIT(见 LICENSE)
