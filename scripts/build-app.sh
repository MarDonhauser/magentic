#!/usr/bin/env bash
# Baut die Desktop-App und signiert sie mit einer stabilen Identität, damit
# macOS erteilte Berechtigungen (Mikrofon für die Spracheingabe) über Builds
# hinweg behält.
set -euo pipefail

cd "$(dirname "$0")/.."
IDENTITY="${MAGENTIC_SIGN_IDENTITY:-magentic-dev}"
APP="app/build/bin/magentic.app"

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

echo "→ wails build…"
(cd app && wails build "$@")

# Kein --options runtime: Hardened Runtime verlangt für den Mikrofonzugriff
# zusätzlich das Entitlement com.apple.security.device.audio-input, sonst
# scheitert die Spracheingabe in den Sessions.
#
# Explizites Designated Requirement: für selbstsignierte Zertifikate ohne
# Vertrauenskette generiert codesign sonst ein cdhash-Requirement — das ändert
# sich mit jedem Build, und TCC vergisst die Mikrofon-Freigabe jedes Mal.
CERT_SHA1="$(security find-certificate -c "$IDENTITY" -Z 2>/dev/null | awk -F': ' '/SHA-1 hash/{print $2}')"
if [ -z "$CERT_SHA1" ]; then
  echo "✗ Zertifikat \"$IDENTITY\" nicht im Keychain gefunden."
  exit 1
fi
echo "→ Signiere mit \"$IDENTITY\"…"
codesign --force --deep --sign "$IDENTITY" \
  --identifier com.wails.magentic \
  -r="designated => identifier \"com.wails.magentic\" and certificate leaf = H\"$CERT_SHA1\"" \
  "$APP"

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
