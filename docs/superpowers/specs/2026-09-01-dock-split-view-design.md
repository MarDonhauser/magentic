# Split View für das Terminal-Dock

## Ziel

Das Terminal-Dock (`app/frontend/src/dock.js`) zeigt heute genau einen aktiven
Tab in einem einzelnen Pane. Split View erlaubt, das Dock beliebig horizontal
und vertikal in mehrere gleichzeitig sichtbare Panes aufzuteilen — jedes mit
eigenem Tab-Streifen — ausgelöst per Drag & Drop eines Tabs oder über ein
Rechtsklick-Kontextmenü am Tab.

## Nicht-Ziele

- Kein Split außerhalb des Terminal-Docks (keine Editor-/App-Panes betroffen).
- Kein geteiltes Anzeigen desselben Tabs in mehreren Panes — ein Tab gehört
  immer genau einem Blatt.
- Keine automatisierte UI-Test-Suite (siehe Testing unten).

## Datenmodell

Der heutige flache `tabs`-Map-Zustand wird durch einen rekursiven Split-Baum
ersetzt. Jeder Knoten ist entweder ein Blatt oder ein Split:

```js
// Blatt: heutiges Verhalten (Tab-Streifen + Terminal-Panes), gekapselt
{ type: 'leaf', id, tabs: Map<key, TabState>, activeKey }

// Split: zwei Kinder, Richtung, Größenverhältnis
{ type: 'split', id, dir: 'row' | 'column', ratio: 0.5, a: Node, b: Node }
```

`rootNode` ersetzt den heutigen globalen Zustand. `focusedLeafId` trackt das
zuletzt fokussierte Blatt (für `⌘⌥←/→`-Tab-Navigation und "neues Terminal im
aktiven Pane öffnen" — beides bisher global, jetzt pro Blatt).

Jeder Tab gehört zu genau einem Blatt. Verschieben eines Tabs (Drag oder
Kontextmenü-Split) entfernt ihn aus seinem Ursprungsblatt und fügt ihn im
Zielblatt ein.

## Rendering

Neue Funktion `renderNode(node)` baut das DOM rekursiv:

- **Split-Knoten** → Flex-Container (`flex-direction: row` bzw. `column`,
  abhängig von `dir`) mit zwei Kind-Containern im Verhältnis `ratio` /
  `1 - ratio`, dazwischen ein `dk-split-grip`.
- **Blatt-Knoten** → heutiges Markup (Tab-Streifen `dk-tabs` + `dk-body` mit
  `dk-pane`-Elementen), jetzt einmal pro Blatt statt einmal global.

Bestehende Funktionen, die aktuell auf dem einzigen globalen Tab-Streifen
operieren (`addTab`, `activate`, `closeDockTab`, `stepTab`, `updateBlank`,
`syncDot`), werden auf den Blatt-Kontext umgestellt (Blatt als Parameter statt
Modul-globaler State).

## Drag & Drop (Tab → Split)

- Tab-Elemente (`.dk-tab`) erhalten `draggable="true"`.
- Während des Drags über einem Blatt-Bereich werden vier Drop-Zonen
  angezeigt (Randstreifen links/rechts/oben/unten, ~25 % der jeweiligen
  Kantenlänge), mit halbtransparentem Overlay zur Vorschau der resultierenden
  Aufteilung — analog zu VS Code.
- Drop auf eine Zone: Zielblatt wird durch einen neuen Split-Knoten ersetzt
  (`dir` je nach Zone: links/rechts → `row`, oben/unten → `column`; `ratio =
  0.5`); der gezogene Tab landet im neuen Blatt auf der durch die Zone
  bestimmten Seite. Der Tab wird aus seinem Ursprungsblatt entfernt.
- Ursprungsblatt kollabiert automatisch, falls es dadurch leer wird (siehe
  Kollaps-Logik unten).
- Drop in der Mitte eines Blatts (außerhalb der Randzonen) verhält sich wie
  heutiges Tab-Reordering innerhalb desselben Streifens (kein Split).

## Rechtsklick-Kontextmenü

`contextmenu` auf `.dk-tab` unterdrückt das native Menü und zeigt ein
minimalistisches Menü mit vier Einträgen: "Nach links teilen", "Nach rechts
teilen", "Nach oben teilen", "Nach unten teilen". Nutzt dieselbe Split-Logik
wie Drag & Drop; Zielblatt ist hier das Blatt des Tabs selbst, das sich in
zwei Blätter aufteilt (der Tab wandert in das neue, das ursprüngliche Blatt
behält die übrigen Tabs).

## Resize

Jeder `dk-split-grip` funktioniert analog zum bestehenden Höhen-Grip
(`startDrag` in `dock.js`): `mousedown` startet Drag, `mousemove` passt
`ratio` live an (inkl. `requestAnimationFrame`-Drosselung wie beim
bestehenden Höhen-Resize), `mouseup` persistiert. Minimum-Ratio 0.15 (bzw.
0.85 als Maximum), damit kein Pane auf 0 kollabieren kann.

## Kollaps beim Schließen

`closeDockTab` operiert jetzt auf dem Blatt, dem der Tab gehört. Wird ein
Blatt dadurch leer, wird sein Elternteil (Split-Knoten) im Baum durch das
verbliebene Geschwisterblatt ersetzt. Das läuft rekursiv nach oben, falls
mehrere Ebenen betroffen sind (z. B. verschachtelte Splits, deren letztes
Blatt geschlossen wird). Der `focusedLeafId` wird beim Kollaps auf das
übernehmende Geschwister-Blatt gesetzt.

## Persistenz

Das `STORE_KEY`-Objekt in `localStorage` bekommt statt `tabs: [...]` ein
`layout`-Feld: der serialisierte Split-Baum mit Tab-Refs (keine Live-Objekte
wie `term`/`fit`). `open`, `height` und weitere bestehende Felder bleiben
unverändert.

**Migration:** Fehlt `layout` in einem gespeicherten Zustand (alte Version),
wird aus dem vorhandenen `tabs`-Array ein einzelnes Blatt als `rootNode`
konstruiert — bestehende `localStorage`-Einträge bleiben nutzbar. Diese
Normalisierung gehört in `dock-state.js`, analog zu `normalizeDockState`.

## Betroffene Dateien

- `app/frontend/src/dock.js` — Kernlogik: Baum-Datenmodell, Rendering,
  Drag & Drop, Kontextmenü, Resize, Kollaps. Größter Umbau.
- `app/frontend/src/dock.css` — Split-Container-Layout, `dk-split-grip`,
  Drop-Zonen-Overlay, Kontextmenü-Styling.
- `app/frontend/src/dock-state.js` — Migration/Normalisierung des neuen
  `layout`-Felds, Serialisierung des Baums für `persist()`.

## Testing

Keine Playwright-/Jest-Infrastruktur für UI-Interaktionen ist im Projekt
vorhanden. Verifikation erfolgt manuell über den Wails-Dev-Server:

- Split per Drag (alle vier Richtungen) und per Rechtsklick-Menü erzeugen.
- Verschachtelte Splits (Split innerhalb eines Split-Blatts).
- Resize eines Split-Grips, inkl. Minimum-Ratio-Grenze.
- Letzten Tab in einem Blatt schließen → Kollaps, Fokus wandert korrekt.
- Neustart der App → gespeichertes Layout wird korrekt wiederhergestellt.
- Alter `localStorage`-Eintrag ohne `layout`-Feld → Migration zu einem
  Einzel-Blatt-Layout funktioniert.
