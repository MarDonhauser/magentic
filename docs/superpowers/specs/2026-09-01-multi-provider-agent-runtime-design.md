# Multi-Provider Agent-Runtime — Teilprojekt 1: Provider-Modul, Identität, Wechsel

Stand: 2026-09-01. Basis: `5a145c6`.

## Ziel

Eine Magentic-Session soll ihren Coding-Agenten selbst kennen. Heute startet
jede Coding-Session Claude Code, weil der Startbefehl in
`core/lifecycle.go:1651` fest verdrahtet ist und jede neue Session in
`core/lifecycle.go:327` blind eine `AgentRunRef` mit `AgentVendorClaude`
erhält. Nach diesem Teilprojekt wählt der Nutzer beim Anlegen einen Vendor,
kann ihn im Betrieb wechseln, und Start wie Resume laufen über einen
Provider-Adapter statt über einen Sonderfall für Claude.

Unterstützte Vendors sind Claude Code, Codex, Gemini CLI und GitHub Copilot —
genau die vier, die `AgentVendor`, `DetectAgentTool` und die
WorkHistory-Adapter bereits kennen. Cursor und Grok sind ausdrücklich nicht
Teil dieses Schnitts; das Provider-Modul ist so geschnitten, dass ein weiterer
Vendor später ein neuer Adapter ist und keine Änderung an den Aufrufern.

## Abgrenzung

Nicht in diesem Teilprojekt, sondern in Folgeprojekten:

- **Teilprojekt 2** — echte Status-Semantik je Vendor und die
  Prompt-Zustellregeln der Outbox. Bis dahin liefern Codex, Gemini und Copilot
  weiterhin `StatusUnknown`, und die Outbox stellt ihnen literale Eingaben zu,
  wie sie es heute schon tut.
- **Teilprojekt 3** — Handoff-Zielseite (`core/handoff.go:195/248/295`),
  Usage-Quota und Preistabelle je Vendor.

## Begriffe

Diese Begriffe kommen nach Abnahme in `CONTEXT.md`.

**AgentProvider**:
Der Adapter, über den ein Coding-Agent-Vendor als Prozess adressiert wird:
erwartetes Binary, Erkennung des Pane-Kommandos, Startzeile inklusive
Resume-Semantik und Herkunft der Run-Identität.
_Vermeiden_: Tool, CLI, Backend

**SessionVendor**:
Der durabel an einer Session gespeicherte Vendor, der bestimmt, welcher
AgentProvider sie startet — unabhängig davon, welcher Prozess gerade
tatsächlich in ihrem Pane läuft.
_Vermeiden_: AgentTool, beobachteter Vendor, Provider-Name

**RunIdentityOrigin**:
Ob die Run-Identität einer Session vom Aufrufer vorgegeben werden kann oder
erst nachträglich aus dem Verlauf des Vendors ermittelt werden muss.
_Vermeiden_: SessionID, ExternalID

## Architektur

Neues Modul `core/provider.go` mit einem Adapter je Vendor, registriert wie
die bereits vorhandenen WorkHistory-Adapter in `builtinHistoryAdapters`.

```go
type AgentProvider interface {
    Vendor() AgentVendor
    // Tool ist die stabile Frontend-Identität, die Observation und die
    // Icon-Auflösung im Frontend bereits verwenden.
    Tool() string
    // Binary muss im PATH auffindbar sein, bevor eine Session startet.
    Binary() string
    // Matches erkennt den Vendor am Pane-Kommando, das tmux meldet.
    Matches(paneCommand string) bool
    // StartCommand baut die vollständige Kommandozeile für mode "new" oder
    // "resume". run ist die gespeicherte AgentRunRef dieses Vendors, falls
    // vorhanden.
    StartCommand(session Session, run *AgentRunRef, mode string) (string, error)
    // NewRunID liefert eine vom Aufrufer vorgegebene Run-Identität, wenn der
    // Vendor eine annimmt, und "" wenn sie nachträglich ermittelt werden muss.
    NewRunID() string
}

func builtinAgentProviders() []AgentProvider
func providerForVendor(vendor AgentVendor) (AgentProvider, bool)
func providerForPaneCommand(paneCommand string) (AgentProvider, bool)
```

`DetectAgentTool` bleibt als öffentliche Funktion erhalten und wird zu einer
dünnen Hülle über `providerForPaneCommand`; die Terminal-Regel und das
neutrale Ergebnis für unbekannte Kommandos bleiben unverändert. Der
`switch` in `core/status.go:162` entfällt.

`statusForAgentRuntime` in `core/observation.go:594` behält seine Struktur:
Claude über `DetectClaudeStatus`, jeder andere bekannte Provider explizit
`StatusUnknown`. Teilprojekt 2 ersetzt genau diesen Zweig durch eine
Status-Methode am Adapter; die Aufrufer ändern sich dann nicht mehr.

## Durable Identität

`Session` bekommt ein Feld:

```go
Vendor AgentVendor `json:"vendor,omitempty"`
```

Regeln:

- Terminal- und Dock-Sessions haben keinen Vendor; das Feld bleibt leer.
- Ein leeres Feld an einer Coding-Session bedeutet Claude. Die
  Registry-Migration, die heute in `core/registry.go:548` die
  Legacy-`SessionID` in eine `AgentRunRef` überführt, setzt in derselben Stelle
  `Vendor = AgentVendorClaude`. Bestehende Zustände bleiben damit gültig,
  ohne dass ein separater Migrationslauf nötig wird.
- `Session.AgentRuns` bleibt unverändert vendor-qualifiziert. Ein Wechsel
  löscht keine Einträge: eine Session kann eine Claude- und eine Codex-Run-Ref
  gleichzeitig führen, und `Session.AgentRun(vendor)` bleibt die einzige
  Auflösung.

## Run-Identität

Claude nimmt eine vorgegebene Identität über `--session-id` an. Für die
übrigen Vendors gilt bis zum Beleg des Gegenteils, dass sie das nicht tun.
Daraus folgen zwei Wege:

- **Vorgegeben** (`NewRunID() != ""`): Provisionierung erzeugt die UUID,
  schreibt sofort die `AgentRunRef` und übergibt sie an die Startzeile — das
  heutige Claude-Verhalten, unverändert.
- **Ermittelt** (`NewRunID() == ""`): die Session startet ohne Run-Ref. Die
  Ref entsteht später aus dem Verlauf des Vendors: WorkHistory kennt die
  Wurzelverzeichnisse aller vier Vendors und parst `cwd` sowie Run-ID bereits.
  Ein Abgleich sucht den jüngsten Run dieses Vendors, dessen Arbeitsverzeichnis
  `Session.Dir` entspricht und der nach `Session.CreatedAt` beginnt, und
  schreibt ihn als `AgentRunRef` in die Registry. Findet er keinen oder mehr
  als einen Kandidaten, bleibt die Session ohne Ref: `mode == "resume"` fällt
  dann auf den Fortsetzungsbefehl des Vendors ohne Identität zurück, und wenn
  es auch den nicht gibt, auf einen frischen Start.

Welcher Vendor welchen Weg geht, entscheidet der erste Umsetzungsschritt
(siehe unten) anhand der tatsächlichen `--help`-Ausgaben. Die Startzeilen
selbst leben ausschließlich im jeweiligen Adapter.

## Start und Resume

`tmuxLifecycleRuntime.Start` verliert seinen Claude-Block. Neu:

1. Terminal-Sessions verhalten sich unverändert — tmux-Session anlegen, kein
   Agent starten.
2. Vendor der Session auflösen; ohne passenden Provider schlägt der Start mit
   einer benannten Meldung fehl, statt eine leere tmux-Session zu hinterlassen.
3. `exec.LookPath(provider.Binary())`. Fehlt das Binary, wird die eben
   angelegte tmux-Session wieder beendet und der Start scheitert mit
   `„<vendor> ist nicht installiert (<binary> nicht im PATH)"`.
4. `provider.StartCommand(session, run, mode)` liefert die Zeile; das Senden
   über `send-keys` literal plus `Enter` bleibt unverändert.

Der Aufruf `session.AgentRun(AgentVendorClaude)` in `core/lifecycle.go:1650`
wird zu `session.AgentRun(session.Vendor)`.

`DeliverInitial` übergibt statt der Konstante `AgentToolClaude` das
`provider.Tool()` der Session an `enqueuePrompt`. Die Signatur nimmt den
Parameter bereits entgegen; die Zustellsemantik dahinter bleibt in diesem
Teilprojekt unverändert.

## Vendor beim Anlegen

`SessionProvision` bekommt ein Feld `Vendor AgentVendor`. Leer bedeutet: den
konfigurierten Standard verwenden.

Der Standard liegt in der bestehenden Magentic-Konfiguration neben
`StatePath()`, als einzelnes Feld `default_vendor`. Fehlt es, ist der Standard
Claude. Damit bleibt das Verhalten für alle bestehenden Aufrufer
unverändert — insbesondere für `startSkillAgent` und die
Cleanup-/Merge-/Deploy-Sessions in `core/actions.go`, die weiterhin keinen
Vendor übergeben.

`CreateAgentSession` bekommt einen Vendor-Parameter und reicht ihn durch.
Ein unbekannter Vendor wird abgelehnt, nicht stillschweigend auf Claude
zurückgesetzt.

## Vendor wechseln

Neue Lifecycle-Operation `SwitchSessionVendor(ctx, sessionID, vendor)`, die
denselben Transitionsweg nimmt wie Stop und Start heute:

1. Unter der Session-Transitionssperre die Session frisch aus der Registry
   auflösen. Fehlt sie oder ist sie ein Terminal, schlägt der Wechsel fehl.
2. Zielprovider auflösen und sein Binary prüfen, **bevor** irgendetwas
   beendet wird. Ein fehlendes Binary lässt die laufende Session unberührt.
3. Ist der Zielvendor der aktuelle, ist der Wechsel ein No-op mit Erfolg.
4. Runtime stoppen.
5. `Vendor` in der Registry setzen. `AgentRuns` bleiben vollständig erhalten.
6. Runtime starten. Existiert bereits eine `AgentRunRef` des Zielvendors,
   ist der Modus `resume`, sonst `new`.

Der gewünschte Zustand der Session bleibt über den gesamten Vorgang
`SessionDesiredRunning`. Bricht Schritt 6 ab, bleibt die Session mit dem neuen
Vendor und ohne Runtime zurück — das ist der reguläre, bereits abgedeckte
Fall einer beendeten Session, den die Rekonvergenz aufgreift.

## Oberfläche

- Der Neue-Session-Dialog bekommt eine Vendor-Auswahl mit vier Einträgen,
  vorbelegt mit `default_vendor`. Vendors, deren Binary nicht im PATH liegt,
  erscheinen deaktiviert mit dem Hinweis, dass sie nicht installiert sind.
- Die Session-Kopfzeile bekommt neben den bestehenden Aktionen einen Wechsel
  auf einen anderen Agenten, mit Rückfrage, weil er den laufenden Prozess
  beendet.
- Die Overview-Nutzlast trägt den Vendor der Session. Das Frontend braucht
  dafür keinen neuen Auflösungsweg: `sessionToolCandidates` in
  `app/frontend/src/session-tool.js` liest `session.provider` bereits.
- Zwei neue Wails-Bindungen: der erweiterte `CreateAgentSession` und
  `SwitchSessionVendor`. Der generierte Vertrag wird neu erzeugt.

## Umsetzungsschritte

**Schritt 0 — Capture-Runde.** Gemeinsam, vor dem ersten Adapter. Für Codex,
Gemini und Copilot je einmal `--help` aufnehmen und die Frage beantworten:
Gibt es einen Resume-Befehl, nimmt er eine Run-ID entgegen, und akzeptiert der
Vendor eine vom Aufrufer vorgegebene Identität? Das Ergebnis legt für jeden
Adapter Startzeile und `NewRunID` fest. Dieselbe Runde nimmt Pane-Inhalte je
Zustand auf; die dienen Teilprojekt 2 und werden hier nur abgelegt.

Danach: Provider-Modul mit Claude-Adapter und unverändertem Verhalten, dann
die drei weiteren Adapter, dann `Vendor` an der Session samt Migration, dann
Start über den Provider, dann der Wechsel, zuletzt die Oberfläche.

## Tests

- Startzeile je Vendor und Modus, tabellengetrieben, gegen die in Schritt 0
  belegten Formen — inklusive `new` mit und ohne vorhandene Run-Ref.
- `DetectAgentTool` über die Provider-Registry: die bestehende Tabelle in
  `core/provider_test.go` muss unverändert grün bleiben, einschließlich
  neutralem Ergebnis für `node` und Terminal-Vorrang.
- Fehlendes Binary: Start scheitert, es bleibt keine tmux-Session zurück.
- Registry-Migration: eine gespeicherte Session ohne `Vendor` wird als Claude
  gelesen und behält ihre Run-Ref.
- Wechsel: genau ein Stop und ein Start, `AgentRuns` beider Vendors bleiben
  erhalten, Zielvendor mit vorhandener Ref startet im Resume-Modus.
- Wechsel auf einen Vendor ohne Binary lässt die laufende Session unberührt.
- Provisionierung ohne Vendor-Angabe erzeugt weiterhin eine Claude-Session
  mit vorgegebener Run-ID.
