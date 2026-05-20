#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
TARGETS=(
  "desktop/wails.json"
  "internal/version/version.go"
  "README.md"
  "README.zh-CN.md"
  "CHANGELOG.md"
)

BACKUP_DIR="$(mktemp -d)"
cleanup() {
  for file in "${TARGETS[@]}"; do
    if [[ -f "$BACKUP_DIR/$file" ]]; then
      mkdir -p "$(dirname "$ROOT_DIR/$file")"
      cp "$BACKUP_DIR/$file" "$ROOT_DIR/$file"
    fi
  done
  rm -rf "$BACKUP_DIR"
}
trap cleanup EXIT

for file in "${TARGETS[@]}"; do
  mkdir -p "$BACKUP_DIR/$(dirname "$file")"
  cp "$ROOT_DIR/$file" "$BACKUP_DIR/$file"
done

"$ROOT_DIR/scripts/sync-version.sh" >/dev/null

DRIFT=0
for file in "${TARGETS[@]}"; do
  if ! cmp -s "$ROOT_DIR/$file" "$BACKUP_DIR/$file"; then
    DRIFT=1
    echo "Version drift detected in $file" >&2
    diff -u "$BACKUP_DIR/$file" "$ROOT_DIR/$file" || true
  fi
done

if [[ "$DRIFT" -ne 0 ]]; then
  echo "Run ./scripts/sync-version.sh to resync versioned files." >&2
  exit 1
fi

echo "Version sync check passed."
