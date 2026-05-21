#!/bin/bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
VERSION_FILE="$ROOT_DIR/VERSION"

if [[ ! -f "$VERSION_FILE" ]]; then
  echo "VERSION file not found: $VERSION_FILE" >&2
  exit 1
fi

VERSION="$(tr -d '[:space:]' < "$VERSION_FILE")"
if [[ ! "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([-.][0-9A-Za-z.]+)?$ ]]; then
  echo "Invalid version in VERSION: $VERSION" >&2
  exit 1
fi

export ROOT_DIR VERSION

node <<'NODE'
const fs = require('fs');
const path = require('path');

const root = process.env.ROOT_DIR;
const version = process.env.VERSION;

function writeJSON(file, updater) {
  const full = path.join(root, file);
  const data = JSON.parse(fs.readFileSync(full, 'utf8'));
  updater(data);
  fs.writeFileSync(full, JSON.stringify(data, null, 2) + '\n');
}

function replaceBetween(file, beginMarker, endMarker, replacement) {
  const full = path.join(root, file);
  const source = fs.readFileSync(full, 'utf8');
  const begin = source.indexOf(beginMarker);
  const end = source.indexOf(endMarker);
  if (begin === -1 || end === -1 || end < begin) {
    throw new Error(`Markers not found in ${file}`);
  }
  const before = source.slice(0, begin + beginMarker.length);
  const after = source.slice(end);
  const next = `${before}\n${replacement}\n${after}`;
  fs.writeFileSync(full, next);
}

function replaceRegex(file, pattern, replacement) {
  const full = path.join(root, file);
  const source = fs.readFileSync(full, 'utf8');
  if (!pattern.test(source)) {
    throw new Error(`Pattern not matched in ${file}`);
  }
  pattern.lastIndex = 0;
  const next = source.replace(pattern, replacement);
  fs.writeFileSync(full, next);
}

writeJSON('desktop/wails.json', (data) => {
  data.info = data.info || {};
  data.info.productVersion = version;
});

fs.mkdirSync(path.join(root, 'internal', 'version'), { recursive: true });
fs.writeFileSync(
  path.join(root, 'internal', 'version', 'version.go'),
  `package version\n\n// Version is generated from the repository-level VERSION file.\nconst Version = ${JSON.stringify(version)}\n`
);

replaceBetween(
  'README.md',
  '<!-- VERSION:CURRENT:BEGIN -->',
  '<!-- VERSION:CURRENT:END -->',
  `Current desktop app version: \`v${version}\`.\n\nThe canonical source is [VERSION](./VERSION). Run \`./scripts/sync-version.sh\` to propagate it into [desktop/wails.json](./desktop/wails.json), the desktop UI, and release-facing docs.`
);

replaceBetween(
  'README.zh-CN.md',
  '<!-- VERSION:CURRENT:BEGIN -->',
  '<!-- VERSION:CURRENT:END -->',
  `当前桌面端版本：\`v${version}\`。\n\n唯一来源是 [VERSION](./VERSION)。执行 \`./scripts/sync-version.sh\` 会把它同步到 [desktop/wails.json](./desktop/wails.json)、桌面 UI 和面向发布的文档块。`
);

replaceRegex(
  'CHANGELOG.md',
  /^## Unreleased(?: \(target: v[^\n]+\))?$/m,
  `## Unreleased (target: v${version})`
);
NODE

echo "Synced version to $VERSION"
