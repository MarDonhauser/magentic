#!/usr/bin/env bash
# Startet die App — baut und installiert nichts.
# Zum Aktualisieren (neu bauen, nach /Applications installieren): ./update.sh
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_APP="$ROOT_DIR/app/build/bin/magentic.app"
INSTALL_APP="/Applications/magentic.app"

usage() {
  echo "Nutzung: ./start.sh [--dev]"
  echo "  ohne Option  installierte App starten (ersatzweise Repo-Build;"
  echo "               baut den Repo-Build, falls noch keiner existiert)"
  echo "  --dev        Wails-Entwicklungsmodus starten"
  echo "  Aktualisieren und nach /Applications installieren: ./update.sh"
}

case "${1:-}" in
  "")
    if [ -d "$INSTALL_APP" ]; then
      open "$INSTALL_APP"
    else
      if [ ! -d "$BUILD_APP" ]; then
        "$ROOT_DIR/scripts/build-app.sh"
      fi
      open "$BUILD_APP"
    fi
    ;;
  --dev)
    shift
    cd "$ROOT_DIR/app"
    exec wails dev "$@"
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
