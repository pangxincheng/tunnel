# CHANGELOG

仓库层面的变更(目录结构、`.github/`、根目录文档、`AGENTS.md`)。命令行见 `cli/CHANGELOG.md`,桌面端见 `desktop/CHANGELOG.md`。

## 2026-11-24
- 修复桌面端发布工作流:Linux 上 `wails3` 命令行本身链接 GTK4 / WebKitGTK,`windows_amd64` 与 `linux_amd64` 两个 ubuntu 任务改为在安装 wails3 之前先装 `libgtk-4-dev` / `libwebkitgtk-6.0-dev` 等(`if: runner.os == 'Linux'`),此前两者都在「安装 wails3」一步链接失败。首次发布的 `desktop/v1.0.0-beta.1` 因此未生成 Release,该 tag 已删除,改发 `desktop/v1.0.0-beta.2`。

## 2026-11-23
- 仓库改为 monorepo:命令行移入 `cli/`(Go 模块 `github.com/pangxincheng/tunnel/cli`),并入原 `tunnel-app` 仓库的桌面端 `desktop/`(Go 模块 `github.com/pangxincheng/tunnel/desktop`)、设计文档与原型(`docs/`),保留 tunnel-app 的完整提交历史;tunnel-app 仓库随后删除。桌面端经 `replace … => ../cli` 直接依赖同一提交的命令行与库。
- 发布改为按目录前缀的 tag:`cli/vX.Y.Z` 触发 `release-cli.yml`(原 `release.yml`),`desktop/vX.Y.Z` 触发 `release-desktop.yml`;旧的根模块 tag `v1.0.0`–`v1.3.0` 保留不动,不再新建不带前缀的 `v*` tag。桌面端版本说明脚本改名为 `.github/scripts/desktop-release-notes.sh`,只在 `desktop/v*` tag 之间比较 `desktop/CHANGELOG.md`,GitHub 自动生成部分的起点限定为上一个 `desktop/v*` tag。
- 新增根目录 `README.md`(项目总览、下载说明与两个子目录的入口)与本 CHANGELOG;原 README 移为 `cli/README.md`。`AGENTS.md` 合并为 monorepo 说明(各部分的 CHANGELOG 与 tag 规则、跨 cli / desktop 的改动放进同一提交)。`.gitignore` 合并桌面端规则。
