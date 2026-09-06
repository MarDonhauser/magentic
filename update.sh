#!/usr/bin/env bash
# Echtes Update: laufende App beenden, neu bauen, installierte Version ersetzen
# und die installierte Version starten. Zum bloßen Starten: ./start.sh
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
BUILD_APP="$ROOT_DIR/app/build/bin/magentic.app"
INSTALL_APP="/Applications/magentic.app"
AUTOSTART_PLIST="$HOME/Library/LaunchAgents/de.donhauser.magentic.plist"

usage() {
  echo "Nutzung: ./update.sh [-- wails-build-Argumente]"
  echo "  Beendet die laufende App, baut neu, ersetzt $INSTALL_APP"
  echo "  (alte Version wird gelöscht, nicht überkopiert) und startet sie."
  echo "  Nur starten, ohne zu bauen: ./start.sh"
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
  # Erst prüfen, dann löschen: Ohne Build-Output würde das rm unten die
  # funktionierende installierte Version vernichten und das cp danach
  # fehlschlagen — der Rechner stünde ohne App da.
  if [ ! -d "$BUILD_APP" ]; then
    echo "✗ Abbruch: kein Build-Output unter $BUILD_APP — installierte Version bleibt unangetastet." >&2
    exit 1
  fi
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

case "${1:-}" in
  -h|--help)
    usage
    ;;
  *)
    quit_running
    "$ROOT_DIR/scripts/build-app.sh" "$@"
    install_build
    refresh_autostart
    echo "→ Starte installierte Version…"
    open "$INSTALL_APP"
    echo "✓ Installiert und gestartet: $INSTALL_APP"
    ;;
esac
