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

  # v1 currently exposes markdown directly. v2 returns structured document content,
  # so keep v1 here until lark-cli provides a markdown-compatible v2 fetch path.
  lark-cli docs +fetch --api-version v1 --doc "$token" --format json > "$json_tmp"
  python3 - "$token" "$title" "$json_tmp" "$tmp" <<'PY'
import json
import pathlib
import sys

token, title, input_path, output_path = sys.argv[1:5]
payload = json.loads(pathlib.Path(input_path).read_text(encoding="utf-8"))
data = payload.get("data") or {}
markdown = data.get("markdown")
remote_title = data.get("title") or title
if not markdown:
    raise SystemExit(f"missing markdown for {token}")

cloud_note = (
    f"> 本文档对应飞书云盘中的云端源文档（链接：https://www.feishu.cn/docx/{token}"
    f"，Token: {token}）。修改时请确保与云端同步，勿直接在此处进行非同步性修改。"
)
content = f"# {remote_title}\n\n{cloud_note}\n\n{markdown.rstrip()}\n"
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
