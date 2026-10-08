#!/usr/bin/env bash
# Baut die Desktop-App und signiert sie mit einer stabilen Identität, damit
# macOS erteilte Berechtigungen (Mikrofon für die Spracheingabe) über Builds
# hinweg behält.
set -euo pipefail

cd "$(dirname "$0")/.."
IDENTITY="${MAGENTIC_SIGN_IDENTITY:-magentic-dev}"
APP="app/build/bin/magentic.app"

WAILS_BIN="${MAGENTIC_WAILS_BIN:-}"
if [ -z "$WAILS_BIN" ]; then
  if command -v wails >/dev/null 2>&1; then
    WAILS_BIN="$(command -v wails)"
  else
    GO_BIN="$(go env GOPATH 2>/dev/null)/bin/wails"
    if [ -x "$GO_BIN" ]; then
      WAILS_BIN="$GO_BIN"
    else
      echo "✗ wails nicht gefunden. Installiere es oder setze MAGENTIC_WAILS_BIN."
      exit 1
    fi
  fi
fi

# Nicht über find-identity prüfen: ein selbstsigniertes Zertifikat ohne
# Trust-Setting taucht dort nicht auf, obwohl codesign es nutzen kann.
can_sign() {
  local probe
  probe="$(mktemp -d)/probe"
  cp /bin/echo "$probe" 2>/dev/null || return 1
  codesign --force --sign "$IDENTITY" "$probe" >/dev/null 2>&1
  local rc=$?
  rm -rf "$(dirname "$probe")"
  return $rc
}

if ! can_sign; then
  echo "→ Keine nutzbare Signatur-Identität — starte einmaliges Setup."
  ./scripts/setup-signing.sh
fi

# Signiert wird im Post-Build-Hook (app/wails.json → scripts/sign-app.sh).
echo "→ wails build…"
(cd app && "$WAILS_BIN" build "$@")

if ! codesign -dv --verbose=2 "$APP" 2>&1 | grep -qx "Authority=$IDENTITY"; then
  echo "✗ $APP ist nicht mit \"$IDENTITY\" signiert — Post-Build-Hook in app/wails.json prüfen."
  exit 1
fi
codesign -dv --verbose=2 "$APP" 2>&1 | grep -E '^(Authority|Identifier)=' || true

# Lief die App, muss sie neu starten — sonst arbeitet man weiter mit der alten
# Version und wundert sich, dass die Änderung fehlt.
if pgrep -x magentic >/dev/null 2>&1; then
  echo "→ Starte die laufende App neu…"
  pkill -x magentic || true
  sleep 1
  open "$APP"
fi

echo "✓ $APP gebaut und signiert."
