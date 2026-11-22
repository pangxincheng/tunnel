# AGENTS.md

## Changelog

**ALWAYS REMEMBER: record every change in the directory's `CHANGELOG.md`.** Every code, configuration, asset, or documentation change must update `CHANGELOG.md` in the same change set.

- Add the newest entry at the top of `CHANGELOG.md`.
- Use the actual date of the change.
- Describe concrete user-facing or engineering changes; avoid vague entries such as "optimization".
- Deferred cross-end syncs are also recorded there as pending items (with the ends still to be synced), so they are not lost.
- Documentation-only and maintenance changes must also be recorded.

---

## Deployment

This project is deployed through the `nginx-configuration` central deploy tooling. **Read `../nginx-configuration/SKILL.md` and follow it exactly** (the path is relative to this repository's checked-out location inside the `website-pxc` umbrella). The essentials:

- Register the service in `nginx-configuration/registry.yaml` (the single source of truth — never bypass it).
- Provide a `deploy.py` in this repository implementing the contract: `python3 deploy.py` to deploy, `python3 deploy.py --undeploy` to undeploy. There is no positional server argument: `DEPLOY_SERVER` / `DEPLOY_BASE_URL` and `DEPLOY_DIR` (static) or `DEPLOY_PORT` (proxy) arrive via environment variables (see `nginx-configuration/SKILL.md` §三; fallback file `.deploy.env[.<env>]` may only contain these four keys). Exit code 0 on success / non-zero on failure (including when the operator cancels).
- This repository's `deploy.py` builds / uploads / starts the service only — it must **never** touch nginx (`nginx -t`, `systemctl reload nginx`, uploading nginx config, etc.); nginx is managed exclusively by `nginx-configuration/deploy.py`.

---

## Git identity

All commits in this repository must be authored as **`pangxincheng <pangxincheng@foxmail.com>`**. Configure it per repository (do not rely on the global config):

```bash
git config user.name pangxincheng
git config user.email pangxincheng@foxmail.com
```
