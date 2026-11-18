<p align="center">
  <img src="assets/icon.svg" width="96" alt="tunnel logo">
</p>

<h1 align="center">tunnel</h1>

<p align="center">
  <a href="https://github.com/pangxincheng/tunnel/actions/workflows/release.yml"><img src="https://img.shields.io/github/actions/workflow/status/pangxincheng/tunnel/release.yml?style=flat-square" alt="release"></a>
  <a href="https://github.com/pangxincheng/tunnel/releases"><img src="https://img.shields.io/github/v/release/pangxincheng/tunnel?style=flat-square" alt="release version"></a>
  <a href="go.mod"><img src="https://img.shields.io/github/go-mod/go-version/pangxincheng/tunnel?style=flat-square" alt="go version"></a>
  <img src="https://img.shields.io/badge/license-MIT-green?style=flat-square" alt="MIT License">

</p>

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
git clone https://github.com/pangxincheng/tunnel.git && cd tunnel
mkdir -p dist && go build -o dist/tunnel ./cmd/tunnel

# 运行测试(不依赖真实 VPS,内置进程内测试 SSH 服务器)
go test ./...
```

注意:二进制不能输出到仓库根目录的 `./tunnel` —— 该位置是库源码目录 `tunnel/`。

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

### 让 VPS 自动分配汇合端口(仅 server)

server 侧把 `remote_listen_port` 设为 `0`(TOML 或 `--remote-listen-port 0`),由 VPS 的 sshd 选择空闲端口:

```bash
tunnel server --remote-ip vps.example.com --remote-listen-port 0 ...
# level=INFO msg=反向转发计划 vps_listen="自动分配(VPS 选择)" inner_target=127.0.0.1:22
# level=INFO msg=隧道就绪 listen=127.0.0.1:38123
```

把「隧道就绪」日志里的实际端口填给 client 的 `remote_listen_port`。注意:

- 自动分配只适用于 server;client 必须知道实际端口,传 `0` 会直接报错。
- 断线重连会重新请求分配,端口可能变化(与 `ssh -R 0:...` 一致);需要固定端口时请改用具体值。

### 日志与故障排查

启动时会打印配置来源、SSH 目标与转发计划,可以直接确认配置文件是否被读取、参数最终生效值:

```
time="2026/11/17 19:12:38" level=INFO msg="tunnel 启动" version=v0.1.0 role=server
time="2026/11/17 19:12:38" level=INFO msg=配置来源 source="命令行 -c" file=/etc/tunnel/tunnel.toml
time="2026/11/17 19:12:38" level=INFO msg="SSH 目标" addr=vps.example.com:22 user=tunnel key=/root/.ssh/id_ed25519
time="2026/11/17 19:12:38" level=INFO msg=反向转发计划 vps_listen=127.0.0.1:5102 inner_target=127.0.0.1:22
```

- **VPS 端口被占用**:sshd 只会回一个通用的 `request denied`,tunnel 会转成明确错误并提示在 VPS 上执行 `ss -ltnp | grep ':5102'`,随后以退出码 1 终止 —— 不再静默重连。
- **本机监听端口被占用**:同样立即失败并给出端口占用提示。
- **私钥/主机公钥/认证错误**:不可恢复,立即退出并指出具体原因。
- **网络抖动、VPS 重启、连接被断开**:保留指数退避重连,每次失败都会记录错误、尝试次数与下次重试等待时间。

退出码:`0` 正常停止(SIGINT/SIGTERM)、`1` 不可恢复错误、`2` 命令行用法错误。

日志级别用 `--log-level=debug|info|warn|error` 控制(默认 `info`,大写亦可):

```bash
tunnel server -c tunnel.toml --log-level=warn   # 只保留告警与错误
tunnel server -c tunnel.toml --log-level=debug  # 额外记录每次转发的连接与 keepalive
```

`debug` 会打印监督循环参数、每条接入连接以及 keepalive 探测结果;每条连接转发失败(内网 sshd 不可达、ssh 通道建立失败)始终以 `WARN` 记录。库层用 `tunnel.WithForwardLogger` / `tunnel.WithLogger` 注入同一个日志器。

## 作为库使用(移动端)

```go
import "github.com/pangxincheng/tunnel/tunnel"

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
// tunnel.NewClient(cfg, tunnel.ReverseForward("127.0.0.1:5102", "127.0.0.1:22"))
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
