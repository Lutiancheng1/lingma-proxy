# Feishu Branch Internal Release Process

This checklist is mandatory for `feat/feishu-bridge-go` releases. The Feishu branch has two distribution paths:

- Internal company channel: GitHub Actions publishes desktop artifacts, OTA manifest, Prompt Pack, and the static download site to Cloudflare R2 / Pages.
- External open-source channel: normal GitHub Release assets are handled from `main`; do not publish Feishu Agent GitHub Release assets from this branch.

## Release Scope

Use this process whenever Feishu Agent behavior changes, including:

- Agent tools, tool schema, prompt routing, Prompt Pack modules, or system prompt rules.
- Feishu message cards, streaming, thinking panels, scheduled tasks, reminders, AI HOT / AI Radar, local file access, permissions, or MCP / Skill routing.
- Desktop UI that affects Feishu Agent settings, logs, update status, or onboarding.
- Internal download site, user guide, R2 OTA manifest, or Pages content.

## Required Steps

1. Sync cloud Feishu docs before editing local doc copies.
   - Run `./scripts/sync-feishu-agent-docs.sh` before touching any of these files:
     - `docs/feishu-agent-features.md`
     - `docs/feishu-agent-pitch.md`
     - `docs/feishu-agent-user-guide.md`
   - Treat the Feishu docx documents as the upstream source. The sync must use `lark-cli docs +fetch --api-version v2 --doc-format markdown --doc <token> --as user` and preserve exported image / attachment URLs as-is.
   - Do not use v1 `docs +fetch` `data.markdown` or `drive +export markdown` as the sync source for image-bearing docs; those paths can omit media blocks or return empty grids.
   - If the sync script reports fewer image links from Feishu than the existing local file, stop. That means the current CLI export path did not expose media blocks even if the Feishu page still renders images.
   - Keep `internal-api-drive-stream.feishu.cn` URLs exactly as returned unless the user explicitly asks to localize media.
   - Full cloud writes must use `lark-cli docs +update --api-version v2 --doc <token> --command overwrite --doc-format markdown --content @<relative-local-file> --as user`.
   - Do not run `docs +update --api-version v1 --mode overwrite --markdown @file` or `--markdown @file` for image-bearing docs; it can return `IMAGE_DOWNLOAD_FAILED` and remove image blocks.
   - After cloud write, fetch the same doc again with v2 Markdown and verify `warnings: []`, image count has not dropped, and the intended wording/version fixes are present. Feishu may regenerate image authcode URLs, so compare image count and content, not exact image URL strings.
   - Before committing, verify the local file and corresponding cloud doc match in substance and that image blocks were not reduced.

2. Update the version.
   - Edit `VERSION`.
   - Run `./scripts/sync-version.sh`.
   - Verify `desktop/wails.json`, `internal/version/version.go`, `README.md`, and `README.zh-CN.md` were synced.

3. Update release notes.
   - Add `## vX.Y.Z - YYYY-MM-DD` to `CHANGELOG.md`.
   - Keep the first English and Chinese bullets clear enough for the OTA update panel.
   - Keep the note that Feishu branch artifacts are internal/R2 channel only.

4. Update the internal R2 / Pages site.
   - Add the same release to `site/changelog.html`.
   - Update `site/index.html` or `site/tutorial.html` if user-facing capabilities, setup, Prompt Pack behavior, or troubleshooting changed.
   - Do not rely on `CHANGELOG.md` alone; the internal download site is a separate published artifact.

5. Update the R2 workflow.
   - Check `.github/workflows/feishu-bridge-artifacts.yml`.
   - OTA manifest `releaseNotes` must describe the current version.
   - Prompt Pack generation must include every module from `internal/feishu/prompt_pack.go` `promptRuleOrder`.
   - Prompt Pack `version` should be advanced for prompt changes, using a date-style value such as `2026.06.01.1`.
   - Prompt Pack `minAppVersion` should match the app version when new prompt rules depend on new app tools.

6. Verify Prompt Pack compatibility.
   - New prompt rule files must be added under `internal/feishu/prompt_rules/`.
   - App-side Prompt Pack loading must tolerate older remote packs by falling back to embedded modules for newly introduced rule files.
   - Add or update tests when adding prompt modules.

7. Run local verification.
   - `./scripts/check-version-sync.sh`
   - `./scripts/check-release-notes.sh`
   - `./scripts/check-feishu-release-site.sh`
   - `go test ./internal/feishu`
   - `go test ./...`
   - `go build -o /tmp/lingma-ipc-proxy-feishu-release ./cmd/lingma-ipc-proxy`
   - `git diff --check`

8. Install the local app for manual validation.
   - Run `./scripts/rebuild-local-app.sh`.
   - Test the Feishu Agent behavior that changed.
   - For Prompt Pack changes, click Settings -> `更新 Prompt Pack` and confirm the status has no module error.

9. Commit and push the Feishu branch.
   - Push `feat/feishu-bridge-go`.
   - The `Feishu Agent Artifacts` workflow should run tests, build macOS/Windows desktop artifacts, publish R2 OTA manifests, publish Prompt Pack, upload site files, and deploy Pages.

10. Verify the internal channel after CI completes.
   - Open `https://lingma-feishu-agent.pages.dev/download`.
   - Confirm it shows the new version and release notes from R2 `updates/feishu/stable/manifest.json`.
   - Confirm `https://lingma-feishu-agent.pages.dev/changelog.html` contains the new version.
   - Confirm `https://lingma-feishu-agent.pages.dev/changelog` and `https://lingma-feishu-agent.pages.dev/changelog.html` are both current. If R2 `site/changelog.html` is current but Pages is stale, the Cloudflare Pages deploy failed and the release is not complete.
   - Confirm the R2 Prompt Pack manifest points to the new Prompt Pack version.

11. If GitHub Cloudflare credentials are broken, deploy Pages from local Wrangler auth.
   - Use this only when the R2 artifacts / manifest are already current but `lingma-feishu-agent.pages.dev` is stale because the GitHub Actions Cloudflare token failed.
   - Confirm local Wrangler is authenticated with Pages write permission: `npx wrangler@latest whoami`.
   - Run `./scripts/deploy-feishu-pages-local.sh`.
   - This script deploys `site/` to project `lingma-feishu-agent` on branch `main`. Do not deploy the production site with `--branch stable`; that creates a preview deployment such as `stable.lingma-feishu-agent.pages.dev` and does not update `https://lingma-feishu-agent.pages.dev`.
   - Re-run the production verification in step 10 after the local deploy.

## Common Failure Modes

- App shows `Prompt Pack 缺少模块: <name>`:
  - The R2 workflow likely omitted a new prompt module.
  - Fix the workflow to derive module order from `promptRuleOrder`, or add the missing module.
  - App-side fallback should prevent this from blocking startup, but the R2 package still needs to be corrected.

- Internal download page still shows the old version:
  - Check whether the R2 publish job was skipped because secrets were missing.
  - Check `updates/feishu/stable/manifest.json` on R2.
  - Check R2 direct `site/changelog.html`; if it is current while `lingma-feishu-agent.pages.dev/changelog` is stale, Cloudflare Pages did not deploy.
  - Pages deploy must not be treated as a warning-only step. Fix `CLOUDFLARE_API_TOKEN` Pages permissions and rerun the workflow, or use `./scripts/deploy-feishu-pages-local.sh` as a documented fallback when the local Cloudflare account has the correct Pages permission.

- Version files are inconsistent:
  - Run `./scripts/sync-version.sh`.
  - Then run `./scripts/check-version-sync.sh`.

- Feature works locally but not in the installed app:
  - Rebuild with `./scripts/rebuild-local-app.sh`.
  - Make sure the running app is `/Applications/Lingma Proxy.app`.
  - Check Settings update status and Feishu Agent logs.
