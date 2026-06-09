#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="$(tr -d '[:space:]' < "$ROOT_DIR/VERSION")"
PROMPT_PACK_VERSION="2026.06.09.1"

if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$ ]]; then
  echo "Invalid version in VERSION: $VERSION" >&2
  exit 1
fi

if ! grep -Eq "^## v${VERSION//./\\.} - [0-9]{4}-[0-9]{2}-[0-9]{2}$" "$ROOT_DIR/CHANGELOG.md"; then
  echo "CHANGELOG.md is missing a release entry for v$VERSION." >&2
  exit 1
fi

if ! grep -Fq "v$VERSION" "$ROOT_DIR/site/changelog.html"; then
  echo "site/changelog.html is missing an internal R2 site entry for v$VERSION." >&2
  exit 1
fi

if ! grep -Fq "$PROMPT_PACK_VERSION" "$ROOT_DIR/site/changelog.html"; then
  echo "site/changelog.html is missing Prompt Pack version $PROMPT_PACK_VERSION." >&2
  exit 1
fi

if [[ ! -x "$ROOT_DIR/scripts/sync-feishu-agent-docs.sh" ]]; then
  echo "scripts/sync-feishu-agent-docs.sh is missing or not executable." >&2
  exit 1
fi

if [[ ! -x "$ROOT_DIR/scripts/deploy-feishu-pages-local.sh" ]]; then
  echo "scripts/deploy-feishu-pages-local.sh is missing or not executable." >&2
  exit 1
fi

for expected in \
  "docs/feishu-agent-features.md:FggndYCZaor2FyxF8hFcs1imnVc" \
  "docs/feishu-agent-pitch.md:Mz3ldFZKvooIkdx6z4hcwn9Mnjb" \
  "docs/feishu-agent-user-guide.md:BwacdC9evoNa1txuGUMcFVChnHd"; do
  doc_path="${expected%%:*}"
  token="${expected##*:}"
  if ! grep -Fq "https://www.feishu.cn/docx/$token" "$ROOT_DIR/$doc_path"; then
    echo "$doc_path is missing its Feishu cloud source link." >&2
    exit 1
  fi
  if ! grep -Fq "$token" "$ROOT_DIR/scripts/sync-feishu-agent-docs.sh"; then
    echo "scripts/sync-feishu-agent-docs.sh is missing token $token." >&2
    exit 1
  fi
done

python3 - "$ROOT_DIR" "$PROMPT_PACK_VERSION" <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
prompt_pack_version = sys.argv[2]
source = (root / "internal/feishu/prompt_pack.go").read_text(encoding="utf-8")
match = re.search(r"var promptRuleOrder = \[\]string\{(?P<body>.*?)\}", source, re.S)
if not match:
    raise SystemExit("promptRuleOrder not found in internal/feishu/prompt_pack.go")
order = re.findall(r'"([^"]+)"', match.group("body"))
missing = [name for name in order if not (root / "internal/feishu/prompt_rules" / f"{name}.md").is_file()]
if missing:
    raise SystemExit("Missing prompt rule files: " + ", ".join(missing))
workflow = (root / ".github/workflows/feishu-bridge-artifacts.yml").read_text(encoding="utf-8")
required = [
    "promptRuleOrder not found",
    "dist/prompt-pack/modules",
    f'"version": "{prompt_pack_version}"',
    '"minAppVersion": os.environ["VERSION"]',
    "site/${rel}",
    "updates/feishu/stable/manifest.json",
    "prompt-pack/feishu/stable/manifest.json",
    "prompt-pack/feishu/stable/modules/",
    "wrangler@latest pages deploy site",
    "Cloudflare Pages deploy failed. R2 artifacts and manifests were already published",
    "CLOUDFLARE_API_TOKEN",
]
missing_workflow = [item for item in required if item not in workflow]
if missing_workflow:
    raise SystemExit("Feishu R2 workflow is missing expected release wiring: " + ", ".join(missing_workflow))
local_deploy = (root / "scripts/deploy-feishu-pages-local.sh").read_text(encoding="utf-8")
for item in [
    "wrangler@latest pages deploy site",
    "--project-name lingma-feishu-agent",
    "--branch stable",
]:
    if item not in local_deploy:
        raise SystemExit("Local Pages deploy fallback is missing: " + item)
PY

echo "Feishu release site/process check passed for v$VERSION."
