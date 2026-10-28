#!/usr/bin/env bash
# Called only for explicit distribution builds, before publication.
set -euo pipefail
TARGET="${1:?Pfad zu einer signierten .app oder .dmg erforderlich}"
AUTH=()
if [ -n "${MAGENTIC_APPLE_API_KEY_FILE:-}" ]; then
  AUTH=(--key "$MAGENTIC_APPLE_API_KEY_FILE" --key-id "${MAGENTIC_APPLE_API_KEY_ID:?Apple API Key ID fehlt}" --issuer "${MAGENTIC_APPLE_API_ISSUER_ID:?Apple API Issuer ID fehlt}")
else
  AUTH=(--keychain-profile "${MAGENTIC_NOTARY_PROFILE:?Notarisierungs-Zugangsdaten fehlen}")
fi
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
case "$TARGET" in
  *.app)
    ditto -c -k --sequesterRsrc --keepParent "$TARGET" "$STAGE/submission.zip"
    UPLOAD="$STAGE/submission.zip"
    ;;
  *.dmg) UPLOAD="$TARGET" ;;
  *) echo 'Nur .app und .dmg werden unterstützt.' >&2; exit 1 ;;
esac
echo "→ Apple-Notarisierung: $(basename "$TARGET")"
# A timeout does not cancel Apple's submission. Do not automatically resubmit.
xcrun notarytool submit "$UPLOAD" "${AUTH[@]}" --wait --timeout 30m --output-format json > "$STAGE/result.json"
python3 - "$STAGE/result.json" <<'PY'
import json, sys
result = json.load(open(sys.argv[1]))
print('Apple submission:', result.get('id'), 'status:', result.get('status'))
if result.get('status') != 'Accepted':
    raise SystemExit('Notarisierung nicht akzeptiert; Veröffentlichung abgebrochen.')
PY
xcrun stapler staple "$TARGET"
xcrun stapler validate "$TARGET"
case "$TARGET" in
  *.app) spctl --assess --type execute --verbose "$TARGET" ;;
  *.dmg) spctl --assess --type open --context context:primary-signature --verbose "$TARGET" ;;
esac
