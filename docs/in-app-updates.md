# Updates in der Desktop-App

Unter **Einstellungen → App-Updates** stehen die installierte Version und
**Nach Updates suchen**. Dieselbe Aktion steht im macOS-Menü **magentic**.
Sparkle öffnet den nativen Dialog, prüft den Feed und zeigt neue Versionen an.
Download, Fortschritt, Abbrechen, Fehlermeldungen, Installation und Neustart
übernimmt Sparkle. Die Installation wird im Dialog bestätigt. Es gibt keine
automatischen Netzwerkprüfungen oder unbeaufsichtigten Installationen.

Die Integration gilt für macOS. Entwicklungsbuilds ohne Release-Konfiguration
zeigen einen Hinweis und bieten keinen funktionslosen Update-Knopf an. Die
Update-Methoden sind in der Remote-Policy als lokale Bedienung klassifiziert.

## Einmalige Einrichtung für Veröffentlichungen

Der öffentliche GitHub-Repository `MarDonhauser/magentic` dient als Feed- und
Download-Host. Es ist kein GitHub-Token in der installierten App enthalten.
Vor dem ersten Release muss ein dauerhaftes Sparkle-Schlüsselpaar eingerichtet
werden. Die privaten Schlüssel gehören weder ins Repository noch in die App.

```bash
./scripts/prepare-sparkle.sh
app/build/sparkle/bin/generate_keys --account magentic-updates
```

Den ausgegebenen öffentlichen Schlüssel als GitHub-Actions-Repository-Variable
**MAGENTIC_UPDATE_PUBLIC_KEY** speichern. Den privaten Schlüssel mit
`generate_keys --account magentic-updates -x /sicherer/pfad/magentic-key`
exportieren, die Dateirechte auf `600` setzen und seinen Inhalt als
Repository-Secret **MAGENTIC_UPDATE_PRIVATE_KEY** hinterlegen. Den privaten
Schlüssel sicher sichern. Bestehende Installationen vertrauen diesem Schlüssel;
ein neu erzeugter Schlüssel pro Release würde die Update-Kette unterbrechen.

Bei einem stabilen Tag wie `v0.1.0` führt `.github/workflows/release.yml` die
Tests aus und baut eine Universal-App für Apple Silicon und Intel. Die Version
kommt aus dem Tag und wird vor dem Signieren in beide Bundle-Versionsfelder
geschrieben. Erlaubt ist `MAJOR.MINOR.PATCH`; Vorabversionen sind kein Bestandteil
dieses stabilen Update-Kanals. Releases in aufsteigender Versionsfolge erstellen.

Der Workflow signiert die App mit Apple Developer ID, notarisiert sie, heftet
das Apple-Ticket an und prüft Gatekeeper. Erst danach erstellt er das
Update-Archiv. Auch die DMG wird signiert, notarisiert und mit einem Ticket
versehen. Schlägt einer dieser Schritte fehl, wird kein Release veröffentlicht.

Er lädt die Dateien zunächst in einen GitHub-Release-Entwurf:

- `magentic-0.1.0.dmg` für die erste Installation.
- `magentic-0.1.0.zip` als signiertes Update-Archiv.
- `appcast.xml` als ebenfalls signierter Update-Feed.
- `SHA256SUMS` mit den Prüfsummen der drei Dateien.

Erst wenn alle Dateien vorhanden sind, veröffentlicht er das Release. Der feste
Feed unter `https://github.com/MarDonhauser/magentic/releases/latest/download/appcast.xml`
verweist auf das Archiv im jeweiligen Versions-Release. Bereits veröffentlichte
Archive werden nicht überschrieben. Manuelle Workflow-Läufe bauen nur Artefakte.

Die App enthält `SUPublicEDKey`, `SURequireSignedFeed` und
`SUVerifyUpdateBeforeExtraction`: Sowohl Feed als auch Archive müssen eine
gültige Ed25519-Signatur tragen. Der Build prüft vorab, dass privater und
öffentlicher Schlüssel zusammenpassen. Sparkle selbst ist auf Version 2.10.1
und den SHA-256-Digest des offiziellen SDK-Archivs festgelegt. Seine Frameworks,
Symlinks und Hilfsprozesse werden vor dem Signieren in die App eingebettet.
Die macOS-Mindestversion wird auf mindestens die des Sparkle-SDK angehoben
(bei dieser Version macOS 12).

Zusätzlich zu den Sparkle-Schlüsseln benötigt GitHub Actions diese
Repository-Secrets für Apple-Signierung und Notarisierung:

- **MAGENTIC_APPLE_CERTIFICATE_P12**: Base64 des passwortgeschützten
  Developer-ID-Application-Zertifikats einschließlich privatem Schlüssel.
- **MAGENTIC_APPLE_CERTIFICATE_PASSWORD**: Passwort dieses PKCS#12-Exports.
- **MAGENTIC_APPLE_API_PRIVATE_KEY**: privater App-Store-Connect-API-Schlüssel.

Als Repository-Variablen werden **MAGENTIC_SIGN_IDENTITY** (der vollständige
Developer-ID-Application-Name), **MAGENTIC_APPLE_API_KEY_ID** und
**MAGENTIC_APPLE_API_ISSUER_ID** hinterlegt. Die Pipeline importiert die
Signieridentität in einen temporären Schlüsselbund und entfernt diesen sowie
die temporären Apple-Schlüsseldateien am Ende des Laufs. Die Signierung
verwendet Hardened Runtime, sichere Zeitstempel und das Mikrofon-Entitlement.
Sparkle-Hilfsprozesse werden vor dem Framework und der App signiert.
Lokale Builds mit `magentic-dev` behalten ihre bestehende Signierlogik.

## Lokaler Release-Build

```bash
export MAGENTIC_UPDATE_PUBLIC_KEY='öffentlicher Base64-Schlüssel'
export MAGENTIC_UPDATE_PRIVATE_KEY_FILE='/sicherer/pfad/magentic-key'
./scripts/build-release.sh 0.1.0 /tmp/magentic-release
```

Ohne `MAGENTIC_NOTARIZE=true` ist das ein lokaler, nicht notarisierter
Artefakt-Build. Für eine notarisierte Distribution zusätzlich die
Developer-ID-Identität als `MAGENTIC_SIGN_IDENTITY` und entweder
`MAGENTIC_NOTARY_PROFILE` oder die drei Werte `MAGENTIC_APPLE_API_KEY_FILE`,
`MAGENTIC_APPLE_API_KEY_ID`, `MAGENTIC_APPLE_API_ISSUER_ID` setzen.

Das Skript baut und prüft die Artefakte, veröffentlicht aber nichts und ersetzt
keine installierte App. Es startet auch keine laufende App neu. Für einen
manuellen lokalen Build ohne Update-Kanal bleiben `scripts/build-app.sh` und
`update.sh` verfügbar. Ohne `MAGENTIC_UPDATE_PUBLIC_KEY` entfernt der
Post-Build-Hook alte Sparkle-Einstellungen aus einem wiederverwendeten Bundle.

Vor der ersten echten Veröffentlichung muss einmal der vollständige Weg mit
zwei installierten Release-Versionen geprüft werden: neue Version finden,
laden, Installation bestätigen, neu starten und Versionswechsel kontrollieren.
Ein lokaler Build mit Testschlüsseln oder ein Test der Oberfläche belegt diese
öffentliche Update-Kette noch nicht. Nutzer einer bisherigen App ohne Updater
müssen einmal eine Version mit dieser Integration manuell installieren.

Quellen: [Sparkle einbinden](https://sparkle-project.org/documentation/),
[programmgesteuerter Start](https://sparkle-project.org/documentation/programmatic-setup/),
[Updates veröffentlichen](https://sparkle-project.org/documentation/publishing/).
