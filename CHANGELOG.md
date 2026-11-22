# CHANGELOG

## 2026-10-24
- `AGENTS.md` deployment contract updated to nginx-configuration v2: services receive `DEPLOY_SERVER` / `DEPLOY_BASE_URL` / `DEPLOY_DIR` or `DEPLOY_PORT` as environment variables (no positional `<server>`), fallback file `.deploy.env[.<env>]` limited to those keys, cancelling exits non-zero.

