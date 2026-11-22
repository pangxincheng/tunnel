# CHANGELOG

## 2026-11-22
- Added phase-1 design `docs/superpowers/specs/2026-11-22-tunnel-app-phase1-design.md`: server-centric desktop app (Wails v3 + React; landscape two-pane window; share-this-computer card with user/system service modes; per-device fixed local ports edited inline; copyable ssh/scp/rsync/sftp command templates with `<USERNAME>`; settings for mode/accent/language), reconnect policy, required tunnel core/CLI changes (`install/status/uninstall/pause/resume/retry`, device info files, machine-derived device_id), mobile deferred to its own plan.
- Added the final interactive desktop prototype `docs/design/desktop-prototype-v12.html`.
- `.gitignore`: ignore `.superpowers/` (local mockup and plan workspaces).
- [pending] tunnel: core/CLI changes listed in design §9 must land in the tunnel repo before the desktop app can use them.
- Created from `pangxincheng/repo-template` as the cross-platform management app for tunnel (desktop: Wails v3 + React; mobile: React Native/Expo + gomobile).
- `AGENTS.md`: replaced the template's nginx deployment section with a project description — this app is distributed as desktop/mobile packages, not deployed behind nginx.

## 2026-10-24
- `AGENTS.md` deployment contract updated to nginx-configuration v2: services receive `DEPLOY_SERVER` / `DEPLOY_BASE_URL` / `DEPLOY_DIR` or `DEPLOY_PORT` as environment variables (no positional `<server>`), fallback file `.deploy.env[.<env>]` limited to those keys, cancelling exits non-zero.

