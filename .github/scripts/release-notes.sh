#!/usr/bin/env bash
# 生成统一发布(命令行 + 桌面端)的 GitHub Release 说明正文(Markdown,输出到 stdout)。
#
# 用法: .github/scripts/release-notes.sh vX.Y.Z          输出说明正文
#       .github/scripts/release-notes.sh --prev vX.Y.Z   只输出「上一版」tag(没有则输出空)
#
# 上一版:正式版与上一个正式版比较(跳过中间的预发布);预发布与紧邻的上一个 v* tag 比较。
# 只看不带前缀的 v* tag(cli/v*、desktop/v* 是旧的分开发布,不算)。
#
# 正文依次为(GitHub 再在末尾追加自动生成的 PR 列表与对比链接,见 release.yml):
#   1. 亮点:附注 tag 的说明(git tag -a vX.Y.Z -F notes.md)。没写就省略。
#   2. 下载与安装:桌面 App 与命令行的下载对照表。
#   3. 详细变更:自上一版以来 desktop/CHANGELOG.md、cli/CHANGELOG.md 新增的行,分「桌面端」「命令行」;
#      上一版还没有该文件时,命令行退回比较当时根目录的 CHANGELOG.md(monorepo 之前命令行的变更记录),
#      桌面端则取整份。写了亮点时折叠显示。
set -euo pipefail

mode=notes
if [ "${1:-}" = "--prev" ]; then mode=prev; shift; fi
tag="${1:?用法: release-notes.sh [--prev] vX.Y.Z}"
git rev-parse -q --verify "refs/tags/$tag" >/dev/null || { echo "找不到 tag $tag" >&2; exit 1; }

prev=""
case "$tag" in *-*) stable_only=0 ;; *) stable_only=1 ;; esac
while read -r t; do
  [ -z "$t" ] || [ "$t" = "$tag" ] && continue
  if [ "$stable_only" = 1 ]; then case "$t" in *-*) continue ;; esac; fi
  prev="$t"
  break
done < <(git tag --merged "$tag^" --list 'v[0-9]*' --sort=-v:refname 2>/dev/null || true)

if [ "$mode" = prev ]; then
  printf '%s\n' "$prev"
  exit 0
fi

# 某个 CHANGELOG 自上一版以来新增的行;日期标题降为四级,挂在小节标题之下。
changes_for() {
  local path="$1" oldpath="${2:-}" base="" out
  if [ -n "$prev" ]; then
    if git cat-file -e "$prev:$path" 2>/dev/null; then
      base="$prev:$path"
    elif [ -n "$oldpath" ] && git cat-file -e "$prev:$oldpath" 2>/dev/null; then
      base="$prev:$oldpath"
    fi
  fi
  if [ -n "$base" ]; then
    out="$(git diff --unified=0 "$base" "$tag:$path" | grep '^+' | grep -v '^+++' | sed 's/^+//' || true)"
  else
    out="$(git show "$tag:$path" | sed '1{/^# /d;}')"
  fi
  printf '%s\n' "$out" | sed '/./,$!d' | sed 's/^## /#### /'
}

# 附注 tag 的说明;轻量 tag 没有说明。去掉签名块与首尾空行。
highlights=""
if [ "$(git cat-file -t "$tag")" = "tag" ]; then
  highlights="$(git tag -l --format='%(contents)' "$tag" \
    | sed '/^-----BEGIN PGP SIGNATURE-----/,$d; /^-----BEGIN SSH SIGNATURE-----/,$d' \
    | sed -e :a -e '/^\n*$/{$d;N;ba' -e '}')"
fi

desktop="$(changes_for desktop/CHANGELOG.md)"
cli="$(changes_for cli/CHANGELOG.md CHANGELOG.md)"

if [ -n "$highlights" ]; then
  printf '%s\n\n' "$highlights"
fi

cat <<'EOF'
## 下载与安装

**桌面 App**（已附带 `tunnel` 命令行，不需要另外安装）

| 系统 | 文件 |
|---|---|
| macOS（Apple 芯片） | `tunnel-app_*_macos_arm64.dmg` |
| macOS（Intel） | `tunnel-app_*_macos_amd64.dmg` |
| Windows 10 / 11 | `tunnel-app_*_windows_amd64_installer.exe` |
| Ubuntu 24.04+ / Debian 13+ | `tunnel-app_*_linux_amd64.deb` |
| Fedora / RHEL 系 | `tunnel-app_*_linux_amd64.rpm` |

macOS 版本未经 Apple 公证，第一次打开时如果提示「无法验证开发者」，请在「访达」里右键点 App →「打开」，或在「系统设置 → 隐私与安全性」中点「仍要打开」。

**命令行**：`tunnel_*_<系统>_<架构>`（Linux / macOS 为 `.tar.gz`，Windows 为 `.zip`；系统取 `linux` / `darwin` / `windows`，架构取 `amd64` / `arm64`）。也可以用 `go install github.com/pangxincheng/tunnel/cli/cmd/tunnel@latest` 安装。

`SHA256SUMS` 可用于校验下载的文件（`shasum -a 256 -c SHA256SUMS`）。
EOF

body=""
if [ -n "$desktop" ]; then body+=$'### 桌面端\n\n'"$desktop"$'\n\n'; fi
if [ -n "$cli" ]; then body+=$'### 命令行\n\n'"$cli"$'\n'; fi
if [ -n "$body" ]; then
  if [ -n "$prev" ]; then
    title="详细变更（自 $prev 起，摘自 CHANGELOG）"
  else
    title="详细变更（摘自 CHANGELOG）"
  fi
  if [ -n "$highlights" ]; then
    printf '\n<details>\n<summary>%s</summary>\n\n%s\n</details>\n' "$title" "$body"
  else
    printf '\n## %s\n\n%s' "$title" "$body"
  fi
fi
