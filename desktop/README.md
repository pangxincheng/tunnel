# tunnel-app 桌面端

Wails v3.1.0(Go 后端 + React 18 / TypeScript / Vite 前端)。设计见 `../docs/superpowers/specs/2026-11-22-tunnel-app-phase1-design.md`。

## 开发环境

- Go 1.27(`go.mod` 声明 1.26,更新的工具链可用)
- Node.js(含 npm)
- Wails v3 命令行:

  ```bash
  go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0
  ```

- Linux 另需 GTK / WebKitGTK 开发包(见 Wails v3 文档)。

## 常用命令

以下命令都在 `desktop/` 下运行。

```bash
wails3 task bundle:tunnel   # 把 tunnel 命令行编进 bin/,打包时随 App 一起发布
wails3 dev                  # 开发模式(前端热更新)
wails3 build                # 构建当前系统的可执行文件(输出在 bin/)
wails3 package              # 打包安装包(macOS .app、Linux deb / rpm / AppImage、Windows 安装程序)
```

首次构建前先运行一次 `wails3 task bundle:tunnel`;没有附带的 tunnel 时 App 仍能启动,但共享功能会报「未找到附带的 tunnel 程序」。

## 测试

```bash
# 前端(desktop/frontend)
npm install
npm test                    # vitest run
npx tsc --noEmit

# Go(desktop/)
cd frontend && npm run build && cd ..   # go:embed 需要 frontend/dist 存在,新克隆的仓库要先构建一次
go test ./...
```

测试不会安装任何服务、不会请求管理员权限、不会注册登录项:涉及服务管理器、提权、开机自启的测试都用假实现或临时目录。

## 环境变量

| 变量 | 作用 |
|---|---|
| `TUNNEL_APP_TUNNEL_BIN` | 指定要使用的 tunnel 可执行文件(默认用 App 旁边附带的那个) |
| `TUNNEL_APP_CONFIG` | 指定配置文件路径,用于隔离的试运行(默认在用户配置目录的 `tunnel-app/` 下) |

## go.mod 的 replace

`go.mod` 里 `github.com/pangxincheng/tunnel` 暂时 `replace` 到 `../../tunnel`(同级检出,需在 `feat/app-cli` 分支)。tunnel v1.3.0 打 tag 后,去掉 `replace`,改为 `require github.com/pangxincheng/tunnel v1.3.0` 并运行 `go mod tidy` 与完整测试。

## 已知缺口

- Linux AppImage 不带 tunnel 命令行(deb / rpm 带),AppImage 用户暂时无法使用共享。[pending]
- macOS 只构建当前架构,没有 universal 包;`bundle:tunnel` 同样只编当前架构。
- 图形界面(窗口、托盘、菜单、标题栏)在开发机上无法观察,尚未在真机验证:请按下面的清单逐项检查。
- Windows / Linux 的打包产物未在 macOS 上验证。

## 真机验证清单

在 macOS、Linux、Windows 三个系统上各执行一遍(「无人登录时也保持」仅 macOS / Linux)。准备:一台只跑 sshd 的 VPS、一把没有口令的私钥,以及另一台已在该 VPS 上共享的设备(用于连接测试)。试运行可设置 `TUNNEL_APP_CONFIG` 指向临时文件,以免动到正式配置。

### 窗口与托盘
- [ ] 能打开 App,首次打开显示「添加第一台服务器」空状态。
- [ ] 窗口按钮位置:macOS 为隐藏式标题栏,红绿灯在左上角、压在侧栏上方,不遮挡内容;Windows / Linux 为系统标题栏,按钮在右上角。
- [ ] 点关闭按钮后窗口隐藏,进程仍在托盘 / 菜单栏(任务管理器或活动监视器里还在);点托盘「打开 Tunnel」窗口回来。
- [ ] Cmd+Q(macOS)/ Ctrl+Q(Windows、Linux)退出 App;托盘「退出」同样退出。没有托盘的 Linux 桌面(如 GNOME)上,关闭窗口改为最小化,用 Ctrl+Q 退出。

### 原地更新
- [ ] 打开设置,依次点显示模式、主题色、语言、两个开关:界面立即变化,弹窗不闪烁、不重新播放进场动画,焦点留在刚点的控件上。
- [ ] 服务器列表与设备行在状态刷新(每 30 秒或手动刷新)时不闪烁、不重新播放动画,输入框里正在输入的内容不丢。

### 添加服务器
- [ ] 添加服务器:填名称、地址、端口、用户,选私钥,点「测试连接」,显示 SHA256 指纹;用 `ssh-keygen -lf /etc/ssh/ssh_host_ed25519_key.pub` 在 VPS 上核对一致后点「指纹一致,信任」,「保存」才可用。
- [ ] 改地址 / 端口 / 用户 / 私钥后,信任被清除,需重新测试并信任。
- [ ] 带口令的私钥、错误的用户、连不上的地址,分别显示对应的整句提示。
- [ ] 保存后自动选中这台服务器。

### 连接其他设备
- [ ] 在线设备打开开关后,本机 `ssh -p <端口> <用户>@127.0.0.1` 能连上该设备;离线设备的开关不可用。
- [ ] 点端口数字可改端口(回车保存、Esc 取消);端口被其他程序占用时打开失败并提示,开关保持关闭。
- [ ] 连接命令弹窗里的命令都能复制。
- [ ] 通过转发复制一个大文件(`scp -P <端口> 大文件 <用户>@127.0.0.1:/tmp/`,几百 MB 以上),传输期间不断开。

### 共享这台电脑
- [ ] 本机 sshd 未开启时状态行给出开启方法并禁止打开共享;开启后检测到端口。
- [ ] user 模式:打开共享,几秒内状态变为「已就绪」,另一台设备能看到本机在线并通过它的转发 ssh 登录本机。
- [ ] 关闭共享开关只是暂停(服务保留,另一台看到本机离线);重新打开恢复。
- [ ] 设备名字重名时提示「已被另一台设备使用」并禁止打开。
- [ ] macOS / Linux:打开「无人登录时也保持」会弹出管理员授权,授权后安装成功、开关变为开启;macOS 先提示 FileVault 的说明。
- [ ] 在授权弹窗点取消:开关回到原位,界面提示「已取消授权」,原来的服务不受影响。
- [ ] 重启电脑、不登录:system 模式下本机仍在线(从另一台设备查看);user 模式则登录后才在线。
- [ ] 暂停后重启:关闭共享开关(暂停)后重启电脑,登录或开机后该共享仍保持暂停、不自动恢复(macOS、Linux、Windows 都要查)。
- [ ] Windows:user 模式打开共享后,在「任务计划程序」里能看到该服务器对应的登录时运行任务;结束该进程后会被自动重新拉起。
- [ ] Windows:App 开着期间(状态每几秒轮询)不会有控制台黑窗闪现。
- [ ] Windows:安装后的目录里 `tunnel.exe` 与 App 在一起。
- [ ] 失败时「查看日志」可用:macOS / Windows 打开日志文件,Linux 显示可复制的 `journalctl` 命令。

### 断开与重连
- [ ] 断开服务器:共享与转发都停止,横幅显示「已断开」,开关状态保留;重新连接后自动恢复,重启 App 后仍保持断开。
- [ ] 把 VPS 网络断开(或填错端口模拟):服务器显示「无法连接」与倒计时,设备列表变灰但仍可见并标注年龄;恢复后自动重连。
- [ ] 删除服务器:确认弹窗默认焦点在「取消」;删除会卸载这台服务器的共享服务(system 模式会要求授权)。

### 登录时启动
- [ ] 打开「登录时启动 Tunnel」,注销再登录后 App 自动启动(留在托盘);关闭后不再启动。
