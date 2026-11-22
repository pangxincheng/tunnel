# Changelog

## 2026-11-22(tunnel-app 支持)

- 修复睡眠唤醒后半开连接长时间停在 ready(终审 I1):共用的 `keepalive` 在 2×间隔内收不到回复即关闭连接(Client / Session / Share 都受益,检测上限约 3×keepalive 间隔);`RetryNow()` / `Share.Retry()` 在 ready 时强制断开当前连接并立即重连、重置退避(Share 此时跳过需要对端回复的远端监听撤销,不会卡 2 秒)。测试服务器新增 `IgnoreKeepalive`(TCP 不断、keepalive 不回复)。
- user 模式控制通道目录加固(终审 I2):`ControlListen(addr, -1)` 要求父目录属于当前用户、权限恰为 0700 且不是符号链接(缺失时以 0700 创建),否则报错拒绝监听,防止 `/tmp` 下被其他本机用户预先占位。
- `ListDevices` 在 ctx 取消时返回 `ctx.Err()`,不再返回「全部离线」且 err 为 nil(终审 M1);`Device.Online` 的 JSON 键改为 `online`(终审 M2,其余跨控制通道 / JSON 的导出结构已核对均有 snake_case 标签)。
- CLI 新增服务子命令(`cmd/tunnel/service.go`):`tunnel install --mode system|user [--profile P] --name NAME [server 的全部参数]` 依次执行权限检查(system 非 root:有终端时经 `sudo` 以绝对路径重新执行自身、参数原样,无终端报「--mode system 需要 root 权限,请用 sudo 运行」;user 以 root 运行报「--mode user 不能以 root 运行(会装进 root 自己的登录项,永远不会启动)」)→ 合并参数(只支持名字模式,端口模式报「tunnel install 只支持 --name 按名字转发」)→ 实际连一次 VPS 并解析 socket 目录(失败退出码 1、不写任何文件)→ 写服务配置目录 `tunnel.toml`(0600,主机公钥内联)→ system 模式复制私钥为 `id_key`(0600)并记录安装用户 UID 到 `owner` → 复制自身到固定位置(临时文件 + 重命名)→ 创建日志目录(launchd 不会自动创建)→ `Manager.Install`;重复执行即覆盖并重启。sudo 下 VPS 用户默认值、`~` 展开与控制通道属主取 `SUDO_USER` / `SUDO_UID`。
- `tunnel run-service`:读服务配置 → `NewShare` → 控制通道(system 模式属主为安装用户)→ `Share.Run`,SIGINT/SIGTERM 优雅退出;`set-name` 原子改写配置中的 `name`(服务名不变)。Windows 下启动即 `FreeConsole` 释放控制台,日志改为追加写入服务目录下的 `tunnel.log`。
- `tunnel status [--json]` / `pause` / `resume` / `retry`:经控制通道读取或操作;`--mode` / `--profile` / `--name` 缺省时依次查找 user、system 中已安装的服务(两种模式都有时要求 `--mode`,profile 下多个服务时要求 `--name`);已安装但控制通道连不上输出「已安装,未运行」、退出码 3,无权访问控制通道时报错、退出码 1。
- `tunnel uninstall`:同样先做权限检查,`Manager.Uninstall` 成功后用服务配置连一次 VPS,经 `RemoveDevice` 删除本机的 `<name>.json` 与残留 `<name>.sock`(VPS 连不上只打 WARN,不阻止卸载),再删除配置目录、状态目录与残留控制 socket;服务删除失败时保留配置,可重新执行;系统中已没有服务登记(如安装中途失败、Windows `schtasks /Delete` 对不存在的任务报错)时先经 `Manager.Installed` 判断并跳过 `Uninstall`(打 WARN),仍清理配置、状态与 VPS 残留,不会永远卸不掉。暂停不删除设备信息文件。
- docopt 用法:`[options]` 不包含任何用法行中显式出现的选项,因此 `server` / `client` 行显式加上 `[--name=NAME]`,行为不变;`server` / `client` 不接受 `--mode` / `--profile` / `--json`。`merge` 抽出 `mergeWith`(默认 VPS 用户与家目录由调用方给出),`tunnel server/client` 行为不变。`FileConfig` 新增 `profile` 字段(`tunnel install` 使用)。
- README 新增「开机自启与后台运行(tunnel install)」一节:两种模式与平台矩阵、status/pause/resume/retry、日志位置、卸载、macOS FileVault 与「断电后自动启动」、Linux BIOS 来电开机提示与真机验证步骤;`tunnel.toml.example` 补充 `profile` 说明。
- [pending] 真机验证 `tunnel install / status / pause / resume / uninstall`:需用户在 macOS(system + user)、Linux(system + user)、Windows(user,确认无控制台窗口、注销再登录后自动运行)上按 README「真机验证」逐项执行;开发机上只用假的服务管理器与临时目录测试。

- 新增 `internal/svc`:系统服务适配。`ServiceName(profile,name)` → `tunnel-<profile>-<name>`;`PathsFor(mode,svc,goos,home)` 给出各平台配置目录/状态文件/控制通道地址(Unix 控制 socket 均位于专用 `tunnel/` 或服务专属目录)/二进制/日志路径;`renderSystemdUnit`、`renderLaunchdPlist`、`renderTaskXML` 渲染服务定义(ExecStart 按 systemd 规则引用并转义 `%`/`$`,plist 与任务 XML 做 XML 转义,Windows 参数按 CommandLineToArgvW 规则引用,任务 XML 以带 BOM 的 UTF-16LE 写出)。`NewManager(mode,goos,home,runner,opts...)` 的 `Install`/`Uninstall`/`Installed` 编排 systemctl(user 加 `--user`)、launchctl(`bootout` 后 `bootstrap system|gui/<uid>`)、schtasks 命令序列;Runner、文件读写(`WithFS`)与 uid(`WithUID`)均可注入,重复安装即更新并重启(systemd 追加 `restart`,Windows 先 `schtasks /End` 再 `/Run`);Windows + system 返回「Windows 暂不支持开机即运行(--mode system)」。
- 新增本机控制通道(`tunnel/control.go`、`control_unix.go`、`control_windows.go`):`ControlListen(addr, ownerUID)` 在 Unix 创建父目录(0700)、仅当残留文件是 socket 时删除、监听后 `chmod 0600`,`ownerUID >= 0` 时 `chown`;Windows 用命名管道(新增依赖 `github.com/Microsoft/go-winio v0.6.2`),安全描述符只允许 SYSTEM 与当前用户。`ServeControl(ctx, l, share, onSetName)` 每个连接一行 JSON 请求(`status|pause|resume|retry|set-name`)、一行 JSON 响应后关闭,单连接 5 秒读写时限,ctx 取消时关闭监听并等待进行中的连接;`set-name` 先 `Share.SetName`,成功后调用 `onSetName` 持久化。`ControlCall(addr, req)` 为 3 秒超时的客户端。
- 新增 `RemoveDevice(sc, dir, name, deviceID)`(`tunnel/remotefs.go`):仅当 `<name>.json` 属于该设备时,同时删除 `<name>.sock` 与 `<name>.json`(真实 sshd 撤销转发或断线时不删除 socket 文件);json 不存在、损坏或属于其他设备时不删任何文件。`Share.SetName` 清理旧名字时改用它,旧名字不再以「只有 socket 的离线设备」残留在设备列表中。
- 新增 `Share` 共享运行时(`tunnel/share.go`):`NewShare(ShareConfig{Conn, SocketDir, Name, Target, StatePath, DeviceID}, opts...)`,每次连接依次解析 socket 目录 → 删除改名前旧名字的本机信息文件 → 名字检查 → 写设备信息(`started_at` = 本次就绪时间)→ 反向转发 `Target`;断线按退避重连,不可恢复错误停在 `failed` 等待 `Retry()`,`Run` 只在 ctx 取消时返回。名字检查:`<name>.json` 的 `device_id` 非空且不是本机时返回新增的 `*NameTakenError`(不可恢复,文案「「<name>」已被另一台设备(<hostname>)使用,换一个设备名字」),损坏或缺 `device_id` 的旧文件视为无主并覆盖。`Pause()` / `Resume()` 把 `{"paused":…}` 原子写入 `StatePath`(0600)并立即断开 / 重连,重启后保持暂停;暂停不删除设备信息文件。`SetName` 校验名字后断开,并在下次连上时删除旧名字的信息文件(仅属于本机时)再以新名字重连。`Status()` 返回 `ShareStatus`(phase/err/retry_at/attempt/name/socket/since/paused/version);主动断开时先撤销远端 socket 监听(限时 2 秒)再关闭连接。`ConnState.RetryAt` 改为 `omitzero`,零值不再出现在 JSON 中;`LocalDeviceInfo` 内部抽出按给定设备 ID 构造的 `localDeviceInfo`。
- `Session` 的本机转发 accept 循环只在监听被关闭(`RemoveForward` / `Run` 返回)时退出;瞬时 Accept 错误(如 EMFILE)记 WARN「本机转发接受连接失败,稍后重试」并退避重试(5ms 起翻倍,封顶 1s),不再让跨重连长期存活的转发静默永久失效。
- 新增 `Session`(`tunnel/session.go`):一条 SSH 连接承载多个按名字的正向转发。`AddForward(name, port)` 立即在 `127.0.0.1:port` 监听,与连接状态无关(断线期间保持监听、接入连接立即关闭,重连后自动恢复);端口占用返回 `*LocalListenError`,只影响该转发;同名同端口为无操作,换端口先关旧再开新;`RemoveForward` 同时关闭进行中的连接;`Forwards()` 返回名字 → 端口;`Devices(ctx)` 用当前连接列设备,未连接返回新导出的 `ErrNotConnected`;`Run` 返回时关闭全部本机监听。状态以 `ConnState{phase, err, retry_at, attempt}`(`Phase`:connecting/ready/waiting/failed/paused)暴露,支持 `WithStateHook` 回调;不可恢复错误不退出而停在 failed,`RetryNow()` 在退避或 failed 时立即重连并重置退避。监督状态机为未导出的 `supervisor`,供后续 `Share` 复用。
- `Client` 的 keepalive 循环抽成共用的 `keepalive(ctx, sc, every, logger)`,行为不变(探测成功的 debug 日志 `addr` 改为连接的实际对端地址)。
- 新增 `ListDevices`(`tunnel/devices.go`):经 SFTP 列出 `*.json` 与 `*.sock` 的并集(只有 socket 的设备仅含名字),对每个名字并发(上限 8)拨号探测 socket,3 秒超时,成功即在线,按名字升序返回 `[]Device`;设备名以文件名为准(json 内 `name` 为空或不一致时仍按文件名探测 `<文件名>.sock`)。
- 新增远端设备信息文件读写(`tunnel/remotefs.go`,经 SFTP,依赖 `github.com/pkg/sftp v1.13.12`):`WriteDeviceInfo`(先写 `<name>.json.tmp` 再 `PosixRename`,原子替换)、`ReadDeviceInfos`(损坏文件以仅含名字的零值收录)、`RemoveDeviceInfo`(仅当 `device_id` 相同才删除)。
- 新增 `ResolveSocketDir`:未配置目录时先 exec,账户禁止执行命令时改用 SFTP 取家目录并创建 `~/.tunnel/sockets`(0700);SFTP 被明确拒绝(通道/子系统被拒、权限不足)时仍返回原不可恢复的 `SocketDirError`;连接中断等瞬时失败则返回可恢复错误,监督循环会继续重试。`ResolveSocketPath` 改为调用它。测试服务器支持 `sftp` 子系统(`WithServerWorkingDirectory`)与 `DenySFTP`;原先断言 exec 失败即报错的 3 个 `TestResolveSocketPath*` 现同时置 `DenySFTP`(否则 SFTP 兜底会成功)。
- 新增设备 ID 与本机设备信息(`tunnel/device.go`):`DeviceID()` 取平台机器标识(macOS `IOPlatformUUID`、Linux `/etc/machine-id` 回退 `/var/lib/dbus/machine-id`、Windows 注册表 `MachineGuid`),派生为 `hex(sha256("tunnel-device-id:"+raw)[:16])`;`LocalDeviceInfo(name, startedAt)` 返回含 device_id/name/hostname/os/os_version/arch/tunnel_version/started_at 的 `DeviceInfo`。`golang.org/x/sys` 升为直接依赖(Windows 注册表)。
- 新增实现计划 `docs/superpowers/plans/2026-11-22-core-for-tunnel-app.md`:为 tunnel-app 一期提供设备信息文件与设备列表、多转发 Session、可暂停/重试的 Share 运行时、本机控制通道、`tunnel install/uninstall/status/pause/resume/retry`。

## 2026-11-22(按名字转发)

- 新增 `ValidateName` / `ResolveSocketPath`(`tunnel/socket.go`):校验服务名(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`),并解析 VPS 上的 socket 路径 `<dir>/<name>.sock`;未指定目录时经 SSH exec 执行 `mkdir -p -m 700 "$HOME/.tunnel/sockets"` 自动创建并取回绝对路径,路径超过 103 字节报不可恢复错误。
- 新增 `SocketDirError`:账户不允许执行命令时给出配置 `remote_socket_dir` 的提示。
- 新增 `ReverseForwardUnix(dir, name, localAddr)` / `LocalForwardUnix(localAddr, dir, name)`:经 VPS 上的 Unix socket 按名字转发(`streamlocal-forward@openssh.com` / `direct-streamlocal@openssh.com`),签名与现有工厂同构,可直接传给 `NewClient`;断线重连时残留 socket 由 sshd 的 `StreamLocalBindUnlink` 替换。
- 新增 `RemoteSocketListenError`:sshd 拒绝 socket 转发时提示检查 `StreamLocalBindUnlink` / `AllowStreamLocalForwarding`,并给出 `ls -l` 与 `sshd -T` 排查命令。
- 四个转发工厂改为共用同一个 accept/代理循环(行为与日志文案不变);client 侧拨号失败日志附带名字。
- CLI 新增 `--name NAME` / `--remote-socket-dir DIR`(TOML `name` / `remote_socket_dir`):`--name` 与 `--remote-listen-port` 二选一,同一来源两者都给报错,flag 优先于 TOML;名字模式下 client 必须给 `--local-ssh-port`;启动日志打印 `name` 与 `vps_socket`。
- README 新增「按名字转发(推荐)」一节,并在准备步骤中加入 sshd 的 `StreamLocalBindUnlink yes` 配置;`tunnel.toml.example` 补充 `name` / `remote_socket_dir` 示例。
- socket 目录解析命令限时 15 秒:远程命令不退出(ForceCommand 阻塞、NFS 家目录卡住等)时报不可恢复错误并提示配置 `remote_socket_dir`,不再无日志地永久挂起。
- client 名字模式在解析出 VPS socket 路径后以 INFO 记录「VPS socket 已解析」;README 注明 server 与 client 必须使用同一 VPS 账户且 `remote_socket_dir` 一致。
- 测试 SSH 服务器支持 session/exec 与 streamlocal 转发;新增实现计划 `docs/superpowers/plans/2026-11-22-name-forwarding.md`。

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
