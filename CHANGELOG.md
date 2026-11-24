# CHANGELOG

仓库层面的变更(目录结构、`.github/`、根目录文档、`AGENTS.md`)。命令行见 `cli/CHANGELOG.md`,桌面端见 `desktop/CHANGELOG.md`。

## 2026-11-24
- `release.yml`:macOS 打包产物改名后从 `bin/Tunnel.dmg` 取 dmg(发布文件名仍为 `tunnel-app_<tag>_macos_<arch>.dmg`)。
- `release.yml`:三个 ubuntu 任务(测试、Windows、Linux 打包)共用 `actions/cache` 缓存的 apt 软件包(`~/apt-cache`,经 `-o Dir::Cache::Archives` 下载到该目录,安装后改回 runner 所有并删掉锁文件以便保存);每个任务加 60 分钟超时。起因:`desktop/v1.0.0-beta.3` 的运行中 Ubuntu 软件源只有约 57 kB/s,GTK4 / WebKitGTK 的 80.6 MB 下载用了 23 分 38 秒,三个任务各下载一遍。该次运行最终全部成功(macOS dmg ×2、Windows 安装程序、deb、rpm 与 SHA256SUMS 已发布到预发布版本)。
- 命令行与桌面端改为同步发布、共用一个 tag `vX.Y.Z`(自 `v1.5.0` 起):新的 `.github/workflows/release.yml` 取代 `release-cli.yml` 与 `release-desktop.yml`,依次测试(`cli/`、前端、桌面端)、并行构建命令行 6 个平台压缩包与桌面端 dmg / NSIS / deb / rpm,发布到同一个 Release(标题「Tunnel vX.Y.Z」,另附 `SHA256SUMS`),随后自动在同一提交补 `cli/vX.Y.Z`(Go 子目录模块只认带前缀的 tag,`go install …/cli/cmd/tunnel@latest` 由此取到新版本;用 GITHUB_TOKEN 推送不会再触发工作流)。版本说明脚本改为通用的 `.github/scripts/release-notes.sh`(取代 `desktop-release-notes.sh`):附注 tag 说明 + 桌面 App 与命令行的下载对照表 + 自上一版以来 `desktop/` 与 `cli/` CHANGELOG 新增的行(分两节;正式版跳过中间的预发布与上一个正式版比较;上一版尚无 `cli/CHANGELOG.md` 时比较当时根目录的 CHANGELOG),`--prev` 输出上一版供 `--notes-start-tag` 使用。`AGENTS.md` 的发布约定、根 README 的徽章与下载说明同步;旧 tag(`v1.0.0`–`v1.3.0`、`cli/v1.4.0`、`desktop/v1.0.0-beta.*`)保留。
- 修复桌面端发布工作流:Linux 上 `wails3` 命令行本身链接 GTK4 / WebKitGTK,`windows_amd64` 与 `linux_amd64` 两个 ubuntu 任务改为在安装 wails3 之前先装 `libgtk-4-dev` / `libwebkitgtk-6.0-dev` 等(`if: runner.os == 'Linux'`),此前两者都在「安装 wails3」一步链接失败。首次发布的 `desktop/v1.0.0-beta.1` 因此未生成 Release,该 tag 已删除,改发 `desktop/v1.0.0-beta.2`。

## 2026-11-23
- 仓库改为 monorepo:命令行移入 `cli/`(Go 模块 `github.com/pangxincheng/tunnel/cli`),并入原 `tunnel-app` 仓库的桌面端 `desktop/`(Go 模块 `github.com/pangxincheng/tunnel/desktop`)、设计文档与原型(`docs/`),保留 tunnel-app 的完整提交历史;tunnel-app 仓库随后删除。桌面端经 `replace … => ../cli` 直接依赖同一提交的命令行与库。
- 发布改为按目录前缀的 tag:`cli/vX.Y.Z` 触发 `release-cli.yml`(原 `release.yml`),`desktop/vX.Y.Z` 触发 `release-desktop.yml`;旧的根模块 tag `v1.0.0`–`v1.3.0` 保留不动,不再新建不带前缀的 `v*` tag。桌面端版本说明脚本改名为 `.github/scripts/desktop-release-notes.sh`,只在 `desktop/v*` tag 之间比较 `desktop/CHANGELOG.md`,GitHub 自动生成部分的起点限定为上一个 `desktop/v*` tag。
- 新增根目录 `README.md`(项目总览、下载说明与两个子目录的入口)与本 CHANGELOG;原 README 移为 `cli/README.md`。`AGENTS.md` 合并为 monorepo 说明(各部分的 CHANGELOG 与 tag 规则、跨 cli / desktop 的改动放进同一提交)。`.gitignore` 合并桌面端规则。
