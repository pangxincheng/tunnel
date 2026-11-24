# AGENTS.md

## Project

`tunnel` is name-based SSH forwarding through a VPS that runs only sshd, plus the apps that manage it. One repository (monorepo) holds every part:

- `cli/` — the `tunnel` command line and the pure-Go library `github.com/pangxincheng/tunnel/cli/tunnel` (Go module `github.com/pangxincheng/tunnel/cli`).
- `desktop/` — the desktop app, Wails v3 (Go backend + React/TypeScript frontend) for Windows / macOS / Linux: list machines per VPS, toggle forwards to fixed local ports, share this machine (user mode on all OSes; system service on macOS / Linux). Its `go.mod` uses `replace github.com/pangxincheng/tunnel/cli => ../cli`, so it always builds against the CLI / library on the same commit; there is no version to sync.
- `mobile/` (planned) — React Native (Expo) for Android / iOS: phase 1 shows machine online status only; Go core reached through gomobile, same `replace` approach.
- `docs/` — design specs, implementation plans and UI prototypes for all parts.

A change that touches the CLI contract the apps rely on (`status --json`, `set-name`, install flags, …) and the app code that uses it goes in the **same commit**.

This project is **not** deployed through `nginx-configuration`; it ships as release binaries and app packages.

---

## Releases

The CLI and the desktop app are released **together, with one tag** `vX.Y.Z` (starting at `v1.5.0`; `-beta.N` style suffixes make a pre-release). Pushing it runs `.github/workflows/release.yml`: tests, CLI binaries for 6 platforms, desktop packages (dmg / NSIS / deb / rpm), one GitHub Release with notes from `.github/scripts/release-notes.sh` (the annotated tag message as highlights, then the CHANGELOG lines added in `desktop/` and `cli/` since the previous version). Write the user-facing highlights into the tag: `git tag -a vX.Y.Z -F notes.md`.

The workflow then adds `cli/vX.Y.Z` on the same commit by itself, because Go resolves the nested module `github.com/pangxincheng/tunnel/cli` only from `cli/`-prefixed tags. Do not create `cli/v*` or `desktop/v*` tags by hand. Older tags stay as they are: `v1.0.0`–`v1.3.0` (the pre-monorepo root module), `cli/v1.4.0` and `desktop/v1.0.0-beta.*` (the short-lived separate releases).

---

## Changelog

**ALWAYS REMEMBER: record every change in the `CHANGELOG.md` of the part it belongs to**, in the same change set:

- `cli/CHANGELOG.md` — changes under `cli/`.
- `desktop/CHANGELOG.md` — changes under `desktop/`.
- `CHANGELOG.md` (repository root) — everything else: repository layout, `.github/`, root docs, `AGENTS.md`.

A change spanning several parts gets an entry in each. Rules for every changelog:

- Add the newest entry at the top of the file.
- Use the actual date of the change.
- Describe concrete user-facing or engineering changes; avoid vague entries such as "optimization".
- Deferred cross-part syncs are recorded as pending items (naming the parts still to be synced), so they are not lost.
- Documentation-only and maintenance changes must also be recorded.

---

## Git identity

All commits in this repository must be authored as **`pangxincheng <pangxincheng@foxmail.com>`**. Configure it per repository (do not rely on the global config):

```bash
git config user.name pangxincheng
git config user.email pangxincheng@foxmail.com
```
