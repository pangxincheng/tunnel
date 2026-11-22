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
- **按名字转发**:`--name nas` 以 VPS 上的 Unix socket 作汇合点,不用记端口,断线重连不撞车
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

如果要用按名字转发(推荐,见下文),在 VPS 的 `/etc/ssh/sshd_config` 中加入后执行 `sudo systemctl reload ssh`:

```
StreamLocalBindUnlink yes        # 必需:重连时替换上次残留的 socket 文件
AllowStreamLocalForwarding yes   # 默认即 yes;配置被收紧过时需确认
```

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

### 按名字转发(推荐)

用 `--name` 代替 `--remote-listen-port`:server 在 VPS 上监听 `~/.tunnel/sockets/<name>.sock`,client 按同一名字连接,不需要记 VPS 端口。断线重连时,sshd(`StreamLocalBindUnlink yes`)会替换残留的 socket,不会出现端口被旧连接占住的问题。

```bash
# 内网服务器
tunnel server --name nas --remote-ip vps.example.com --remote-ssh-pub ~/.ssh/vps_host.pub
# 客户端:名字模式下必须指定本机监听端口
tunnel client --name nas --local-ssh-port 5102 --remote-ip vps.example.com --remote-ssh-pub ~/.ssh/vps_host.pub
ssh -p 5102 <内网用户>@127.0.0.1
```

TOML 中写 `name = "nas"`。`name` 与 `remote_listen_port` 只能二选一;同时出现时,命令行 flag 优先,被压过的那一项忽略。

注意:

- **server 与 client 必须用同一个 VPS 账户登录,且 `remote_socket_dir` 配置一致**(都不写,或写成同一路径)。每一端解析的都是自己账户的 `$HOME/.tunnel/sockets`,socket 目录的权限是 700,其他账户无法访问。两边不一致时,client 的每次连接都会失败,表现和"server 不在线"一样。排查时对照 server 的「隧道就绪」日志和 client 的「VPS socket 已解析」日志,看两边路径是否相同。
- **名字需全局唯一**:两台机器用同一个名字时,后连上的一台会顶替前一台。
- **socket 目录**:默认每次连接后在 VPS 上执行 `mkdir -p -m 700 "$HOME/.tunnel/sockets"`,自动创建并解析目录。如果 tunnel 账户不允许执行命令(shell 为 nologin 或设置了 ForceCommand),请配置 `remote_socket_dir`(`--remote-socket-dir`,绝对路径),并在 VPS 上预先创建该目录、`chmod 700`。
- 目录权限为 700,只有 tunnel 账户能连接这些 socket。这比 loopback 端口(VPS 上任何本地用户都能连)更收紧。
- 名字只能包含字母、数字和 `. _ -`,须以字母或数字开头,最长 64 个字符。socket 完整路径不能超过 103 字节。
- 如果 VPS 拒绝 socket 转发,tunnel 会提示检查 `StreamLocalBindUnlink` / `AllowStreamLocalForwarding`,并以退出码 1 终止。

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

### 开机自启与后台运行(tunnel install)

`tunnel install` 把内网侧的按名字共享装成系统服务:开机或登录后自动运行,断线自动重连,出错不退出,可随时查看状态、暂停或恢复。服务进程是 `tunnel run-service`,由服务管理器启动,不需要手动运行。

```bash
# 开机即运行(不需要登录),需要 root:非 root 且有终端时会自动经 sudo 重新执行
sudo tunnel install --mode system --name nas -c tunnel.toml
# 登录后运行(当前用户的登录项),不要用 sudo
tunnel install --mode user --name nas -c tunnel.toml
```

| 平台 | `--mode system`(开机即运行) | `--mode user`(登录后运行) |
| --- | --- | --- |
| macOS | LaunchDaemon(`/Library/LaunchDaemons`) | LaunchAgent(`~/Library/LaunchAgents`) |
| Linux | systemd 系统服务 | `systemd --user` 服务 |
| Windows | 不支持 | 任务计划程序登录任务(无控制台窗口,失败后重启) |

- `install` 只支持按名字转发(必须有 `--name` 或配置文件 `name`),共享本机 `127.0.0.1:<local_ssh_port>`(默认 22)。参数与 `tunnel server` 相同,也读 `-c` / `./tunnel.toml`;`--user` 仍是 VPS 登录用户(sudo 下默认取发起 sudo 的用户)。
- 安装前会实际连一次 VPS 并解析 socket 目录,失败直接报错(退出码 1),不留下服务。
- 服务名为 `tunnel-<profile>-<name>`;`--profile`(或配置文件 `profile`)区分不同的 VPS,默认 `default`。
- 安装时把自身复制到固定位置(system:`/usr/local/bin/tunnel`;user:`~/.local/bin/tunnel`、macOS `~/Library/Application Support/tunnel/bin/tunnel`、Windows `%USERPROFILE%\AppData\Local\tunnel\tunnel.exe`),并把合并后的配置写到服务的配置目录(`tunnel.toml`,权限 0600,主机公钥内联)。system 模式还会把私钥复制为配置目录下的 `id_key`(0600),服务不依赖你家目录里的文件。
- 重复执行 `install` = 覆盖配置并重启服务(升级 tunnel 或修改参数时直接重新 install)。中途失败可修正后重新执行。

查看与操作(经本机控制通道;只有安装该服务的用户与 root 可以操作):

```bash
tunnel status                 # 状态:连接中 / 已就绪 / 等待重连 / 失败 / 已暂停
tunnel status --json          # 原样输出 JSON(供脚本 / 桌面 App 使用)
tunnel pause                  # 暂停共享并断开;重启后仍保持暂停
tunnel resume                 # 恢复共享
tunnel retry                  # 跳过退避等待立即重连(失败修正后用它重试)
```

`--mode` / `--profile` / `--name` 缺省时自动查找:依次查找 user、system 中已安装的服务(两者都有时要求 `--mode`),该 profile 下只有一个服务时自动选中。服务已安装但控制通道连不上时,`status` 输出「已安装,未运行」,退出码 3。暂停**不**删除 VPS 上的设备信息,其他设备会看到它「离线 / 最后在线」。

日志:Linux 用 `journalctl -u tunnel-<profile>-<name> -f`(user 模式加 `--user`);macOS 在 `/var/log/tunnel/<服务名>.log`(system)或 `~/Library/Application Support/tunnel/<服务名>/tunnel.log`(user);Windows 在 `%USERPROFILE%\AppData\Roaming\tunnel\<服务名>\tunnel.log`。

卸载:

```bash
sudo tunnel uninstall --mode system --name nas   # user 模式不加 sudo
```

卸载会停止并删除服务,用服务配置连一次 VPS 删除本机的设备信息文件与残留 socket(只删属于本机的;VPS 连不上只打印警告,不阻止卸载),再删除配置目录与状态文件。复制到固定位置的 `tunnel` 二进制与日志保留。

断电 / 重启后自动恢复的提示:

- **macOS**:开启 FileVault 时,开机后需要有人在登录窗口输入密码解锁磁盘,之前任何服务(包括 system 模式)都不会运行;无人值守的机器请关闭 FileVault 或接受这一点。另在「系统设置 → 能源」中打开「断电后自动启动」。
- **Linux**:要在来电后自动开机,需在 BIOS/UEFI 中把「AC Power Recovery / Restore on AC Power Loss」设为 Power On。user 模式默认只在登录后运行;需要不登录也运行时用 system 模式(或 `loginctl enable-linger <用户>`)。
- **Windows**:只支持 user 模式,需要登录(可配合 Windows 自动登录)。

#### 真机验证(由使用者在目标机器上执行)

开发与自动化测试只使用假的服务管理器与临时目录,从不安装真实服务。发布前请在目标机器上逐项确认:

1. **macOS**:`sudo tunnel install --mode system --name <名字> -c tunnel.toml` 后 `tunnel status` 为「已就绪」;重启后**无需登录**仍可经 VPS 连上;`tunnel pause` / `tunnel resume` 生效(暂停后其他设备看到离线);`sudo tunnel uninstall` 后 `launchctl print system/com.pangxincheng.tunnel-default-<名字>` 报不存在、`/etc/tunnel/tunnel-default-<名字>` 已删除。再用 `--mode user` 做一次(重新登录后自动运行)。
2. **Linux**:同上,system 与 user 各一次(`systemctl status tunnel-default-<名字>`、`systemctl --user status ...`)。
3. **Windows**:`tunnel install --mode user ...` 后服务立即运行、**没有控制台窗口**,`tunnel status` 为「已就绪」;注销再登录后自动运行;`tunnel uninstall` 后任务计划程序中的任务消失。

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

按名字转发对应 `tunnel.ReverseForwardUnix(dir, "nas", "127.0.0.1:22")` / `tunnel.LocalForwardUnix("127.0.0.1:5102", dir, "nas")`(`dir` 传 `""` 表示连接后自动解析 `$HOME/.tunnel/sockets`)。

`ReverseForward`/`LocalForward` 工厂已内置 listener 创建与 accept/代理循环,开箱即用。

经 `gomobile bind` 编译即可供 iOS/Android 调用。

## 安全说明

- 两段 SSH(VPS↔内网、客户端↔VPS)都是标准 OpenSSH 加密通道;经 VPS 转发的应用流量(如客户端到内网的 ssh)对 VPS 不可见
- VPS 主机公钥钉住:与配置不符即拒绝连接 —— 主机密钥变更时应先确认是重装还是攻击
- 反向转发默认绑 VPS loopback,公网无法直接触达内网端口;攻击面 = VPS 的 sshd

## 文档

- 设计文档:`docs/superpowers/specs/2026-08-14-tunnel-design.md`、`docs/superpowers/specs/2026-11-22-name-forwarding-design.md`(按名字转发)
- 实施计划:`docs/superpowers/plans/2026-08-14-tunnel-orchestrator.md`、`docs/superpowers/plans/2026-11-22-name-forwarding.md`
- 变更记录:`CHANGELOG.md`

## License

MIT(见 LICENSE)
