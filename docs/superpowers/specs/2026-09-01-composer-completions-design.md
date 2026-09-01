# Composer-Completions und Auflösung des doppelten Eingabefeldes

Stand: 2026-09-01 · Branch `feature/multi-provider-agent-runtime`

## Ausgangslage

Eine Agentensession nimmt Text an zwei Stellen entgegen: in der Prompt-Zeile,
die der Agent selbst in den tmux-Pane zeichnet, und im Composer der Anwendung
darunter. Im Pane bietet Claude Code beim Tippen von `@` und `/` eigene
Auswahllisten an, im Composer nicht. Wer eine Datei referenzieren oder einen
Skill starten will, muss deshalb in den Pane wechseln, und der Composer bleibt
das schwächere der beiden Felder.

### Was der Spike geklärt hat

Ein Verdacht aus der Vorüberlegung hat sich als falsch erwiesen und ist damit
kein Teil dieses Entwurfs mehr. Getestet wurden in einer eigenen Claude-Session
vier Zustellwege: literale Tastenanschläge, Bracketed Paste, beides jeweils mit
Pause und unmittelbar hintereinander, dazu eine `@`-Referenz mit angehängtem
Satz.

Alle vier funktionieren. Das Auswahlmenü des Agenten öffnet sich zwar, aber der
vollständig getippte Name steht darin an erster Stelle, und das folgende Enter
schickt ab, statt einen falschen Eintrag zu übernehmen. Auch ein mehrdeutiges
Präfix war unkritisch: bei vollständig getipptem `/context` stand `/context`
oben.

**Die Zustellung braucht keine Änderung.** `sendPromptLiteralValidated` in
`core/actions.go` bleibt, wie sie ist.

### Was daraus folgt

Die Richtigkeit der Vorschlagsliste ist unkritisch. Der Composer tippt nur
Text; aufgelöst wird er vom Agenten. Eine veraltete oder unvollständige Liste
führt zu demselben Ergebnis wie eine Fehleingabe von Hand — der Agent zeigt
nichts an oder meldet den Befehl als unbekannt. Die Liste muss also gut genug
zum Tippen sein, nicht vollständig.

## Ziele

1. Der Composer bietet beim Tippen von `@` Dateien aus dem Worktree der Session
   an und beim Tippen von `/` die Befehle und Skills des jeweiligen Vendors.
2. Die Liste richtet sich nach dem Vendor der Session, nicht nach Claude als
   Annahme.
3. Solange die Session nicht auf eine Rückfrage wartet, ist nur noch ein
   Eingabefeld sichtbar.

## Nicht-Ziele

- Kein Ersatz für die Interaktion im Pane. Bestätigungen, Abbrechen, `shift+tab`
  und das Agents-Menü laufen weiter über das Terminal.
- Keine Vollständigkeit gegenüber der Liste des Agenten. Eingebaute Befehle und
  über MCP eingebrachte Einträge stehen nicht auf der Platte; was wir nicht
  finden, fehlt in der Liste und bleibt trotzdem tippbar.
- Kein Vorschlagswesen im Pane selbst.

## Teil 1 — Dateien pro Session

### Quelle

`Session.Dir` (`core/state.go:102`) ist das Arbeitsverzeichnis, `Session.Worktree`
sagt, ob es ein Git-Worktree ist. Die Liste entsteht aus zwei Aufrufen in diesem
Verzeichnis:

```
git ls-files -z
git ls-files -z --others --exclude-standard
```

Damit sind versionierte und noch nicht versionierte Dateien enthalten, und
`.gitignore` gilt ohne eigene Implementierung. Ist das Verzeichnis kein
Git-Repository, tritt ein begrenzter Verzeichnisdurchlauf an die Stelle, der bei
einer festen Zahl von Einträgen abbricht.

### Schnittstelle

Neu in `core/completions.go`:

```go
// WorktreeFiles liefert die zum Präfix passenden Pfade einer Session,
// relativ zu ihrem Arbeitsverzeichnis und auf limit Einträge gedeckelt.
func WorktreeFiles(session Session, query string, limit int) ([]string, error)
```

Gefiltert und gedeckelt wird in Go. Ein Monorepo mit sechsstelliger Dateizahl
darf die Oberfläche nicht erreichen. Die Reihenfolge ist: exakter Präfixtreffer
auf dem Dateinamen, dann Präfixtreffer auf dem Pfad, dann Teilstring, jeweils
nach Pfadlänge aufsteigend.

Ergebnisse werden pro Session zwischengespeichert und verfallen nach zwei
Sekunden, damit das Tippen keine Prozesskette pro Anschlag auslöst. `limit` ist
von der Bindung her 50 — mehr als das liest niemand in einem Popover.

### Bindung

`app/app.go` bekommt eine Methode, die die Session auflöst und durchreicht:

```go
func (a *App) CompleteFiles(sessionID, query string) ([]string, error)
```

## Teil 2 — Befehle pro Vendor

### Quelle

Für Claude, verifiziert auf dieser Maschine:

| Ort | Form | Ergebnis |
|---|---|---|
| `~/.claude/commands/*.md` | Frontmatter `description:` | `/<dateiname>` |
| `~/.claude/skills/<name>/SKILL.md` | Frontmatter `name:`, `description:` | `/<name>` |
| `<dir>/.claude/commands`, `<dir>/.claude/skills` | wie oben | projektlokal, überschreibt global |
| `~/.claude/plugins/cache/<owner>/<plugin>/<version>/skills/<name>/SKILL.md` | wie oben | `/<plugin>:<name>` |

Für Codex, Gemini und Copilot gilt dieselbe Struktur mit anderen Pfaden. Ein
Vendor ohne bekannte Quelle liefert eine leere Liste; der Composer zeigt dann
kein `/`-Menü und verhält sich wie heute.

### Schnittstelle

Bewusst **nicht** als weitere Methode an `AgentProvider`. Das Interface in
`core/provider.go` wird gerade parallel bearbeitet, und sein Kommentar zieht die
Grenze bei der Kommandozeile des Vendors. Stattdessen ein eigener, kleiner
Adapter in `core/completions.go`, der nur über den Vendor-Wert verbunden ist:

```go
type SlashCommand struct {
    Name        string `json:"name"`        // ohne führenden Schrägstrich
    Description string `json:"description"`
    Source      string `json:"source"`      // "user", "project", "plugin"
}

// SlashCommands liefert die Befehle und Skills eines Vendors für ein
// Arbeitsverzeichnis. Ein unbekannter Vendor liefert nil ohne Fehler.
func SlashCommands(vendor AgentVendor, dir string) []SlashCommand
```

Das hält die Konfliktfläche zur laufenden Umbauarbeit klein und lässt sich
später zusammenführen, falls `AgentProvider` ohnehin breiter wird.

### Bindung

```go
func (a *App) CompleteCommands(sessionID, query string) ([]SlashCommand, error)
```

## Teil 3 — Ein sichtbares Eingabefeld

### Mechanik

Die Prompt-Zeile des Agenten lässt sich nicht abschalten, sie gehört ihm. Sie
wird verdeckt: der Composer legt sich mit `--term-bg` über die untersten Zeilen
des Terminals.

Die Höhe wird gemessen, nicht geraten. Der Frontend liest `term.buffer.active`
von unten nach oben und sucht die letzte Zeile, die auf das Prompt-Muster des
Vendors passt. Verdeckt wird von einer Zeile darüber bis zum unteren Rand.

Das Muster kommt aus Go, damit Vendor-Wissen an einer Stelle bleibt — aus
demselben Adapter wie die Befehle:

```go
// PromptLinePattern beschreibt die Eingabezeile eines Vendors als Präfix der
// getrimmten Pufferzeile. Ein leerer Wert bedeutet: nicht verdecken.
func PromptLinePattern(vendor AgentVendor) string
```

Für Claude ist das `❯`. Ausgeliefert wird der Wert mit dem übrigen
Sessionzustand, den das Frontend ohnehin je Session bezieht.

**Fehlt das Muster, wird nichts verdeckt.** Inhalt zu verstecken, den wir nicht
sicher identifiziert haben, wäre schlimmer als ein doppeltes Feld.

### Wann wieder aufgedeckt wird

- `status === 'blocked'` — die Session wartet auf eine Entscheidung, und die
  steht genau in dem Bereich, der sonst verdeckt ist. `updateTermComposer`
  (`app/frontend/src/main.js:422`) führt diesen Zustand bereits.
- Das Terminal hat den Fokus. Wer bewusst in den Pane klickt, will ihn ganz
  sehen.

## Teil 4 — Die Oberfläche im Composer

### Auslöser

Ein Menü öffnet sich, wenn an der Schreibmarke ein Auslösezeichen an einer
Wortgrenze steht:

- `@` an beliebiger Stelle → Dateien.
- `/` **nur als erstes Zeichen der Nachricht** → Befehle. Claude Code deutet
  einen Schrägstrich auch nur dort als Befehl.

Getippt wird weiter im Textfeld; die Eingabe hinter dem Auslöser ist die
Suchanfrage. Ein Leerzeichen oder das Löschen des Auslösers schließt das Menü.

### Bedienung

Pfeiltasten wählen, Enter und Tab übernehmen, Escape schließt, ein Klick
übernimmt ebenfalls. Beides muss gleichwertig funktionieren; laut PRODUCT.md
darf die Anwendung keine Tastaturkenntnis voraussetzen.

Bei geschlossenem Menü behält Enter seine heutige Bedeutung. Übernehmen
schreibt nur Text in das Feld und schickt nie ab — Absenden bleibt eine
getrennte Entscheidung.

### Darstellung

Ein Popover über dem Textfeld, verankert am Composer, mit `--popover-shadow`
und `--radius-panel`. Pro Zeile Name und Beschreibung, die Beschreibung in
`--ink-2`. Keine Statuspillen, keine Eckenhinweise.

Die Liste ist auf zehn sichtbare Einträge begrenzt und scrollt. Ohne Treffer
schließt das Menü, statt eine leere Fläche zu zeigen.

## Fehlerfälle

| Fall | Verhalten |
|---|---|
| `git` fehlt oder das Verzeichnis ist kein Repository | begrenzter Verzeichnisdurchlauf |
| Verzeichnis nicht lesbar | leere Liste, kein Menü, keine Fehlermeldung im Weg |
| Vendor ohne bekannte Befehlsquelle | kein `/`-Menü |
| Session beendet | Composer ist ohnehin deaktiviert, kein Menü |
| Prompt-Muster im Puffer nicht gefunden | nichts wird verdeckt |

Keiner dieser Fälle darf das Tippen blockieren. Die Completions sind eine
Hilfe, kein Tor.

## Tests

**Go, `core/completions_test.go`:**

- `WorktreeFiles` in einem angelegten Repository mit versionierten, nicht
  versionierten und ignorierten Dateien: ignorierte fehlen, nicht versionierte
  sind da.
- Ohne Git-Repository greift der Durchlauf und respektiert das Limit.
- Sortierung: Namenstreffer vor Pfadtreffer vor Teilstring.
- `SlashCommands` gegen ein angelegtes Verzeichnis mit Command-Datei, Skill mit
  Frontmatter und Plugin-Skill: Namen und Beschreibungen stimmen, ein
  projektlokaler Eintrag verdrängt den gleichnamigen globalen.
- Unbekannter Vendor liefert nil ohne Fehler.

**Frontend:** Auslösererkennung und Übernahme sind reine Textfunktionen und
werden als solche geprüft — Auslöser am Wortanfang, `/` nur an Position 0,
Einfügen an der richtigen Stelle, Menü schließt bei Leerzeichen.

Die Verdeckung wird von Hand geprüft, gegen eine laufende Session in beiden
Zuständen.

## Risiken

**Die Vendor-Schicht wird parallel umgebaut.** `core/provider.go` war während
der Vorüberlegung zeitweise gelöscht und ist inzwischen wieder da. Der eigene
Adapter in `core/completions.go` ist die Antwort darauf: er berührt
`AgentProvider` nicht.

**Die Liste driftet gegen die des Agenten.** Eingebaute Befehle und
MCP-Einträge fehlen. Das ist hingenommen, siehe Nicht-Ziele; die Zustellung
funktioniert unabhängig davon.

**Die Verdeckung ist die unsicherste Stelle.** Sie hängt an einem Muster im
Terminalpuffer. Sie ist fail-open ausgelegt und der einzige Teil, der ohne die
anderen beiden ausgeliefert werden könnte, falls sie sich in der Praxis als zu
unruhig erweist.

## Reihenfolge

1. `core/completions.go` mit Tests — Dateien und Befehle, ohne Oberfläche.
2. Wails-Bindungen und das Popover im Composer.
3. Die Verdeckung der Prompt-Zeile.

Nach Schritt 2 ist der Nutzen bereits vollständig da; Schritt 3 ist die
Kosmetik, die das doppelte Feld auflöst.
