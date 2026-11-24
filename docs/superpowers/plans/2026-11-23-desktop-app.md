# tunnel-app 桌面端(一期)Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 Wails v3 + React/TS 做出 tunnel 的桌面管理 App:管理多台服务器,每台服务器上「共享这台电脑」与「连接其他设备」,视觉与交互按原型 v12。

**Architecture:** Go 侧分为几个不依赖 Wails 的包(本机配置、连接测试、共享服务 CLI 封装、sshd 检测、登录启动、核心管理器 `core.Manager`),外加一个很薄的 Wails 服务 `AppService`。连接其他设备在 App 进程内运行,每台服务器一个 `tunnel.Session`。共享这台电脑通过随 App 附带的 `tunnel` CLI(`install / status --json / pause / resume / retry / set-name / uninstall`)安装为独立服务,App 只负责控制。Go 侧每次状态变化都把完整快照 `AppState` 作为 `state` 事件推给前端,前端只渲染快照、调用绑定方法。

**Tech Stack:** Go 1.27、Wails **v3.1.0**(`github.com/wailsapp/wails/v3`,CLI `wails3`)、`github.com/pangxincheng/tunnel`(v1.2.0 起;开发期 `replace` 到同级目录)、`golang.org/x/crypto/ssh`、React 19 + TypeScript + Vite、Vitest + @testing-library/react。

**Spec:** `tunnel-app/docs/superpowers/specs/2026-11-22-tunnel-app-phase1-design.md`(第 5–8 节);交互原型 `tunnel-app/docs/design/desktop-prototype-v12.html`。

## Global Constraints

- 仓库:App 代码全部在 `tunnel-app/desktop/`;Task 1 改的是 `tunnel/` 仓库(单独分支 `feat/app-cli`),各自记 CHANGELOG(最新条目在顶部,日期用实际日期)。
- Git 身份:`pangxincheng <pangxincheng@foxmail.com>`,每个仓库各自配置。
- **安全(用户定):不要在这台开发机上安装任何系统或用户服务,不要用 root/sudo 运行 install/uninstall;不要注册登录启动项。** 所有涉及服务管理器、提权、自启动的测试只用假的 Runner 或临时目录。手动运行 App 时不要打开共享开关和「登录时启动」。
- 服务器名称、设备名字的规则:`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`(直接用 `tunnel.ValidateName`)。服务器名称就是共享服务的 profile(服务名 `tunnel-<服务器名称>`)。
- 本机配置放在 `os.UserConfigDir()/tunnel-app/config.json`,**不保存私钥内容**。
- 本机端口:设备第一次出现时从 5102 起分配一个未被占用、且不与本 App 已分配端口冲突的端口,之后固定;允许范围 1024–65535。
- 设备列表:窗口打开时立即刷新,之后每 30 秒刷新;服务器断开或连不上时停止刷新。
- 重连沿用 tunnel:指数退避 1s → 30s(±20%),稳定 60s 后重置。不可恢复错误停止重试,等待用户点「重试」。
- 窗口:默认 940×600,最小 720×460,可调整大小,可全屏;左栏 240px;设备行高 54px。
- 浅色:画布 `#F3F4F6`、侧栏 `#ECEEF1`、表面 `#FFFFFF`、文字 `#16191D`、次要 `#6A717B`、弱 `#9AA1AA`、分隔线 `#E1E4E8`、错误 `#B3422E`。
- 深色:画布 `#1A1C20`、侧栏 `#15171A`、表面 `#23262B`、文字 `#E8EAED`、次要 `#A0A7B0`、弱 `#6E757E`、分隔线 `#2F3338`、错误 `#E06A52`。
- 主题色(浅 / 深):green `#13795B / #3DBE8B`(默认)、blue `#2457D6 / #6E9BFF`、violet `#6544E0 / #A592FF`、amber `#A85A06 / #F0A24B`、graphite `#3E4651 / #C9CFD7`。主题色只用于在线 / 已连接、开关开启、已连接的端口数字、链接与焦点。
- 字体:界面用系统字体栈 `-apple-system, BlinkMacSystemFont, "PingFang SC", "Segoe UI Variable Text", "Segoe UI", "Microsoft YaHei", system-ui, sans-serif`;等宽 `ui-monospace, "SF Mono", "Cascadia Mono", Menlo, Consolas, monospace` 只用于端口、命令和路径。已连接的端口数字用等宽、15px、主题色。
- 副标题一律单行,超出部分省略并在悬停时显示全文。动效只用于弹窗出现和开关切换,并尊重 `prefers-reduced-motion`。所有图标按钮都有 `aria-label`,焦点可见。
- 国际化:简体中文与 English。文案集中放在 `desktop/frontend/src/i18n/`,用带 `{占位符}` 的整句,禁止拼接句子片段。命令、路径与 `<USERNAME>` 不翻译。
- 连接命令(`<端口>` 替换为实际端口,其余原样):`ssh -p <端口> <USERNAME>@127.0.0.1`、`scp -P <端口> <本地文件> <USERNAME>@127.0.0.1:<路径>`、`scp -P <端口> <USERNAME>@127.0.0.1:<路径> <本地目录>`、`rsync -av -e "ssh -p <端口>" <源> <USERNAME>@127.0.0.1:<目标>`、`sftp -P <端口> <USERNAME>@127.0.0.1`、`127.0.0.1:<端口>`。
- 带口令私钥的提示原文:「Tunnel 需要一把没有口令的私钥。可以为它单独生成一把:`ssh-keygen -t ed25519 -N "" -f ~/.ssh/tunnel_ed25519`,并把公钥加到服务器的 `~/.ssh/authorized_keys`」。
- 指纹核对命令:`ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`。
- 删除确认原文:「删除 {name}?会停止在它上面的共享和 {n} 个转发,并删除这台服务器在本机的配置。服务器上的数据不受影响。」默认焦点在「取消」。
- 重名提示原文:「「{name}」在 {server} 上已被另一台设备使用,换一个名字」。
- 弹窗:Esc 或点遮罩 = 取消,回车 = 主按钮。
- **原地更新(用户定,修复原型 bug)**:弹窗内的任何操作(如设置里切换显示模式、主题色、语言、开关)只更新被改动的控件,弹窗本身不卸载、不重新挂载、不重播进场动画,焦点留在被点击的控件上。组件 key 必须稳定,不能把弹窗内容放在随状态变化而重建的组件里。整个 App 同理:`state` 事件到来时只做原地更新。
- **窗口按钮的平台差异(用户定)**:
  - macOS:标题栏隐藏并内嵌(Wails `Mac.TitleBar` 用 hidden-inset 一类的预设,以 v3.1.0 文档为准),红绿灯按钮在**左上角**,叠在侧栏顶部;侧栏顶部留出 46px 高的拖动区(`--wails-draggable: drag`),左侧为红绿灯让出约 78px。
  - Windows / Linux:使用系统原生标题栏,最小化 / 最大化 / 关闭在**右上角**,由系统绘制;侧栏顶部不留红绿灯空位,也不自绘任何窗口按钮。
  - 前端根元素带 `data-platform="darwin|windows|linux"`(取自 `AppState.Platform`),只用它切换上述布局差异。
  - 原型 v12 侧栏顶部的假红绿灯只是演示,不要照搬。
- Wails 只能出现在 `desktop/main.go`、`desktop/appservice.go`、`desktop/tray.go` 中;其余 Go 包不得 import Wails。

## Review Focus

1. **本机端口被其他程序占用时打开转发**:开关保持关闭,行下方显示「端口 {port} 已被其他程序占用」,配置中不记成打开。由 Task 7 `TestSetForwardPortBusy` 覆盖。
2. **已打开转发的设备掉线**:转发保持打开(本机仍在监听),行状态为「离线,上线后恢复」,设备重新上线后无需操作即可恢复。由 Task 7 `TestForwardSurvivesDeviceOffline` 覆盖。
3. **系统管理员授权被用户取消**(osascript 返回 -128,pkexec 退出码 126/127):不改变任何状态,开关回到原位,提示「已取消授权」,而不是显示一段原始错误输出。由 Task 5 `TestElevationCancelled` 和 Task 7 `TestSetShareSystemCancelled` 覆盖。
4. **配置文件损坏或缺失**:App 照常启动,服务器列表为空;损坏的文件改名为 `config.json.broken-<unix时间>` 保留,不覆盖用户数据。由 Task 3 `TestLoadCorrupt` 覆盖。
5. **App 启动时服务器连不上,但上次有打开的转发**:本机端口照常监听,服务器显示「无法连接」并按退避重试,连上后转发自动可用;「启动时自动打开上次的转发」关闭时则不打开。由 Task 7 `TestRestoreForwardsWhileUnreachable` 覆盖。

---

### Task 1: tunnel CLI 补充(tunnel 仓库)

App 需要通过 CLI 判断「未安装 / 未运行 / 运行中」以及服务的运行方式,并在不重装的情况下改设备名字。

**Files:**
- Modify: `tunnel/cmd/tunnel/main.go`(usage 增加 `tunnel set-name NAME [--mode=MODE] [--profile=P]`,分派到新命令)
- Modify: `tunnel/cmd/tunnel/service.go`(`statusCmd`、`resolveTarget`、新增 `setNameCmd`)
- Modify: `tunnel/README.md`、`tunnel/CHANGELOG.md`
- Test: `tunnel/cmd/tunnel/service_test.go`

**Interfaces:**
- Produces(App 在 Task 5 依赖这些 CLI 行为):
  - `tunnel status --json --profile P` 运行中:退出码 0,stdout 输出一行 JSON = `ShareStatus` 的全部字段,另加 `"mode":"user"|"system"`。
  - 已安装但未运行:退出码 3,stdout 输出一行 `{"installed":true,"running":false,"mode":"<mode>"}`(加 `--json` 时只输出这一行 JSON)。
  - 未安装:退出码 **4**,加 `--json` 时 stdout 输出一行 `{"installed":false}`。
  - `tunnel set-name NAME [--mode] [--profile]`:经控制通道发送 `set-name`,成功退出码 0;名字不合法退出码 1;被占用时 stderr 包含 `NameTakenError` 的信息,退出码 1。
  - 不给 `--profile` 时,如果本机(所有模式合计)只安装了一个服务,就自动选它;安装了多个时报错「已安装多个服务(…),请用 --profile 指定」。

- [ ] **Step 1:** 在 `tunnel` 仓库新建分支 `feat/app-cli`,然后在 `service_test.go` 中先写失败的测试:`TestStatusJSONIncludesMode`(假控制通道返回 ready,断言 JSON 中有 `"mode":"user"` 和 `"phase":"ready"`)、`TestStatusJSONNotRunning`(退出码 3 + 那行 JSON)、`TestStatusNotInstalledExit4`(退出码 4 + `{"installed":false}`)、`TestSetNameCmd`(假控制通道收到 `{op:"set-name",name:"nas2"}`)、`TestResolveTargetAutoProfile`(只装了 `home` 时不给 `--profile` 也能选中;装了 `home` 和 `work` 时报错并列出两者)。测试沿用该文件已有的假 `svcEnv`、临时目录和假控制监听写法。
- [ ] **Step 2:** 运行 `go test ./cmd/tunnel -run 'StatusJSON|NotInstalled|SetName|AutoProfile' -race`,预期失败。
- [ ] **Step 3:** 实现。在 `service.go` 中定义 `const exitNotInstalled = 4`;找不到服务时 `resolveTarget` 返回哨兵错误 `errNotInstalled`(用 `errors.Is` 判断);自动选择 profile 时扫描两种模式的 `/etc/tunnel/`、用户配置目录下的 `tunnel-*` 目录,复用 `env.paths` 推出父目录。
- [ ] **Step 4:** 运行 `go test ./... -race`,预期全部通过。
- [ ] **Step 5:** README 的「开机自启与后台运行」一节补充 `set-name`、`status --json` 的退出码与输出,以及自动选择 profile 的行为;CHANGELOG 顶部新增 `## 2026-11-23` 条目;提交 `feat: status --json 输出运行方式与安装状态、新增 set-name、单服务时自动选择 profile`。**不要推送,也不要打 tag**(由控制者在收尾时征求用户同意)。

### Task 2: 搭建 Wails v3 桌面工程

**Files:**
- Create: `tunnel-app/desktop/`(`wails3 init -n desktop -t react-ts` 生成后精简)、`desktop/go.mod`(module `github.com/pangxincheng/tunnel-app/desktop`)
- Create: `desktop/Taskfile.yml` 增加任务 `bundle:tunnel`
- Create: `desktop/internal/bundled/bundled.go`
- Modify: `tunnel-app/.gitignore`(`desktop/bin/`、`desktop/frontend/node_modules/`、`desktop/frontend/dist/`、`desktop/build/bin/`)、`tunnel-app/CHANGELOG.md`
- Test: `desktop/internal/bundled/bundled_test.go`

**Interfaces:**
- Produces:`bundled.TunnelPath() (string, error)`:优先读取环境变量 `TUNNEL_APP_TUNNEL_BIN`;否则在 App 可执行文件同目录查找 `tunnel`(Windows 上为 `tunnel.exe`;macOS .app 包内即 `Contents/MacOS/`);找不到时返回 `bundled.ErrMissing`。

- [ ] **Step 1:** 运行 `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0`,再运行 `wails3 doctor`,预期没有致命错误。在 `tunnel-app/` 下运行 `wails3 init -n desktop -t react-ts`;删掉模板里的示例服务(GreetService 一类)和示例页面;`go.mod` 中把 `github.com/wailsapp/wails/v3` 固定为 `v3.1.0`;添加 `require github.com/pangxincheng/tunnel v1.2.0` 和 `replace github.com/pangxincheng/tunnel => ../../tunnel`(开发期使用同级的 tunnel 目录,Task 1 的 CLI 修改发布后再去掉)。
- [ ] **Step 2:** 先写失败的测试 `TestTunnelPathEnv`(设置环境变量后返回该路径)和 `TestTunnelPathMissing`(环境变量为空、可执行文件旁没有 tunnel 时返回 `ErrMissing`),然后运行 `go test ./internal/bundled`,预期失败;实现后再次运行,预期通过。
- [ ] **Step 3:** 在 Taskfile 中添加 `bundle:tunnel` 任务:`go build -ldflags "-X github.com/pangxincheng/tunnel/tunnel.Version={{.TUNNEL_VERSION}}" -o bin/tunnel{{exe}} github.com/pangxincheng/tunnel/cmd/tunnel`,其中 `TUNNEL_VERSION` 由 `go list -m -f '{{.Version}}' github.com/pangxincheng/tunnel` 得到,replace 时用 `dev`;把它挂到 `build` 和 `dev` 的依赖上,并让打包步骤把 `bin/tunnel` 复制进 .app、AppImage 和 NSIS 的安装目录。
- [ ] **Step 4:** 先运行 `wails3 task bundle:tunnel`,再运行 `wails3 build`,预期两者都成功,并生成可执行文件(只构建,不运行)。
- [ ] **Step 5:** 更新 `.gitignore`;在 CHANGELOG 中记录「新增 desktop/ Wails v3.1.0 工程骨架,附带 tunnel CLI」;提交 `feat(desktop): Wails v3 工程骨架与附带 tunnel CLI`。

### Task 3: 本机配置 `internal/config`

**Files:**
- Create: `desktop/internal/config/config.go`、`desktop/internal/config/ports.go`
- Test: `desktop/internal/config/config_test.go`

**Interfaces:**
- Produces:
```go
type Server struct {
    Name, Host, User, KeyPath, HostKey, SocketDir string // HostKey: 已信任主机公钥(authorized_keys 一行);SocketDir 空 = 自动
    Port         int
    Disconnected bool
    Share        ShareIntent
    Forwards     map[string]Forward // 键:设备名
    LastSeen     map[string]time.Time // 设备上次探测在线的时间
}
type ShareIntent struct { On bool; Name string; System bool }
type Forward struct { Port int; On bool }
type Settings struct {
    Mode   string // "system"|"light"|"dark",默认 "system"
    Accent string // green|blue|violet|amber|graphite,默认 green
    Lang   string // "system"|"zh"|"en",默认 "system"
    LaunchAtLogin bool
    RestoreForwards bool // 默认 true
}
type Config struct { Servers []Server; Settings Settings; Selected string }
func Load(path string) (*Config, error)
func Save(path string, c *Config) error
func DefaultPath() (string, error) // os.UserConfigDir()/tunnel-app/config.json
func (c *Config) Server(name string) (*Server, bool)
func (c *Config) ValidateServerName(name, except string) error // 规则 + 本机唯一;except 用于改名时排除自己
func (c *Config) AllocatePort(isFree func(int) bool) int // 从 5102 起找第一个未被本 App 占用且 isFree 的端口
func (c *Config) CheckPort(server, device string, port int) error // 1024–65535,且不与其他设备已分配的端口冲突
```
JSON 字段名用小写下划线(`host_key`、`socket_dir`、`last_seen`、`restore_forwards`、`launch_at_login`)。

- [ ] **Step 1:** 先写失败的测试:
  - `TestLoadMissing`:返回空配置和默认值,`RestoreForwards == true`、`Accent == "green"`。
  - `TestSaveLoadRoundTrip`。
  - `TestSaveIsAtomicAnd0600`:写临时文件后改名,文件权限为 0600,所在目录为 0700。
  - `TestLoadCorrupt`:文件内容为 `{`,返回空配置且无错误;原文件被改名为 `config.json.broken-<数字>`,内容不变。
  - `TestValidateServerName`:`"home"` 通过;`""`、`"-a"`、`"a b"` 和 65 个字符的名字被拒绝;重名被拒绝;改名时排除自己。
  - `TestAllocatePortSkipsUsedAndBusy`:已分配 5102,5103 的 isFree 为 false,得到 5104。
  - `TestCheckPort`:1023 和 65536 被拒绝;与另一台服务器上某设备的端口冲突时被拒绝;同一设备保持原端口时通过。
- [ ] **Step 2:** 运行 `go test ./internal/config`,预期失败。
- [ ] **Step 3:** 实现;`Save` 时使用 `os.CreateTemp` 和 `os.Rename`。
- [ ] **Step 4:** 运行 `go test ./internal/config -race`,预期通过。
- [ ] **Step 5:** 记 CHANGELOG,提交 `feat(desktop): 本机配置与端口分配`。

### Task 4: 测试连接 `internal/probe`

**Files:**
- Create: `desktop/internal/probe/probe.go`
- Test: `desktop/internal/probe/probe_test.go`(在测试中用 `x/crypto/ssh` 起一个进程内 SSH 服务器:接受指定公钥,处理 `exec` 和 `sftp` 子系统时,复用 tunnel 仓库测试服务器的思路自己写一个最小版本)

**Interfaces:**
- Produces:
```go
type Params struct { Host string; Port int; User, KeyPath, SocketDir string }
type Result struct {
    Fingerprint string // ssh.FingerprintSHA256,如 "SHA256:abc…"
    HostKey     string // authorized_keys 格式的一行,保存为 Server.HostKey
    SocketDir   string // ResolveSocketDir 解析出的绝对路径
}
func Test(ctx context.Context, p Params) (Result, error)
var ErrPassphrase = errors.New("passphrase-protected key") // 私钥带口令
type AuthError struct{ Err error }   // 服务器拒绝了这把私钥
type DialError struct{ Err error }   // 网络不可达或超时(10s)
type SocketDirError struct{ Err error }
```
做法:用 `ssh.ParsePrivateKey` 解析私钥;遇到 `*ssh.PassphraseMissingError` 时返回 `ErrPassphrase`。`HostKeyCallback` 记录对方公钥后放行(这次连接只用于测试,信任要等用户确认指纹之后)。认证成功后调用 `tunnel.ResolveSocketDir(sc, p.SocketDir)`。

- [ ] **Step 1:** 先写失败的测试:`TestProbeOK`(返回的指纹等于测试服务器主机公钥的 `ssh.FingerprintSHA256`,SocketDir 非空)、`TestProbePassphrase`(用带口令生成的 ed25519 私钥,返回 `errors.Is(err, ErrPassphrase)`)、`TestProbeAuthRejected`(返回 `*AuthError`)、`TestProbeDialFail`(连接一个已关闭的端口,返回 `*DialError`)。
- [ ] **Step 2:** 运行 `go test ./internal/probe`,预期失败。
- [ ] **Step 3:** 实现。
- [ ] **Step 4:** 运行 `go test ./internal/probe -race`,预期通过。
- [ ] **Step 5:** 记 CHANGELOG,提交 `feat(desktop): 测试连接(读取主机指纹、识别带口令私钥)`。

### Task 5: 共享服务控制 `internal/sharesvc`

**Files:**
- Create: `desktop/internal/sharesvc/sharesvc.go`、`elevate.go`
- Test: `desktop/internal/sharesvc/sharesvc_test.go`

**Interfaces:**
- Consumes:Task 1 的 CLI 约定;Task 2 的 `bundled.TunnelPath`。
- Produces:
```go
type Runner interface { Run(ctx context.Context, name string, args ...string) (stdout, stderr []byte, exit int, err error) }
type State struct {
    Installed, Running bool
    Mode   string              // "user"|"system",未安装时为空
    Status *tunnel.ShareStatus // 只在 Running 时非空
}
type InstallOpts struct {
    Profile, Name, Host, User, KeyPath, HostKey, SocketDir string
    Port, LocalPort int // LocalPort:本机 sshd 端口
    System bool
}
type Service struct { /* bin, runner, goos, currentUser */ }
func New(bin string, r Runner, goos, currentUser string) *Service
func (s *Service) Status(ctx context.Context, profile string) (State, error)
func (s *Service) Install(ctx context.Context, o InstallOpts) error   // System 时经提权
func (s *Service) Uninstall(ctx context.Context, profile string, system bool) error
func (s *Service) Pause(ctx context.Context, profile string) error
func (s *Service) Resume(ctx context.Context, profile string) error
func (s *Service) Retry(ctx context.Context, profile string) error
func (s *Service) SetName(ctx context.Context, profile, name string) error
func (s *Service) LogPath(profile string, system bool) string // 与 tunnel README 中的日志位置一致;Linux 返回 "" (用 journalctl)
var ErrCancelled = errors.New("authorization cancelled")
```
- install 参数:`install --mode <user|system> --profile P --name N --remote-ip H --remote-ssh-port Port --user U --local-ssh-key KeyPath --remote-ssh-pub "<HostKey 内联>" --local-ssh-port LocalPort [--remote-socket-dir D]`;system 模式另加 `--for-user <currentUser>`。
- 提权:macOS 通过 `osascript -e 'do shell script "<逐个参数用 shell 单引号转义后拼接的命令>" with administrator privileges'` 执行;stderr 含 `-128` 时返回 `ErrCancelled`。Linux 用 `pkexec <bin> <args…>`,退出码 126 或 127 时返回 `ErrCancelled`。Windows 不支持 system 模式,返回错误。
- 运行 CLI 时把子进程的工作目录设为临时空目录,避免 CLI 自动读到 `./tunnel.toml`。

- [ ] **Step 1:** 先写失败的测试,使用记录调用参数并按预设返回结果的假 Runner:
  - `TestStatusRunning`:退出码 0,JSON 中有 `mode` → `Running`、`Mode=="user"`,`Status.Phase=="ready"`。
  - `TestStatusNotRunning`:退出码 3 → `Installed && !Running`。
  - `TestStatusNotInstalled`:退出码 4 → `!Installed`。
  - `TestInstallUserArgs`:参数与上面的约定逐项一致,不经过提权。
  - `TestInstallSystemDarwinUsesOsascript`:第一个参数是 `osascript`,脚本中含 `--for-user alice` 和 `with administrator privileges`,含空格或单引号的路径被正确转义。
  - `TestInstallSystemLinuxUsesPkexec`。
  - `TestElevationCancelled`:osascript 的 stderr 为 `execution error: User canceled. (-128)` 时返回 `ErrCancelled`;pkexec 退出码 126 时返回 `ErrCancelled`。
  - `TestInstallSystemWindowsRejected`。
- [ ] **Step 2:** 运行 `go test ./internal/sharesvc`,预期失败。
- [ ] **Step 3:** 实现,其中 `ExecRunner` 用 `exec.CommandContext`。
- [ ] **Step 4:** 运行 `go test ./internal/sharesvc -race`,预期通过。
- [ ] **Step 5:** 记 CHANGELOG,提交 `feat(desktop): 经附带的 tunnel CLI 控制共享服务(含系统授权)`。

### Task 6: 本机 sshd 检测 `internal/sshd`

**Files:**
- Create: `desktop/internal/sshd/sshd.go`
- Test: `desktop/internal/sshd/sshd_test.go`

**Interfaces:**
- Produces:
```go
type Result struct { Running bool; Port int }
func Detect(ctx context.Context, goos string, readConfig func() ([]byte, error)) Result
```
做法:候选端口为 sshd 配置中所有 `Port N` 行(`readConfig` 在 macOS / Linux 读 `/etc/ssh/sshd_config`,Windows 读 `%ProgramData%\ssh\sshd_config`;读不到时忽略),再加上 22。对每个端口用 1s 超时连接 `127.0.0.1:<port>`,读到以 `SSH-` 开头的握手行即认定为 sshd,取第一个命中的端口。另外导出一个包级变量 `dial`,供测试替换。

- [ ] **Step 1:** 先写失败的测试:`TestDetectBanner`(在测试中监听一个端口并发送 `SSH-2.0-OpenSSH_9\r\n`,配置中写 `Port <该端口>`,得到 `Running` 和该端口)、`TestDetectNonSSH`(监听端口但发送 `HTTP/1.1`,得到 `!Running`)、`TestDetectConfigParse`(注释行和 `Port` 大小写混写时能正确解析)。
- [ ] **Step 2–4:** 运行 `go test ./internal/sshd`,预期先失败;实现后运行 `go test ./internal/sshd -race`,预期通过。
- [ ] **Step 5:** 记 CHANGELOG,提交 `feat(desktop): 检测本机 sshd 及端口`。

### Task 7: 核心管理器 `internal/core`

这是 App 的大脑:管理每台服务器的连接、设备刷新、转发、共享的调度,并产出完整的状态快照。

**Files:**
- Create: `desktop/internal/core/manager.go`、`state.go`、`server.go`、`share.go`
- Test: `desktop/internal/core/manager_test.go`、`fakes_test.go`

**Interfaces:**
- Consumes:Task 3 的 `config`;Task 5 的 `sharesvc.Service`(通过下面的 `ShareCtl` 接口注入);Task 6 的 `sshd.Detect`(通过函数注入);`tunnel.Session` 的方法(通过下面的 `Session` 接口注入,真实实现是 `tunnel.NewSession`)。
- Produces:
```go
type Session interface {
    Run(ctx context.Context) error
    RetryNow()
    State() tunnel.ConnState
    AddForward(name string, port int) error
    RemoveForward(name string)
    Devices(ctx context.Context) ([]tunnel.Device, error)
}
type ShareCtl interface { /* 与 sharesvc.Service 的 Status/Install/Uninstall/Pause/Resume/Retry/SetName 同签名 */ }
type Deps struct {
    ConfigPath string
    NewSession func(cfg *tunnel.Config, socketDir string, onState func(tunnel.ConnState)) Session
    Share      ShareCtl
    DetectSSHD func(ctx context.Context) sshd.Result
    DeviceID   string
    PortFree   func(port int) bool
    Now        func() time.Time
    OnChange   func(AppState) // 每次状态变化时调用(内部合并,最多每 100ms 一次)
    RefreshEvery time.Duration // 默认 30s,测试时调小
}
func New(d Deps) (*Manager, error)
func (m *Manager) Start(ctx context.Context)  // 打开 RestoreForwards 允许的转发,连接未断开的服务器
func (m *Manager) State() AppState
func (m *Manager) Refresh(server string)       // 窗口打开时调用:立即刷新设备列表
func (m *Manager) AddServer(s config.Server) error
func (m *Manager) UpdateServer(oldName string, s config.Server) error // 改名时迁移已安装的共享服务
func (m *Manager) DeleteServer(name string) error                     // 卸载共享服务、关闭转发、删除配置
func (m *Manager) SetConnected(server string, on bool) error          // 断开 = 停止 Session、暂停共享,保留开关状态
func (m *Manager) Retry(server string)
func (m *Manager) SetForward(server, device string, on bool) error
func (m *Manager) SetPort(server, device string, port int) error
func (m *Manager) SetShare(server string, on bool) error
func (m *Manager) SetShareName(server, name string) error
func (m *Manager) SetShareSystem(server string, system bool) error
func (m *Manager) RetryShare(server string) error
func (m *Manager) UpdateShare(server string) error // Outdated 时以当前方式重新 install(system 需授权)
func (m *Manager) UpdateSettings(s config.Settings) error
func (m *Manager) Select(server string)
```
`AppState`(JSON 字段使用小写下划线,前端以它为唯一数据源):
```go
type AppState struct {
    Servers  []ServerView
    Selected string
    Settings config.Settings
    Platform string // runtime.GOOS
    SSHD     sshd.Result
}
type ServerView struct {
    Name, Host, User string; Port int
    Conn        string    // "ready"|"connecting"|"waiting"|"failed"|"disconnected"
    ConnErr     string
    RetryAt     time.Time // Conn=="waiting" 时有值
    DevicesAt   time.Time // 设备列表的获取时间(用于「设备列表是 X 前的」)
    Connected   int       // 真正接通的转发数:转发打开 + 设备在线 + Conn=="ready"
    Online      int       // 在线设备数(不含本机)
    Share       ShareView
    Devices     []DeviceView // 不含本机(device_id 等于本机的设备),按名字排序
}
type ShareView struct {
    On bool; Name string; System bool
    Phase   string // "off"|"connecting"|"ready"|"waiting"|"failed"|"paused"|"not_running"
    Err     string; RetryAt time.Time
    NameErr string // 重名或格式错误
    Busy    bool   // 正在安装或卸载
    LogPath string
    Outdated bool // 已安装服务的版本旧于附带的 tunnel
}
type DeviceView struct {
    tunnel.DeviceInfo
    Online   bool
    LastSeen time.Time // 离线时:max(started_at, 本地记录的上次探测在线时间)
    Port     int       // 已分配的本机端口
    On       bool      // 转发开关
    Err      string    // 行下方的错误(端口占用、端口不合法)
}
```
规则(都来自 spec 第 7、8 节):
- 设备第一次出现时用 `AllocatePort` 分配端口并写入配置。离线设备不能 `SetForward(on=true)`(返回 `ErrDeviceOffline`)。已打开的转发在设备掉线后保持打开。
- `SetForward` 打开时调用 `AddForward`;遇到 `*tunnel.LocalListenError` 时不记为打开,`DeviceView.Err` 设为端口占用的提示。改端口时,如果转发开着,先关再以新端口重开。
- 共享开关:打开时检查 sshd(没在运行则返回 `ErrNoSSHD`),再检查重名(读取设备列表,同名且 `device_id` 不是本机就设置 `NameErr`,拒绝打开)。未安装时调用 `Install`,已安装时调用 `Resume`。关闭时调用 `Pause`。共享服务的状态每 3 秒调用一次 `ShareCtl.Status` 同步。
- 断开服务器:取消该服务器 Session 的 ctx,共享为开时调用 `Pause`,配置中写 `Disconnected=true`,并保留各开关。重新连接时反向执行,恢复打开的转发与共享。
- `SetShareSystem`:先卸载当前方式,再用新方式安装。授权被取消(`ErrCancelled`)时保留原方式,配置不变。
- `UpdateServer` 改了服务器名称且共享已安装时:先卸载旧 profile 再按新名称安装;连接参数变化时重建 Session。
- Session 的状态映射:`connecting / ready / waiting / failed` 原样对应;`disconnected` 来自配置中的 `Disconnected`。

- [ ] **Step 1:** 先在 `fakes_test.go` 中写假的 Session(可以控制状态、设备列表和 AddForward 的错误)与假的 ShareCtl,然后写失败的测试:
  - `TestAddServerAllocatesPortsOnDevicesSeen`:两台设备分到 5102、5103,第二次刷新时端口不变。
  - `TestSetForwardPortBusy`。
  - `TestSetForwardOfflineRejected`。
  - `TestForwardSurvivesDeviceOffline`:设备掉线后 `RemoveForward` 没有被调用,`On` 仍为 true,`Connected` 减 1。
  - `TestConnectedCountsOnlyLive`。
  - `TestSetPortRestartsForward`。
  - `TestDisconnectPausesAndKeepsSwitches`:断开后配置里的 `Disconnected` 为 true,转发 `On` 不变,`Pause` 被调用;重新连接后 `Resume` 被调用,转发重新 `AddForward`。
  - `TestShareNameTaken`:读到同名而 device_id 不同时设置 `NameErr`,且 `Install` 没有被调用。
  - `TestShareNoSSHD`。
  - `TestShareInstallThenResume`:未安装时调用 `Install`,已安装时调用 `Resume`。
  - `TestSetShareSystemCancelled`:`Install` 返回 `ErrCancelled` 时 `System` 保持原值。
  - `TestRenameServerMigratesShare`:按顺序调用 `Uninstall(old)` 和 `Install(new)`。
  - `TestDeleteServerUninstalls`。
  - `TestRestoreForwardsWhileUnreachable`:Session 一直处于 waiting,但 `AddForward` 已被调用;`RestoreForwards=false` 时没有被调用。
  - `TestLastSeen`:设备在线时记录当时的时间,离线后 `LastSeen` 取 max(started_at, 记录值),并写入配置。
  - `TestRefreshStopsWhenDisconnected`。
  - `TestOwnDeviceHidden`。
- [ ] **Step 2:** 运行 `go test ./internal/core`,预期失败。
- [ ] **Step 3:** 实现。所有可变状态由一把互斥锁保护,`OnChange` 在锁外调用。每台服务器一个 goroutine 运行 `Session.Run`,另一个按 `RefreshEvery` 刷新设备列表;共享状态由一个 3 秒的 ticker 同步。
- [ ] **Step 4:** 运行 `go test ./internal/core -race -count=3`,预期通过。
- [ ] **Step 5:** 记 CHANGELOG,提交 `feat(desktop): 核心管理器(服务器连接、设备、转发、共享调度)`。

### Task 8: Wails 服务、窗口、托盘与登录启动

**Files:**
- Create: `desktop/appservice.go`、`desktop/tray.go`、`desktop/internal/autostart/autostart.go`
- Modify: `desktop/main.go`
- Test: `desktop/internal/autostart/autostart_test.go`、`desktop/appservice_test.go`

**Interfaces:**
- Consumes:Task 7 的 `core.Manager`;Task 4 的 `probe.Test`。
- Produces(前端绑定,Task 9–11 使用,方法名逐字一致):
  `GetState() core.AppState`、`Select(name string)`、`Refresh(name string)`、`TestConnection(p probe.Params) (ProbeView, error)`、`AddServer(s config.Server) error`、`UpdateServer(old string, s config.Server) error`、`DeleteServer(name string) error`、`SetConnected(name string, on bool) error`、`Retry(name string)`、`SetForward(server, device string, on bool) error`、`SetPort(server, device string, port int) error`、`SetShare(server string, on bool) error`、`SetShareName(server, name string) error`、`SetShareSystem(server string, on bool) error`、`RetryShare(server string) error`、`UpdateShare(server string) error`、`OpenLog(server string) error`、`UpdateSettings(s config.Settings) error`、`PickKeyFile() (string, error)`、`AppVersion() string`。
  事件:`state`(载荷为 `core.AppState`)。
  `ProbeView{Fingerprint, HostKey, SocketDir string}`;错误一律转成带稳定代码的消息,前缀为 `passphrase:`、`auth:`、`dial:`、`socketdir:`,前端据此选择对应文案。
- `autostart`:`Enable(goos, exe, home string) error`、`Disable(...) error`、`Enabled(...) bool`。macOS 写 `~/Library/LaunchAgents/com.pangxincheng.tunnel-app.plist`;Linux 写 `~/.config/autostart/tunnel-app.desktop`;Windows 写 `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` 中的 `TunnelApp` 值(通过一个可替换的 `regSet`/`regDel` 实现)。

- [ ] **Step 1:** 先写失败的测试:`TestAutostartDarwinPlist` 与 `TestAutostartLinuxDesktop`(home 指向临时目录,断言文件内容中有 exe 路径,`Disable` 后文件被删除)、`TestAutostartWindowsUsesRegistry`(假的 regSet)、`TestProbeErrorCodes`(`ErrPassphrase` 转成以 `passphrase:` 开头的消息,其余错误类似)。
- [ ] **Step 2–4:** 运行 `go test ./... -race`,预期先失败;实现后再运行,预期通过。
- [ ] **Step 5:** `main.go`:用 `application.New` 注册 `AppService`;主窗口标题 `Tunnel`,默认 940×600,最小 720×460;按 Global Constraints「窗口按钮的平台差异」配置:macOS 使用隐藏内嵌标题栏,Windows / Linux 使用原生标题栏;`WindowClosing` 钩子取消关闭并隐藏窗口。托盘(`app.SystemTray.New()`)的菜单为「打开 Tunnel」「退出」;有转发开着时图标显示主题色点(可选)。检测不到托盘时(GNOME 下 `SystemTray` 创建失败或平台为 linux 且无 StatusNotifier),关闭窗口改为最小化。`Manager.OnChange` 中调用 `app.Event.Emit("state", st)`;窗口显示时调用 `Refresh(selected)`。`UpdateSettings` 改变 `LaunchAtLogin` 时调用 `autostart`。
- [ ] **Step 6:** 运行 `wails3 build`,预期成功;再运行 `wails3 generate bindings`,预期生成 `frontend/bindings/...`。手动打开 App,确认窗口出现、空状态正常,关闭后进程仍在、托盘「退出」可以退出。**不要打开共享开关,也不要打开「登录时启动」。**
- [ ] **Step 7:** 记 CHANGELOG,提交 `feat(desktop): Wails 服务、窗口与托盘、登录时启动`。

### Task 9: 前端基础:主题、国际化、状态、侧栏、设置

**Files:**
- Create: `desktop/frontend/src/styles/tokens.css`、`app.css`
- Create: `desktop/frontend/src/i18n/zh.ts`、`en.ts`、`index.ts`
- Create: `desktop/frontend/src/state.ts`、`api.ts`
- Create: `desktop/frontend/src/components/Sidebar.tsx`、`EmptyState.tsx`、`Dialog.tsx`、`Switch.tsx`、`SettingsDialog.tsx`
- Modify: `desktop/frontend/src/App.tsx`、`main.tsx`
- Test: `desktop/frontend/src/__tests__/i18n.test.ts`、`sidebar.test.tsx`、`settings.test.tsx`、`dialog.test.tsx`

**Interfaces:**
- Consumes:Task 8 生成的绑定(只在 `api.ts` 中 import,其余代码都从 `api.ts` 取用,测试时 mock `api.ts`)。
- Produces:
  - `useAppState(): AppState`(订阅 `state` 事件;首次调用 `GetState()`)。
  - `t(key: MsgKey, vars?: Record<string, string|number>): string`;`MsgKey` 由 `zh.ts` 的键推导。`en.ts` 必须有完全相同的键,用类型约束检查。
  - `<Dialog title onCancel onSubmit initialFocus?>`:实现 Esc、遮罩点击和回车;焦点留在弹窗内,关闭后焦点回到打开它的元素。
  - `<Switch checked disabled onChange label>`。
  - 主题:在根元素上设置 `class="dark"` 和 `data-accent`;mode 为 `"system"` 时跟随 `prefers-color-scheme`;lang 为 `"system"` 时根据 `navigator.language` 以 `zh` 开头与否选择 zh 或 en。

- [ ] **Step 1:** 用 `npm i -D vitest @testing-library/react @testing-library/user-event jsdom` 安装测试依赖,在 `package.json` 中加入 `"test": "vitest run"`。
- [ ] **Step 2:** 先写失败的测试:
  - `i18n.test`:zh 与 en 键集合相等;`t('deleteConfirm', {name:'home', n:2})` 的 zh 结果等于 Global Constraints 中的删除确认原文。
  - `sidebar.test`:根据给定的 AppState 渲染服务器行,摘要为「已连接 1　在线 3」;`Conn=='waiting'` 时显示红点和「无法连接」,`Share.Phase=='failed'` 时显示红点(不显示「无法连接」),`disconnected` 时显示「已断开」;行内操作按钮有 aria-label(编辑、连接 / 断开、删除);点击行时调用 `Select`。
  - `settings.test`:选择深色后调用 `UpdateSettings`,参数中 `mode:"dark"`;5 个主题色按钮存在。
  - `dialog.test`:Esc 触发 onCancel,回车触发 onSubmit。
  - `settings.test` 增加 `TestSettingsUpdatesInPlace`:打开设置后记下弹窗根节点的引用,依次点击「深色」、某个主题色、「English」,并推送新的 AppState;断言弹窗根节点仍是同一个 DOM 节点(没有重新挂载),进场动画类没有再次添加,`document.activeElement` 是刚点击的按钮。
  - `platform.test`:`Platform=='darwin'` 时根元素有 `data-platform="darwin"`,侧栏顶部有为红绿灯留出的拖动区;`windows`、`linux` 时没有这个留白,页面中也没有任何自绘的窗口按钮。
- [ ] **Step 3:** 运行 `npm test`,预期失败。
- [ ] **Step 4:** 实现。`tokens.css` 中的颜色值逐字取自 Global Constraints,类名与结构参考原型 v12(`.app`、`.sidebar`、`.srv`、`.sa`、`.banner` 等)。服务器为零台时右栏显示 `EmptyState`(「添加第一台服务器」加主按钮)。左栏底部放「＋ 添加服务器」和设置齿轮(齿轮使用原型 v12 中的齿轮图标,不要用太阳图标)。
- [ ] **Step 5:** 运行 `npm test` 与 `npx tsc --noEmit`,预期都通过。
- [ ] **Step 6:** 记 CHANGELOG,提交 `feat(desktop): 前端主题、国际化、侧栏与设置`。

### Task 10: 前端服务器详情:横幅、共享这台电脑、连接其他设备、连接命令

**Files:**
- Create: `desktop/frontend/src/components/ServerDetail.tsx`、`Banner.tsx`、`ShareSection.tsx`、`DeviceRow.tsx`、`PortField.tsx`、`CommandsDialog.tsx`
- Create: `desktop/frontend/src/commands.ts`、`format.ts`(时长,如「3 天」「5 分钟」,随语言变化)
- Test: `desktop/frontend/src/__tests__/commands.test.ts`、`device-row.test.tsx`、`share.test.tsx`、`banner.test.tsx`

**Interfaces:**
- Consumes:Task 9 的 `useAppState`、`t`、`Dialog`、`Switch`、`api`。
- Produces:`commandsFor(port: number): {key: MsgKey; text: string}[]`,共 6 条,顺序和文本逐字取自 Global Constraints。

- [ ] **Step 1:** 先写失败的测试:
  - `commands.test`:`commandsFor(5102)` 的 6 条文本逐字相等。
  - `device-row.test`:
    - 行内顺序为:名字、系统与在线时长 → 端口与开关 → 分隔线 → 命令按钮。
    - 离线设备的开关被禁用;已打开的离线设备显示「离线,上线后恢复」。
    - 点击端口数字后变为输入框,回车调用 `SetPort`,Esc 不调用。
    - 输入 80 时行下方显示范围错误,且不调用 `SetPort`。
    - 已连接的端口数字使用 `.port.live` 类。
  - `share.test`:
    - 设备名字输入框的副标题为「在 home 上需唯一」。
    - `NameErr` 显示在输入框下方,开关被禁用。
    - Platform 为 windows 时不渲染「无人登录时也保持共享」。
    - Phase 为 `failed` 时显示错误、「重试」与「查看日志」。
    - sshd 未运行时显示开启方法,开关被禁用。
    - `Outdated` 时显示「更新共享服务」,点击后调用 `UpdateShare`。
    - Platform 为 darwin 时,打开「无人登录时也保持共享」前先确认 FileVault 提示(开启 FileVault 时重启后仍需在开机界面输入密码)。
  - `banner.test`:waiting 时显示「无法连接 home:<原因>。设备列表是 5 分钟前的。N 秒后重试」和「立即重试」;disconnected 时显示灰色横幅和「连接」。
- [ ] **Step 2:** 运行 `npm test`,预期失败。
- [ ] **Step 3:** 实现。倒计时只更新显示数字的那个 span,不重新渲染整行(避免抢走焦点)。「复制」使用 `navigator.clipboard.writeText`,成功后按钮短暂显示「已复制」。连接命令弹窗只有「完成」一个按钮。原地更新时不播放进场动画。设备行高 54px。
- [ ] **Step 4:** 运行 `npm test` 与 `npx tsc --noEmit`,预期都通过。
- [ ] **Step 5:** 记 CHANGELOG,提交 `feat(desktop): 服务器详情(共享这台电脑、连接其他设备、连接命令)`。

### Task 11: 前端添加 / 编辑服务器与删除确认

**Files:**
- Create: `desktop/frontend/src/components/ServerDialog.tsx`、`ConfirmDeleteDialog.tsx`
- Test: `desktop/frontend/src/__tests__/server-dialog.test.tsx`、`delete.test.tsx`

**Interfaces:**
- Consumes:`api.TestConnection`、`AddServer`、`UpdateServer`、`DeleteServer`、`PickKeyFile`。

- [ ] **Step 1:** 先写失败的测试:
  - `server-dialog.test`:
    - 名称格式不对或与已有服务器重名时实时提示,「保存」被禁用。
    - 「测试连接」成功后显示 SHA256 指纹与核对命令 `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub`;点「指纹一致,信任」之前「保存」被禁用,点了之后可用。
    - 测试返回 `passphrase:` 错误时显示 Global Constraints 中的口令提示原文。
    - 编辑时改了地址、端口、账户或私钥中的任意一项,就需要重新测试并信任;只改名称不需要。
    - 高级项「socket 目录」默认为空,显示「自动」。
    - 添加成功后调用 `Select(新名称)`。
  - `delete.test`:文案与 Global Constraints 逐字一致(n 取该服务器上打开的转发数);默认焦点在「取消」;确认后调用 `DeleteServer`。
- [ ] **Step 2:** 运行 `npm test`,预期失败。
- [ ] **Step 3:** 实现。
- [ ] **Step 4:** 运行 `npm test`、`npx tsc --noEmit`,再运行 `wails3 build`,预期都成功。
- [ ] **Step 5:** 记 CHANGELOG,提交 `feat(desktop): 添加 / 编辑服务器(测试连接与指纹信任)与删除确认`。

### Task 12: 文档与 spec 同步

**Files:**
- Create: `tunnel-app/desktop/README.md`(开发:`wails3 dev`、`wails3 build`、`npm test`、`go test ./...`、环境变量 `TUNNEL_APP_TUNNEL_BIN`;真机验证清单)
- Modify: `tunnel-app/docs/superpowers/specs/2026-11-22-tunnel-app-phase1-design.md`:§6.4 和 §9 中的服务名改为 `tunnel-<服务器名称>`(每台服务器一个共享服务,设备名字只保存在服务配置中),§9 补充 `--for-user`、`status --json` 的退出码 3/4、`set-name`;§5.1「登录后运行」的 Windows 一项改为任务计划程序;状态改为「已实现(桌面一期)」。
- Modify: `tunnel-app/CHANGELOG.md`、`tunnel/CHANGELOG.md`(删去已完成的 `[pending]` 行,改为一条注明「已在 tunnel-app spec 同步」的记录)

- [ ] **Step 1:** 写 README 的真机验证清单,由用户执行:
  - 三个系统都能打开 App,关闭窗口后进程留在托盘。
  - 添加服务器、测试连接、信任指纹的流程。
  - 打开转发,用 ssh 连通。
  - 共享这台电脑:user 模式可用;macOS 和 Linux 的「无人登录时也保持」会弹出授权并安装成功。
  - 取消授权后开关回到原位。
  - 重启后在线。
  - 断开或连接服务器后,开关状态都能保留。
- [ ] **Step 2:** 同步 spec,记两边的 CHANGELOG,分别提交 `docs: 桌面端 README 与 spec 同步`。

---

## 收尾(控制者执行,需征得用户同意)

1. tunnel 的 `feat/app-cli` 分支合并到 master,推送并打 tag `v1.3.0`;`desktop/go.mod` 去掉 `replace`,改为 `require github.com/pangxincheng/tunnel v1.3.0`,运行 `go mod tidy`,再运行一遍完整测试。
2. tunnel-app 推送 main;总仓库把两个指针都更新,推送后运行 `python3 scripts/check_repos.py`。
