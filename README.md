# magentic

Verwaltung von Claude-Code-Sessions über mehrere Projekte hinweg. Jeder Agent läuft in einer eigenen tmux-Session und überlebt damit das Schließen der UI.

Zwei Oberflächen, eine gemeinsame Logik (`core/`):

- **TUI** (`magentic`) — schnelle Übersicht und Verwaltung im Terminal.
- **Desktop-App** (`app/`, Wails) — Übersicht mit Token-Limits, Todos, Aktionen
  und **echtem eingebetteten Terminal** (xterm.js): natives Markieren, klickbare
  Links, natives Scrollen.

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
magentic              # TUI starten
magentic add [pfad]   # Projekt registrieren (Default: aktuelles Verzeichnis)
open app/build/bin/magentic.app   # Desktop-App
```

## Tasten (TUI)

| Taste | Aktion |
|---|---|
| `n` | Neue Claude-Session im Projektverzeichnis (Name leer = auto) |
| `w` | Neue Claude-Session in frischem Git-Worktree (Branch `agent/<name>`) |
| `⏎` / `a` | Agent: an die tmux-Session attachen (zurück mit `ctrl-b d`) · Projekt: auf-/zuklappen |
| `d` | `/done` an die Session des gewählten Agents senden |
| `D` | `/deploy` senden (ohne Agent: neue Session im Projekt-Root) |
| `t` | Neues Todo (merkt sich das aktuelle Projekt) |
| `e` | Todo bearbeiten (leer = löschen) |
| `r` | Agent umbenennen |
| `x` | Agent beenden (Worktree bleibt bestehen) / Projekt entfernen |
| `p` | Projekt hinzufügen |
| `j`/`k` / `↑`/`↓` | Navigieren |
| `g` | Sofort aktualisieren |
| `q` | Beenden (Agents laufen weiter) |

Maus: Klick links wählt aus (Projekt-Klick klappt auf/zu), zweiter Klick auf
einen Agent attacht, Scrollrad navigiert.

## Desktop-App

Die App (Wails, Go + xterm.js) zeigt links Sessions **nach Projekt gruppiert**
und Todos, unten die Claude-Limits (5h/7d). Die Übersicht enthält Tiles,
Projekt-Karten mit Worktree-Zeilen (ahead/behind, Git-Status, Warnungen),
Agent-Pills und alle Aktionen:

- **⌨** — Terminal zur Session öffnen (echtes PTY-Attach, natives
  Markieren/Kopieren, klickbare Links, Scrollback)
- **✓ done** — schickt `/done` an die laufende Session
- **🔀 branch → main** — Claude-Session, die den Branch merged
- **✨ Cleanup** — Claude-Session im verwaisten Worktree: sichten, committen, mergen
- **⌫ entfernen** — `git worktree remove` (nur sauber + ohne aktive Session)
- **🚀 deploy** — Claude-Session mit `/deploy`
- **+ Session / ⑂ Worktree** — neue Claude-Session im Projekt
- **Todos** — anlegen, bearbeiten, löschen, **▶ Session**: startet eine Session,
  der Todo-Text landet im Eingabefeld (ohne Abschicken)

**Hauptbranch pro Projekt:** ahead/behind, Warnungen und Merge-Ziele beziehen
sich auf den konfigurierbaren Hauptbranch (✎ neben dem Projektnamen; leer =
automatisch der Branch des Haupt-Worktrees).

App bauen:

```sh
cd app && wails build          # → app/build/bin/magentic.app
```

## Todos (TUI)

`t` legt ein Todo an (links unten über der Usage-Anzeige, mit Projekt-Tag aus
dem aktuellen Kontext). `e` bearbeitet, `x` löscht. `⏎` auf einem Todo startet
eine neue Claude-Session im zugehörigen Projekt und tippt den Todo-Text ins
Eingabefeld — **ohne ihn abzuschicken**. Das Todo wird dabei aus der Liste
entfernt.

## Git pro Session

Die Git-Anzeige eines Agents zeigt nur, was **seit Start dieser Session**
passiert ist: Beim Erstellen wird eine Baseline (HEAD + bereits geänderte
Dateien) gespeichert; angezeigt werden nur neue Commits und Dateien, die die
Session selbst angefasst hat. Adoptierte Sessions ohne Baseline zeigen den
Gesamtstatus. Die Projektzeile zeigt weiterhin den Repo-Gesamtzustand.

## Status

- `●` läuft — Claude arbeitet gerade
- `◆` wartet — Claude braucht Input (Permission-Dialog, Frage, Trust-Prompt)
- `○` idle — bereit für den nächsten Prompt
- `▪` beendet — Claude wurde beendet, Shell läuft noch
- `✗` tot — tmux-Session existiert nicht mehr (mit `x` entfernen)

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

## Wie es funktioniert

- Agents sind tmux-Sessions mit Prefix `mgt-`. Auch von Hand erstellte Sessions (`tmux new -s mgt-foo`) werden beim Start adoptiert und anhand des Verzeichnisses einem Projekt zugeordnet.
- Der Status wird alle 2 Sekunden per `tmux capture-pane` erkannt.
- Worktrees landen unter `<projekt>-agents/<agentname>` neben dem Projektordner.
- Konfiguration und Agent-Registry: `~/.config/magentic/state.json`
- Gemeinsame Logik von TUI und App liegt in `core/` (State, tmux, Status,
  Git, Overview, Usage, Aktionen).

## Build

```sh
go build -o ~/.local/bin/magentic .
```

Nicht per `cp` über die bestehende Binary kopieren — macOS invalidiert dabei
die Code-Signatur und killt das Programm beim Start (`zsh: killed`). Entweder
direkt ins Ziel bauen (oben) oder vorher `rm ~/.local/bin/magentic`.
