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
   - Treat the Feishu docx documents as the upstream source. The sync must pull the latest cloud content first and preserve exported image / attachment URLs as-is.
   - If the sync script reports fewer image links from Feishu than the existing local file, stop. That means the current CLI export path did not expose media blocks even if the Feishu page still renders images.
   - Do not run full-document cloud overwrite from local Markdown when the document contains Feishu internal image URLs. `docs +update --mode overwrite --markdown @file` can fail to re-import those images and silently remove image blocks.
   - For image-bearing docs, update the cloud doc first in Feishu or use narrow text-only updates that do not touch media blocks, then pull the cloud version back down before committing.
   - Before committing, verify the local file and corresponding cloud doc still match in substance and that image blocks were not reduced.

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
   - Confirm the R2 Prompt Pack manifest points to the new Prompt Pack version.

## Common Failure Modes

- App shows `Prompt Pack 缺少模块: <name>`:
  - The R2 workflow likely omitted a new prompt module.
  - Fix the workflow to derive module order from `promptRuleOrder`, or add the missing module.
  - App-side fallback should prevent this from blocking startup, but the R2 package still needs to be corrected.

- Internal download page still shows the old version:
  - Check whether the R2 publish job was skipped because secrets were missing.
  - Check `updates/feishu/stable/manifest.json` on R2.
  - Check Pages deploy logs if `site/` changed but the download manifest did update.

- Version files are inconsistent:
  - Run `./scripts/sync-version.sh`.
  - Then run `./scripts/check-version-sync.sh`.

- Feature works locally but not in the installed app:
  - Rebuild with `./scripts/rebuild-local-app.sh`.
  - Make sure the running app is `/Applications/Lingma Proxy.app`.
  - Check Settings update status and Feishu Agent logs.
