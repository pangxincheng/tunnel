# Changelog

## 2026-08-14(实现)

- 新增 `backoff.go`:带 ±20% 抖动的指数退避。
- 新增 `ssh.go`:Dial —— 私钥认证(KeyPEM/KeyPath)+ 主机公钥钉住(.pub 文件或内联 authorized_keys 双形态,算法收敛防多密钥误判);`testserver_test.go` 进程内测试 SSH 服务器(双主机密钥)。
- 新增 `forward.go`:ReverseForward(ssh -R 程序化)+ ProxyToLocal/proxyBidirectional(L4 双向转发);测试服务器扩展 tcpip-forward/forwarded-tcpip 协议;端到端 echo 验证。
- `forward.go` 追加 LocalForward(ssh -L 程序化,direct-tcpip 通道);测试服务器支持 direct-tcpip 代连;正向端到端 echo 验证。
- 新增 `client.go`:监督循环 —— keepalive 探活(15s,失败主动断连)、指数退避重连(1s→30s,稳定 60s 归零)、ctx 取消优雅退出;断线重连测试验证。
- 新增 `cmd/tunnel`:CLI 薄壳,`server`/`client` 双子命令;公共参数 `--remote-ip/--remote-ssh-port/--remote-ssh-pub/--local-ssh-key/--user`。本机冒烟通过:server(15102→:22)+ client(15103→15102)两层转发后 `ssh -p 15103` 成功登入。
- 库代码迁入 `tunnel/` 子目录,module 更名 `remote-agent`(库导入路径 `remote-agent/tunnel`);新增 TOML 配置支持(`tunnel/configfile.go` + CLI `-c/--config`,默认自动读取 `./tunnel.toml`,优先级:默认 < TOML < flag,`~` 路径展开),附 `tunnel.toml.example`;TOML 冒烟通过(server 纯配置启动,client 配置 + flag 覆盖)。


## 2026-08-14(修订 2)

- spec 与实施计划重写:x/crypto/ssh 库(模块根 package tunnel,gomobile 可注入)+ 薄 CLI;命令最终定为 `server`/`client` 双子命令(砍掉 connect);公共参数 `--remote-ip/--remote-ssh-port/--remote-ssh-pub/--local-ssh-key/--user`,主机公钥钉住替代 known_hosts TOFU(确定性模型,移动端注入友好);新增 `docs/superpowers/plans/2026-08-14-tunnel-orchestrator.md`(6 任务)。

## 2026-08-14(修订)

- 重写 `docs/superpowers/specs/2026-08-14-tunnel-design.md`:实现路线由"自研 SSH 协议栈"改为 **ssh 编排器** —— tunnel 只做参数组装与进程监督,认证/加密/known_hosts/心跳全部交还系统 ssh;命令形态改为 flags + ssh_cmd 原样透传;client 分交互登录与 -N 守护两种模式;砍掉配置文件。

## 2026-08-14

- 新增 `docs/superpowers/specs/2026-08-14-tunnel-design.md`:仓库范围重新定为单一通用 SSH 转发工具(移除 ra/rad 终端复用);设计定稿 —— 单 binary 双子命令(server 反向转发 VPS loopback:PORT → 内网:22,client 正向转发经 direct-tcpip 接入)、单映射、TOML 配置 + flag 覆盖、主机公钥钉住、keepalive + 指数退避重连、VPS 侧零自研服务端。
