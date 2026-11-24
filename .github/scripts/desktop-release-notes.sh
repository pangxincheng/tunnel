#!/usr/bin/env bash
# 生成桌面端 GitHub Release 的说明正文(Markdown,输出到 stdout)。
#
# 用法: .github/scripts/desktop-release-notes.sh desktop/vX.Y.Z
#
# 正文由三部分组成,GitHub 再在末尾追加自动生成的部分(PR 列表与
# 「Full Changelog: 上一版...本版」对比链接,见 release-desktop.yml 的 --generate-notes):
#   1. 亮点:附注 tag 的说明(git tag -a desktop/vX.Y.Z -F notes.md)。没写就省略。
#   2. 下载与安装:固定的安装指引。
#   3. 详细变更:本 tag 相对上一个 desktop/v* tag 在 desktop/CHANGELOG.md 中新增的行;
#      第一次发布(没有上一个 tag)时取整份 desktop/CHANGELOG.md。写了亮点时折叠显示。
# 只看 desktop/ 前缀的 tag,命令行的 cli/v* 与旧的 v* tag 都不算「上一版」。
set -euo pipefail

tag="${1:?用法: desktop-release-notes.sh desktop/vX.Y.Z}"
changelog="desktop/CHANGELOG.md"
git rev-parse -q --verify "refs/tags/$tag" >/dev/null || { echo "找不到 tag $tag" >&2; exit 1; }

# 上一个桌面端版本 tag(不含本 tag)。
prev="$(git describe --tags --abbrev=0 --match 'desktop/v[0-9]*' "$tag^" 2>/dev/null || true)"

# 1. 附注 tag 的说明;轻量 tag 没有说明。去掉签名块与首尾空行。
highlights=""
if [ "$(git cat-file -t "$tag")" = "tag" ]; then
  highlights="$(git tag -l --format='%(contents)' "$tag" \
    | sed '/^-----BEGIN PGP SIGNATURE-----/,$d; /^-----BEGIN SSH SIGNATURE-----/,$d' \
    | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}')"
fi

# 3. CHANGELOG 新增的行。
if [ -n "$prev" ]; then
  changes="$(git diff --unified=0 "$prev" "$tag" -- "$changelog" \
    | grep '^+' | grep -v '^+++' | sed 's/^+//' || true)"
else
  changes="$(git show "$tag:$changelog" | sed '1{/^# /d;}')"
fi
# 去掉开头的空行;日期标题降一级,挂在「详细变更」之下。
changes="$(printf '%s\n' "$changes" | sed '/./,$!d' | sed 's/^## /### /')"

if [ -n "$highlights" ]; then
  printf '%s\n\n' "$highlights"
fi

cat <<'EOF'
## 下载与安装

| 系统 | 文件 |
|---|---|
| macOS（Apple 芯片） | `*_macos_arm64.dmg` |
| macOS（Intel） | `*_macos_amd64.dmg` |
| Windows 10 / 11 | `*_windows_amd64_installer.exe` |
| Ubuntu 24.04+ / Debian 13+ | `*_linux_amd64.deb` |
| Fedora / RHEL 系 | `*_linux_amd64.rpm` |

安装包已附带 `tunnel` 命令行，不需要另外安装。`SHA256SUMS` 可用于校验下载的文件（`shasum -a 256 -c SHA256SUMS`）。

macOS 版本未经 Apple 公证，第一次打开时如果提示「无法验证开发者」，请在「访达」里右键点 App →「打开」，或在「系统设置 → 隐私与安全性」中点「仍要打开」。
EOF

if [ -n "$changes" ]; then
  if [ -n "$prev" ]; then
    title="详细变更（自 ${prev#desktop/} 起，摘自 CHANGELOG）"
  else
    title="详细变更（摘自 CHANGELOG）"
  fi
  if [ -n "$highlights" ]; then
    printf '\n<details>\n<summary>%s</summary>\n\n%s\n\n</details>\n' "$title" "$changes"
  else
    printf '\n## %s\n\n%s\n' "$title" "$changes"
  fi
fi
