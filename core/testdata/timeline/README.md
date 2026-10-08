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

## omp-Frame-Fixtures

`omp-run.ndjson` sind die `in`-Frames aus
`core/testdata/omp/approval-deny.ndjson` (Spike 2.11, `omp/18.2.8`), eine Zeile
pro Frame ohne die `{"dir":..., "frame": ...}`-Hülle — genau die Frames, die
`OmpFrameScan.Normalize` verarbeitet. Inhalt ist unverändert echt: ein
Entwickler-Prompt, eine gestreamte Überlegung, ein `write`-Werkzeugaufruf, der
zweimal per Freigabe-Anfrage abgelehnt wird (`file-change`, `failed`), und die
abschließende Antwort des Agenten. `omp-run.golden.json` hält die erwartete
Item-Folge, neu schreiben mit `go test ./core/ -run TestGoldenOmpConversation
-update`.

`omp-subagent.ndjson` ist synthetisch: omp hat während des Spikes keine
`subagent_*`-Frames gesendet (design.md, "Still not exercised"), also gibt es
keine echte Aufzeichnung zum Kuratieren. Die Zeilen folgen der Form, die
omp's eigener Quellcode dokumentiert
(`pi-tui/overlays/session-observer-registry.ts`,
`pi-coding-agent/src/modes/rpc/rpc-subagents.ts`): eine `subagent_lifecycle`
mit einem `parentToolCallId`, der auf einen vorher gesehenen
`task`-Werkzeugaufruf zeigt, eine `subagent_progress` dazu, ein
`subagent_event`, und dieselbe Folge ein zweites Mal ohne passenden
`parentToolCallId`, damit der unbekannte-Elternteil-Fall mitgeprüft wird.
`omp-subagent.golden.json` neu schreiben mit `go test ./core/ -run
TestGoldenOmpSubagentConversation -update`.
