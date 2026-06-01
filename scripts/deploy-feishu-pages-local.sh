#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

cd "$ROOT_DIR"

if [[ ! -d site ]]; then
  echo "site directory not found." >&2
  exit 1
fi

npx wrangler@latest pages deploy site \
  --project-name lingma-feishu-agent \
  --branch main

echo "Verify:"
echo "  https://lingma-feishu-agent.pages.dev/changelog"
echo "  https://lingma-feishu-agent.pages.dev/changelog.html"
