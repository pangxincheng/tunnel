#!/usr/bin/env python3
"""Write the release version into the packaging metadata.

Usage (from desktop/):  python3 build/set_version.py 1.2.0
                        python3 build/set_version.py v1.2.0-beta.1

Touches only the version fields of the files below, so the customised build
assets (bundled tunnel CLI, install paths) survive. Do not use
`wails3 task common:update:build-assets` for this: it regenerates the files
from templates and drops those customisations.

Windows' fixed file/product version must be numeric, so a pre-release suffix
("-beta.1") is kept everywhere except there.
"""

import json
import re
import sys
from pathlib import Path

BUILD = Path(__file__).resolve().parent
SEMVER = re.compile(r"^v?(\d+\.\d+\.\d+)(-[0-9A-Za-z.-]+)?$")


def sub_once(path: Path, pattern: str, repl: str, count: int = 1) -> None:
    text = path.read_text(encoding="utf-8")
    new, n = re.subn(pattern, repl, text, flags=re.MULTILINE)
    if n != count:
        sys.exit(f"{path.relative_to(BUILD.parent)}: expected {count} match(es) for {pattern!r}, found {n}")
    path.write_text(new, encoding="utf-8")


def main() -> None:
    if len(sys.argv) != 2 or not SEMVER.match(sys.argv[1]):
        sys.exit("usage: set_version.py <X.Y.Z[-pre]> (a leading v is allowed)")
    m = SEMVER.match(sys.argv[1])
    numeric = m.group(1)
    full = numeric + (m.group(2) or "")

    plist = BUILD / "darwin" / "Info.plist"
    for key in ("CFBundleVersion", "CFBundleShortVersionString"):
        sub_once(plist, rf"(<key>{key}</key>\s*<string>)[^<]*(</string>)", rf"\g<1>{full}\g<2>")

    info = BUILD / "windows" / "info.json"
    data = json.loads(info.read_text(encoding="utf-8"))
    data["fixed"]["file_version"] = numeric
    data["fixed"]["product_version"] = numeric
    for lang in data["info"].values():
        lang["FileVersion"] = numeric
        lang["ProductVersion"] = full
    info.write_text(json.dumps(data, ensure_ascii=False, indent="\t") + "\n", encoding="utf-8")

    sub_once(BUILD / "windows" / "nsis" / "wails_tools.nsh",
             r'(!define INFO_PRODUCTVERSION ")[^"]*(")', rf"\g<1>{numeric}\g<2>")
    sub_once(BUILD / "linux" / "nfpm" / "nfpm.yaml", r'^(version: ")[^"]*(")', rf"\g<1>{full}\g<2>")
    sub_once(BUILD / "config.yml", r'^(  version: ")[^"]*(")', rf"\g<1>{full}\g<2>")

    print(full)


if __name__ == "__main__":
    main()
