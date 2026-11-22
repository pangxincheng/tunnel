# CHANGELOG

## 2026-11-22
- Created from `pangxincheng/repo-template` as the cross-platform management app for tunnel (desktop: Wails v3 + React; mobile: React Native/Expo + gomobile).
- `AGENTS.md`: replaced the template's nginx deployment section with a project description — this app is distributed as desktop/mobile packages, not deployed behind nginx.

## 2026-10-24
- `AGENTS.md` deployment contract updated to nginx-configuration v2: services receive `DEPLOY_SERVER` / `DEPLOY_BASE_URL` / `DEPLOY_DIR` or `DEPLOY_PORT` as environment variables (no positional `<server>`), fallback file `.deploy.env[.<env>]` limited to those keys, cancelling exits non-zero.

