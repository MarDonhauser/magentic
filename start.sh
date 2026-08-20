#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
APP_PATH="$ROOT_DIR/app/build/bin/magentic.app"

usage() {
  echo "Nutzung: ./start.sh [--build|--dev]"
  echo "  ohne Option  vorhandenen Build starten; falls nötig zuerst bauen"
  echo "  --build      neu bauen, signieren und anschließend starten"
  echo "  --dev        Wails-Entwicklungsmodus starten"
}

case "${1:-}" in
  "")
    if [ ! -d "$APP_PATH" ]; then
      "$ROOT_DIR/scripts/build-app.sh"
    fi
    open "$APP_PATH"
    ;;
  --build)
    "$ROOT_DIR/scripts/build-app.sh"
    open "$APP_PATH"
    ;;
  --dev)
    cd "$ROOT_DIR/app"
    exec wails dev
    ;;
  -h|--help)
    usage
    ;;
  *)
    echo "Unbekannte Option: $1" >&2
    usage >&2
    exit 2
    ;;
esac
