<p align="center">
  <img src="assets/icon.svg" width="96" alt="tunnel logo">
</p>

<h1 align="center">tunnel</h1>

<p align="center">
  <a href="https://github.com/pangxincheng/tunnel/actions/workflows/release.yml"><img src="https://img.shields.io/github/actions/workflow/status/pangxincheng/tunnel/release.yml?style=flat-square" alt="release"></a>
  <a href="https://github.com/pangxincheng/tunnel/releases"><img src="https://img.shields.io/github/v/release/pangxincheng/tunnel?filter=v*&style=flat-square" alt="release version"></a>
  <img src="https://img.shields.io/badge/license-MIT-green?style=flat-square" alt="MIT License">
</p>

按名字访问你的设备：公网 VPS 上**只需一个 sshd**，不需要部署任何自研服务端。内网的电脑把自己的 SSH「共享」到 VPS 上，其他设备按名字（如 `nas`）连过去。

本仓库包含：

| 目录 | 内容 |
|---|---|
| [`cli/`](cli/) | `tunnel` 命令行与纯 Go 库（`github.com/pangxincheng/tunnel/cli/tunnel`）：按名字转发、断线自愈、主机公钥钉住、注册为系统服务。 |
| [`desktop/`](desktop/) | 桌面 App（Windows / macOS / Linux）：管理服务器、一键共享这台电脑、给其他设备开关转发、复制连接命令。安装包已附带 `tunnel` 命令行。 |
| [`docs/`](docs/) | 设计文档、实施计划与界面原型。 |

## 下载

命令行和桌面 App 同步发布，版本号相同。在 [Releases](https://github.com/pangxincheng/tunnel/releases) 页面的同一个版本里：

- **桌面 App**：按系统下载 `tunnel-app_*` 开头的 `.dmg`、Windows 安装程序、`.deb` 或 `.rpm`。
- **命令行**：按系统下载 `tunnel_<版本>_<os>_<arch>` 压缩包，或用 `go install github.com/pangxincheng/tunnel/cli/cmd/tunnel@latest` 安装。用法见 [`cli/README.md`](cli/README.md)。

## 开发

`cli/` 与 `desktop/` 是两个独立的 Go 模块；`desktop/` 通过 `replace … => ../cli` 直接使用同一提交里的命令行与库，无需同步版本。各自的构建与测试见 [`cli/README.md`](cli/README.md) 与 [`desktop/README.md`](desktop/README.md)，协作约定（CHANGELOG、tag）见 [`AGENTS.md`](AGENTS.md)。

## License

MIT（见 [`LICENSE`](LICENSE)）
