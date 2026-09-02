# Magentic-Steuer-API

Diese Anleitung gilt nur, wenn du in einer von Magentic verwalteten Session
läufst. Prüfe zuerst die Umgebungsvariable `MAGENTIC_ENV`. Steht dort nicht `1`,
dann brich hier ab und versuche keinen einzigen Aufruf der Steuer-API.

Ist der Marker gesetzt, findest du in der Umgebung außerdem:

- `MAGENTIC_SOCKET` — die Sitzungs-Adresse der lokalen Steuer-API.
- `MAGENTIC_SESSION_ID` — die SessionID deiner eigenen Session.
- `MAGENTIC_PROJECT_ID` — die ProjectID des Projekts, in dem du arbeitest.
- `MAGENTIC_WORKTREE` — `1`, wenn du in einem Worktree arbeitest, sonst `0`.
- `MAGENTIC_WORKTREE_DIR` — der Worktree, falls du in einem läufst.

## Verben

Jedes Verb gibt es als Kommandozeile (`magentic session <verb>`) und als
Anfrage über die Sitzungs-Adresse. Beide sprechen dieselbe Sprache. Mit `--json`
schreibt die Kommandozeile genau ein JSON-Dokument auf die Standardausgabe.

| Verb | Kommandozeile | Anfrage | Wozu |
| --- | --- | --- | --- |
| start | `magentic session start` | `session.start` | Session in einem Projekt oder Worktree starten |
| list | `magentic session list` | `session.list` | Sessions mit ihrer Beobachtung auflisten |
| send | `magentic session send` | `session.send` | Text an den Coding-Agent einer Session senden |
| output | `magentic session output` | `session.output` | Sichtbaren Inhalt einer Session lesen |
| wait | `magentic session wait` | `session.wait` | Auf die gepinnte Belegung einer Session warten |
| kill | `magentic session kill` | `session.kill` | Runtime einer Session beenden, der Worktree bleibt |
| whoami | `magentic session whoami` | `session.whoami` | Eigene Session aus den Marker-Angaben auflösen |
| watch | `magentic session watch` | `session.watch` | Zustandswechsel als Ereignisstrom mitlesen |

## Adressierung

Eine Session sprichst du über ihre SessionID an oder über ihren Namen zusammen
mit dem Projekt. Ein bloßer Name, den mehrere Projekte führen, wird nicht
geraten, sondern mit `ambiguous` abgelehnt; die Antwort nennt die Kandidaten.
Magentic setzt niemals deine eigene Session ein, wenn du keine nennst — eine
solche Anfrage endet mit `no-target`.

Ein Verzeichnis wird nie auf Zuruf übernommen. Ein Worktree wird über sein
Projekt-qualifiziertes Handle angesprochen und unmittelbar vor der Benutzung
frisch aufgelöst; ein Verzeichnis außerhalb des Projekts endet mit
`containment`.

## Ergebnis-Codes

Jede Antwort trägt genau einen dieser Codes:

`ok`, `not-found`, `ambiguous`, `no-target`, `containment`, `refused`,
`unavailable`, `not-managed`, `unauthorized`, `invalid-request`,
`unknown-verb`, `failed`, `subscriber-stalled`.

Ein `unavailable` heißt, dass etwas nicht gelesen werden konnte — es ist nie ein
Zustand. Wenn eine Beobachtung nicht verfügbar ist, steht in der Antwort kein
Status; deute das nicht als leere oder untätige Session.

## Der Warte-Vertrag

`session wait` löst die adressierte Session einmal zu Beginn in eine Belegung
auf: SessionID, RuntimeName und AgentRunRef. Dieses Tripel wird für die gesamte
Wartezeit gepinnt. Wird die Belegung ersetzt, endet das Warten — auch dann, wenn
die neue Belegung untätig ist.

Ein Warten endet mit genau einem dieser Codes:

- `done` — die gepinnte Belegung hat aufgehört zu arbeiten und wartet auf einen
  neuen Prompt.
- `waiting` — die gepinnte Belegung braucht eine Antwort; nur bei `--until waiting`.
- `blocked` — bei `--until done`: die Belegung hängt an einer Rückfrage. Das ist
  kein Fehler deinerseits, sondern ein Fall für einen Menschen.
- `occupant-replaced` — Runtime neu angelegt, anderer Agent-Lauf oder Session neu
  registriert. Das Ergebnis nennt die gepinnte und die beobachtete Belegung.
- `session-gone` — der Runtime existiert bestätigt nicht mehr, ohne dass die
  Bedingung eingetreten wäre.
- `timeout` — die Zeitgrenze lief ab; die Antwort nennt den zuletzt beobachteten
  Zustand.
- `cancelled` — du hast abgebrochen oder die Verbindung getrennt.
- `no-occupant` — die adressierte Session führt keinen auflösbaren
  Coding-Agent-Lauf; das Warten blockiert gar nicht erst.

## Delegieren: starten, warten, lesen

So gibst du Arbeit an eine zweite Session ab, ohne dein eigenes Verzeichnis zu
teilen:

```sh
test "$MAGENTIC_ENV" = "1" || exit 0

start=$(magentic session start \
  --project "$MAGENTIC_PROJECT_ID" \
  --name review --vendor claude --new-worktree \
  --prompt "Prüfe die Änderungen auf diesem Branch." --json)
session=$(printf '%s' "$start" | jq -r .result.sessionId)

magentic session wait --session "$session" --until done --timeout 1800 --json
# Der Exit-Code ist 0 bei done, sonst lies das Ergebnis im Dokument.

magentic session output --session "$session" --lines 120 --json
magentic session kill --session "$session" --json
```

`kill` beendet nur den Runtime. Der Worktree und sein Checkout bleiben liegen,
damit nichts von der Arbeit verloren geht.

## Ohne bedienenden Prozess

Die Kommandozeile ist ein reiner Client der Sitzungs-Adresse. Bedient kein
Magentic die Steuer-API, antwortet sie mit `unavailable` und nennt den erwarteten
Pfad. Starte in diesem Fall nichts selbst; sage stattdessen dem Menschen, dass
die TUI, die Desktop-App oder `magentic serve` laufen muss.
