#!/usr/bin/env bash
# Startet magentic bei jeder Anmeldung. `off` entfernt den Autostart wieder.
set -euo pipefail

cd "$(dirname "$0")/.."
LABEL="de.donhauser.magentic"
PLIST="$HOME/Library/LaunchAgents/$LABEL.plist"
APP="$(pwd)/app/build/bin/magentic.app"

if [ "${1:-on}" = "off" ]; then
  launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
  rm -f "$PLIST"
  echo "✓ Autostart entfernt."
  exit 0
fi

if [ ! -d "$APP" ]; then
  echo "✗ $APP nicht gefunden — erst ./scripts/build-app.sh ausführen."
  exit 1
fi

mkdir -p "$(dirname "$PLIST")"
cat >"$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>$LABEL</string>
  <key>ProgramArguments</key>
  <array>
    <string>/usr/bin/open</string>
    <string>-a</string>
    <string>$APP</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>ProcessType</key><string>Interactive</string>
</dict>
</plist>
EOF

# bootout vor bootstrap: sonst schlägt ein erneutes Laden mit "service already
# loaded" fehl und eine geänderte Plist würde nie übernommen.
launchctl bootout "gui/$(id -u)/$LABEL" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"

echo "✓ magentic startet ab jetzt bei jeder Anmeldung."
echo "  App:  $APP"
echo "  Aus:  ./scripts/autostart.sh off"
