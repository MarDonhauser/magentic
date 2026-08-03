#!/usr/bin/env bash
# Legt ein selbstsigniertes Code-Signing-Zertifikat an, damit magentic.app bei
# jedem Build dieselbe Signatur bekommt. macOS bindet die Mikrofon-Freigabe
# (TCC) sonst an den cdhash der ad-hoc-Signatur — der ändert sich bei jedem
# Build, und die Spracheingabe fragt jedes Mal neu nach Erlaubnis.
set -euo pipefail

IDENTITY="${MAGENTIC_SIGN_IDENTITY:-magentic-dev}"
KEYCHAIN="$HOME/Library/Keychains/login.keychain-db"

can_sign() {
  local probe
  probe="$(mktemp -d)/probe"
  cp /bin/echo "$probe" 2>/dev/null || return 1
  codesign --force --sign "$IDENTITY" "$probe" >/dev/null 2>&1
  local rc=$?
  rm -rf "$(dirname "$probe")"
  return $rc
}

if can_sign; then
  echo "✓ Signatur-Identität \"$IDENTITY\" ist einsatzbereit."
  exit 0
fi

# Über den Fingerabdruck löschen: delete-certificate -c lässt gleichnamige
# Einträge stehen, und mehrere davon lassen codesign das falsche wählen.
# `|| true`: findet grep nichts, wäre der Pipe-Status 1 und set -e würde das
# Skript hier beenden, statt das Zertifikat anzulegen.
stale=$(security find-identity -p codesigning "$KEYCHAIN" 2>/dev/null | grep "\"$IDENTITY\"" | awk '{print $2}' || true)
if [ -n "$stale" ]; then
  echo "→ Unbrauchbaren Rest von \"$IDENTITY\" entfernen…"
  for sha in $stale; do
    security delete-identity -Z "$sha" "$KEYCHAIN" >/dev/null 2>&1 || true
  done
fi

echo "→ Erzeuge selbstsigniertes Code-Signing-Zertifikat \"$IDENTITY\"…"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
pw="$(openssl rand -hex 16)"

cat >"$tmp/openssl.cnf" <<EOF
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no

[dn]
CN = $IDENTITY

[ext]
basicConstraints = critical,CA:false
keyUsage = critical,digitalSignature
extendedKeyUsage = critical,codeSigning
EOF

openssl req -x509 -newkey rsa:2048 -sha256 -days 7300 -nodes \
  -config "$tmp/openssl.cnf" \
  -keyout "$tmp/key.pem" -out "$tmp/cert.pem" >/dev/null 2>&1

# macOS' Keychain versteht die OpenSSL-3-Standardalgorithmen nicht und lehnt
# ein passwortloses Bundle mit "MAC verification failed" ab — daher die alten
# PBE-Verfahren und ein echtes (hier zufälliges) Passwort.
openssl pkcs12 -export \
  -keypbe PBE-SHA1-3DES -certpbe PBE-SHA1-3DES -macalg sha1 \
  -inkey "$tmp/key.pem" -in "$tmp/cert.pem" \
  -out "$tmp/bundle.p12" -passout "pass:$pw" >/dev/null 2>&1

echo "→ Importiere ins Login-Keychain…"
# -A statt -T: sonst fragt macOS beim ersten Signieren per Dialog nach dem
# Schlüsselbund-Passwort.
security import "$tmp/bundle.p12" -k "$KEYCHAIN" -P "$pw" -A >/dev/null

# Ohne Vertrauensstellung meldet codesign "no identity found" — auch wenn man
# die Identität über ihren SHA-1 anspricht (nachgemessen: die Identität ist
# vorhanden, aber CSSMERR_TP_NOT_TRUSTED). Dieser Schritt ist der einzige, der
# nach dem Passwort fragt; danach bleibt die Einstellung dauerhaft bestehen.
echo "→ Vertrauensstellung setzen — macOS fragt jetzt einmal nach deinem Passwort…"
security add-trusted-cert -r trustRoot -p codeSign -k "$KEYCHAIN" "$tmp/cert.pem"

if can_sign; then
  echo "✓ Fertig. scripts/build-app.sh signiert ab jetzt mit \"$IDENTITY\"."
  echo "  Beim nächsten Start einmal das Mikrofon erlauben — danach bleibt es erlaubt."
else
  echo "✗ Vertrauensstellung wurde nicht übernommen."
  echo "  Schlüsselbundverwaltung öffnen → Zertifikat \"$IDENTITY\" → Vertrauen →"
  echo "  „Code-Signierung\" auf „Immer vertrauen\" setzen, dann erneut versuchen."
  exit 1
fi
