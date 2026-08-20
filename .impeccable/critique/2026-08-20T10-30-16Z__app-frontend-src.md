---
target: app/frontend/src
total_score: 22
max_score: 40
na_heuristics: 
p0_count: 0
p1_count: 3
timestamp: 2026-08-20T10-30-16Z
slug: app-frontend-src
---
Method: dual-agent (A: `/root/assessment_a` · B: `/root/assessment_b`)

## Design Health Score

| # | Heuristik | Score | Kernproblem |
| --- | --- | ---: | --- |
| 1 | Sichtbarkeit des Systemstatus | 3 | Viele gute Session-/Deploy-Zustände, aber Refresh-Fehler bleiben unsichtbar. |
| 2 | Übereinstimmung mit der realen Welt | 2 | Git-Sprache passt; „Hydra“, „Zeitgeist“ und Claude-spezifische Begriffe brauchen Vorwissen. |
| 3 | Kontrolle und Freiheit | 3 | Zurück, Esc, Parken und Bestätigungen sind gut; Undo fehlt und `⌘⇧W` beendet direkt. |
| 4 | Konsistenz und Standards | 2 | Visuell kohärent, semantisch aber ein Mix aus Buttons, klickbaren Divs/Spans und Hover-/Rechtsklick-Mustern. |
| 5 | Fehlervermeidung | 2 | Dirty Worktrees sind geschützt; nicht alle Kill-Pfade verwenden denselben Schutz. |
| 6 | Wiedererkennen statt Erinnern | 2 | Labels und Historie helfen; Modifier-Klicks, Rechtsklick und viele `title`-Hinweise bleiben verborgen. |
| 7 | Flexibilität und Effizienz | 3 | Gute Shortcuts, Hydra und Dock; Status-first-Navigation und vollständige Tastaturparität fehlen. |
| 8 | Ästhetik und Minimalismus | 2 | Ruhige Palette, aber zu viele gleich gewichtete Ziele und Aktionen. |
| 9 | Fehler erkennen und beheben | 2 | Teilweise gute Inline-Fehler; rohe, flüchtige Meldungen und stille Stale-Data-Zustände helfen nicht bei der Erholung. |
| 10 | Hilfe und Dokumentation | 1 | Tooltips und einzelne gute Empty States, aber keine auffindbare, aufgabenbezogene Hilfe. |
| **Gesamt** |  | **22/40** | **Akzeptabel – deutliche Verbesserungen nötig** |

Alle zehn Heuristiken sind für diese Operate-Oberfläche relevant.

## Design Specificity Verdict

**LLM-Bewertung:** Magentic ist im Verhalten klar eigenständig: deterministische Roboter-Avatare, Projekt/Worktree/Session-Gruppierung, Hydra, persistente Terminals, agentenbewusste Pausen und Attention-States sind produktspezifisch. Die Komposition bleibt jedoch näher an einem austauschbaren dunklen Developer-Dashboard. Vor allem verkörpert sie das stärkste Produktversprechen nicht: „Attention before administration“ steht visuell hinter Navigation, Pipeline und Verwaltung.

**Deterministischer Scan:** Der CLI-Detector fand nur zwei echte Warnungen: `transition: width` in `board.css:151` und `breaks.css:278`. Beide lassen sich mit `transform: scaleX(...)` lösen; der Break-Timer ist wegen seiner wiederholten Updates wichtiger. Der Browser-Scan meldete 12 Elementgruppen/14 Treffer. Belastbar sind besonders 3,4:1 Kontrast für `.side-label` und 10,5 px Funktionstext für `.nav-key`. Die acht Cyan-Palette-Treffer sind überwiegend vererbte Doppelzählungen auf SVG-Nachfahren; „flat type hierarchy“ und „dark glow“ wurden an einer unvollständigen Shell gemessen und sind keine tragfähigen Einzelbefunde.

**Visuelle Overlays:** Die Injektion funktionierte technisch, aber die Browser-Instanz bot keine sichtbare Human-Tab-Präsentation. Der frische Tab wurde geschlossen; es bleibt kein verlässlich sichtbares Overlay. Die lokale Browserroute blieb wegen der fehlenden Wails-Bridge im Hauptbereich leer. Eine native Aufnahme bestätigte daher nur die Grundkomposition, nicht alle Ansichten oder den pixelgenauen aktuellen Source-Stand.

## Overall Impression

Die UI wirkt kompetent, schnell und ungewöhnlich produktspezifisch. Der größte Hebel ist kein Reskin, sondern eine strukturelle Neuordnung: zuerst zeigen, wer den Entwickler braucht; dann den Kontext zum Handeln; Verwaltung und seltene Werkzeuge erst danach. Accessibility und Vertrauenssignale sind aktuell die Release-relevanten Schwächen.

## What's Working

1. **Aufmerksamkeitszustände sind stark codiert.** Wartende Sessions kombinieren Farbe, Text, Symbol, globale Anzahl und einen erklärenden Terminalzustand.
2. **Kontext bleibt erhalten.** Branch, Worktree, Alter, Unread, Historie, Suche, „Für später“, Dock und Hydra unterstützen den Wechsel zwischen parallelen Arbeiten sehr gut.
3. **Magentic hat echte Signaturdetails.** Namensbasierte Roboter, Hydra und der agentenbewusste Pausenmodus erzeugen Charakter ohne dekoratives Rauschen.

## Cognitive Load

Sechs von acht Prüfpunkten scheitern: Single Focus, visuelle Hierarchie, One Thing at a Time, Minimal Choices, Progressive Disclosure und Chunking. Gruppierung sowie sichtbarer Kontext/Working Memory sind gut. Besonders belastend sind acht globale Ziele vor dem Sessionbereich, sieben Projektaktionen plus Branch-Konfiguration, sechs Sessionaktionen im Terminalkopf und acht gleichzeitig sichtbare Pauseneinstellungen.

## Priority Issues

| Priorität | Before | After | Why | Suggested command |
| --- | --- | --- | --- | --- |
| **P1** | Acht Navigationsziele sowie Deploy stehen vor Attention und Sessions; die Übersicht beginnt mit Pipeline/Argo. | „Braucht dich“ direkt unter die Marke, danach aktive/aktuelle Sessions; Tools und Pipeline als sekundäre Utilities oder kontextuelle Projekt-Linsen. | Der Nutzer scannt Verwaltung, bevor er weiß, wo er gebraucht wird. | `$impeccable layout` |
| **P1** | Navigation, Sessionzeilen, Attention, Suchtreffer und Menüs sind teils klickbare Divs/Spans; Modal-Fokus, Toast-Live-Regionen und zugängliche Diagrammnamen fehlen. | Semantische Buttons/Links/Nav, `aria-current`/`aria-keyshortcuts`, tastaturfähige Menüs, Dialog-Fokusvertrag, `role=status/alert`, benannte Charts plus Tabellenalternative. | Tastatur und Screenreader können den primären Flow nicht zuverlässig abschließen. | `$impeccable audit` |
| **P1** | Pointer-Kill bestätigt, `⌘⇧W` beendet sofort; Refresh-Fehler werden verschluckt; rohe Fehler verschwinden nach fünf Sekunden. | Ein gemeinsamer Kill-Vertrag, reversibles Parken als Default und ein persistenter Stale-Data-Zustand mit Zeitstempel und Retry. | Ein persistentes Kontrollzentrum darf Datenverlust oder veralteten Status nie wie Erfolg aussehen lassen. | `$impeccable harden` |
| **P2** | Sieben gleichgewichtete Projektaktionen und sechs Aktionen im Sessionkopf. | Zwei häufige Aktionen sichtbar; Graph/Board als Linsen; Terminal, Deploy, Branch-Settings und Entfernen in ein beschriftetes, tastaturfähiges Aktionsmenü. | Nutzer müssen dieselben Toolbars immer wieder neu parsen; Seltenes konkurriert mit dem Kernfluss. | `$impeccable distill` |
| **P2** | „Claude-Session“, Claude-Rollen und Claude-spezifische Aktionen dominieren trotz agentenagnostischem Produktversprechen. | „Agent-Session“ als Primärsprache, Provider als sekundäres Badge; `/done`/Deploy über explizite Fähigkeiten je Provider. | Codex-/Gemini-Nutzer erleben das Produkt sonst als Claude-Frontend. | `$impeccable clarify` |

## Persona Red Flags

**Alex (Power User):** Die Shortcuts, Hydra und Dock sind stark. Es fehlen jedoch „nächste wartende Session“ und statusbasierte Navigation; ab Session zehn gibt es keinen Direktshortcut; Hydra zeigt maximal sechs; `⌘⇧W` ist gefährlich schneller als der geschützte Pointer-Pfad; Projekt-Reorder ist pointer-only.

**Sam (Screenreader/Tastatur):** Primärnavigation, Sessionzeilen, Attention, Suchtreffer, Git-Elemente, Deployzeilen und Untermenüs sind nicht durchgängig semantisch erreichbar. Das generische Modal hat keinen vollständigen Dialog-/Fokusvertrag. Toasts werden nicht angekündigt. Chartdetails sind unbenannte `role=img`-SVGs mit Pointer-Tooltips.

**Jordan (First-Timer):** Vor der ersten Session stehen acht globale Ziele. „Hydra“, „Zeitgeist“, Worktree, `/done`, ArgoCD und Modifier-Klicks sind nur über Hover oder gar nicht erklärt. Ein Klick auf den Projektnamen öffnet überraschend Hydra. Eine kurze Einführung in den Kernloop fehlt; der Board-Empty-State ist die positive Ausnahme.

## Minor Observations

- Deutsch und Englisch mischen sich: „läuft“, „idle“, „done“, „deploy“, „Turns“, „Cache-Read“.
- `.proj-add` (18×18 px) und `.nav-gear` (20×20 px) liegen unter dem 24-px-Desktop-Floor.
- Reduced Motion deckt Terminal und Break gut ab, aber nicht alle Sidebar-/Deploy-Pulse.
- Die Detector-Treffer in Board- und Break-Fortschritt sollten auf `scaleX` umgestellt werden.
- Der 3,4:1-Kontrast des „Sessions“-Labels und der 10,5-px-Shortcuttext sind kleine, aber reale Lesbarkeitsfehler.
- Der Wechsel vom dunklen Cockpit zum hellen Single-Session-Fokus kann ein starkes Signaturmuster sein, sollte aber bewusst validiert und dokumentiert werden.

## Questions to Consider

- Wenn Attention das Produkt ist: Warum ist „Wer braucht mich jetzt?“ kein eigener Startzustand?
- Sind Graph, Board und Statistik globale Ziele oder Linsen auf das gerade ausgewählte Projekt?
- Wenn „Für später“ der sichere reversible Close ist: Darf ein Shortcut jemals direkt killen?
- Würde ein Codex-only-Entwickler nach einer Minute glauben, dass Magentic für ihn gebaut ist?
