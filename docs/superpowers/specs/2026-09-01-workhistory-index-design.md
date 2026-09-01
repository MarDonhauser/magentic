# WorkHistory — abfragbarer Index statt einer geladenen Indexdatei

Stand: 2026-09-01. Basis: `3097103`.

## Ziel

Der Verlauf soll sofort etwas anzeigen und nie wieder auf `lade…` stehen
bleiben. Dafür bekommt WorkHistory einen Index, der abgefragt statt geladen
wird, ein Aufbewahrungsfenster von 14 Tagen für Roh-Events, dauerhafte
Tagesaggregate für die Statistik und einen Aufbau, der im Hintergrund läuft,
während Abfragen bereits Teilergebnisse liefern.

## Befund

Der heutige Index ist eine einzige JSON-Datei mit allen geparsten Datensätzen
aller Provider. Auf dem Rechner des Entwicklers ist
`~/.config/magentic/work-history/index.json` **609 MB** groß.

- `loadHistoryIndex` (`core/workhistory.go:692`) liest diese Datei vollständig
  und deserialisiert sie, `saveHistoryIndex` (`core/workhistory.go:716`)
  serialisiert sie vollständig zurück.
- `refresh` (`core/workhistory.go:573`) tut das bei **jeder** Abfrage, hält
  dabei einen Prozess-Mutex und einen Dateilock, und blockiert damit alle
  anderen Verlaufsfunktionen.
- `refreshIndex` (`core/workhistory.go:589`) läuft zusätzlich über alle
  Transkripte: 2.688 Claude-Dateien (2,5 GB) und 3.179 Codex-Dateien (15 GB).
- `Timeline` (`app/tools.go:340`), `SearchTranscripts` (`app/tools.go:186`),
  `SessionLinks` (`app/tools.go:120`), `BuildStats` (`core/stats.go:422`) und
  `core/provider_run.go:61` öffnen jeweils eine **eigene** WorkHistory-Instanz.
  Es gibt keinen Cache über Aufrufe hinweg; jeder Bedienvorgang zahlt den
  vollen Preis erneut.
- Alle Aufrufer benutzen `context.Background()`, also ohne Frist. Das Frontend
  hat für den Verlauf keinen Fortschritts- und keinen Zwischenzustand:
  `app/frontend/index.html:110` setzt `lade…`, und `refreshTimeline`
  (`app/frontend/src/main.js:2257`) ersetzt das erst, wenn die Antwort da ist.

Der Verlauf hängt also nicht endlos, er dauert Minuten — und sieht dabei aus
wie ein Fehler.

## Entscheidungen

1. Roh-Events werden **14 Tage** vorgehalten. Ältere Prompts und Ausgaben sind
   im Produkt nicht mehr sichtbar.
2. Tagesaggregate bleiben **dauerhaft**, damit die Statistik ihre Bereiche von
   7, 30 und 90 Tagen (`app/frontend/src/stats.js:883`) behält.
3. Der Index wird eine **SQLite-Datenbank** über `modernc.org/sqlite`, reines
   Go ohne cgo. Der Wails-Build bleibt unverändert.
4. Der Aufbau läuft im **Hintergrund**, neueste Transkripte zuerst. Abfragen
   antworten sofort mit dem bereits Indexierten.

## Abgrenzung

Nicht in diesem Schnitt:

- Die Chat-Übersicht als Oberfläche und das Hineinziehen eines Chats per
  Drag & Drop. Dieser Schnitt liefert dafür nur die Abfrage `Conversations`.
- Änderungen an den Provider-Adaptern (`core/workhistory_adapters.go`). Ihre
  Schnittstelle `historyProviderAdapter` (`core/workhistory_adapters.go:22`)
  und ihr Parseverhalten bleiben unangetastet.
- Quota und Preistabelle. Kostenberechnung bleibt, wo sie heute ist.

## Begriffe

Diese Begriffe kommen nach Abnahme in `CONTEXT.md`.

**HistoryRetentionWindow**:
Der Zeitraum, für den WorkHistory einzelne Prompts und Ausgaben vorhält;
außerhalb davon existieren nur noch Tagesaggregate.
_Vermeiden_: Cache-Dauer, TTL, Aufräumfrist

**HistoryActivityAggregate**:
Die je Quelle, Tag, Stunde, Provider, Conversation und Modell fortgeschriebenen
Kennzahlen — Prompts, Turns, Tokenwerte und Kosten — die den Verfall der
Roh-Events überdauern.
_Vermeiden_: Statistik, Zusammenfassung, Summary

**HistoryIndexProgress**:
Der beobachtbare Zustand eines laufenden Indexaufbaus, den eine Abfrage
zusammen mit ihrem Teilergebnis zurückgibt.
_Vermeiden_: Ladezustand, Spinner, Fortschrittsbalken

## Speicherung

`index.json` entfällt. An seine Stelle tritt
`~/.config/magentic/work-history/history.db`, angelegt mit Rechten `0600` im
weiterhin auf `0700` geschützten Verzeichnis (`ensurePrivateHistoryDir`,
`core/workhistory.go:455`). Die Datenbank läuft im WAL-Modus, damit Lesen und
Indexieren sich nicht behindern.

### Tabellen

`sources` ersetzt `historyIndex.Files`:

| Spalte | Bedeutung |
|---|---|
| `source_id` | Primärschlüssel, aus `historySourceID` (`core/workhistory.go:759`) |
| `provider` | Provider-Kennung |
| `path` | Pfad des Transkripts |
| `adapter_version` | `adapter.Version()` beim Parsen |
| `digest` | `adapter.Fingerprint(...)` |
| `size`, `mod_time` | für die Wiederverwendungsprüfung |
| `indexed_at` | Zeitpunkt des letzten erfolgreichen Parsens |
| `problems` | JSON-Liste von `HistoryProblem` |

`events` hält je einen `historyRecord` (`core/workhistory.go:552`) mit
denselben Feldern; `occurred_at` als Unix-Nanosekunden und nullbar, damit
`IncludeUnknownTime` weiter funktioniert. Indizes auf `occurred_at`,
`(provider, conversation_id)` und `source_id`.

`events_fts` ist eine FTS5-Tabelle über `text` mit `content=events`.

`activity` hält die dauerhaften Aggregate:

| Spalte | Bedeutung |
|---|---|
| `source_id` | Quelle, aus der die Zeile stammt |
| `day` | lokaler Tag als `YYYY-MM-DD` |
| `hour` | lokale Stunde 0–23, für Tagesverlauf und Wochen-Heatmap |
| `provider`, `conversation_id`, `cwd`, `project_alias`, `model` | Merkmale für die spätere Zuordnung |
| `prompts`, `turns` | Zähler |
| `input_tokens`, `output_tokens`, `cache_read_tokens`, `cache_write_tokens` | Summen |
| `cost` | summierte Kosten der bepreisten Ereignisse |
| `unpriced_events` | Anzahl der Ereignisse ohne belastbaren Preis |

Primärschlüssel ist
`(source_id, day, hour, provider, conversation_id, model)`.

`hour`, `cost` und `unpriced_events` sind nicht optional: `statsAcc.addEvent`
(`core/stats.go:268`) baut daraus heute den Stundenverlauf, die Heatmap und den
Kostenzustand. Ohne diese Merkmale wären sie jenseits von 14 Tagen leer.

### Warum die Aggregate keine Namen speichern

`activity` speichert bewusst **keinen** Projekt- oder Sessionnamen, sondern nur
`conversation_id`, `cwd` und `project_alias`. Attribution wird weiterhin bei
jeder Abfrage über `newHistoryAssociationResolver`
(`core/workhistory.go:993`) aus den übergebenen `HistoryAssociations`
aufgelöst. Sonst würden umbenannte oder später übernommene Projekte in alten
Statistiken einfrieren — genau die Eigenschaft, die der Kommentar bei
`HistoryAssociations` (`core/workhistory.go:158`) heute zusichert.

### Warum die Aggregate an der Quelle hängen

Weil der Primärschlüssel `source_id` enthält, ist erneutes Parsen derselben
Datei idempotent: die Zeilen dieser Quelle werden in einer Transaktion
gelöscht und neu geschrieben, nicht addiert. Ein Transkript, das noch wächst,
wird bei jedem Lauf neu geparst, ohne dass die Statistik doppelt zählt.
Abfragen summieren über `source_id` hinweg.

## Aufbewahrung

Beim Indexlauf werden Events mit `occurred_at` älter als 14 Tage gelöscht;
`activity` bleibt unangetastet. Events ohne bekannten Zeitpunkt werden anhand der
Änderungszeit ihrer Quelle behandelt.

Die Reihenfolge ist entscheidend: Aggregate einer Quelle werden **beim Parsen**
geschrieben, also bevor der Verfall greift. Sonst gingen die Kennzahlen für
Tage außerhalb des Fensters verloren.

Zusätzlich filtert der Indexer vor dem Öffnen: Transkripte, deren Änderungszeit
älter als das Fenster ist und die bereits eine `sources`-Zeile mit passender
Größe, Änderungszeit und Adapterversion haben, werden weder gelesen noch
gefingerprintet. Das erspart den Großteil der 15 GB Codex-Dateien jeden Zugriff.
Unbekannte alte Dateien werden einmalig gelesen, damit ihre Aggregate entstehen.

## Zugriff

### Unveränderte Schnittstellen

`Events` (`core/workhistory.go:465`), `Links` (`core/workhistory.go:478`) und
`Summarize` (`core/workhistory.go:513`) behalten Signatur, Semantik und
Rückgabetypen. `HistoryEventPage`, `HistoryLinkPage`, `HistorySummary`,
`HistoryMeta` und `HistoryProviderCoverage` bleiben wie sie sind, ergänzt um
den Fortschritt (siehe unten). `app/tools.go` ändert sich nur dort, wo es die
Instanz beschafft. `core/stats.go` ändert sich darüber hinaus, siehe den
Abschnitt Statistik.

`queryHistoryEvents` (`core/workhistory.go:851`) wird von einer Schleife über
eine Speicherstruktur zu einer SQL-Abfrage. Die Filterreihenfolge und die
Bedeutung von `Limit`, `Offset` und `paginate` bleiben gleich; die
Attributionsauflösung geschieht weiterhin in Go, nach dem Lesen der Zeilen.

### Eine Instanz je Prozess

Statt sechs Aufrufen von `OpenWorkHistory` pro Bedienvorgang gibt es eine
prozessweite, beim ersten Zugriff geöffnete Instanz. `OpenWorkHistory` bleibt
für Tests bestehen und öffnet weiter eine eigene Instanz auf einem eigenen
`IndexDir`; die Aufrufer in `app/tools.go` und `core/stats.go` gehen über die
gemeinsame Instanz.

### Statistik

`buildStats` (`core/stats.go:427`) baut heute Tageswerte, Projektzeilen,
Stundenverlauf, Heatmap und Kostenzustand aus einer Schleife über die einzelnen
Events (`core/stats.go:470`). Mit einem Fenster von 14 Tagen bricht das für die
Bereiche 30 und 90 Tage: die Summen aus `Summarize` blieben zwar richtig, die
Tages- und Projektaufschlüsselung wäre für alles Ältere leer.

Deshalb bekommt WorkHistory eine zweite Abfrage, die aus `activity` liest:

```go
type HistoryActivityQuery struct {
    Since, Before time.Time
    Providers     []HistoryProvider
    Location      *time.Location
}

type HistoryActivityBucket struct {
    Day            string          `json:"day"`
    Hour           int             `json:"hour"`
    Provider       HistoryProvider `json:"provider"`
    ConversationID string          `json:"conversationId"`
    Model          string          `json:"model"`
    Prompts        int             `json:"prompts"`
    Turns          int             `json:"turns"`
    Usage          HistoryUsage    `json:"usage"`
    Cost           float64         `json:"cost"`
    UnpricedEvents int             `json:"unpricedEvents"`
    Attribution    HistoryAttribution `json:"attribution"`
}
```

`statsAcc` bekommt dazu ein `addBucket` neben dem bestehenden `addEvent`, und
`buildStats` speist sich aus Buckets statt aus Events. Beide Wege müssen
dieselben Zahlen liefern; genau das prüft ein Test innerhalb des
14-Tage-Fensters, in dem beide Quellen vorhanden sind.

Zwei Folgen sind hinzunehmen und gehören in die Abnahme:

- Die Zahl gleichzeitiger Sessions je Tag zählt fortan verschiedene
  Conversations statt aufgelöster Sessionschlüssel. Innerhalb eines Providers
  ist das dieselbe Menge; über Provider hinweg kann eine Session, die den
  Vendor gewechselt hat, doppelt zählen.
- `pagedStatsHistory` und die Revisionsschleife `coherentStatsHistory`
  (`core/stats.go:579`) entfallen für den Aggregatpfad. Kohärenz entsteht
  stattdessen dadurch, dass Summen und Buckets in einer Lesetransaktion
  derselben Datenbank ermittelt werden.

### Neue Abfrage: Conversations

```go
type HistoryConversationQuery struct {
    Since, Before time.Time
    Providers     []HistoryProvider
    ProjectKeys   []string
    SessionKeys   []string
    Limit, Offset int
}

type HistoryConversation struct {
    Provider       HistoryProvider        `json:"provider"`
    ConversationID string                 `json:"conversationId"`
    StartedAt      HistoryFact[time.Time] `json:"startedAt"`
    LastActivityAt HistoryFact[time.Time] `json:"lastActivityAt"`
    Turns          int                    `json:"turns"`
    LastPrompt     HistoryFact[string]    `json:"lastPrompt"`
    Attribution    HistoryAttribution     `json:"attribution"`
}

type HistoryConversationPage struct {
    Conversations []HistoryConversation `json:"conversations"`
    Total         int                   `json:"total"`
    Meta          HistoryMeta           `json:"meta"`
}
```

Gruppierung nach `(provider, conversation_id)`, sortiert nach letzter
Aktivität. Attribution wird wie bei `Events` pro Abfrage aufgelöst. In diesem
Schnitt entsteht nur diese Abfrage samt Tests, keine Oberfläche und keine
Wails-Bindung.

### Suche

Die heutige Textsuche vergleicht Teilstrings (`HistoryEventQuery.Text`). FTS5
sucht wortweise und würde das Verhalten spürbar ändern. Deshalb dient FTS nur
der Vorauswahl: Die Anfrage wird zu einem FTS-Präfixausdruck normalisiert, die
Treffermenge anschließend in Go mit dem exakten, groß- und
kleinschreibungsunabhängigen Teilstring gefiltert. Anfragen, aus denen kein
sinnvoller FTS-Ausdruck entsteht — etwa reine Satzzeichen — fallen auf eine
`LIKE`-Abfrage über das Zeitfenster zurück.

## Aufbau im Hintergrund

`refresh` blockiert nicht mehr. Stattdessen:

- Beim ersten Zugriff startet ein Indexerlauf als eigene Goroutine. Er
  ermittelt die zu bearbeitenden Dateien über `discoverHistoryFiles`
  (`core/workhistory_adapters.go:48`), sortiert sie nach Änderungszeit
  absteigend und schreibt nach jeder Datei eine Transaktion.
- Solange ein Lauf aktiv ist, startet kein zweiter. Ist der letzte Lauf länger
  als eine Minute abgeschlossen, stößt der nächste Zugriff einen neuen an.
- Abfragen lesen die Datenbank sofort und liefern das bereits Indexierte.
- `HistoryMeta` bekommt ein Feld `Progress`:

```go
type HistoryIndexProgress struct {
    Active         bool      `json:"active"`
    PendingFiles   int       `json:"pendingFiles"`
    CompletedFiles int       `json:"completedFiles"`
    StartedAt      time.Time `json:"startedAt,omitempty"`
}
```

- Solange ein Lauf aktiv ist, meldet die betroffene
  `HistoryProviderCoverage` den Zustand `partial` mit einem Problem der Art
  `indexing`. Damit bleibt die bestehende Zusicherung erhalten, dass eine
  unvollständige Teilmenge nie als exakte Null gelesen wird
  (`historySourcesComplete`, `core/workhistory.go:1346`).

Der Dateilock bleibt bestehen, aber nur noch um den Indexerlauf, damit zwei
Magentic-Prozesse nicht gleichzeitig dieselben Dateien parsen. Lesen braucht
ihn nicht mehr.

### Frontend

- `app/frontend/index.html:110` verliert das feste `lade…`; der leere Zustand
  entsteht in `renderTimeline` (`app/frontend/src/main.js:2270`).
- `refreshTimeline` (`app/frontend/src/main.js:2257`) rendert das Teilergebnis
  und zeigt im Kopf des Verlaufs, wie viele Dateien noch gelesen werden.
- Solange ein Lauf aktiv ist, prüft der Verlauf alle 3 Sekunden nach; danach
  gilt wieder das bestehende Intervall von 60 Sekunden
  (`app/frontend/src/main.js:2254`).
- Ein Fehler wird angezeigt, statt den Ladezustand stehen zu lassen; der
  bestehende `catch`-Zweig bleibt dafür der richtige Ort.
- `historyCoverageNotice` bleibt unverändert und trägt den `indexing`-Hinweis
  über den vorhandenen Weg.

## Übergang vom alten Index

Beim ersten Start nach dem Umbau:

1. Liegt `index.json` vor, wird sie **einmalig** im Hintergrund gelesen, und es
   werden ausschließlich Tagesaggregate daraus abgeleitet. Sonst wäre die
   90-Tage-Statistik direkt nach dem Umbau leer.
2. Danach wird `index.json` gelöscht.
3. Schlägt die Übernahme fehl, wird sie übersprungen und als
   `HistoryProblem` der Art `legacy-index` gemeldet. Die Datei wird in diesem
   Fall nicht gelöscht, sondern nach `index.json.rejected` umbenannt.

Roh-Events werden nicht übernommen: sie sind aus den Transkripten
reproduzierbar und fallen zum größten Teil ohnehin aus dem 14-Tage-Fenster.

Ein Schemawechsel später wird über eine `schema_version`-Tabelle erkannt; bei
unbekannter Version wird die Datenbank verworfen und neu aufgebaut, so wie es
`loadHistoryIndex` heute bei abweichender `Version` tut
(`core/workhistory.go:704`).

## Prüfung

Die bestehenden Tests in `core/workhistory_test.go`,
`core/workhistory_discovery_test.go` und `core/workhistory_usage_test.go`
arbeiten gegen die Attrappe `workHistoryFS` und bleiben die Grundlage; sie
müssen ohne inhaltliche Änderung weiterlaufen. Neu dazu:

- **Schema und Abfragen**: `Events`, `Links` und `Summarize` liefern gegen eine
  temporäre Datenbank dieselben Ergebnisse wie gegen den bisherigen Index, für
  jeden Filter aus `HistoryEventQuery`.
- **Aufbewahrung**: Events außerhalb von 14 Tagen verschwinden, die zugehörigen
  Aggregate bleiben, und eine Statistik über 90 Tage bleibt vollständig —
  einschließlich Tageswerten, Projektzeilen, Stundenverlauf und Heatmap.
- **Gleichstand beider Statistikpfade**: Innerhalb des 14-Tage-Fensters liefern
  der Aggregatpfad und die alte Schleife über Events dieselben Zahlen.
- **Idempotenz**: Dieselbe Quelle zweimal parsen ergibt dieselben Aggregate.
  Eine gewachsene Quelle neu parsen ergibt die Summe des neuen Inhalts, nicht
  die Summe beider Läufe.
- **Teilergebnis**: Eine Abfrage während eines laufenden Aufbaus liefert die
  bereits indexierten Events, `Progress.Active` ist wahr, und die betroffene
  Abdeckung meldet `partial`.
- **Suche**: Teilstringtreffer innerhalb eines Wortes werden gefunden; eine
  Anfrage aus reinen Satzzeichen fällt sauber zurück statt zu scheitern.
- **Conversations**: Gruppierung, Sortierung nach letzter Aktivität und
  Attribution nach einer Projektumbenennung.
- **Übergang**: Eine vorhandene `index.json` erzeugt Aggregate und wird
  gelöscht; eine beschädigte wird umbenannt und gemeldet.
