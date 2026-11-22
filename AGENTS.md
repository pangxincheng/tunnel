# AGENTS.md

## Changelog

**ALWAYS REMEMBER: record every change in the directory's `CHANGELOG.md`.** Every code, configuration, asset, or documentation change must update `CHANGELOG.md` in the same change set.

- Add the newest entry at the top of `CHANGELOG.md`.
- Use the actual date of the change.
- Describe concrete user-facing or engineering changes; avoid vague entries such as "optimization".
- Deferred cross-end syncs are also recorded there as pending items (with the ends still to be synced), so they are not lost.
- Documentation-only and maintenance changes must also be recorded.

---

## Project

`tunnel-app` is the cross-platform management app for [tunnel](https://github.com/pangxincheng/tunnel) (name-based SSH forwarding through a VPS that runs only sshd). One repository holds both ends:

- `desktop/` — Wails v3 (Go backend + React/TypeScript frontend) for Windows / macOS / Linux: list machines per VPS, toggle forwards to fixed local ports, share this machine (user mode on all OSes; system service on macOS / Linux).
- `mobile/` — React Native (Expo) for Android / iOS: phase 1 shows machine online status only; Go core reached through gomobile.
- Go core logic is reused from `github.com/pangxincheng/tunnel`.

This project is **not** deployed through `nginx-configuration`; it ships as desktop / mobile app packages.

---

## Git identity

All commits in this repository must be authored as **`pangxincheng <pangxincheng@foxmail.com>`**. Configure it per repository (do not rely on the global config):

```bash
git config user.name pangxincheng
git config user.email pangxincheng@foxmail.com
```
