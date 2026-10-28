#!/usr/bin/env bash
# Produces local, signed update artifacts. Publishing is a separate action.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT_DIR"
export MAGENTIC_VERSION="${1:?Nutzung: scripts/build-release.sh MAJOR.MINOR.PATCH [Ausgabeverzeichnis]}"
OUT="${2:-$ROOT_DIR/app/build/bin/release}"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
: "${MAGENTIC_UPDATE_PUBLIC_KEY:?Öffentlichen Sparkle-Schlüssel setzen}"
: "${MAGENTIC_UPDATE_PRIVATE_KEY_FILE:?Pfad zum privaten Sparkle-Schlüssel setzen}"
[[ "$MAGENTIC_VERSION" =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo 'Version muss MAJOR.MINOR.PATCH entsprechen.' >&2; exit 1; }
swift scripts/verify-update-key.swift "$MAGENTIC_UPDATE_PRIVATE_KEY_FILE" "$MAGENTIC_UPDATE_PUBLIC_KEY"
./scripts/prepare-sparkle.sh
SDK="$ROOT_DIR/app/build/sparkle"
WAILS_BIN="${MAGENTIC_WAILS_BIN:-$(command -v wails || true)}"
WAILS_BIN="${WAILS_BIN:-$(go env GOPATH)/bin/wails}"
# CI and local artifact builds can use ad-hoc code signing. The EdDSA key is
# mandatory and authenticates both the archive and the appcast independently.
export MAGENTIC_SIGN_IDENTITY="${MAGENTIC_SIGN_IDENTITY:--}"
case "${MAGENTIC_NOTARIZE:-false}" in
  true)
    [[ "$MAGENTIC_SIGN_IDENTITY" == "Developer ID Application:"* ]] || { echo 'Notarisierung benötigt eine Developer-ID-Signatur.' >&2; exit 1; }
    if [ -z "${MAGENTIC_APPLE_API_KEY_FILE:-}" ] && [ -z "${MAGENTIC_NOTARY_PROFILE:-}" ]; then
      echo 'Notarisierungs-Zugangsdaten fehlen.' >&2; exit 1
    fi
    ;;
  false) ;;
  *) echo 'MAGENTIC_NOTARIZE muss true oder false sein.' >&2; exit 1 ;;
esac
# Keep earlier local installers in build/bin; the app is rebuilt and configured
# for this version without erasing unrelated artifacts from that directory.
(cd app && "$WAILS_BIN" build -platform darwin/universal)
APP="$ROOT_DIR/app/build/bin/magentic.app"
codesign --verify --deep --strict "$APP"
if [ "${MAGENTIC_NOTARIZE:-false}" = true ]; then
  ./scripts/notarize.sh "$APP"
fi
mkdir -p "$OUT"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
mkdir "$STAGE/feed" "$STAGE/dmg"
ZIP="magentic-$MAGENTIC_VERSION.zip"
ditto -c -k --sequesterRsrc --keepParent "$APP" "$STAGE/feed/$ZIP"
"$SDK/bin/generate_appcast" --ed-key-file "$MAGENTIC_UPDATE_PRIVATE_KEY_FILE" \
  --download-url-prefix "https://github.com/MarDonhauser/magentic/releases/download/v$MAGENTIC_VERSION/" \
  --link "https://github.com/MarDonhauser/magentic/releases/tag/v$MAGENTIC_VERSION" \
  --maximum-versions 1 --maximum-deltas 0 "$STAGE/feed"
# SURequireSignedFeed makes generate_appcast sign the feed as well. Verify it
# again so a tool/configuration regression cannot silently ship an unsigned feed.
"$SDK/bin/sign_update" --verify --ed-key-file "$MAGENTIC_UPDATE_PRIVATE_KEY_FILE" "$STAGE/feed/appcast.xml"
ditto "$APP" "$STAGE/dmg/magentic.app"
ln -s /Applications "$STAGE/dmg/Applications"
hdiutil create -volname magentic -srcfolder "$STAGE/dmg" -ov -format UDZO "$OUT/magentic-$MAGENTIC_VERSION.dmg"
if [ "${MAGENTIC_NOTARIZE:-false}" = true ]; then
  codesign --force --sign "$MAGENTIC_SIGN_IDENTITY" --timestamp "$OUT/magentic-$MAGENTIC_VERSION.dmg"
  ./scripts/notarize.sh "$OUT/magentic-$MAGENTIC_VERSION.dmg"
fi
hdiutil verify "$OUT/magentic-$MAGENTIC_VERSION.dmg"
cp "$STAGE/feed/$ZIP" "$STAGE/feed/appcast.xml" "$OUT/"
(cd "$OUT" && shasum -a 256 "magentic-$MAGENTIC_VERSION.dmg" "$ZIP" appcast.xml > SHA256SUMS)
echo "Release-Artefakte: $OUT"
