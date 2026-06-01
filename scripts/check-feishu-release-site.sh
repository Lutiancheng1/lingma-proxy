#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="$(tr -d '[:space:]' < "$ROOT_DIR/VERSION")"

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

python3 - "$ROOT_DIR" <<'PY'
import pathlib
import re
import sys

root = pathlib.Path(sys.argv[1])
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
    'version": "2026.',
    '"minAppVersion": os.environ["VERSION"]',
    "site/${rel}",
    "updates/feishu/stable/manifest.json",
    "prompt-pack/feishu/stable/manifest.json",
]
missing_workflow = [item for item in required if item not in workflow]
if missing_workflow:
    raise SystemExit("Feishu R2 workflow is missing expected release wiring: " + ", ".join(missing_workflow))
PY

echo "Feishu release site/process check passed for v$VERSION."
