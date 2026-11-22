# Changelog

## 2026-11-22(按名字转发设计)

- 新增设计文档 `docs/superpowers/specs/2026-11-22-name-forwarding-design.md`:server/client 将支持 `--name`,以 VPS 上的 Unix socket(`~/.tunnel/sockets/<name>.sock`)代替 loopback 端口作为汇合点,解决固定端口重连撞车与随机端口难记的问题;端口模式保留。

## 2026-11-17(日志级别)

- 新增 `--log-level=debug|info|warn|error`(默认 info,slog 原生解析,大小写不敏感);非法值以退出码 2 报用法错误。
- `debug` 级别新增内容:监督循环参数、每次转发的接入连接、keepalive 探测成功。
- 转发工厂新增 `WithForwardLogger` 选项(可变参数,保持向后兼容);每条连接的目标拨号失败以 `WARN` 记录,不再静默丢弃,CLI 已自动注入同一日志器。
- README 补充日志级别用法。

## 2026-11-17(自动分配端口)

- server 侧支持 `remote_listen_port = 0`(或 `--remote-listen-port 0`):请求 VPS sshd 自动分配空闲端口,实际端口打印在「隧道就绪」日志中;启动日志会提示重连后可能变化。
- `FileConfig.RemoteListenPort` 改为 `*int`,区分"未配置"(nil)与"显式 0"(自动分配);未配置仍然报错,client 模式传 0 明确拒绝。
- 新增端口范围校验(0-65535、remote-ssh-port 1-65535),`portArg` 统一处理"显式 0"语义。
- `tunnel.toml.example` 与 README 补充自动分配端口说明;新增 TOML 显式 0、CLI 合并与校验测试。

## 2026-11-17

- 修复 TOML 配置中的 `remote_ssh_port` 永远不生效:docopt 的 `[default: 22]` 被当作显式参数,覆盖了配置文件;默认值改在 `merge` 里兜底,优先级恢复为 默认 < TOML < flag。`cmd/tunnel/main_test.go` 增加合并优先级测试。
- 启动日志改为 `log/slog`:打印版本/角色、配置来源(显式 `-c`、自动发现 `./tunnel.toml` 或纯命令行)、SSH 目标、私钥路径与转发计划,`tunnel server -c xxx.toml` 可直接确认配置是否被读取。
- VPS 端口被占用(sshd 拒绝 `tcpip-forward`)时立即报错退出:错误文案包含端口号与 `ss -ltnp | grep ':PORT'` 排查命令,不再每 30 秒静默重试;新增 `RemoteListenError`/`LocalListenError`。
- 不可恢复错误分类(新增 `ErrPermanent`/`IsPermanent`):私钥读取或解析失败、主机公钥解析或指纹不匹配、SSH 认证失败、本机监听失败、VPS 拒绝监听均快速失败并以退出码 1 结束;网络抖动、对端断开、keepalive 失败仍按指数退避重连。
- 重连日志带尝试次数、失败原因与下次等待时间;keepalive 探测失败会记录原因,ctx 正常取消时不刷告警。
- `Client` 新增 `WithLogger` 选项注入 slog 日志器(移动端可传 discard 静默),`NewClient` 保持向后兼容的可变参数签名。
- README 增加「日志与故障排查」小节(含退出码约定),并修正库示例中过时的 `ReverseForward` 参数签名。

## 2026-08-14(实现)

- 新增 `backoff.go`:带 ±20% 抖动的指数退避。
- 新增 `ssh.go`:Dial —— 私钥认证(KeyPEM/KeyPath)+ 主机公钥钉住(.pub 文件或内联 authorized_keys 双形态,算法收敛防多密钥误判);`testserver_test.go` 进程内测试 SSH 服务器(双主机密钥)。
- 新增 `forward.go`:ReverseForward(ssh -R 程序化)+ ProxyToLocal/proxyBidirectional(L4 双向转发);测试服务器扩展 tcpip-forward/forwarded-tcpip 协议;端到端 echo 验证。
- `forward.go` 追加 LocalForward(ssh -L 程序化,direct-tcpip 通道);测试服务器支持 direct-tcpip 代连;正向端到端 echo 验证。
- 新增 `client.go`:监督循环 —— keepalive 探活(15s,失败主动断连)、指数退避重连(1s→30s,稳定 60s 归零)、ctx 取消优雅退出;断线重连测试验证。
- 新增 `cmd/tunnel`:CLI 薄壳,`server`/`client` 双子命令;公共参数 `--remote-ip/--remote-ssh-port/--remote-ssh-pub/--local-ssh-key/--user`。本机冒烟通过:server(15102→:22)+ client(15103→15102)两层转发后 `ssh -p 15103` 成功登入。
- 库代码迁入 `tunnel/` 子目录,module 更名 `remote-agent`(库导入路径 `remote-agent/tunnel`);新增 TOML 配置支持(`tunnel/configfile.go` + CLI `-c/--config`,默认自动读取 `./tunnel.toml`,优先级:默认 < TOML < flag,`~` 路径展开),附 `tunnel.toml.example`;TOML 冒烟通过(server 纯配置启动,client 配置 + flag 覆盖)。
- 补强中文注释:`cmd/tunnel/main.go` 详解两阶段配置合并(预扫描 -c 的原因)与 server/client 复用同一监督循环的 establish 注入设计;`forward.go` 补齐 tcpip-forward/direct-tcpip 的协议步骤说明。
- 新增 `tunnel version` 子命令(版本常量 `tunnel.Version = 0.1.0` 位于库层,移动端可共享);usage 文案更新为子命令说明 + 公共参数 + 完整使用示例。
- 修复 .gitignore:`/tunnel` 规则在库迁入 `tunnel/` 目录后误伤源码,导致 `configfile.go`/`configfile_test.go`/`version.go` 三个新文件此前未被提交(已跟踪文件不受影响所以本地一切正常);移除该规则并补提交。
- 新增 `README.md`(架构图、编译、server/client 使用、TOML 配置、库/gomobile 用法、安全说明)与 MIT `LICENSE`。
- 新增 `tunnel/forwarders.go`:`ReverseForwarder`/`LocalForwarder` 工厂 —— 把 listener 创建与 accept/代理循环封装为可直接传给 `NewClient` 的 establish 闭包,调用方(CLI/移动端)不再手写胶水;CLI 改用工厂,README 库示例同步简化。
- 精简转发 API:删除只建 listener 的中间层函数,逻辑并入工厂;工厂更名 `ReverseForward`/`LocalForward`(去掉 -er 后缀);冗余的底层端到端测试合并入工厂测试。
- 合并 `forwarders.go` 回 `forward.go`(转发能力单文件承载:两工厂 + ProxyToLocal + proxyBidirectional),测试文件同步更名 `forward_test.go`。
- CLI 参数解析改用 `urfave/cli/v3`:子命令/flag 声明式定义,公共 flag 与端口 flag 收敛为 `commonFlags`/`portFlags` 两个生成函数,TOML 值作为 flag 默认注册(优先级 默认 < TOML < flag 不变);曾评估 cobra+viper 因依赖过重放弃。
- 端口默认值收敛为常量:`defaultRemoteListenPort=5102`、`defaultServerTargetPort=22`、`defaultClientListenPort=5102`,消除散落的魔法数字。
- CLI 参数解析最终定稿为 docopt(参照 pTunnel):usage 文本即语法定义,合并规则 flag > TOML > 内置默认;`--remote-listen-port` 无默认值(部署决策必填),client 的 `--local-ssh-port` 默认跟随汇合端口;移除 urfave/cli 依赖。
- 修复 README 编译命令:二进制输出改为 `dist/tunnel`(根目录 `./tunnel` 与库源码目录冲突)。
- 新增 GitHub Actions 发布工作流(`.github/workflows/release.yml`):推送 `v*` tag 触发 → 测试 → linux/windows/darwin × amd64/arm64 六平台交叉编译(`CGO_ENABLED=0`、`-trimpath`、`-ldflags` 注入版本)→ 打包(含 `tunnel.toml.example` 与 `LICENSE`)→ 自动创建 GitHub Release;`tunnel.Version` 由 const 改为 var 以支持注入(默认 "dev")。
- README 增加图标(`assets/icon.svg`,手绘 SVG:穿墙箭头)与徽章行(CI 状态、最新 release、Go 版本、License);徽章链接按 `pangxincheng/remote-agent` 仓库路径生成,如实际路径不同需调整。
- 仓库定名 `tunnel`:Go module 路径改为 `github.com/pangxincheng/tunnel`,CLI 导入、CI ldflags 注入路径、README 徽章与克隆/引用说明同步更新;README 克隆地址填入真实仓库 URL。


## 2026-08-14(修订 2)

- spec 与实施计划重写:x/crypto/ssh 库(模块根 package tunnel,gomobile 可注入)+ 薄 CLI;命令最终定为 `server`/`client` 双子命令(砍掉 connect);公共参数 `--remote-ip/--remote-ssh-port/--remote-ssh-pub/--local-ssh-key/--user`,主机公钥钉住替代 known_hosts TOFU(确定性模型,移动端注入友好);新增 `docs/superpowers/plans/2026-08-14-tunnel-orchestrator.md`(6 任务)。

## 2026-08-14(修订)

- 重写 `docs/superpowers/specs/2026-08-14-tunnel-design.md`:实现路线由"自研 SSH 协议栈"改为 **ssh 编排器** —— tunnel 只做参数组装与进程监督,认证/加密/known_hosts/心跳全部交还系统 ssh;命令形态改为 flags + ssh_cmd 原样透传;client 分交互登录与 -N 守护两种模式;砍掉配置文件。

## 2026-08-14

- 新增 `docs/superpowers/specs/2026-08-14-tunnel-design.md`:仓库范围重新定为单一通用 SSH 转发工具(移除 ra/rad 终端复用);设计定稿 —— 单 binary 双子命令(server 反向转发 VPS loopback:PORT → 内网:22,client 正向转发经 direct-tcpip 接入)、单映射、TOML 配置 + flag 覆盖、主机公钥钉住、keepalive + 指数退避重连、VPS 侧零自研服务端。
