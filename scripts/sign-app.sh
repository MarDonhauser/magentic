#!/usr/bin/env bash
# Signiert magentic.app mit der stabilen Identität. Läuft als Post-Build-Hook
# von `wails build` (siehe app/wails.json), damit auch ein roher Build nicht bei
# der Ad-hoc-Signatur stehen bleibt — TCC würde der App sonst die
# Mikrofon-Freigabe entziehen und die Spracheingabe hört nur Stille.
set -euo pipefail

IDENTITY="${MAGENTIC_SIGN_IDENTITY:-magentic-dev}"
TARGET="${1:-$(cd "$(dirname "$0")/.." && pwd)/app/build/bin/magentic.app}"

# Wails übergibt die Binary in Contents/MacOS, signiert wird das ganze Bundle.
if [[ "$TARGET" == *.app/* ]]; then
  TARGET="${TARGET%%.app/*}.app"
fi

CERT_SHA1="$(security find-certificate -c "$IDENTITY" -Z 2>/dev/null | awk -F': ' '/SHA-1 hash/{print $2}')"
if [ -z "$CERT_SHA1" ]; then
  echo "✗ Zertifikat \"$IDENTITY\" nicht im Keychain — einmalig ./scripts/setup-signing.sh ausführen." >&2
  exit 1
fi

# Kein --options runtime: Hardened Runtime verlangt für den Mikrofonzugriff
# zusätzlich das Entitlement com.apple.security.device.audio-input, sonst
# scheitert die Spracheingabe in den Sessions.
#
# Explizites Designated Requirement: für selbstsignierte Zertifikate ohne
# Vertrauenskette generiert codesign sonst ein cdhash-Requirement — das ändert
# sich mit jedem Build, und TCC vergisst die Mikrofon-Freigabe jedes Mal.
codesign --force --deep --sign "$IDENTITY" \
  --identifier com.wails.magentic \
  -r="designated => identifier \"com.wails.magentic\" and certificate leaf = H\"$CERT_SHA1\"" \
  "$TARGET"
