# CHANGELOG

## 2026-11-23
- 新增 `desktop/internal/sharesvc`:通过附带的 tunnel CLI 子进程控制共享服务。`Status`(解析 `status --json`,退出码 0/3/4 对应运行中/已安装未运行/未安装)、`Install` / `Uninstall` / `Pause` / `Resume` / `Retry` / `SetName`(均显式带 `--profile`),`LogPath` 给出各系统日志位置。系统模式安装/卸载经提权:macOS 用 `osascript ... with administrator privileges`(逐参数 shell 单引号转义后再转义为 AppleScript 字符串),Linux 用 `pkexec`,stderr 含 `-128` 或退出码 126/127 返回 `ErrCancelled`,Windows 返回不支持错误。`ExecRunner` 在运行后即删除的空临时目录中执行 CLI,避免读到 `./tunnel.toml`;CLI 失败时错误信息取 stderr 里 `err="..."` 的值。测试只用假 Runner。
- 修复 `internal/probe`:握手成功后仍保持 ctx/10 秒超时守卫,服务器认证后不应答 exec 时 `Test` 不再无限阻塞,ctx 取消或超时返回 `*DialError`(新增测试 `TestProbeStalledExec`);去掉无效的 `ClientConfig.Timeout`,测试改用 `t.TempDir()`。
- 新增 `desktop/internal/probe`:`Test(ctx, Params)` 测试连接——用私钥连接服务器(10 秒超时,并响应 ctx 取消),放行并记录主机公钥,返回 `Fingerprint`(SHA256 指纹)、`HostKey`(authorized_keys 单行)与 `ResolveSocketDir` 解析出的绝对路径。带口令私钥返回 `ErrPassphrase`;认证被拒返回 `*AuthError`;网络/握手/超时失败返回 `*DialError`;socket 目录解析失败返回 `*SocketDirError`;私钥文件无法读取返回普通包装错误。测试使用进程内 SSH 服务器。
- 新增 `desktop/internal/config`:本机配置 `config.json`(`os.UserConfigDir()/tunnel-app/`,JSON 小写下划线字段)。`Load` 缺文件或缺字段时套用默认值(mode/lang=system、accent=green、restore_forwards=true),文件损坏时改名为 `config.json.broken-<数字>` 并返回默认配置;`Save` 经 `os.CreateTemp` + `os.Rename` 原子写入,文件 0600、目录 0700;`ValidateServerName`(`tunnel.ValidateName` + 本机唯一,改名可排除自身);`AllocatePort`(从 5102 起,跳过已分配与被占用端口)与 `CheckPort`(1024–65535,且不与全部服务器其他设备已分配端口冲突)。`go mod tidy` 后 tunnel 成为直接依赖。
- 新增 `desktop/` Wails v3.1.0 工程骨架(React + TypeScript + Vite,已删除模板示例服务与页面,仅保留空白根组件;窗口 "Tunnel" 940×600),附带 tunnel CLI:`wails3 task bundle:tunnel` 把 `github.com/pangxincheng/tunnel/cmd/tunnel` 构建到 `bin/tunnel`(版本号取 go.mod,replace 时为 `dev`),`build` / `dev` 依赖该任务;macOS `.app` 的 `Contents/MacOS/`、deb/rpm/archlinux 包的 `/usr/local/bin/`、NSIS 安装目录均包含 tunnel CLI。新增 `internal/bundled.TunnelPath()`(优先 `TUNNEL_APP_TUNNEL_BIN`,否则查找 App 可执行文件同目录的 tunnel,找不到返回 `ErrMissing`)及测试。`go.mod` 临时 `replace github.com/pangxincheng/tunnel => ../../tunnel`,tunnel 的 CLI 改动发布后去掉。
- [pending] Linux AppImage:`wails3 generate appimage` 只打包单个二进制,尚未把 tunnel CLI 放进 AppImage;Linux / Windows 打包流程未在本机验证。
- 新增桌面端一期实现计划 `docs/superpowers/plans/2026-11-23-desktop-app.md`(Wails v3.1.0 + React/TS,12 个任务;含设置弹窗原地更新、macOS 与 Windows/Linux 窗口按钮位置差异、tunnel CLI 的 status --json 退出码 / set-name 补充)。

## 2026-11-22
- Added phase-1 design `docs/superpowers/specs/2026-11-22-tunnel-app-phase1-design.md`: server-centric desktop app (Wails v3 + React; landscape two-pane window; share-this-computer card with user/system service modes; per-device fixed local ports edited inline; copyable ssh/scp/rsync/sftp command templates with `<USERNAME>`; settings for mode/accent/language), reconnect policy, required tunnel core/CLI changes (`install/status/uninstall/pause/resume/retry`, device info files, machine-derived device_id), mobile deferred to its own plan.
- Added the final interactive desktop prototype `docs/design/desktop-prototype-v12.html`.
- `.gitignore`: ignore `.superpowers/` (local mockup and plan workspaces).
- [pending] tunnel: core/CLI changes listed in design §9 must land in the tunnel repo before the desktop app can use them.
- Created from `pangxincheng/repo-template` as the cross-platform management app for tunnel (desktop: Wails v3 + React; mobile: React Native/Expo + gomobile).
- `AGENTS.md`: replaced the template's nginx deployment section with a project description — this app is distributed as desktop/mobile packages, not deployed behind nginx.

## 2026-10-24
- `AGENTS.md` deployment contract updated to nginx-configuration v2: services receive `DEPLOY_SERVER` / `DEPLOY_BASE_URL` / `DEPLOY_DIR` or `DEPLOY_PORT` as environment variables (no positional `<server>`), fallback file `.deploy.env[.<env>]` limited to those keys, cancelling exits non-zero.

