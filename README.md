# magentic

Terminal-UI zum Verwalten von Claude-Code-Sessions über mehrere Projekte hinweg. Jeder Agent läuft in einer eigenen tmux-Session und überlebt damit das Schließen der UI.

```
 ⚡ magentic                                  ● 1 läuft   ◆ 1 wartet   ○ 2 idle
╭──────────────────────────────────╮╭ Details ──────────────────────────────╮
│ ▾ devpilot                   ± 3 ││ hera · devpilot                       │
│   ● hera    läuft   12m  ±       ││ ● läuft · seit 12m                    │
│   ◆ atlas   wartet   3m  ✓ ⑂     ││ Git: main  ± 2 geändert               │
│   ○ nyx     idle    45m  ✓       ││ Worktrees · Terminal-Preview …        │
│ ▸ reqpilot                   ✓ 1 ││                                       │
╰──────────────────────────────────╯╰───────────────────────────────────────╯
 n neu · w neu+worktree · ⏎ attach/auf-zu · o browser · x kill · q ende
```

## Voraussetzungen

- tmux
- claude (Claude Code CLI)

## Nutzung

```sh
magentic              # TUI starten
magentic add [pfad]   # Projekt registrieren (Default: aktuelles Verzeichnis)
magentic web [port]   # Session-/Worktree-Übersicht im Browser (Default 4321)
```

## Tasten

| Taste | Aktion |
|---|---|
| `n` | Neue Claude-Session im Projektverzeichnis (Name leer = auto) |
| `w` | Neue Claude-Session in frischem Git-Worktree (Branch `agent/<name>`) |
| `⏎` | Agent: im rechten Panel bedienen (Fokus-Modus) · Projekt: auf-/zuklappen |
| `ctrl+q` | Fokus-Modus verlassen, zurück zur Übersicht |
| `a` | Klassisch als Vollbild attachen (zurück mit `ctrl-b d`) |
| `o` | Session-/Worktree-Übersicht im Browser öffnen |
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
einen Agent oder Klick in die Terminal-Vorschau öffnet den Fokus-Modus,
Scrollrad navigiert.

## Todos

`t` legt ein Todo an (links unten über der Usage-Anzeige, mit Projekt-Tag aus
dem aktuellen Kontext). `e` bearbeitet, `x` löscht. `⏎` auf einem Todo startet
eine neue Claude-Session im zugehörigen Projekt und tippt den Todo-Text ins
Eingabefeld — **ohne ihn abzuschicken**: Du landest direkt im Fokus-Modus,
kannst den Text noch anpassen und schickst ihn selbst mit Enter ab. Das Todo
wird dabei aus der Liste entfernt.

## Fokus-Modus

`⏎` oder Klick auf einen Agent bedient dessen Claude-Session direkt im rechten
Panel: Alle Tasten (Text, Enter, Esc, Pfeile, `shift+tab`, `ctrl+…`) gehen an
Claude, die Anzeige aktualisiert sich live in Farbe, und die tmux-Fenstergröße
folgt automatisch dem Panel. `ctrl+q` kehrt zur Übersicht zurück, ein Klick
links wechselt direkt zu einem anderen Agent/Projekt.

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
zu vermeiden). Für den gerade fokussierten Agent wird nicht benachrichtigt.

## Browser-Übersicht (`magentic web` oder Taste `o`)

Zeigt alle Projekte als Git-Strang-Graph: der Hauptbranch als Stamm, jeder
Worktree als Abzweig — mit ahead/behind gegenüber dem Hauptbranch, uncommitteten
Änderungen, zugeordneten Agents samt Live-Status und Warnungen wie
„uncommitted Änderungen, keine aktive Session" oder „N Commits nicht in main".
Aktualisiert sich alle 3 Sekunden selbst; hell/dunkel folgt dem System.

**Hauptbranch pro Projekt:** ahead/behind, Warnungen und Merge-Ziele beziehen
sich auf den konfigurierbaren Hauptbranch (✎ neben dem Projektnamen; leer =
automatisch der Branch des Haupt-Worktrees). So sieht z. B. dev.pilot, welche
Commits von `dev` noch nicht auf `main` sind.

Aktionen — alle über echte Claude-Sessions (lösen auch Konflikte), nie über
fest verdrahtete git-Kommandos; jede Session zeigt erst einen Plan:

- **🔀 branch → main** — startet eine Claude-Session, die den Branch in den
  Hauptbranch merged (Ziel-Branch beim Klick änderbar, z. B. dev statt main).
- **✓ done** (in der Agent-Pill) — schickt `/done` an die laufende Session:
  committen und auf dev bringen.
- **🚀 deploy** (im Projektkopf) — startet eine Claude-Session, die `/deploy`
  ausführt.
- **✨ Cleanup-Agent** — Claude-Session im verwaisten Worktree: sichten,
  committen, mergen.
- **⌫ entfernen** — entfernt den Worktree direkt (`git worktree remove`), aber
  nur wenn er sauber ist und kein Agent darin arbeitet; der Haupt-Worktree ist
  geschützt. Idle-Sessions im Worktree werden mitbeendet.

## Wie es funktioniert

- Agents sind tmux-Sessions mit Prefix `mgt-`. Auch von Hand erstellte Sessions (`tmux new -s mgt-foo`) werden beim Start adoptiert und anhand des Verzeichnisses einem Projekt zugeordnet.
- Der Status wird alle 2 Sekunden per `tmux capture-pane` erkannt.
- Worktrees landen unter `<projekt>-agents/<agentname>` neben dem Projektordner.
- Konfiguration und Agent-Registry: `~/.config/magentic/state.json`

## Build

```sh
go build -o ~/.local/bin/magentic .
```

Nicht per `cp` über die bestehende Binary kopieren — macOS invalidiert dabei
die Code-Signatur und killt das Programm beim Start (`zsh: killed`). Entweder
direkt ins Ziel bauen (oben) oder vorher `rm ~/.local/bin/magentic`.
