#!/bin/bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DESKTOP_DIR="$REPO_ROOT/desktop"
APP_NAME="Lingma Proxy.app"
APP_BUNDLE_NAME="LingmaProxy"
BUILD_APP_PATH="$DESKTOP_DIR/build/bin/$APP_NAME"
INSTALL_APP_PATH="/Applications/$APP_NAME"
WAILS_BIN="${WAILS_BIN:-/Users/tiancheng/go/bin/wails}"
OPEN_AFTER_BUILD="${OPEN_AFTER_BUILD:-1}"

log() {
  printf '[rebuild-local-app] %s\n' "$1"
}

quit_gui_app() {
  log "Requesting macOS app quit (double quit sequence)"
  osascript -e 'tell application "Lingma Proxy" to quit' >/dev/null 2>&1 || true
  sleep 0.6
  osascript -e 'tell application "Lingma Proxy" to quit' >/dev/null 2>&1 || true
}

kill_processes() {
  log "Force-stopping remaining Lingma Proxy processes"
  pkill -f "$INSTALL_APP_PATH/Contents/MacOS/$APP_BUNDLE_NAME" >/dev/null 2>&1 || true
  pkill -f "$BUILD_APP_PATH/Contents/MacOS/$APP_BUNDLE_NAME" >/dev/null 2>&1 || true
  pkill -x "$APP_BUNDLE_NAME" >/dev/null 2>&1 || true
}

wait_for_exit() {
  local retries=20
  while [ "$retries" -gt 0 ]; do
    if ! pgrep -f "$APP_BUNDLE_NAME" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
    retries=$((retries - 1))
  done
  return 1
}

stop_existing_app() {
  quit_gui_app
  if ! wait_for_exit; then
    kill_processes
    sleep 1
  fi
  if pgrep -f "$APP_BUNDLE_NAME" >/dev/null 2>&1; then
    log "ERROR: Lingma Proxy process is still running after force stop"
    pgrep -af "$APP_BUNDLE_NAME" || true
    exit 1
  fi
}

build_app() {
  log "Building desktop app with Wails"
  cd "$DESKTOP_DIR"
  "$WAILS_BIN" build -platform darwin/arm64 -clean
}

install_app() {
  log "Replacing app in /Applications"
  rm -rf "$INSTALL_APP_PATH"
  cp -R "$BUILD_APP_PATH" "$INSTALL_APP_PATH"
}

open_app() {
  if [ "$OPEN_AFTER_BUILD" = "1" ]; then
    log "Opening installed app"
    open -a "$INSTALL_APP_PATH"
  fi
}

print_summary() {
  local version
  version="$(defaults read "$INSTALL_APP_PATH/Contents/Info.plist" CFBundleShortVersionString 2>/dev/null || echo unknown)"
  log "Done. Installed: $INSTALL_APP_PATH (version $version)"
}

main() {
  build_app
  stop_existing_app
  install_app
  open_app
  print_summary
}

main "$@"
