# tunnel 核心库与 CLI(为 tunnel-app 一期)Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为桌面 App 提供核心能力:设备信息文件与设备列表、一条 SSH 连接承载多个转发的会话、可暂停/重试的共享运行时与本机控制通道,以及 `tunnel install / status / uninstall / pause / resume / retry`。

**Architecture:** 全部新增能力放在 `tunnel` 包(库,供桌面 App 直接 import)与 `internal/svc`(系统服务适配,仅 CLI 用)。共享端由 `Share` 运行时驱动:连上 VPS → 写设备信息文件 → 反向转发;不可恢复错误不退出、停在 Failed;状态与操作经本机控制 socket 暴露。连接端由 `Session` 驱动:一条 SSH 连接,本机监听器独立于连接存活,断线重连不影响监听。

**Tech Stack:** Go 1.25,`golang.org/x/crypto/ssh` v0.55,新增 `github.com/pkg/sftp`(读写 VPS 上的设备信息文件、nologin 账户下解析目录)、`github.com/Microsoft/go-winio`(Windows 命名管道)、`golang.org/x/sys`(已是间接依赖,Windows 注册表)。

**Spec:** tunnel-app 仓库 `docs/superpowers/specs/2026-11-22-tunnel-app-phase1-design.md`(本计划实现其第 5.1、5.2、6.1、6.2、6.4、7.5、8、9 节中属于 tunnel 仓库的部分;手机端相关不在本计划)。

## Global Constraints

- 名字规则(服务器 profile、设备名字):`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`(已有 `ValidateName`)。
- 服务名:`tunnel-<profile>-<name>`;profile 缺省 `default`。
- `device_id` = `hex(sha256("tunnel-device-id:" + 原始机器标识)[:16])`;原始标识:macOS `IOPlatformUUID`、Linux `/etc/machine-id`(缺失时 `/var/lib/dbus/machine-id`)、Windows `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid`。
- 设备信息文件:`<socket目录>/<name>.json`,JSON 字段 `device_id, name, hostname, os, os_version, arch, tunnel_version, started_at`(RFC 3339);只含这些字段。写入必须原子(写临时文件后 rename)。
- 重连:退避 1s→30s ±20%,连接稳定 60s 后重置;keepalive 15s(沿用 `Backoff` 与现有常量)。
- 共享服务遇不可恢复错误**不退出**;服务定义中的自动重启只为进程崩溃,间隔 10 秒。
- 控制 socket 权限 `0600`,属主为安装用户;system 模式私钥复制到 `/etc/tunnel/...` 且权限 `0600`。
- `--mode system|user` 在 install 时必填;system 非 root → 有终端时 `sudo` 重新执行自身(参数原样),无终端时报错;user 以 root 运行 → 报错。
- 平台矩阵:macOS(LaunchAgent / LaunchDaemon)、Linux(`systemd --user` / systemd 系统服务)、Windows(仅 user:任务计划程序登录任务,失败后重启,不显示控制台窗口)。
- **禁止在开发机上安装系统或用户服务、禁止用 root/sudo 运行任何 install/uninstall**:测试只使用假的 `Runner`/`Manager` 与临时目录;Task 8 Step 7 的真机验证由用户在目标机器上执行。(用户要求)
- 现有 `tunnel server / client` 的参数与行为保持不变(`--user` 仍是 VPS 登录用户)。
- 用户可见文案为中文,风格与现有日志、错误一致;每个提交同步更新 `CHANGELOG.md` 顶部当日条目(`## 2026-11-22(tunnel-app 支持)`)。

## Review Focus

1. **笔记本睡眠唤醒后**:旧 TCP 连接已死,用户期望几秒内恢复 → Task 4 `TestSessionRetryNowSkipsBackoff`(`RetryNow` 在退避等待中立即重连)。
2. **本机端口已被其他程序占用时打开转发**:只有这一个转发失败,其他转发与会话不受影响 → Task 4 `TestSessionAddForwardPortBusyIsolated`。
3. **旧版本 tunnel 留下的无 `device_id` 或损坏的 json**:不应把名字永久锁死 → Task 5 `TestShareNameCheckIgnoresUnownedInfo`(缺 `device_id` 或解析失败视为无主,允许共享并覆盖)。
4. **其他本机用户连接控制 socket**:应被拒 → Task 6 `TestControlListenPermissions`(socket 文件权限为 0600)。
5. **`--mode user` 被人用 sudo 运行**:会装进 root 的用户域、永远不启动 → Task 8 `TestCheckPrivilegeUserModeAsRoot`。

---

### Task 1: 设备 ID 与本机设备信息

**Files:**
- Create: `tunnel/device.go`、`tunnel/machineid_darwin.go`、`tunnel/machineid_linux.go`、`tunnel/machineid_windows.go`、`tunnel/machineid_other.go`(`//go:build !darwin && !linux && !windows`)
- Test: `tunnel/device_test.go`
- Modify: `go.mod`(Windows 文件用到 `golang.org/x/sys/windows/registry`,升为直接依赖)、`CHANGELOG.md`

**Interfaces:**
- Produces:
  - `type DeviceInfo struct { DeviceID string \`json:"device_id"\`; Name string \`json:"name"\`; Hostname string \`json:"hostname"\`; OS string \`json:"os"\`; OSVersion string \`json:"os_version"\`; Arch string \`json:"arch"\`; TunnelVersion string \`json:"tunnel_version"\`; StartedAt time.Time \`json:"started_at"\` }`
  - `func DeviceID() (string, error)` — 读平台机器标识后调用 `deriveDeviceID`。
  - `func deriveDeviceID(raw string) string` — 对 `strings.TrimSpace(raw)` 计算。
  - `func LocalDeviceInfo(name string, startedAt time.Time) (DeviceInfo, error)` — `OS` 取 `macOS` / `Linux` / `Windows`(其他平台用 `runtime.GOOS`);`OSVersion` 尽力获取(macOS `sw_vers -productVersion`、Linux `/etc/os-release` 的 `PRETTY_NAME`、Windows 注册表 `ProductName`),取不到留空不报错;`Arch` = `runtime.GOARCH`;`TunnelVersion` = `Version`。
  - `func machineID() (string, error)` — 各平台文件实现;macOS 解析 `ioreg -rd1 -c IOPlatformExpertDevice` 输出中的 `"IOPlatformUUID" = "<uuid>"`。

- [ ] **Step 1: 写失败测试**

```go
func TestDeriveDeviceIDStable(t *testing.T) {
	a := deriveDeviceID("abc\n")
	if a != deriveDeviceID("abc") { t.Fatal("应忽略首尾空白") }
	if len(a) != 32 { t.Fatalf("应为 32 位十六进制,got %q", a) }
	if a == deriveDeviceID("abd") { t.Fatal("不同输入应得到不同 ID") }
	sum := sha256.Sum256([]byte("tunnel-device-id:abc"))
	if a != hex.EncodeToString(sum[:16]) { t.Fatal("派生公式与规范不符") }
}
func TestDeviceIDOnThisMachine(t *testing.T) // 在 darwin/linux/windows 上:DeviceID() 无错误、两次调用结果相同;其他平台 t.Skip
func TestLocalDeviceInfo(t *testing.T)       // name="nas":Name/Arch/TunnelVersion/DeviceID 非空且正确;json.Marshal 后的键集合恰为规范 8 个
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./tunnel/ -run 'DeviceID|LocalDeviceInfo' -v`
Expected: FAIL(`undefined: deriveDeviceID`)

- [ ] **Step 3: 实现上述函数与各平台 `machineID`**

- [ ] **Step 4: 运行确认通过,并交叉编译三平台**

Run: `go test ./tunnel/ -run 'DeviceID|LocalDeviceInfo' -v && GOOS=windows go build ./... && GOOS=linux go build ./...`
Expected: PASS;两次交叉编译无错误

- [ ] **Step 5: Commit**(`feat: 设备 ID 与本机设备信息`,附 CHANGELOG)

---

### Task 2: 远端文件:SFTP 读写设备信息、目录解析兜底

**Files:**
- Create: `tunnel/remotefs.go`、`tunnel/remotefs_test.go`
- Modify: `tunnel/socket.go`(`ResolveSocketPath` 改为调用 `ResolveSocketDir`)、`tunnel/testserver_test.go`(session 上支持 `subsystem sftp`)、`go.mod`、`CHANGELOG.md`

**Interfaces:**
- Consumes: `DeviceInfo`(Task 1)。
- Produces:
  - `func ResolveSocketDir(sc *ssh.Client, dir string) (string, error)` — `dir` 非空直接返回;否则先走现有 exec(`DefaultSocketDirCommand`);exec 得到 `*SocketDirError` 时改用 SFTP:`Getwd()` 得家目录,`MkdirAll(home/.tunnel/sockets)` 并 `Chmod 0700`;两者都失败返回原 `SocketDirError`。
  - `ResolveSocketPath` 行为不变(仍校验 103 字节),内部改用 `ResolveSocketDir`。
  - `func WriteDeviceInfo(sc *ssh.Client, dir string, info DeviceInfo) error` — 写 `dir/<name>.json.tmp` 后 `PosixRename` 为 `dir/<name>.json`。
  - `func ReadDeviceInfos(sc *ssh.Client, dir string) (map[string]DeviceInfo, error)` — 键为文件名去掉 `.json`;解析失败的文件以零值 `DeviceInfo{Name: 键}` 收录,不报错。
  - `func RemoveDeviceInfo(sc *ssh.Client, dir, name, deviceID string) error` — 仅当文件存在且其 `device_id == deviceID` 时删除;不存在不报错。
  - 测试服务器:`handleSession` 收到 `subsystem` 请求且名字为 `sftp` 时,用 `sftp.NewServer(ch, sftp.WithServerWorkingDirectory(s.Home))` 服务;新字段 `DenySFTP bool`。

- [ ] **Step 1: 写失败测试**

```go
func TestDeviceInfoRoundTrip(t *testing.T)          // Write 后 Read 得到相同字段(StartedAt 用 time.Equal 比较);目录中不残留 .tmp
func TestReadDeviceInfosCorruptFile(t *testing.T)   // 目录中放入 "bad.json" 内容 "{" → map 含 "bad",DeviceID 为空,无错误
func TestRemoveDeviceInfoOnlyOwn(t *testing.T)      // device_id 不同 → 文件仍在;相同 → 删除;不存在 → nil
func TestResolveSocketDirFallsBackToSFTP(t *testing.T) // DenyExec=true、dir="" → 返回 server.Home+"/.tunnel/sockets",目录权限 0700
func TestResolveSocketDirBothDenied(t *testing.T)   // DenyExec 且 DenySFTP → errors.As *SocketDirError 且 IsPermanent
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./tunnel/ -run 'DeviceInfo|ResolveSocketDir' -v`
Expected: FAIL(`undefined: WriteDeviceInfo`)

- [ ] **Step 3: `go get github.com/pkg/sftp`,实现上述函数与测试服务器 sftp 支持**

若所取版本没有 `WithServerWorkingDirectory`,改为让测试服务器进程内 `Getwd` 返回 `s.Home` 的等价方式,并在 CHANGELOG 记录所用版本。

- [ ] **Step 4: 运行确认通过 + 全量**

Run: `go test ./... -race`
Expected: PASS(含原有 `TestResolveSocketPath*`)

- [ ] **Step 5: Commit**(`feat: SFTP 读写设备信息文件,目录解析在禁止执行命令时改用 SFTP`)

---

### Task 3: 设备列表与在线探测

**Files:**
- Create: `tunnel/devices.go`、`tunnel/devices_test.go`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: `ReadDeviceInfos`(Task 2)。
- Produces:
  - `type Device struct { DeviceInfo; Online bool }`
  - `func ListDevices(ctx context.Context, sc *ssh.Client, dir string) ([]Device, error)` — 设备集合 = 所有 `*.json` ∪ 所有 `*.sock`(只有 socket 没有 json 的设备 `Name` 取文件名,其余字段为空);对每个名字并发(上限 8)探测 `sc.Dial("unix", dir/<name>.sock)`,3 秒超时,成功即 `Online=true` 并立刻关闭;按 `Name` 升序返回。

- [ ] **Step 1: 写失败测试**

```go
func TestListDevicesOnlineAndOffline(t *testing.T)
// 连接 A:ReverseForwardUnix(dir,"macmini",echo) + WriteDeviceInfo(name macmini);另写一份 "pi.json" 无 socket
// ListDevices → [{macmini Online:true},{pi Online:false}],顺序按名字
func TestListDevicesSocketWithoutInfo(t *testing.T) // 只有 lab.sock 在线、无 json → 出现 {Name:"lab", Online:true, DeviceID:""}
func TestListDevicesStaleSocket(t *testing.T)       // 目录中有残留 old.sock 文件但无人监听 → Online=false,且整体在 5 秒内返回
```

- [ ] **Step 2: 运行确认失败** — `go test ./tunnel/ -run ListDevices -v`,Expected: FAIL(`undefined: ListDevices`)

- [ ] **Step 3: 实现 `ListDevices`**(SFTP `ReadDir` 列目录)

- [ ] **Step 4: 运行确认通过 + 全量** — `go test ./... -race`,Expected: PASS

- [ ] **Step 5: Commit**(`feat: ListDevices 列出服务器上的设备并探测在线`)

---

### Task 4: Session:一条连接承载多个转发

**Files:**
- Create: `tunnel/session.go`、`tunnel/session_test.go`
- Modify: `tunnel/client.go`(把 keepalive 循环抽成 `keepalive(ctx context.Context, sc *ssh.Client, every time.Duration, logger *slog.Logger)`,`Client` 改用它,行为不变)、`CHANGELOG.md`

**Interfaces:**
- Consumes: `Dial`、`Backoff`、`ResolveSocketDir`、`ListDevices`、`IsPermanent`。
- Produces:
  - `type Phase string`;常量 `PhaseConnecting="connecting"`、`PhaseReady="ready"`、`PhaseWaiting="waiting"`(退避中)、`PhaseFailed="failed"`(不可恢复)、`PhasePaused="paused"`(Task 5 用)。
  - `type ConnState struct { Phase Phase; Err string; RetryAt time.Time; Attempt int }`
  - `func NewSession(cfg *Config, socketDir string, opts ...SessionOption) *Session`;`SessionOption`:`WithSessionLogger(*slog.Logger)`、`WithStateHook(func(ConnState))`。
  - `func (s *Session) Run(ctx context.Context) error` — 监督循环;ctx 取消返回 nil;**不可恢复错误进入 `PhaseFailed` 并阻塞等待 `RetryNow` 或 ctx**(不返回)。
  - `func (s *Session) RetryNow()` — 退避等待或 Failed 时立即开始下一次连接,并重置退避。
  - `func (s *Session) AddForward(name string, localPort int) error` — 立刻在 `127.0.0.1:localPort` 监听(与 SSH 连接无关,断线期间保持监听);端口被占用返回 `*LocalListenError`;同名重复调用:端口相同为无操作,不同则先关旧再开新。每个接入连接经**当前** SSH 连接 `Dial("unix", socketDir/<name>.sock)`;未连接或拨号失败立即关闭接入连接。
  - `func (s *Session) RemoveForward(name string)`
  - `func (s *Session) Forwards() map[string]int` — 名字 → 本机端口。
  - `func (s *Session) Devices(ctx context.Context) ([]Device, error)` — 用当前连接调用 `ListDevices`;未连接返回 `ErrNotConnected`(新导出错误)。
  - `func (s *Session) State() ConnState`
  - `socketDir` 为空时每次连上后用 `ResolveSocketDir` 解析。

- [ ] **Step 1: 写失败测试**

```go
func TestSessionForwardEndToEnd(t *testing.T)        // 另一连接挂 ReverseForwardUnix("", "nas", echo);Session.AddForward("nas", 空闲端口) → TCP 连本机端口收到 echo
func TestSessionListenerSurvivesReconnect(t *testing.T) // server.CloseAllClients() 后本机端口仍可 net.Dial(连接被立刻关闭);重连就绪后 echo 恢复
func TestSessionAddForwardPortBusyIsolated(t *testing.T) // 占用端口 P:AddForward("a",P) 返回 *LocalListenError;AddForward("b",空闲) 仍可用;State().Phase 仍为 ready
func TestSessionRetryNowSkipsBackoff(t *testing.T)   // reconnectMin 设为 10s;断开后调用 RetryNow,1 秒内 Phase 回到 ready
func TestSessionPermanentErrorWaitsForRetry(t *testing.T) // 主机公钥不匹配:Run 不返回,Phase=failed 且 Err 非空;RetryNow 后再次尝试(Attempt 增加)
func TestSessionDevicesNotConnected(t *testing.T)    // 未连上时 Devices 返回 errors.Is(err, ErrNotConnected)
```

- [ ] **Step 2: 运行确认失败** — `go test ./tunnel/ -run Session -v`,Expected: FAIL(`undefined: NewSession`)

- [ ] **Step 3: 实现 Session 与 keepalive 抽取**(测试中缩短 `reconnectMin` / `keepaliveInterval` 的方式与 `Client` 相同:未导出字段)

- [ ] **Step 4: 运行确认通过 + 全量** — `go test ./... -race`,Expected: PASS(含原有 `TestClient*`)

- [ ] **Step 5: Commit**(`feat: Session 一条 SSH 连接承载多个转发,本机监听独立于连接`)

---

### Task 5: Share:可暂停、可重试的共享运行时

**Files:**
- Create: `tunnel/share.go`、`tunnel/share_test.go`
- Modify: `tunnel/errors.go`(新增 `NameTakenError`)、`CHANGELOG.md`

**Interfaces:**
- Consumes: `DeviceID`、`LocalDeviceInfo`、`ResolveSocketDir`、`ReadDeviceInfos`、`WriteDeviceInfo`、`RemoveDeviceInfo`、`ReverseForwardUnix`、`Phase` / `ConnState`。
- Produces:
  - `type ShareConfig struct { Conn Config; SocketDir string; Name string; Target string; StatePath string; DeviceID string }`(`Target` 如 `127.0.0.1:22`;`StatePath` 为持久化暂停状态的 JSON 文件;`DeviceID` 为空时用 `DeviceID()`)。
  - `type ShareStatus struct { ConnState; Name string; Socket string; Since time.Time; Paused bool; Version string }`(JSON 键小写下划线)
  - `func NewShare(cfg ShareConfig, opts ...SessionOption) (*Share, error)` — 读取 `StatePath`(不存在 = 未暂停)。
  - `func (s *Share) Run(ctx context.Context) error` — 只在 ctx 取消时返回 nil。每次连接:解析目录 → 名字检查 → `WriteDeviceInfo`(`StartedAt` = 本次进入 ready 的时间)→ 反向转发 `Target`。不可恢复错误 → `PhaseFailed`,等待 `Retry`。暂停时 `PhasePaused`,不连接。
  - 名字检查:`<name>.json` 存在且 `device_id` 非空且 ≠ 本机 → `*NameTakenError{Name, Owner DeviceInfo}`(Is `ErrPermanent`);文案「「<name>」已被另一台设备(<hostname>)使用,换一个设备名字」。
  - `func (s *Share) Pause() error` / `Resume() error` — 写 `StatePath`(`{"paused":true}`,权限 0600),立即生效。
  - `func (s *Share) Retry()`
  - `func (s *Share) SetName(name string) error` — `ValidateName`;断开当前连接,删除旧名字的本机信息文件(`RemoveDeviceInfo`),以新名字重连。持久化由调用方(CLI 写配置文件)负责。
  - `func (s *Share) Status() ShareStatus`

- [ ] **Step 1: 写失败测试**

```go
func TestShareWritesInfoAndForwards(t *testing.T)   // Run 后:<name>.json 的 device_id 为配置值;经 socket 连到 Target(echo)成功;Status().Phase=ready
func TestShareNameTakenByOtherDevice(t *testing.T)  // 预置 nas.json(device_id="other") → Phase=failed,Err 含「已被另一台设备」;进程不退出
func TestShareNameCheckIgnoresUnownedInfo(t *testing.T) // 预置 nas.json 内容 "{" 或无 device_id → 正常 ready 并覆盖为本机信息
func TestSharePausePersists(t *testing.T)           // Pause 后 socket 不可连、Phase=paused;用同一 StatePath 新建 Share 并 Run → 仍 paused;Resume → ready
func TestShareRetryAfterFailure(t *testing.T)       // 先让名字冲突 → failed;删除冲突文件后 Retry → ready
func TestShareSetNameRemovesOldInfo(t *testing.T)   // nas → nas2:nas.json 被删除,nas2.json 存在,nas2.sock 可连
```

- [ ] **Step 2: 运行确认失败** — `go test ./tunnel/ -run Share -v`,Expected: FAIL(`undefined: NewShare`)

- [ ] **Step 3: 实现 Share**(内部复用 Task 4 的监督循环形态;可在 session.go 中抽出共用的 `supervisor` 结构,两者共享状态机代码)

- [ ] **Step 4: 运行确认通过 + 全量** — `go test ./... -race`,Expected: PASS

- [ ] **Step 5: Commit**(`feat: Share 共享运行时(名字检查、暂停持久化、失败后等待重试)`)

---

### Task 6: 本机控制通道

**Files:**
- Create: `tunnel/control.go`(协议与服务端)、`tunnel/control_unix.go`(`//go:build !windows`)、`tunnel/control_windows.go`、`tunnel/control_test.go`
- Modify: `go.mod`(`github.com/Microsoft/go-winio`)、`CHANGELOG.md`

**Interfaces:**
- Consumes: `Share`(Task 5)。
- Produces:
  - `type ControlRequest struct { Op string \`json:"op"\`; Name string \`json:"name,omitempty"\` }`,`Op` ∈ `status|pause|resume|retry|set-name`。
  - `type ControlResponse struct { OK bool \`json:"ok"\`; Error string \`json:"error,omitempty"\`; Status *ShareStatus \`json:"status,omitempty"\` }`
  - 协议:每个连接一行 JSON 请求、一行 JSON 响应,然后关闭。
  - `func ControlListen(addr string, ownerUID int) (net.Listener, error)` — Unix:创建父目录(0700),删除残留 socket,监听后 `chmod 0600`,`ownerUID >= 0` 时 `chown`。Windows:`addr` 为 `\\.\pipe\...`,用 go-winio,安全描述符只允许当前用户与 SYSTEM。
  - `func ServeControl(ctx context.Context, l net.Listener, s *Share, onSetName func(string) error) error` — `set-name` 先调 `s.SetName`,成功后调 `onSetName` 持久化。
  - `func ControlCall(addr string, req ControlRequest) (ControlResponse, error)` — 3 秒超时。

- [ ] **Step 1: 写失败测试**

```go
func TestControlStatusPauseResume(t *testing.T) // 起 Share + ServeControl;ControlCall status → OK 且 Status.Phase=ready;pause → Status.Paused=true;resume → ready
func TestControlSetNameInvalid(t *testing.T)    // set-name "a/b" → OK=false,Error 含「不合法」;onSetName 未被调用
func TestControlListenPermissions(t *testing.T) // Unix:socket 文件 Mode().Perm()==0600;非 Windows 平台运行
func TestControlUnknownOp(t *testing.T)         // op "reboot" → OK=false,Error 含「未知操作」
```

- [ ] **Step 2: 运行确认失败** — `go test ./tunnel/ -run Control -v`,Expected: FAIL

- [ ] **Step 3: 实现**

- [ ] **Step 4: 运行确认通过 + 交叉编译** — `go test ./... -race && GOOS=windows go vet ./tunnel/`,Expected: PASS

- [ ] **Step 5: Commit**(`feat: 共享服务本机控制通道(Unix socket / 命名管道)`)

---

### Task 7: 系统服务适配(internal/svc)

**Files:**
- Create: `internal/svc/svc.go`、`internal/svc/paths.go`、`internal/svc/systemd.go`、`internal/svc/launchd.go`、`internal/svc/taskschd.go`、`internal/svc/svc_test.go`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Produces:
  - `type Mode string`;`ModeSystem="system"`、`ModeUser="user"`。
  - `func ServiceName(profile, name string) string` → `tunnel-<profile>-<name>`。
  - `type Paths struct { ConfigDir, StatePath, ControlAddr, BinPath, LogPath string }`;`func PathsFor(mode Mode, svc string, goos string, home string) Paths`:

    | | system(linux / darwin) | user linux | user darwin | user windows |
    |---|---|---|---|---|
    | ConfigDir | `/etc/tunnel/<svc>` | `~/.config/tunnel/<svc>` | `~/Library/Application Support/tunnel/<svc>` | `%AppData%\tunnel\<svc>` |
    | StatePath | `/var/lib/tunnel/<svc>/state.json` | ConfigDir`/state.json` | 同左 | 同左 |
    | ControlAddr | `/run/tunnel/<svc>.sock`(darwin `/var/run/tunnel/<svc>.sock`) | `$XDG_RUNTIME_DIR/tunnel/<svc>.sock`,无则 ConfigDir`/control.sock` | ConfigDir`/control.sock` | `\\.\pipe\tunnel-<svc>` |
    | BinPath | `/usr/local/bin/tunnel` | `~/.local/bin/tunnel` | `~/Library/Application Support/tunnel/bin/tunnel` | `%LocalAppData%\tunnel\tunnel.exe` |
    | LogPath | systemd 用 journal(空);darwin `/var/log/tunnel/<svc>.log` | 空 | ConfigDir`/tunnel.log` | ConfigDir`\tunnel.log` |

  - `type Spec struct { Name string; Mode Mode; Exec string; Args []string; LogPath string }`
  - `type Runner interface { Run(name string, args ...string) error }`(真实实现调用 `exec.Command`;测试用假实现记录命令)。
  - `type Manager interface { Install(Spec) error; Uninstall(name string) error; Installed(name string) (bool, error) }`
  - `func NewManager(mode Mode, goos string, home string, r Runner) (Manager, error)` — 不支持的组合(windows + system)返回错误「Windows 暂不支持开机即运行(--mode system)」。
  - 渲染函数(可测):`renderSystemdUnit(Spec) string`(`Restart=on-failure`、`RestartSec=10`、system 用 `WantedBy=multi-user.target` 与 `After=network-online.target`,user 用 `WantedBy=default.target`);`renderLaunchdPlist(Spec) string`(Label=`com.pangxincheng.<name>`,`RunAtLoad`、`KeepAlive` 字典 `SuccessfulExit=false`、`ThrottleInterval=10`、标准输出与错误写 LogPath);`renderTaskXML(Spec) string`(LogonTrigger、`RestartOnFailure` 间隔 `PT1M` 次数 999、`Hidden`、`ExecutionTimeLimit=PT0S`)。
  - 安装命令序列:systemd `daemon-reload` + `enable --now <name>`(user 加 `--user`);launchd system `launchctl bootstrap system <plist>`,user `launchctl bootstrap gui/<uid> <plist>`(已加载先 `bootout`);Windows `schtasks /Create /TN <name> /XML <file> /F` + `schtasks /Run /TN <name>`。卸载为对应逆序。

- [ ] **Step 1: 写失败测试**

```go
func TestServiceName(t *testing.T)            // ("default","nas") → "tunnel-default-nas"
func TestPathsForTable(t *testing.T)          // 覆盖上表每个单元格(home="/home/u" 或 `C:\Users\u`)
func TestRenderSystemdUnit(t *testing.T)      // system:含 "Restart=on-failure"、"RestartSec=10"、"WantedBy=multi-user.target"、ExecStart 为 Exec+Args 用空格连接
func TestRenderLaunchdPlist(t *testing.T)     // 含 <key>KeepAlive</key> 下 SuccessfulExit false、ThrottleInterval 10、Label com.pangxincheng.tunnel-default-nas
func TestRenderTaskXML(t *testing.T)          // 含 <LogonTrigger>、<RestartOnFailure>、<Hidden>true</Hidden>
func TestInstallCommandSequence(t *testing.T) // 假 Runner:linux system → ["systemctl daemon-reload", "systemctl enable --now tunnel-default-nas"];linux user 每条带 --user
func TestWindowsSystemUnsupported(t *testing.T)
```

- [ ] **Step 2: 运行确认失败** — `go test ./internal/svc/ -v`,Expected: FAIL

- [ ] **Step 3: 实现**(`NewManager` 以参数接收 goos/home,便于在任意平台测试所有渲染)

- [ ] **Step 4: 运行确认通过** — `go test ./internal/svc/ -v`,Expected: PASS

- [ ] **Step 5: Commit**(`feat: internal/svc 渲染并安装 systemd / launchd / 任务计划程序服务`)

---

### Task 8: CLI 子命令与文档

**Files:**
- Modify: `cmd/tunnel/main.go`(usage 与分发)、`cmd/tunnel/main_test.go`、`README.md`、`tunnel.toml.example`、`CHANGELOG.md`
- Create: `cmd/tunnel/service.go`(install / uninstall / status / pause / resume / retry / run-service)、`cmd/tunnel/service_test.go`、`cmd/tunnel/privilege_unix.go`、`cmd/tunnel/privilege_windows.go`

**Interfaces:**
- Consumes: Task 5–7 全部;现有 `merge` / `params.config`(复用 `server` 的参数合并)。
- Produces(usage 新增):

```
  tunnel install --mode=MODE [--profile=P] [options]
  tunnel uninstall [--mode=MODE] [--profile=P] [--name=NAME]
  tunnel status [--mode=MODE] [--profile=P] [--name=NAME] [--json]
  tunnel pause|resume|retry [--mode=MODE] [--profile=P] [--name=NAME]
  tunnel run-service --mode=MODE --profile=P --name=NAME
```

  - `--mode`:install 必填;其他命令缺省时依次查找 user、system 中已安装的服务(两者都有时报错要求指定)。`--name` 缺省:该 profile 下只有一个服务时自动选中。
  - `install` 只支持名字模式(必须有 `name`,端口模式报错「tunnel install 只支持 --name 按名字转发」)。流程:`checkPrivilege` → 合并参数并校验 → 实际 `Dial` + `ResolveSocketDir` 一次(失败直接报错退出码 1)→ 复制自身到 `Paths.BinPath` → 写 `ConfigDir/tunnel.toml`(system 模式同时复制私钥到 `ConfigDir/id_key`,0600,配置引用它;主机公钥内联写入)→ 记录安装用户 UID 到 `ConfigDir/owner`(system)→ `Manager.Install(Spec{Exec: BinPath, Args: ["run-service","--mode",M,"--profile",P,"--name",N]})` → 打印结果(服务名、查看状态/日志的命令)。重复执行 = 覆盖并重启。
  - `run-service`:读 `ConfigDir/tunnel.toml` → `NewShare`(StatePath、DeviceID)→ `ControlListen(Paths.ControlAddr, owner)` → `ServeControl`(`onSetName` 改写 tunnel.toml 的 `name` 字段;服务名不变)→ `Share.Run`;SIGINT/SIGTERM 优雅退出。
  - `status`:经控制通道读 `ShareStatus`;`--json` 原样输出 `ControlResponse.Status`;服务已安装但控制通道连不上时输出「已安装,未运行」(退出码 3)。
  - `pause/resume/retry`:对应控制通道操作。
  - `uninstall`:`Manager.Uninstall` 停止服务 → 用 ConfigDir 中的配置直接 `Dial` 一次 VPS,`RemoveDeviceInfo(name, 本机 device_id)` 删除服务器上的本机信息文件(连不上只打印警告,不阻止卸载)→ 删除 ConfigDir 与 StatePath。system 模式同样走 `checkPrivilege`。暂停(pause)**不**删除信息文件:断开期间其他设备应看到「离线 / 最后在线」。
  - `func checkPrivilege(mode svc.Mode, euid int, isTerminal bool, args []string) (reexec []string, err error)`:system 且 euid≠0 → isTerminal 时返回 `["sudo","--",os.Args[0],args...]`,否则错误「--mode system 需要 root 权限,请用 sudo 运行」;user 且 euid==0 → 错误「--mode user 不能以 root 运行(会装进 root 自己的登录项,永远不会启动)」。调用方拿到 reexec 时 `syscall.Exec`(Unix)。Windows:system 已在 Task 7 拒绝;user 不检查。

- [ ] **Step 1: 写失败测试**

```go
func TestCheckPrivilegeSystemReexec(t *testing.T)      // (system, euid 501, terminal) → reexec[0]=="sudo",且其后原样包含 args
func TestCheckPrivilegeSystemNoTerminal(t *testing.T)  // (system, 501, false) → 错误含「sudo」
func TestCheckPrivilegeUserModeAsRoot(t *testing.T)    // (user, 0, true) → 错误含「不能以 root 运行」
func TestInstallRejectsPortMode(t *testing.T)          // 参数只有 --remote-listen-port → 错误含「只支持 --name」
func TestRunServiceEndToEnd(t *testing.T)              // 用测试 SSH 服务器 + 临时 ConfigDir 直接调用 runService(不经 Manager):status → ready;pause → paused;set-name 后 tunnel.toml 的 name 已改写
func TestStatusNotRunning(t *testing.T)                // 有 ConfigDir、无控制 socket → 输出含「已安装,未运行」,退出码 3
```

(`runService`、`installFlow` 等以可注入的 `svc.Manager`、`Paths`、`Dial` 实现,测试不接触真实系统服务。)

- [ ] **Step 2: 运行确认失败** — `go test ./cmd/tunnel/ -v`,Expected: FAIL

- [ ] **Step 3: 实现子命令**

- [ ] **Step 4: 运行确认通过 + 全量 + 三平台编译**

Run: `go test ./... -race && go vet ./... && GOOS=windows go build ./... && GOOS=linux go build ./... && mkdir -p dist && go build -o dist/tunnel ./cmd/tunnel && ./dist/tunnel --help`
Expected: 全部 PASS;help 中出现 install / uninstall / status / pause / resume / retry

- [ ] **Step 5: 文档**:README 新增「开机自启与后台运行」一节(install 的两种模式与平台矩阵、status/pause/resume/retry、卸载、macOS FileVault 与「断电后自动启动」提示、Linux BIOS 来电开机提示);`tunnel.toml.example` 加 `profile` 说明;CHANGELOG 补全本日条目。

- [ ] **Step 6: Commit**(`feat: tunnel install/uninstall/status/pause/resume/retry 与 run-service`)

- [ ] **Step 7: 真机验证(由用户在目标机器上手动执行;实现者不在开发机上执行,只把步骤写进 README 并在 CHANGELOG 标记 [pending])**
  - macOS:`sudo tunnel install --mode system --name <名字> -c tunnel.toml` 后 `tunnel status` 为 ready;重启后无需登录仍可经 VPS 连上;`tunnel pause` / `resume` 生效;`sudo tunnel uninstall` 后服务与配置消失。
  - Linux:同上(system 与 user 各一次)。
  - Windows:`tunnel install --mode user ...` 后立即运行、无控制台窗口;注销再登录后自动运行。
