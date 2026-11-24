#!/usr/bin/env bash
# 从 icon-src/ 的 SVG 重新生成全部图标(需要 rsvg-convert:brew install librsvg;Assets.car 需在 macOS 上生成)。
# 不要依赖 `wails3 task common:generate:icons`:它只看 appicon.png / appicon.icon 的时间戳,
# 察觉不到 appicon.icon/Assets/ 里的图层变化,会跳过 Assets.car。
set -euo pipefail
cd "$(dirname "$0")"
rsvg-convert -w 1024 -h 1024 icon-src/appicon.svg -o appicon.png
rsvg-convert -w 44 -h 44 icon-src/tray-template.svg -o tray-template.png
rsvg-convert -w 64 -h 64 ../../assets/icon.svg -o tray.png
cp icon-src/glyph.svg appicon.icon/Assets/tunnel_glyph.svg
wails3 generate icons -input appicon.png -macfilename darwin/icons.icns -windowsfilename windows/icon.ico \
  -iconcomposerinput appicon.icon -macassetdir darwin
