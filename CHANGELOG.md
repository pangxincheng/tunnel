# Changelog

## 2026-08-14

- 新增 `docs/superpowers/specs/2026-08-14-tunnel-design.md`:仓库范围重新定为单一通用 SSH 转发工具(移除 ra/rad 终端复用);设计定稿 —— 单 binary 双子命令(server 反向转发 VPS loopback:PORT → 内网:22,client 正向转发经 direct-tcpip 接入)、单映射、TOML 配置 + flag 覆盖、主机公钥钉住、keepalive + 指数退避重连、VPS 侧零自研服务端。
