#!/usr/bin/env bash
# Runs an isolated Cocoa bundle, without starting agents or checking a feed.
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
APP="$STAGE/magentic.app"
mkdir -p "$APP/Contents/MacOS"
cat > "$STAGE/smoke.m" <<'OBJC'
#import <AppKit/AppKit.h>
#import "updates_darwin.m"
int main(int argc, char **argv) {
    @autoreleasepool {
        [NSApplication sharedApplication];
        if (argc > 1) {
            NSString *problem = startUpdater();
            if (problem != nil) { fprintf(stderr, "%s\n", problem.UTF8String); return 2; }
        } else {
            char *problem = checkForUpdatesC();
            BOOL rejected = problem != NULL && problem[0] != '\0';
            free(problem);
            if (!rejected) return 3;
        }
        char *status = updatesStatusC();
        puts(status);
        free(status);
    }
    return 0;
}
OBJC
clang -fobjc-arc -framework AppKit -I"$ROOT_DIR/app" "$STAGE/smoke.m" -o "$APP/Contents/MacOS/magentic"
python3 - "$APP" <<'PY'
from pathlib import Path
import plistlib, sys
info = dict(CFBundleIdentifier='com.wails.magentic', CFBundleExecutable='magentic',
            CFBundleName='magentic', CFBundlePackageType='APPL',
            CFBundleVersion='0.0.1', CFBundleShortVersionString='0.0.1', LSMinimumSystemVersion='12.0')
(Path(sys.argv[1]) / 'Contents/Info.plist').write_bytes(plistlib.dumps(info))
PY
"$APP/Contents/MacOS/magentic" > "$STAGE/development.json"
python3 - "$STAGE/development.json" <<'PY'
import json, sys
status = json.load(open(sys.argv[1]))
assert status['enabled'] is False and status['canCheck'] is False
assert status['sessionInProgress'] is False and status['reason']
assert status['version'] == '0.0.1'
print('Native development bundle: unavailable, check rejected, valid boolean JSON.')
PY
if [ -d "$ROOT_DIR/app/build/sparkle/Sparkle.framework" ]; then
  # Public fixture value only; there is no private key and no network request.
  MAGENTIC_UPDATE_PUBLIC_KEY="AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" \
    python3 "$ROOT_DIR/scripts/configure-updates.py" "$APP"
  codesign --force --deep --sign - "$APP"
  "$APP/Contents/MacOS/magentic" start > "$STAGE/release.json"
  python3 - "$STAGE/release.json" <<'PY'
import json, sys
status = json.load(open(sys.argv[1]))
assert status['enabled'] is True and status['canCheck'] is True
assert status['sessionInProgress'] is False and status['reason'] == ''
print('Native release bundle: Sparkle loaded and started, no update session, valid boolean JSON.')
PY
fi
