# magentic

Verwaltung von Claude-Code-Sessions über mehrere Projekte hinweg. Jeder Agent läuft in einer eigenen tmux-Session und überlebt damit das Schließen der UI.

Zwei Oberflächen, eine gemeinsame Logik (`core/`):

- **TUI** (`magentic`) — schnelle Übersicht und Verwaltung im Terminal.
- **Desktop-App** (`app/`, Wails) — Übersicht mit Token-Limits, Aktionen
  und **echtem eingebetteten Terminal** (xterm.js): natives Markieren, klickbare
  Links, natives Scrollen. Dazu Git-Graph, Spec-Board, Statistik und ein
  Terminal-Dock mit Tabs.

```
 ⚡ magentic                                  ● 1 läuft   ◆ 1 wartet   ○ 2 idle
╭──────────────────────────────────╮╭ Details ──────────────────────────────╮
│ ▾ devpilot                   ± 3 ││ hera · devpilot                       │
│   ● hera    läuft   12m  ±       ││ ● läuft · seit 12m                    │
│   ◆ atlas   wartet   3m  ✓ ⑂     ││ Git: main  ± 2 geändert               │
│   ○ nyx     idle    45m  ✓       ││ Worktrees · Terminal-Preview …        │
│ ▸ reqpilot                   ✓ 1 ││                                       │
╰──────────────────────────────────╯╰───────────────────────────────────────╯
 n neu · w neu+worktree · ⏎ attach · x kill · q ende
```

## Voraussetzungen

- tmux
- claude (Claude Code CLI)

## Nutzung

```sh
magentic                   # TUI starten
magentic add [pfad]        # Projekt registrieren (Default: aktuelles Verzeichnis)
magentic agents            # Status-Manifeste prüfen und ihre Quelle nennen
magentic hooks install     # Claude-Code-Hooks für Status-Meldungen einrichten
open app/build/bin/magentic.app   # Desktop-App
./start.sh                         # Desktop-App bequem starten
```

## Tasten (TUI)

| Taste | Aktion |
|---|---|
| `n` | Neue Claude-Session im Projektverzeichnis (Name leer = auto) |
| `w` | Neue Claude-Session in frischem Git-Worktree (Branch `agent/<name>`) |
| `T` | Neues **Terminal** — reine Shell, kein Claude. Ist ein Agent ausgewählt, öffnet es in *dessen* Verzeichnis (also im Worktree), sonst im Projektverzeichnis |
| `⏎` / `a` | Agent: an die tmux-Session attachen (zurück mit `ctrl-b d`) · Projekt: auf-/zuklappen |
| `d` | `/done` an die Session des gewählten Agents senden |
| `D` | `/deploy` senden (ohne Agent: neue Session im Projekt-Root) |
| `z` | Zeitgeist-Timer starten (fragt Projekt) bzw. stoppen (fragt optionale Notiz) |
| `Z` | Zeitgeist-Timer pausieren / fortsetzen |
| `r` | Agent umbenennen |
| `x` | Agent beenden (Worktree bleibt bestehen) / Projekt entfernen |
| `p` | Projekt hinzufügen |
| `j`/`k` / `↑`/`↓` | Navigieren |
| `g` | Sofort aktualisieren |
| `q` | Beenden (Agents laufen weiter) |

Maus: Klick links wählt aus (Projekt-Klick klappt auf/zu), zweiter Klick auf
einen Agent attacht, Scrollrad navigiert.

## Desktop-App

Die App (Wails, Go + xterm.js) zeigt links Sessions **nach Projekt gruppiert**,
unten die Claude-Limits (5h/7d). Die Übersicht enthält Projekt-Karten mit
Worktree-Zeilen (ahead/behind, Git-Status, Warnungen), Agent-Pills und alle
Aktionen:

- **⌨** — Terminal zur Session öffnen (echtes PTY-Attach, natives
  Markieren/Kopieren, klickbare Links, Scrollback)
- **✓ done** — schickt `/done` an die laufende Session
- **🔀 branch → main** — Claude-Session, die den Branch merged
- **✨ Cleanup** — Claude-Session im verwaisten Worktree: sichten, committen, mergen
- **⌫ entfernen** — `git worktree remove` (nur sauber + ohne aktive Session)
- **🚀 deploy** — Claude-Session mit `/deploy`
- **+ Session / ⑂ Worktree** — neue Claude-Session im Projekt
- **⌨ Terminal** — neue Session mit reiner Shell statt Claude (auch über
  ⇧-Klick auf das `+` in der Sidebar und in der Hydra-Leiste)

### Sidebar

Jede Session bekommt einen aus ihrem Namen erzeugten **Roboter-Avatar** — immer
derselbe Roboter für denselben Namen, damit Sessions auf einen Blick
auseinanderzuhalten sind. Daneben steht der **Branch** (bzw. der Worktree, in
dem die Session läuft).

Zwei Marker zeigen, wo du gebraucht wirst:

- **`!` mit gelbem Rand** — die Session wartet auf deine Eingabe
  (Permission-Dialog, Rückfrage). Solche Sessions stehen innerhalb ihres
  Projekts oben.
- **blauer Punkt** — die Session ist durchgelaufen, seit du zuletzt
  hingeschaut hast. Der Punkt verschwindet, sobald du die Session öffnest;
  magentic merkt sich pro Session, wann du sie zuletzt gesehen hast
  (`seen_at` im State).

Wartet mindestens eine Session, erscheint oben ein Banner („2 Sessions warten
auf dich"), das per Klick zur ersten davon springt. Für bloß Ungelesenes gibt
es keins — das zeigt der Punkt an der Session selbst schon deutlich genug.

### Weitere Ansichten

- **Suche** — durchsucht normalisierte Prompts und Antworten aller unterstützten
  Coding-Agent-Anbieter. Treffer behalten Anbieter und bekannte
  Projektzuordnung; eine unbekannte Zuordnung wird ausdrücklich so angezeigt.
- **Verlauf** — führt die eigenen Prompts der letzten sieben Tage aus den
  lokalen Sessions von **Claude Code, Codex, Gemini CLI und GitHub Copilot**
  chronologisch zusammen. Quelle und Projekt stehen direkt am Eintrag; bekannte
  magentic-Sessions lassen sich weiterhin per Klick öffnen. Codex berücksichtigt
  dabei auch ein abweichendes `CODEX_HOME`.
- **Git-Graph** (`⌘G`, oder Button auf der Projektkarte) — echter Lane-Graph
  über alle Branches eines Projekts. Zeigt, wo Worktrees abzweigen und wo sie
  wieder zusammenlaufen, mit ahead/behind zum Hauptbranch, „merged"-Badges und
  den Avataren der Sessions, die gerade auf einem Commit sitzen.
- **Board** (`⌘B`) — Kanban-Ansicht aus den Spec-Ordnern des Projekts. Erkennt
  OpenSpec (`openspec/changes/*`), Spec-Kit (`specs/<NNN-name>/*`), Kiro
  (`.kiro/specs/*`) und Agent OS (`.agent-os/specs/*`) — **alle gleichzeitig**,
  nicht nur das erste gefundene. Liegen mehrere nebeneinander, landen sie in
  einem Board; die Chips im Kopf zeigen je Quelle die Anzahl und blenden sie per
  Klick aus. Liest die Checkboxen aus `tasks.md` und verteilt die Changes auf
  Geplant / In Arbeit / Zur Abnahme / Erledigt. Karten, an denen gerade eine
  Session arbeitet, sind hervorgehoben; „hieran arbeiten" startet eine
  Claude-Session mit dem passenden Auftrag.
- **Statistik** (`⌘⇧S`) — Aktivität, Tokens, bekannte Kosten, Cache-Quote,
  Commits, Projekte, Modelle und eine Heatmap Wochentag × Stunde. Sie nutzt
  denselben normalisierten WorkHistory-Index wie der Verlauf und zeigt für
  Claude Code, Codex, Gemini CLI und GitHub Copilot, ob die jeweilige Quelle
  vollständig, teilweise oder gar nicht lesbar war. Der Index liegt privat
  neben dem State unter `work-history/index.json` und wird inkrementell
  aktualisiert.

  Alle Zahlen beziehen sich ausschließlich auf die eigene Arbeit:
  - **Prompts, Turns, Sessions und Tokens** stammen aus den lokalen Verläufen
    der unterstützten Coding-Agent-Anbieter. Provider-Adapter normalisieren
    Rollen, Lineage und Usage-Fakten; fehlende Token-Fakten bleiben unbekannt
    und werden nicht als Null erfunden.
  - **Commits** zählt nur, was unter der git-Identität des jeweiligen
    Repositories (`user.email` / `user.name`) steht. In geteilten Repos stammt
    sonst der Großteil der Commits von Kolleginnen und Kollegen. Fehlende
    Identität oder ein nicht lesbarer Verlauf erscheinen als unvollständige
    Commit-Abdeckung, nie als exakte Null.

  Zwei Zahlen brauchen zusätzlich Einordnung — in der App steht sie als
  Tooltip an der jeweiligen Kachel:
  - **Tokens** enthält Cache-Read, und bei jedem Turn wird der komplette
    bisherige Kontext erneut gelesen. Deshalb stehen dort schnell Milliarden;
    das ist kein zusätzlicher Verbrauch. Die Unterzeile „ohne Cache" zeigt die
    tatsächlich neu verarbeiteten Tokens.
  - **Kosten** werden nur für erkannte Claude-Modelle aus den hinterlegten
    API-Listenpreisen hochgerechnet. Nutzung anderer oder unbekannter Modelle
    bleibt ausdrücklich unbepreist; bei gemischter Nutzung zeigt die App nur
    den bekannten Teilbetrag. Mit einem Max-Abo zahlst du diesen Betrag nicht.
- **Terminal-Dock** (`⌃\``) — Panel über die volle Fensterbreite unterhalb von
  Sidebar und Hauptbereich, mit mehreren Tabs, per Drag in der Höhe
  verstellbar. Offene Tabs und Höhe überleben den Neustart.

  Dock-Terminals sind Werkzeug, keine Sitzung: sie laufen unter `kind: "dock"`,
  erscheinen **nicht** in der Sitzungsliste, den Projektkarten oder den
  Zählern der Übersicht, und werden beim Schließen des Tabs auch wirklich
  beendet — sonst liefe die tmux-Session unsichtbar weiter. Wer ein Terminal
  will, das in der Liste bleibt, nimmt weiterhin `⌘T` oder den
  Terminal-Knopf auf der Projektkarte.

### Pausen

Kein Timer zum Starten und Stoppen — magentic erkennt selbst, wie lange du
schon durcharbeitest, und schlägt eine Pause im richtigen Moment vor.

Als Aktivität zählt, wenn du in magentic arbeitest oder in einer Session tippst.
Dass die Agents rechnen, zählt ausdrücklich **nicht** — sonst liefe die Uhr
weiter, während du längst weg bist. Bleibst du einige Minuten still, gilt das
rückwirkend als Pause; du musst also nichts drücken, wenn du einfach aufstehst.

Der Vorschlag kommt bevorzugt dann, wenn **alle Sessions gerade rechnen** und
keine auf dich wartet — genau der Moment, in dem eine Pause nichts blockiert.
Wartet dagegen eine Session auf deine Eingabe, hält magentic sich zurück, bis
das erledigt ist. Wird es deutlich überfällig, meldet sich die App auch per
Desktop-Benachrichtigung; die erreicht dich auch dann, wenn du gerade
woanders bist.

**Die Erinnerung gibt nicht nach.** Eine Benachrichtigung ist nach fünf
Sekunden weg und die Pause damit wieder vergessen — deshalb meldet sich
magentic alle 8 Minuten erneut und wird dabei hartnäckiger:

1. Beim ersten Mal eine Benachrichtigung, das Dock-Icon hüpft einmal.
2. Beim zweiten Mal hüpft es **so lange, bis du reagierst**
   (`NSCriticalRequest`) — das überlebt auch einen Blick in den Browser.
3. Ab dem dritten Mal holt sich das Fenster den Vordergrund — aber nur, wenn
   ohnehin alle Agents rechnen. Mitten im Tippen drängelt es nicht.

Sobald du die Pause nimmst oder vertagst, hört es sofort auf.

Die Pause selbst gibt bewusst **fast nichts zu lesen**. Reddit ist keine
Erholung, weil der Kopf sofort weiterliest — eine Pausenansicht voller Text
wäre dasselbe in kleiner. Groß und deutlich steht deshalb nur eins da:
**steh auf**. Alle Impulse zielen darauf — ein paar Schritte gehen, strecken,
Wasser holen, ans Fenster stellen. Nach wenigen Sekunden blendet auch dieser
Text weg; übrig bleibt ein atmender Kreis mit einem einzigen Wort (4 s ein,
4 s halten, 6 s aus), falls du sitzen bleibst. Kein Ziffern-Countdown, keine
Statistik, keine Bilanz.

Am Ende der Pause kommt eine Benachrichtigung. Du kannst also wegschauen,
die Augen zumachen oder aus dem Fenster sehen, ohne auf die Uhr zu achten.
`Esc` beendet jederzeit; wartet eine Session auf dich, steht das drin.

**Wann die nächste Pause fällig ist**, steht am Sidebar-Eintrag „Pause":
`54 min` bis dahin, `fällig` sobald es so weit ist (gelb bzw. orange, wenn es
überfällig wird), `läuft` während einer Pause. Der Indikator unten rechts
zeigt dieselbe Restzeit, taucht aber erst auf, wenn der erste Hinweis fällig
wird — die Sidebar-Zeile ist immer da.

**Einstellen** lässt sich das über das Zahnrad am Sidebar-Eintrag „Pause"
(oder über das Zahnrad in der Pausenansicht selbst), gruppiert nach den zwei
Fragen, um die es geht:

| | Standard | |
|---|---|---|
| **Wie lange** — eine Pause dauert | 5 min | ist auch die Vorauswahl in der Pausenansicht |
| **Wie oft** — leiser Hinweis nach | 40 min | ununterbrochener Arbeit |
| **Wie oft** — Pause fällig nach | 55 min | ab hier kommt die Benachrichtigung |
| **Wie oft** — hartnäckig ab | 80 min | ab hier auch mitten in der Arbeit |
| „Später" verschiebt um | 10 min | |
| Zählt als Pause ab | 4 min | Ruhe, die als Pause durchgeht |
| Abwesenheit erkannt nach | 6 min | ohne Aktivität endet der Arbeitsblock |

Die in der Pausenansicht gewählte Länge (2 / 5 / 10 Minuten oder der eigene
Wert) wird zugleich die neue Voreinstellung. Alles liegt in
`~/.config/magentic/breaks.json`, getrennt von der übrigen Konfiguration.

**Tasten in der App:** `⌘0` Übersicht · `⌘1`–`⌘9` Session aus der Sidebar ·
`⌘G` Git-Graph · `⌘B` Board · `⌘⇧S` Statistik · `⌃\`` Terminal-Dock ·
`Esc` beendet die Pausenansicht ·
`⌘W` zurück zur Übersicht · `⌘⇧W` Session für später schließen · **`⌘T` Terminal im
Verzeichnis der gerade offenen Session** (funktioniert auch, wenn der Fokus im
Terminal liegt — Claude bekommt die Kombo nicht zu sehen).
- **Zeitgeist** — Widget links unten: Timer starten/pausieren/stoppen,
  laufende Zeit + Verdienst live, Tagessumme

**Hauptbranch pro Projekt:** ahead/behind, Warnungen und Merge-Ziele beziehen
sich auf den konfigurierbaren Hauptbranch (✎ neben dem Projektnamen; leer =
automatisch der Branch des Haupt-Worktrees).

App bauen:

```sh
cd app && wails build          # → app/build/bin/magentic.app
```

## Git pro Session

Die Git-Anzeige eines Agents zeigt nur, was **seit Start dieser Session**
passiert ist: Beim Erstellen wird eine Baseline (HEAD + bereits geänderte
Dateien) gespeichert; angezeigt werden nur neue Commits und Dateien, die die
Session selbst angefasst hat. Adoptierte Sessions ohne Baseline zeigen den
Gesamtstatus. Die Projektzeile zeigt weiterhin den Repo-Gesamtzustand.

## Status

- `●` läuft — der Agent arbeitet gerade
- `◆` wartet — der Agent braucht Input (Permission-Dialog, Frage, Trust-Prompt)
- `✓` fertig — der Agent hat seine Runde beendet und du hast das Ergebnis noch nicht gesehen
- `○` idle — bereit für den nächsten Prompt
- `⌨` Terminal — reine Shell-Session, dort läuft kein Agent
- `▪` beendet — der Agent wurde beendet, die Shell läuft noch
- `✗` tot — tmux-Session existiert nicht mehr (mit `x` entfernen)
- `?` unbekannt — Magentic kann den Bildschirm nicht deuten

Unbekannt wird nie zu idle, fertig oder tot geschönt, und in eine Session mit
unbekanntem Status tippt Magentic nichts hinein. Das ist eine
Sicherheitseigenschaft: ein falsches „idle" würde eine wartende Nachricht in
einen offenen Dialog schreiben.

### Status-Manifeste

Welche Bildschirmzeile welchen Status bedeutet, steht nicht im Go-Code, sondern
in einem Manifest pro Agent-Art. Magentic bringt Manifeste für Claude Code,
Codex, GitHub Copilot und Gemini CLI mit (`core/agents/*.yaml`, in die Binary
eingebettet). Eigene Manifeste liegen in `~/.config/magentic/agents/*.yaml`;
eine Datei dort ersetzt eine mitgelieferte Agent-Art mit derselben `kind`
vollständig oder bringt eine neue hinzu.

```sh
magentic agents   # jedes Manifest mit Agent-Art, Quelle und Ablehnungsgrund
```

Ein Manifest beschreibt genau eine Agent-Art:

```yaml
kind: acme                  # stabile Kennung, einmalig pro Quelle
label: Acme Agent           # Klartext für die Oberfläche
tool: acme                  # Identität für die Entwickler-Icons
observed_version: "3.1.4"   # an welchem Build die Regeln aufgenommen wurden
screens_recorded: true      # false, solange niemand die Bildschirme gesehen hat
tail: 25                    # wie viele Zeilen vom Ende gelesen werden (max. 200)

pane_commands:              # woran tmux die Agent-Art erkennt
  - regex: '^acme(-.*)?$'
  - literal: 'acme-cli'

states:                     # nur working, blocked, done, idle
  working:
    - literal: 'esc to interrupt'
  blocked:
    - literal: 'do you want'
  idle:
    - literal: 'ask acme anything'

composer:                   # woran die eigene Eingabezeile zu sehen ist
  - literal: 'ask acme anything'

details:
  blocked:
    - label: Shell-Freigabe
      patterns:
        - literal: 'run this command'
  working:
    - capture: '(?i)waiting for (\d+) helpers'
      singular: 'wartet auf %d Helfer'
      plural: 'wartet auf %d Helfer'
```

Die Reihenfolge der Auswertung steht im Format, nicht in der Datei: erst
`working`, dann `blocked`, dann `done`, dann `idle`; innerhalb eines Zustands
gewinnt das erste Muster in Dateireihenfolge. Ein Muster ist entweder ein
`literal` (Teilzeichenkette, Groß- und Kleinschreibung egal) oder ein `regex`.
Ein Detail ändert nie den Status. Wer keine `done`-Muster nennt — die meisten
Agents haben keinen eigenen Fertig-Bildschirm — bekommt `done` daraus
abgeleitet, dass der ruhende Bildschirm seit dem letzten Blick neu ist.

Ein ungültiges Manifest wird mit Begründung abgelehnt; die mitgelieferte
Agent-Art bleibt dann in Kraft. Eine Agent-Art, für die nur ein kaputtes
Manifest existiert, bleibt unbekannt statt idle. Gemini CLI ist ein
mitgeliefertes Beispiel für `screens_recorded: false`: startbar und
verwaltbar, aber ohne gedeuteten Status, bis jemand seine Bildschirme
aufnimmt.

### Status-Meldungen aus Claude Code

Wo ein Agent seinen eigenen Lebenszyklus melden kann, muss Magentic ihn nicht
vom Bildschirm raten. Claude Code kann das über seine Hooks:

```sh
magentic hooks install     # sagt vorher, was wohin geschrieben wird
magentic hooks uninstall   # entfernt nur die von Magentic geschriebenen Hooks
```

Installiert wird in `~/.claude/settings.json`; eigene Hook-Definitionen bleiben
unangetastet, und ein zweiter Lauf ändert nichts. Die Hooks hängen eine
JSON-Zeile an `~/.config/magentic/hook-reports.jsonl` (nur für den eigenen
Benutzer lesbar), die Magentic beim nächsten Blick einliest. Eine Meldung gilt
60 Sekunden lang; danach zählt wieder der Bildschirm. Ohne Hooks ändert sich
nichts: jede Session wird weiter über ihr Manifest gedeutet.

Rechts daneben: `✓` Arbeitsverzeichnis sauber, `±` uncommitted Änderungen, `⑂` läuft im Worktree.

Agents sind pro Projekt nach Dringlichkeit sortiert: Wer auf Input wartet,
steht immer oben (dann laufend, idle, beendet, tot). Die Zeitspalte zeigt die
**letzte Aktivität** der Session (tmux `window_activity`), nicht ihr Alter —
vergessene Agents fallen so sofort auf.

## Benachrichtigungen

magentic schickt Desktop-Benachrichtigungen mit Ton (macOS `osascript`, Linux
`notify-send`), solange die TUI läuft: wenn ein Agent auf Eingabe wartet
(Permission-Dialog, Rückfrage — Sound „Glass") und wenn ein laufender Agent
fertig ist (Sound „Ping", mit einem Poll Verzögerung bestätigt, um Fehlalarme
zu vermeiden).

## Zeitgeist

Wenn der [Zeitgeist](../zeitgeist)-Zeittracker installiert ist
(`~/.zeitgeist/data.json` existiert), lässt sich der Timer direkt aus magentic
steuern — TUI (`z` / `Z`, Anzeige links unten und im Header) und Desktop-App
(Widget in der Sidebar). magentic liest und schreibt die Datendatei direkt;
die Zeitgeist-App und ihr Menüleisten-Timer bekommen Änderungen über ihren
File-Watcher sofort mit.

## Wie es funktioniert

### Architektur

Die gemeinsame Logik in `core/` ist entlang weniger tiefer Module organisiert:

- **Registry** hält Projects und Sessions unter stabilen IDs. Änderungen sind
  semantisch, prozessübergreifend koordiniert und atomar.
- **Session Lifecycle** schreibt DesiredState vor jeder Änderung an Worktree,
  Dateisystem oder tmux und gleicht partielle Ausführung idempotent ab.
- **Session Observation** liest tmux in einem zeitlich begrenzten Durchlauf und
  unterscheidet bekannte, partielle und nicht verfügbare Beobachtungen.
- **Repositories** besitzt die gemeinsame Bedeutung von Git- und
  Worktree-Zustand; fehlgeschlagene oder fehlerhafte Git-Antworten gelten nie als
  „sauber“. Desktop-Aktionen lösen opake WorktreeRefs frisch auf.
- **WorkHistory** normalisiert lokale Verläufe von Claude Code, Codex, Gemini
  CLI und GitHub Copilot einmal für Verlauf, Suche, Links und Statistik.
- **Specifications** adaptiert die unterstützten Spec-Layouts, vergibt stabile
  SpecificationRefs, hält physische Projektgrenzen ein und verbindet laufende
  Sessions nur über deren dauerhafte Referenz. Das Board scannt Archive nur
  explizit und begrenzt.
- **Attention** leitet Benachrichtigungen, Dock-Badge, native Aufmerksamkeit
  und Pauseneskalation deterministisch aus einer Observation und expliziten
  AttentionEvents ab; der Watcher führt nur noch diese Intents aus.

Die Domain-Sprache steht in `CONTEXT.md`, dauerhafte Entscheidungen in
`docs/adr/`. Formatabhängige Adapter bleiben privat hinter den Modulen;
historische Namensdaten überschreiten nur explizite, eng begrenzte
Migrationsgrenzen.

- Sessions erhalten standardmäßig eine tmux-Runtime mit Prefix `mgt-`; deren
  eigener `RuntimeName` bleibt auch nach einem Anzeigenamenwechsel die einzige
  Adresse. Von Hand erstellte Runtimes (`tmux new -s mgt-foo`) werden beim Start
  adoptiert und anhand des Verzeichnisses einem Projekt zugeordnet.
- Terminal-Sessions (`kind: "term"` im State) starten nur die Shell. Sie werden
  beim Neustart als Shell wiederhergestellt (nicht mit `claude --continue`),
  bekommen keine Claude-Statuserkennung und keine Benachrichtigungen;
  `/done` & Co. lehnen sie ab. Git-Änderungen pro Session zählt magentic
  trotzdem — committen im Terminal wird also mitgezählt.
- Die TUI liest alle Runtime-Fakten alle zwei Sekunden in genau einer
  Observation und alle Git-Fakten in einem Repositories-Durchlauf; partielle
  Abfragen bleiben sichtbar unbekannt.
- Worktrees landen unter `<projekt>-agents/<agentname>` neben dem Projektordner.
- Konfiguration und Agent-Registry: `~/.config/magentic/state.json`
- Gemeinsame Logik von TUI und App liegt in den tiefen Modulen unter `core/`:
  Registry, Session Lifecycle, Observation, Repositories, WorkHistory,
  Specifications, Attention sowie deren Projektionen für Overview, Git-Graph,
  Board und Statistik.
- Neue Sessions werden nach dem **Branch** benannt, auf dem das Verzeichnis
  steht — außer der Branch ist ein Integrationsbranch (`main`, `dev`,
  `master`, `develop`), dann greift der Projektname. Präfixe wie `agent/`
  oder `feature/` fallen weg.
- Das **Board** projiziert das Specifications-Modul; es besitzt keine zweite
  Datenhaltung und keinen eigenen Parser. Die privaten Source-Adapter prüfen
  jedes unterstützte Layout und physische Project-Containment. Laufende Arbeit
  wird ausschließlich über die persistierte `SpecificationRef` plus eine
  bekannte, lebende Observation zugeordnet. „Hieran arbeiten“ transportiert
  nur ein opakes Start-Token, das Specifications unmittelbar vor dem Start
  erneut auflöst.
- Die **Statistik** liest Provider-Aktivität aus demselben normalisierten
  WorkHistory-Index wie Verlauf, Suche und Links. Claude Code, Codex, Gemini CLI
  und GitHub Copilot werden durch private Adapter vereinheitlicht; unlesbare
  Quellen und nicht bepreisbare Modelle bleiben als teilweise oder unbekannt
  sichtbar. Git-Commits und Identität kommen über das Repositories-Modul.

## Build

**TUI:**

```sh
go build -o ~/.local/bin/magentic .
```

Nicht per `cp` über die bestehende Binary kopieren — macOS invalidiert dabei
die Code-Signatur und killt das Programm beim Start (`zsh: killed`). Entweder
direkt ins Ziel bauen (oben) oder vorher `rm ~/.local/bin/magentic`.

**Desktop-App:**

```sh
./scripts/build-app.sh
```

`build-app.sh` startet eine bereits laufende App am Ende automatisch neu —
sonst arbeitet man weiter mit der alten Version. Welcher Build gerade läuft,
steht im Tooltip des „magentic"-Schriftzugs oben links in der App.

**Autostart bei der Anmeldung:**

```sh
./scripts/autostart.sh       # einrichten
./scripts/autostart.sh off   # wieder entfernen
```

Legt einen LaunchAgent unter `~/Library/LaunchAgents/de.donhauser.magentic.plist`
an, der die gebaute App startet. Der Pfad zeigt in den Projektordner — ein
späterer `build-app.sh` ersetzt die App an Ort und Stelle, der Autostart bleibt
gültig. Nach einem Rechnerneustart ist also nichts zu tun; es startet immer
der zuletzt gebaute Stand.

Das Skript baut mit `wails build` und signiert die App danach mit einer
**stabilen Identität**. Das ist nötig für die Spracheingabe: macOS bindet die
Mikrofon-Freigabe (TCC) bei einer ad-hoc-Signatur an den `cdhash` der Binary.
Der ändert sich bei jedem Build — deshalb fragt macOS sonst bei jeder neuen
Version wieder nach Erlaubnis. Mit einem festen Zertifikat hängt die Freigabe
am Zertifikat und bleibt bestehen.

Beim ersten Lauf legt `scripts/setup-signing.sh` ein selbstsigniertes
Code-Signing-Zertifikat `magentic-dev` im Login-Schlüsselbund an (macOS fragt
dabei einmal nach dem Passwort, und beim ersten Signieren ggf. nach „Immer
erlauben"). Danach läuft `build-app.sh` ohne Rückfragen durch. Anschließend
einmal das Mikrofon erlauben — das gilt dann für alle künftigen Builds.
