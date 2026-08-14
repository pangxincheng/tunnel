# tunnel · 设计文档

日期:2026-08-14(修订:实现路线改为 ssh 编排器)
状态:已评审

通用 SSH 端口转发工具。单 binary 双子命令:`tunnel server`(内网侧反向转发守护)与 `tunnel client`(客户端接入)。组合效果:客户端一条命令登进内网 sshd,公网 VPS 只暴露一个 SSH 端口。

## 1. 范围

**做**:一条端口映射(VPS loopback:PORT ↔ 内网 sshd);server 守护(掉线退避重连);client 交互登录与 `-N` 守护两种模式;纯 flags + ssh_cmd 透传。

**不做**:自研 SSH 协议栈(2026-08-14 修订:全部交还给系统 ssh)、配置文件(参数已足够少)、多映射、公网直连模式、终端复用。

## 2. 实现路线:ssh 编排器

tunnel **不包含任何 SSH 协议逻辑**,只做两件事:**参数组装**(把意图翻译成正确的 ssh 命令行)与**进程监督**(保活、掉线重连)。认证、加密、known_hosts 校验、心跳、字节代理全部归还给系统 OpenSSH 客户端。

带来的性质:

- 用户的认证方式(密钥/agent/密码)、TOFU 确认、`-o` 自定义选项原样生效 —— 零适配成本
- 主机公钥校验由 ssh 的 known_hosts 负责(标准 TOFU 模型,交互可见)
- 假死发现用 ssh 原生的 `ServerAliveInterval/ServerAliveCountMax`,转发失败用 `ExitOnForwardFailure=yes` 立即暴露

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
tunnel server [--local-ssh-port 22] [--remote-listen-port 5102] -- <ssh_cmd...>

tunnel client [--remote-listen-port 5102] [--remote-listen-user $USER] -- <ssh_cmd...>

tunnel client -N [--listen 127.0.0.1:5102] [--remote-listen-port 5102] -- <ssh_cmd...>
```

- `ssh_cmd`:完整的 ssh 参数(如 `ssh -p 22 ubuntu@1.2.3.4` 去掉开头的 `ssh`),**原样透传、零解析**;tunnel 只在前面拼接自己计算的转发/选项参数
- `--local-ssh-port`:内网 sshd 端口(server 转发目标)
- `--remote-listen-port`:VPS loopback 上的汇合端口,server/client 必须一致
- `--remote-listen-user`:内网 sshd 的登录用户(client 交互模式内层登录用)
- `--listen`:client `-N` 模式的本机监听地址

### server 翻译结果

```bash
ssh -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=15 -o ServerAliveCountMax=3 \
    -R <remote-listen-port>:127.0.0.1:<local-ssh-port> <ssh_cmd...>
```

监督:进程退出 → 指数退避(1s→30s,稳定 60s 归零)→ 重连;Ctrl-C 杀掉 ssh 子进程后退出。

### client 交互模式翻译结果

```bash
# ① 外层(后台):
ssh -N -o ExitOnForwardFailure=yes -L <remote-listen-port>:127.0.0.1:<remote-listen-port> <ssh_cmd...>
# ② 外层端口就绪后,内层(前台,交互):
ssh -p <remote-listen-port> <remote-listen-user>@127.0.0.1
```

内层退出 → 杀掉外层 → 退出。**不重连**(交互会话状态续不上,重连是假象)。

### client -N 守护模式翻译结果

```bash
ssh -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=15 -o ServerAliveCountMax=3 \
    -L <listen-port>:127.0.0.1:<remote-listen-port> <ssh_cmd...>
```

监督逻辑与 server 相同(退避重连)。

## 5. 错误处理

| 场景 | 行为 |
|---|---|
| 参数缺失/无法解析 | 打印用法,退出码 2 |
| 系统无 `ssh` 可执行文件 | 启动报错 |
| 首次连接 TOFU 确认 | ssh 原生交互提示,原样呈现 |
| VPS 端口被占 / 认证失败 | ssh 立即退出(ExitOnForwardFailure),server/-N 模式退避重连;client 交互模式报错退出 |
| 连接假死 | ServerAlive 超时,ssh 自行退出,进入重连 |
| client 交互模式外层断开 | 内层 ssh 随之断开,tunnel 报错退出(不重连) |
| SIGINT/SIGTERM | 杀掉 ssh 子进程,优雅退出 |

## 6. 测试策略

- **参数组装单测**:给定 flags + ssh_cmd,断言生成的 argv 精确匹配(三种模式)
- **监督循环单测**:用假的 ssh 可执行文件(shell 脚本,可控退出时机/退出码),验证退避增长、稳定归零、Ctrl-C 杀子进程
- **集成冒烟**(手动):本机 sshd 扮演 VPS,`tunnel server` + `tunnel client` 交互模式登入;`ExitOnForwardFailure` 端口占用路径
