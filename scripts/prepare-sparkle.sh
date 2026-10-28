#!/usr/bin/env bash
# Pinned upstream SDK; never execute a download before its digest matches.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SDK="$ROOT_DIR/app/build/sparkle"
VERSION="2.10.1"
SHA256="1a856d2a9edb8e628de88121d7be04cf0e2167f8641650b78fca00b14244cf27"
if [ -f "$SDK/.archive-sha256" ] && [ "$(cat "$SDK/.archive-sha256")" = "$SHA256" ] && [ -x "$SDK/bin/generate_appcast" ]; then
  exit 0
fi
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
curl --fail --location --silent --show-error --retry 3 --max-time 120 \
  "https://github.com/sparkle-project/Sparkle/releases/download/$VERSION/Sparkle-$VERSION.tar.xz" -o "$STAGE/sdk.tar.xz"
printf '%s  %s\n' "$SHA256" "$STAGE/sdk.tar.xz" | shasum -a 256 -c -
mkdir "$STAGE/sdk"
tar -xJf "$STAGE/sdk.tar.xz" -C "$STAGE/sdk"
printf '%s' "$SHA256" > "$STAGE/sdk/.archive-sha256"
mkdir -p "$(dirname "$SDK")"
rm -rf "$SDK"
mv "$STAGE/sdk" "$SDK"
