#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v lark-cli >/dev/null 2>&1; then
  echo "lark-cli is required to sync Feishu Agent docs." >&2
  exit 1
fi

sync_doc() {
  local token="$1"
  local path="$2"
  local title="$3"
  local tmp
  local json_tmp
  tmp="$(mktemp)"
  json_tmp="$(mktemp)"
  trap 'rm -f "$tmp" "$json_tmp"' RETURN

  lark-cli docs +fetch --api-version v2 --doc-format markdown --doc "$token" --as user > "$json_tmp"
  python3 - "$token" "$title" "$json_tmp" "$ROOT_DIR/$path" "$tmp" <<'PY'
import json
import pathlib
import re
import sys

token, title, input_path, current_path, output_path = sys.argv[1:6]
payload = json.loads(pathlib.Path(input_path).read_text(encoding="utf-8"))
document = ((payload.get("data") or {}).get("document") or {})
markdown = document.get("content")
if not markdown:
    raise SystemExit(f"missing markdown for {token}")

image_pattern = r"!\[[^\]]*\]\("
image_count = len(re.findall(image_pattern, markdown))
current = pathlib.Path(current_path)
current_image_count = 0
if current.exists():
    current_image_count = len(re.findall(image_pattern, current.read_text(encoding="utf-8")))
if current_image_count and image_count < current_image_count:
    raise SystemExit(
        f"refusing to overwrite {current.name}: fetched {image_count} image links, "
        f"local file has {current_image_count}. Check Feishu export/media handling first."
    )
if image_count:
    print(
        f"warning: fetched {image_count} image links for {token}; do not overwrite the cloud doc from local markdown unless image handling is verified",
        file=sys.stderr,
    )

content = f"{markdown.rstrip()}\n"
pathlib.Path(output_path).write_text(content, encoding="utf-8")
PY

  mv "$tmp" "$ROOT_DIR/$path"
  echo "synced $path from https://www.feishu.cn/docx/$token"
  rm -f "$json_tmp"
  trap - RETURN
}

sync_doc "FggndYCZaor2FyxF8hFcs1imnVc" "docs/feishu-agent-features.md" "Lingma Feishu Agent — 技术架构与实现详解"
sync_doc "Mz3ldFZKvooIkdx6z4hcwn9Mnjb" "docs/feishu-agent-pitch.md" "基于 Lingma 账号资源的飞书个人专属智能体"
sync_doc "BwacdC9evoNa1txuGUMcFVChnHd" "docs/feishu-agent-user-guide.md" "Lingma Feishu Agent 食用指南"
