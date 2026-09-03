# Claude-Transkript-Fixtures

`claude-run.jsonl` und die Dateien unter `claude-run/subagents/` sind echte
Records aus Claude-Code-Läufen in diesem Repository, kuratiert für die
Golden-Tests in `timeline_golden_test.go`:

- Ausgewählt wurde je ein Record pro Shape, die der Normalizer abbilden muss:
  ein Metadaten-Record, ein Entwickler-Prompt, eine Überlegung, Prosa, ein
  Bash-Aufruf mit Ergebnis, eine Dateiänderung mit Ergebnis, eine delegierte
  Aufgabe mit Ergebnis und die Records ihres Subagenten.
- Lange Payloads (Prosa, Werkzeug-Ausgaben, Thinking-Signaturen) sind gekürzt,
  Telemetriefelder wie `usage` und `requestId` sind entfernt. Die Struktur der
  Records ist unverändert.
- Der Text der Überlegung wurde ersetzt: in den Transkripten dieses Repos
  existiert kein Record mit aufgezeichnetem Thinking, und der Inhalt eines
  fremden Projekts gehört nicht hierher. Alles andere ist Originalinhalt.

`claude-run.golden.json` hält die erwartete Item-Folge. Neu schreiben mit
`go test ./core/ -run TestGoldenClaudeConversation -update`.
