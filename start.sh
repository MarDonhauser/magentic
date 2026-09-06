#!/usr/bin/env bash
# Baut die App neu, ersetzt die installierte Version und startet sie.
# Default ist bewusst ein echtes Update — kein bloßes Öffnen eines alten Builds.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_APP="$ROOT_DIR/app/build/bin/magentic.app"
INSTALL_APP="/Applications/magentic.app"
AUTOSTART_PLIST="$HOME/Library/LaunchAgents/de.donhauser.magentic.plist"

usage() {
  echo "Nutzung: ./start.sh [--build|--start-only|--dev]"
  echo "  ohne Option  Update: laufende App beenden, neu bauen, nach"
  echo "               /Applications installieren (alte Version wird ersetzt)"
  echo "               und die installierte Version starten"
  echo "  --build      wie ohne Option ( ggf. weitere Argumente an wails build)"
  echo "  --start-only nur starten, nichts bauen/installieren (schnell, alte Version)"
  echo "  --dev        Wails-Entwicklungsmodus starten"
}

quit_running() {
  if pgrep -x magentic >/dev/null 2>&1; then
    echo "→ Beende laufende Version…"
    pkill -x magentic || true
    for _ in $(seq 1 20); do
      pgrep -x magentic >/dev/null 2>&1 || break
      sleep 0.5
    done
    if pgrep -x magentic >/dev/null 2>&1; then
      echo "→ Erzwinge das Beenden…"
      pkill -9 -x magentic || true
      sleep 1
    fi
  fi
}

install_build() {
  echo "→ Installiere nach $INSTALL_APP (alte Version wird ersetzt)…"
  # Nicht per cp über die bestehende App kopieren — macOS invalidiert dabei
  # die Signatur. Immer erst löschen, dann frisch kopieren.
  rm -rf "$INSTALL_APP"
  cp -R "$BUILD_APP" "$INSTALL_APP"
  xattr -dr com.apple.quarantine "$INSTALL_APP" 2>/dev/null || true
}

refresh_autostart() {
  # Zeigte der Autostart noch auf den Repo-Build, biegt er nach dem Update auf
  # die installierte App um — sonst startet nach der Anmeldung die alte Version.
  if [ -f "$AUTOSTART_PLIST" ] && grep -q "$BUILD_APP" "$AUTOSTART_PLIST" 2>/dev/null; then
    echo "→ Autostart zeigt jetzt auf $INSTALL_APP…"
    "$ROOT_DIR/scripts/autostart.sh" >/dev/null
  fi
}

do_update() {
  quit_running
  "$ROOT_DIR/scripts/build-app.sh" "$@"
  install_build
  refresh_autostart
  echo "→ Starte installierte Version…"
  open "$INSTALL_APP"
  echo "✓ Installiert und gestartet: $INSTALL_APP"
}

do_start_only() {
  # Bewusst ohne Bauen/Installieren — startet nur, was bereits da ist.
  if [ -d "$INSTALL_APP" ]; then
    open "$INSTALL_APP"
  elif [ -d "$BUILD_APP" ]; then
    open "$BUILD_APP"
  else
    echo "✗ Keine installierte Version gefunden — ./start.sh ohne Option baut und installiert." >&2
    exit 1
  fi
}

case "${1:-}" in
  ""|--build|--update|--install)
    shift $(( $# > 0 ? 1 : 0 ))
    do_update "$@"
    ;;
  --start-only|--run|--open)
    do_start_only
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
