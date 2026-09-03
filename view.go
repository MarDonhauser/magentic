package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"magentic/core"
)

var (
	colAccent  = lipgloss.Color("205")
	colRunning = lipgloss.Color("42")
	colAgents  = lipgloss.Color("44")
	colBlocked = lipgloss.Color("214")
	colIdle    = lipgloss.Color("245")
	colTerm    = lipgloss.Color("111")
	colDead    = lipgloss.Color("196")
	colDim     = lipgloss.Color("240")
	colText    = lipgloss.Color("252")

	styleTitle   = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	styleAgents  = lipgloss.NewStyle().Foreground(colAgents)
	styleDim     = lipgloss.NewStyle().Foreground(colDim)
	styleText    = lipgloss.NewStyle().Foreground(colText)
	styleProj    = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111"))
	styleSel     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("231")).Background(lipgloss.Color("54"))
	styleErr     = lipgloss.NewStyle().Bold(true).Foreground(colDead)
	styleOK      = lipgloss.NewStyle().Foreground(colRunning)
	styleWarn    = lipgloss.NewStyle().Foreground(colBlocked)
	styleSection = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("111"))
	// Fortsetzbare Sessions sind weder Alarm (rot) noch Aufforderung (gelb):
	// ruhiges Petrol für einen Eintrag, der nur auf seinen Einsatz wartet.
	styleResumable = lipgloss.NewStyle().Foreground(colAgents)
)

// linePainter applies a colour-only Style to one single-line string. Style.Render
// measures the display width of everything it styles, and for the terminal
// preview that grapheme walk costs more than the rest of a render together. The
// sequences are taken from the Style itself, so a changed colour still follows.
type linePainter struct{ prefix, suffix string }

func newLinePainter(style lipgloss.Style) linePainter {
	prefix, suffix, _ := strings.Cut(style.Render("\x00"), "\x00")
	return linePainter{prefix: prefix, suffix: suffix}
}

func (p linePainter) paint(line string) string { return p.prefix + line + p.suffix }

var paintDim = newLinePainter(styleDim)

func statusStyle(s AgentStatus) lipgloss.Style {
	if style, ok := statusStyleCache[s]; ok {
		return style
	}
	return statusStyleCache[StatusIdle]
}

// statusStyleCache keeps one Style per status for the render hot path.
// Building a Style per line (lipgloss.NewStyle().Foreground(...)) allocates on
// every agent line of every frame; these values are fixed, so they are built
// once. Style is used by value, copying one is cheap.
var statusStyleCache = map[AgentStatus]lipgloss.Style{
	StatusRunning: lipgloss.NewStyle().Foreground(colRunning),
	StatusAgents:  lipgloss.NewStyle().Foreground(colAgents),
	StatusShell:   lipgloss.NewStyle().Foreground(colAgents),
	StatusBlocked: lipgloss.NewStyle().Foreground(colBlocked).Bold(true),
	StatusDone:    lipgloss.NewStyle().Foreground(colAgents),
	StatusDead:    lipgloss.NewStyle().Foreground(colDead),
	StatusTerm:    lipgloss.NewStyle().Foreground(colTerm),
	StatusIdle:    lipgloss.NewStyle().Foreground(colIdle),
	StatusExited:  lipgloss.NewStyle().Foreground(colIdle),
	StatusUnknown: lipgloss.NewStyle().Foreground(colIdle),
}

// printableASCII reports whether every byte is a printable ASCII character. For
// such a string the display width equals its byte length, so trunc and pad can
// skip the grapheme segmentation that dominates a render.
func printableASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}
	return true
}

func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if printableASCII(s) {
		if len(s) <= w {
			return s
		}
		return s[:w-1] + "…"
	}
	return ansi.Truncate(s, w, "…")
}

func pad(s string, w int) string {
	if printableASCII(s) {
		if len(s) >= w {
			return trunc(s, w)
		}
		return s + strings.Repeat(" ", w-len(s))
	}
	gap := w - lipgloss.Width(s)
	if gap <= 0 {
		return trunc(s, w)
	}
	return s + strings.Repeat(" ", gap)
}

func (m model) layout() (treeW, detailW, innerH int) {
	innerH = m.height - 4
	if innerH < 3 {
		innerH = 3
	}
	treeW = m.treeWidth()
	detailW = m.width - treeW
	if detailW < 20 {
		detailW = 20
	}
	return
}

func (m model) layoutWithTreeW(treeW int) (detailW, innerH int) {
	innerH = m.height - 4
	if innerH < 3 {
		innerH = 3
	}
	detailW = m.width - treeW
	if detailW < 20 {
		detailW = 20
	}
	return
}

// frameRows is one View's coherent tree: the rows, the name width every agent
// line pads to, the per-project session counts, and the selected row. View
// computes it once and hands it down, so a frame builds and sorts the tree a
// single time instead of once per panel plus once per selection lookup.
type frameRows struct {
	rows    []treeRow
	nameW   int
	counts  map[string]int
	orphans int
	sel     *treeRow
}

func (m model) frame() frameRows {
	rows := m.rows()
	f := frameRows{rows: rows, nameW: m.maxAgentNameLen()}
	if m.state != nil {
		known := make(map[string]bool, len(m.state.Projects))
		for i := range m.state.Projects {
			known[m.state.Projects[i].Name] = true
		}
		f.counts = make(map[string]int, len(m.state.Projects))
		for i := range m.state.Agents {
			if name := m.state.Agents[i].Project; name != "" && known[name] {
				f.counts[name]++
			} else {
				f.orphans++
			}
		}
	}
	if m.cursor >= 0 && m.cursor < len(rows) {
		r := rows[m.cursor]
		f.sel = &r
	}
	return f
}

func (f frameRows) selectedAgent() *Agent {
	if f.sel != nil && f.sel.kind == rowAgent {
		return &f.sel.agent
	}
	return nil
}

func (m model) contextProjectIn(rows []treeRow) *Project {
	if m.cursor < 0 || m.cursor >= len(rows) {
		return nil
	}
	r := rows[m.cursor]
	if r.project != nil {
		return r.project
	}
	if r.kind == rowAgent && r.agent.Project != "" {
		return m.state.ProjectByName(r.agent.Project)
	}
	return nil
}

func (m model) View() string {
	if m.width == 0 {
		return "starte…"
	}
	f := m.frame()
	treeW := m.treeWidthWithNameW(f.nameW)
	detailW, innerH := m.layoutWithTreeW(treeW)

	header := m.renderHeader()
	detailContent := m.renderDetailsIn(f, detailW-4, innerH)
	tree := m.renderPanel(m.renderTreeIn(f, treeW-4, innerH), treeW-2, innerH, true)
	details := m.renderPanel(detailContent, detailW-2, innerH, false)
	body := lipgloss.JoinHorizontal(lipgloss.Top, tree, details)
	footer := m.renderFooter()
	return header + "\n" + body + "\n" + footer
}

func (m model) renderPanel(content string, w, h int, focused bool) string {
	borderCol := colDim
	if focused {
		borderCol = colAccent
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderCol).
		Padding(0, 1).
		Width(w).
		Height(h).
		MaxHeight(h + 2).
		Render(content)
}

func (m model) renderHeader() string {
	counts := map[AgentStatus]int{}
	resumable := 0
	for _, session := range m.state.Agents {
		if _, res, _ := m.resumabilityFor(session); res.Resumable {
			resumable++
			continue
		}
		counts[m.statusFor(session)]++
	}
	title := styleTitle.Render(" ⚡ magentic ")
	doneSeg := ""
	if counts[StatusDone] > 0 {
		doneSeg = fmt.Sprintf("%s %d fertig   ", styleAgents.Render("✓"), counts[StatusDone])
	}
	resumableSeg := ""
	if resumable > 0 {
		resumableSeg = fmt.Sprintf("%s %d fortsetzbar   ", styleResumable.Render("↻"), resumable)
	}
	stats := fmt.Sprintf("%s %d läuft   %s%s%s %d wartet   %s %d idle   %s %d aus",
		styleOK.Render("●"), counts[StatusRunning],
		doneSeg,
		resumableSeg,
		styleWarn.Render("◆"), counts[StatusBlocked],
		styleDim.Render("○"), counts[StatusIdle],
		styleErr.Render("✗"), counts[StatusExited]+counts[StatusDead])
	if counts[StatusUnknown] > 0 {
		stats += fmt.Sprintf("   %s %d unbekannt", styleDim.Render("?"), counts[StatusUnknown])
	}
	if zg := m.poll.zeitgeist; zg.Active {
		sym, sty := "▶", styleOK
		if zg.State == "paused" {
			sym, sty = "⏸", styleWarn
		}
		stats = sty.Render("⏱ "+sym+" "+zg.Project+" "+formatDurShort(zg.ElapsedSec)) + styleDim.Render("   ·   ") + stats
	}
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(stats) - 1
	if gap < 1 {
		gap = 1
	}
	return title + strings.Repeat(" ", gap) + stats
}

func (m model) projectLine(r treeRow, w int) string {
	return m.projectLineIn(m.frame(), r, w)
}

func (m model) projectLineIn(f frameRows, r treeRow, w int) string {
	name := "(ohne projekt)"
	count := f.orphans
	key := orphanKey
	dirty := ""
	if r.project != nil {
		name = r.project.Name
		count = f.counts[r.project.Name]
		key = r.project.Name
		repository := m.repositoryFactsForProject(*r.project)
		if repository.presence == core.RepositoryKnown {
			if repository.changes.Known() && repository.changes.Value.Clean() {
				dirty = styleDim.Render(" ✓")
			} else if repository.changes.Known() {
				dirty = styleWarn.Render(" ±")
			} else {
				dirty = styleDim.Render(" ?")
			}
		} else if repository.presence == core.RepositoryUnknown {
			dirty = styleDim.Render(" ?")
		}
	}
	arrow := "▾"
	if m.collapsed[key] {
		arrow = "▸"
	}
	label := fmt.Sprintf("%s %s", arrow, name)
	counts := fmt.Sprintf("%d", count)
	return pad(styleProj.Render(trunc(label, w-8)), w-4) + dirty + " " + styleDim.Render(counts)
}

func (m model) agentLine(a Agent, w int) string {
	return m.agentLineWith(a, w, m.maxAgentNameLen(), m.statusFor(a))
}

func (m model) agentLineWith(a Agent, w, nameW int, st AgentStatus) string {
	if _, res, ok := m.resumabilityFor(a); ok && res.Resumable {
		return m.resumableLine(a, w, nameW)
	}
	icon := statusStyle(st).Render(st.Icon())
	name := pad(trunc(a.Name, nameW), nameW+1)
	status := statusStyle(st).Render(pad(st.Label(), 11))
	lastActive := a.CreatedAt
	if observation, ok := m.observationFor(a); ok && observation.ActivityKnown {
		lastActive = observation.Activity
	}
	age := pad(formatAge(lastActive), 7)
	marks := styleDim.Render("? ")
	switch m.sessionChangeMark(a) {
	case sessionChangesClean:
		marks = styleDim.Render("✓ ")
	case sessionChangesDirty:
		marks = styleWarn.Render("± ")
	case sessionChangesNotRepository:
		marks = "  "
	}
	wt := " "
	if a.Worktree {
		wt = styleDim.Render("⑂")
	}
	return fmt.Sprintf("  %s %s%s%s%s%s", icon, name, status, age, marks, wt)
}

// resumableLine renders a Session without a runtime whose record is intact:
// its own icon and label, the last known status with its time in the age
// column, and the usual Git marks. It keeps its Project group and row shape.
func (m model) resumableLine(a Agent, w, nameW int) string {
	icon := styleResumable.Render(core.ResumableStatusIcon)
	name := pad(trunc(a.Name, nameW), nameW+1)
	status := styleResumable.Render(pad(core.ResumableStatusLabel, 11))
	age := pad("?", 7)
	if !a.LastStatusAt.IsZero() {
		age = pad(formatAge(a.LastStatusAt), 7)
	}
	marks := styleDim.Render("? ")
	switch m.sessionChangeMark(a) {
	case sessionChangesClean:
		marks = styleDim.Render("✓ ")
	case sessionChangesDirty:
		marks = styleWarn.Render("± ")
	case sessionChangesNotRepository:
		marks = "  "
	}
	wt := " "
	if a.Worktree {
		wt = styleDim.Render("⑂")
	}
	return fmt.Sprintf("  %s %s%s%s%s%s", icon, name, status, age, marks, wt)
}

func (m model) renderTree(w, h int) string {
	return m.renderTreeIn(m.frame(), w, h)
}

func (m model) renderTreeIn(f frameRows, w, h int) string {
	rows := f.rows
	var lines []string
	if len(rows) == 0 {
		lines = []string{styleDim.Render("Keine Projekte."), "", styleDim.Render("p = Projekt hinzufügen")}
	}
	for i, r := range rows {
		var line string
		switch r.kind {
		case rowProject:
			line = m.projectLineIn(f, r, w)
		case rowAgent:
			line = m.agentLineWith(r.agent, w, f.nameW, m.statusFor(r.agent))
		case rowSep:
			line = styleSection.Render(r.label) + " " + styleDim.Render(strings.Repeat("─", max(0, w-len([]rune(r.label))-1)))
			lines = append(lines, trunc(line, w))
			continue
		case rowHint:
			lines = append(lines, trunc(styleDim.Render(" "+r.label), w))
			continue
		}
		if i == m.cursor {
			plain := ansi.Strip(line)
			line = styleSel.Render(pad(plain, w))
		}
		lines = append(lines, trunc(line, w))
	}
	extra := append(m.zeitgeistLines(w), m.usageLines(w)...)
	if len(extra) > 0 && len(lines)+len(extra) < h {
		for len(lines) < h-len(extra) {
			lines = append(lines, "")
		}
		lines = append(lines, extra...)
	}
	return strings.Join(lines, "\n")
}

func (m model) zeitgeistLines(w int) []string {
	zg := m.poll.zeitgeist
	if !zg.Exists {
		return nil
	}
	lines := []string{
		styleDim.Render(strings.Repeat("─", max(0, w))),
		styleSection.Render("Zeitgeist"),
	}
	if zg.Active {
		sym, sty := "▶", styleOK
		if zg.State == "paused" {
			sym, sty = "⏸", styleWarn
		}
		lines = append(lines, trunc(sty.Render(sym+" "+zg.Project)+" "+
			styleText.Render(formatDurShort(zg.ElapsedSec))+styleDim.Render(" · "+formatEuro(zg.Earnings)), w))
	} else {
		lines = append(lines, trunc(styleDim.Render("○ kein Timer · z startet"), w))
	}
	if zg.TodaySec > 0 {
		lines = append(lines, trunc(styleDim.Render("heute "+formatDurShort(zg.TodaySec)+" · "+formatEuro(zg.TodayCash)), w))
	}
	return lines
}

func usageBar(pct float64, width int) string {
	filled := int(pct/100*float64(width) + 0.5)
	if filled > width {
		filled = width
	}
	col := styleOK
	if pct >= 90 {
		col = styleErr
	} else if pct >= 70 {
		col = styleWarn
	}
	return col.Render(strings.Repeat("▓", filled)) + styleDim.Render(strings.Repeat("░", width-filled))
}

func (m model) usageLines(w int) []string {
	u := m.usage
	if u.FetchedAt.IsZero() {
		return nil
	}
	if u.Err != "" {
		return []string{styleDim.Render(trunc("Usage: "+u.Err, w))}
	}
	barW := 10
	l1 := fmt.Sprintf("5h %s %3.0f%% %s", usageBar(u.FiveHour, barW), u.FiveHour,
		styleDim.Render("↻"+u.FiveHourReset.Format("15:04")))
	l2 := fmt.Sprintf("7d %s %3.0f%% %s", usageBar(u.SevenDay, barW), u.SevenDay,
		styleDim.Render("↻"+shortWeekday(u.SevenDayReset)))
	return []string{
		styleDim.Render(strings.Repeat("─", max(0, w))),
		styleSection.Render("Claude-Usage"),
		trunc(l1, w),
		trunc(l2, w),
	}
}

func (m model) renderDetails(w, h int) string {
	return m.renderDetailsIn(m.frame(), w, h)
}

func (m model) renderDetailsIn(f frameRows, w, h int) string {
	if m.inboxOpen {
		return strings.Join(m.inboxLines(w, h), "\n")
	}
	lines, _ := m.detailContentIn(f, w, h)
	return strings.Join(lines, "\n")
}

// waitingKindLabel names what the listed Session asks of the developer.
func waitingKindLabel(kind core.AttentionWaitingKind) string {
	if kind == core.AttentionWaitingReview {
		return "Ergebnis wartet auf einen Blick"
	}
	return "Frage oder Freigabe offen"
}

// waitingTimeLabel keeps a wait whose start is unknown recognizable as a lower
// bound instead of presenting it as a wait that just began.
func waitingTimeLabel(entry core.AttentionInboxEntry) string {
	if entry.WaitingSince.IsZero() {
		return "wartet seit unbekannter Zeit"
	}
	if entry.WaitingSinceKnown {
		return "wartet seit " + formatAge(entry.WaitingSince)
	}
	return "wartet mindestens seit " + formatAge(entry.WaitingSince)
}

// inboxLines renders the planned inbox read-only. The order is the one the
// attention planner produced; nothing is sorted, filtered or derived here.
func (m model) inboxLines(w, h int) []string {
	var lines []string
	add := func(s string) { lines = append(lines, trunc(s, w)) }
	add(styleTitle.Render("Posteingang") + styleDim.Render(" · ⏎ öffnet die Session · esc schließt"))
	switch m.inbox.State {
	case core.AttentionInboxUnavailable:
		add(styleWarn.Render("Die wartenden Sessions konnten gerade nicht gelesen werden."))
		return lines
	case core.AttentionInboxIncomplete:
		add(styleWarn.Render("Von einem Teil der Sessions ist gerade nicht bekannt, ob sie warten."))
	}
	rows := m.inboxRows()
	if len(rows) == 0 {
		add(styleDim.Render("Im Moment wartet keine Session auf dich."))
		return lines
	}
	// One block per entry, so a list longer than the panel scrolls by whole
	// entries and the selected one always stays visible.
	blocks := make([][]string, 0, len(rows))
	for index, row := range rows {
		name, project := string(row.entry.SessionID), "—"
		if row.known {
			name = row.agent.Name
			if row.agent.Project != "" {
				project = row.agent.Project
			}
		}
		marker := "  "
		if index == m.inboxCursor {
			marker = styleTitle.Render("▸ ")
		}
		block := []string{
			trunc(marker+styleText.Render(name)+styleDim.Render(" · "+project), w),
			trunc("    "+styleWarn.Render(waitingKindLabel(row.entry.Kind))+
				styleDim.Render(" · "+waitingTimeLabel(row.entry)), w),
			trunc("    "+styleDim.Render(inboxReason(row.entry)), w),
		}
		blocks = append(blocks, block)
	}
	first := 0
	for {
		height := len(lines)
		for _, block := range blocks[first:] {
			height += len(block)
		}
		if height <= h || first >= m.inboxCursor {
			break
		}
		first++
	}
	for _, block := range blocks[first:] {
		lines = append(lines, block...)
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

// inboxReason gives the one line that explains the wait, or says that the
// Session's output could not be read.
func inboxReason(entry core.AttentionInboxEntry) string {
	if !entry.ExcerptKnown {
		return "Der Grund ist nicht bekannt — die Ausgabe konnte nicht gelesen werden."
	}
	lines := strings.Split(strings.TrimRight(entry.Excerpt, "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if text := strings.TrimSpace(lines[index]); text != "" {
			return text
		}
	}
	return "Die Session zeigt gerade keinen Text, der die Frage erklärt."
}

func (m model) detailContent(w, h int) ([]string, int) {
	return m.detailContentIn(m.frame(), w, h)
}

func (m model) detailContentIn(f frameRows, w, h int) ([]string, int) {
	a := f.selectedAgent()
	var proj *Project
	if a == nil {
		proj = m.contextProjectIn(f.rows)
	} else if f.sel != nil {
		proj = f.sel.project
		if proj == nil && a.Project != "" {
			proj = m.state.ProjectByName(a.Project)
		}
	}
	return m.detailBody(a, proj, w, h)
}

// detailBody renders the right panel for the given selection. The frame-based
// callers resolve the selection from their one shared tree; the plain
// detailContent wrapper keeps the event path (click hit-testing) working.
func (m model) detailBody(a *Agent, proj *Project, w, h int) ([]string, int) {
	var lines []string
	previewStart := -1
	add := func(s string) { lines = append(lines, trunc(s, w)) }

	if a == nil && proj == nil {
		return []string{
			styleDim.Render("Nichts ausgewählt."),
			"",
			styleDim.Render("p  Projekt hinzufügen"),
			styleDim.Render("n  neue Claude-Session"),
		}, -1
	}

	if a != nil {
		projName := a.Project
		if projName == "" {
			projName = "—"
		}
		add(styleTitle.Render(a.Name) + styleDim.Render(" · "+projName))
		observation, observed := m.observationFor(*a)
		st := StatusUnknown
		if observed {
			st = observation.Status
		}
		wtNote := ""
		if a.Worktree {
			wtNote = styleDim.Render(" · ⑂ worktree")
		}
		active := ""
		if observed && observation.ActivityKnown {
			active = " · aktiv " + formatAgeWord(observation.Activity)
		}
		if _, res, _ := m.resumabilityFor(*a); observed && res.Resumable {
			// Keine Runtime mehr, aber ein intakter Satz: Die Session zeigt,
			// was sie zuletzt getan hat — nicht, dass sie noch liefe.
			seen := ""
			if last := core.ResumeLastSeen(*a); last != "" {
				seen = " · " + last
			}
			add(styleResumable.Render(core.ResumableStatusIcon+" "+core.ResumableStatusLabel) + styleDim.Render(seen+active) + wtNote)
			add(styleDim.Render("R fortsetzen · x verwerfen"))
		} else {
			add(statusStyle(st).Render(st.Icon()+" "+st.Label()) + styleDim.Render(" · seit "+formatAge(a.CreatedAt)+active) + wtNote)
		}
		if observed && observation.Detail != "" {
			add(styleAgents.Render("◍ " + observation.Detail))
		}
		if observed && observation.Presence == core.SessionPresenceAbsent {
			if _, res, _ := m.resumabilityFor(*a); !res.Resumable && !res.Unknown && res.Reason != "" {
				add(styleDim.Render(res.Reason))
			}
		}
		if !observed || observation.Availability != core.ObservationAvailable {
			add(styleDim.Render("? Runtime-Status nicht vollständig verfügbar"))
		}
		add(styleDim.Render(shortPath(a.Dir)))
		add("")
		m.addAgentGit(a, w, add)
	} else {
		add(styleTitle.Render(proj.Name))
		add(styleDim.Render(shortPath(proj.Path)))
		add("")
		m.addRepoGit(*proj, add)
	}
	add("")

	if a != nil {
		observation, observed := m.observationFor(*a)
		previewKnown := observed && observation.ContentKnown
		preview := observation.Content
		remaining := h - len(lines) - 1
		if remaining > 3 && previewKnown && preview != "" {
			previewStart = len(lines)
			label := "Terminal · klick zum Öffnen "
			add(styleSection.Render("Terminal") + styleDim.Render(" · klick zum Öffnen "+strings.Repeat("─", max(0, w-len([]rune(label))-9))))
			appendPreviewTail(&lines, preview, remaining, w)
		} else if remaining > 3 && observed && observation.Presence == core.SessionPresencePresent && !previewKnown {
			add(styleSection.Render("Terminal"))
			add(" " + styleDim.Render("Inhalt unbekannt"))
		}
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines, previewStart
}

// appendPreviewTail appends at most maxLines trailing lines of the terminal
// preview. It scans for the last newlines first and splits only that suffix,
// so a 200-line capture shown in a 40-line panel splits 40 lines instead of
// 200 on every frame.
func appendPreviewTail(lines *[]string, preview string, maxLines, w int) {
	trimmed := strings.TrimRight(preview, "\n")
	start := 0
	seen := 0
	for i := len(trimmed) - 1; i >= 0 && seen < maxLines; i-- {
		if trimmed[i] == '\n' {
			seen++
			if seen == maxLines {
				start = i + 1
				break
			}
		}
	}
	for _, l := range strings.Split(trimmed[start:], "\n") {
		if strings.Contains(l, "\t") {
			l = strings.ReplaceAll(l, "\t", "  ")
		}
		// Truncate first, then colour: the raw line keeps trunc on its
		// ASCII fast path and the painter never measures at all.
		*lines = append(*lines, paintDim.paint(trunc(l, w)))
	}
}

type tuiRepositoryFacts struct {
	presence   core.RepositoryKnowledge
	problem    string
	checkout   core.RepositoryFact[core.RepositoryCheckout]
	changes    core.RepositoryFact[core.RepositoryWorkingChanges]
	divergence core.RepositoryFact[core.RepositoryDivergence]
	delta      *core.RepositoryBaselineDelta
}

func tuiRepositoryFactsFrom(inspection core.RepositoryInspection) tuiRepositoryFacts {
	return tuiRepositoryFacts{
		presence:   inspection.Presence,
		problem:    repositoryProblemMessage(inspection.Problem),
		checkout:   inspection.Checkout,
		changes:    inspection.Changes,
		divergence: inspection.Divergence,
		delta:      inspection.Delta,
	}
}

func (m model) repositoryFactsForAgent(session Agent) tuiRepositoryFacts {
	key := sessionKey(session)
	if inspection, ok := m.poll.inspections[key]; ok {
		return tuiRepositoryFactsFrom(inspection)
	}
	if problem, ok := m.poll.inspectionProblem[key]; ok {
		return tuiRepositoryFacts{presence: core.RepositoryUnknown, problem: problem}
	}
	return tuiRepositoryFacts{presence: core.RepositoryUnknown, problem: m.poll.repositoryProblem}
}

// Every checkout the TUI shows is inspected directly, so a Project row reads the
// same kind of fact as a Session row rather than a pre-computed Survey entry.
func (m model) repositoryFactsForProject(project Project) tuiRepositoryFacts {
	if inspection, ok := m.poll.projectInspections[repositoryDirectoryKey(project.Path)]; ok {
		return tuiRepositoryFactsFrom(inspection)
	}
	return tuiRepositoryFacts{presence: core.RepositoryUnknown, problem: m.poll.repositoryProblem}
}

func repositoryProblemMessage(problem *core.RepositoryProblem) string {
	if problem == nil {
		return ""
	}
	return problem.Message
}

type sessionChangeKnowledge int

const (
	sessionChangesUnknown sessionChangeKnowledge = iota
	sessionChangesClean
	sessionChangesDirty
	sessionChangesNotRepository
)

type tuiSessionChangeFacts struct {
	baseline bool
	paths    core.RepositoryFact[[]string]
	commits  core.RepositoryFact[int]
}

func sessionChanges(session Agent, repository tuiRepositoryFacts) tuiSessionChangeFacts {
	result := tuiSessionChangeFacts{baseline: session.BaseCommit != ""}
	if result.baseline {
		if repository.delta != nil {
			result.paths = repository.delta.Paths
			result.commits = repository.delta.Commits
		}
		return result
	}
	if repository.changes.Known() {
		result.paths = core.RepositoryFact[[]string]{
			State: core.RepositoryKnown,
			Value: append([]string(nil), repository.changes.Value.Paths...),
		}
	}
	return result
}

func (m model) sessionChangeMark(session Agent) sessionChangeKnowledge {
	repository := m.repositoryFactsForAgent(session)
	if repository.presence == core.RepositoryNotRepository {
		return sessionChangesNotRepository
	}
	if repository.presence != core.RepositoryKnown {
		return sessionChangesUnknown
	}
	changes := sessionChanges(session, repository)
	if changes.paths.Known() && len(changes.paths.Value) > 0 {
		return sessionChangesDirty
	}
	if changes.commits.Known() && changes.commits.Value > 0 {
		return sessionChangesDirty
	}
	if changes.baseline && (!changes.paths.Known() || !changes.commits.Known()) {
		return sessionChangesUnknown
	}
	if changes.paths.Known() {
		return sessionChangesClean
	}
	return sessionChangesUnknown
}

func (m model) addAgentGit(session *Agent, w int, add func(string)) {
	repository := m.repositoryFactsForAgent(*session)
	label := "Git · diese Session"
	if session.BaseCommit == "" {
		label = "Git · Arbeitsbaum (Session-Start unbekannt)"
	}
	add(styleSection.Render(label))
	if !renderRepositoryPresence(repository, add) {
		return
	}
	renderRepositoryHead(repository, add)

	changes := sessionChanges(*session, repository)
	if changes.baseline && changes.paths.Known() && changes.commits.Known() &&
		len(changes.paths.Value) == 0 && changes.commits.Value == 0 {
		add(" " + styleOK.Render("✓ nichts geändert"))
		return
	}
	if !changes.baseline && changes.paths.Known() && len(changes.paths.Value) == 0 {
		add(" " + styleOK.Render("✓ Arbeitsbaum sauber"))
		return
	}

	var known []string
	if changes.commits.Known() && changes.commits.Value > 0 {
		word := "Commits"
		if changes.commits.Value == 1 {
			word = "Commit"
		}
		known = append(known, fmt.Sprintf("%d %s", changes.commits.Value, word))
	}
	if changes.paths.Known() && len(changes.paths.Value) > 0 {
		word := "Dateien"
		if len(changes.paths.Value) == 1 {
			word = "Datei"
		}
		known = append(known, fmt.Sprintf("%d %s geändert", len(changes.paths.Value), word))
	}
	if len(known) > 0 {
		add(" " + styleWarn.Render("± "+strings.Join(known, " · ")))
	}
	var unknown []string
	if !changes.paths.Known() {
		unknown = append(unknown, "Dateien")
	}
	if changes.baseline && !changes.commits.Known() {
		unknown = append(unknown, "Commits")
	}
	if len(unknown) > 0 {
		add(" " + styleDim.Render("? "+strings.Join(unknown, " und ")+" unbekannt"))
	}
	maxFiles := 6
	for i, file := range changes.paths.Value {
		if i == maxFiles {
			add("   " + styleDim.Render(fmt.Sprintf("… +%d weitere", len(changes.paths.Value)-maxFiles)))
			break
		}
		add("   " + styleDim.Render(trunc(file, w-4)))
	}
}

func (m model) addRepoGit(project Project, add func(string)) {
	add(styleSection.Render("Git"))
	repository := m.repositoryFactsForProject(project)
	if !renderRepositoryPresence(repository, add) {
		return
	}
	renderRepositoryHead(repository, add)
	if !repository.changes.Known() {
		add(" " + styleDim.Render("? Arbeitsbaum-Status unbekannt"))
		return
	}
	changes := repository.changes.Value
	if changes.Clean() {
		add(" " + styleOK.Render("✓ sauber"))
		return
	}
	parts := []string{}
	if changes.Staged > 0 {
		parts = append(parts, fmt.Sprintf("%d staged", changes.Staged))
	}
	if changes.Modified > 0 {
		parts = append(parts, fmt.Sprintf("%d geändert", changes.Modified))
	}
	if changes.Untracked > 0 {
		parts = append(parts, fmt.Sprintf("%d neu", changes.Untracked))
	}
	if changes.Conflicted > 0 {
		parts = append(parts, fmt.Sprintf("%d Konflikte", changes.Conflicted))
	}
	add(" " + styleWarn.Render("± "+strings.Join(parts, " · ")))
}

func renderRepositoryPresence(repository tuiRepositoryFacts, add func(string)) bool {
	switch repository.presence {
	case core.RepositoryKnown:
		return true
	case core.RepositoryNotRepository:
		add(" " + styleDim.Render("kein Git-Repo"))
	default:
		add(" " + styleDim.Render("? Git-Status unbekannt"))
	}
	return false
}

func renderRepositoryHead(repository tuiRepositoryFacts, add func(string)) {
	checkout := "? Checkout unbekannt"
	checkoutStyle := styleDim
	if repository.checkout.Known() {
		checkoutStyle = styleText
		switch repository.checkout.Value.Kind {
		case core.RepositoryBranchCheckout:
			checkout = repository.checkout.Value.Branch
			if checkout == "" {
				checkout = "? Branch unbekannt"
				checkoutStyle = styleDim
			}
		case core.RepositoryDetached:
			checkout = "detached HEAD"
		case core.RepositoryUnborn:
			checkout = "unborn HEAD"
		case core.RepositoryBare:
			checkout = "bare Repository"
		default:
			checkout = "? Checkout unbekannt"
			checkoutStyle = styleDim
		}
	}
	divergence := styleDim.Render(" ↑↓?")
	if repository.divergence.Known() {
		value := repository.divergence.Value
		divergence = ""
		if value.Ahead > 0 {
			divergence += fmt.Sprintf(" ↑%d", value.Ahead)
		}
		if value.Behind > 0 {
			divergence += fmt.Sprintf(" ↓%d", value.Behind)
		}
		divergence = styleWarn.Render(divergence)
	}
	add(" " + checkoutStyle.Render(checkout) + divergence)
}

func (m model) renderFooter() string {
	if m.inputKind != inputNone {
		return " " + m.input.View()
	}
	if m.confirmKill {
		a := m.selectedAgent()
		name := ""
		if a != nil {
			name = a.Name
			if _, res, _ := m.resumabilityFor(*a); res.Resumable {
				return " " + styleWarn.Render(fmt.Sprintf("Eintrag %q verwerfen (Verzeichnis bleibt)? y/n", name))
			}
		}
		return " " + styleWarn.Render(fmt.Sprintf("Agent %q beenden (tmux-Session wird gekillt)? y/n", name))
	}
	if m.confirmRmProj {
		name := ""
		if r := m.selectedRow(); r != nil && r.project != nil {
			name = r.project.Name
		}
		return " " + styleWarn.Render(fmt.Sprintf("Projekt %q aus der Liste entfernen (Dateien bleiben)? y/n", name))
	}
	if m.flash != "" && time.Since(m.flashTime) < 5*time.Second {
		if m.flashIsErr {
			return " " + styleErr.Render(m.flash)
		}
		return " " + styleOK.Render(m.flash)
	}
	if m.inboxOpen {
		return " " + styleDim.Render(strings.Join([]string{"↑↓ wählen", "⏎ Session öffnen", "esc zurück", "g neu lesen", "q ende"}, " · "))
	}
	keys := []string{"n neu", "w worktree", "T terminal", "⏎ attach", "i posteingang", "d done", "D deploy", "z timer", "r name", "R fortsetzen", "x kill", "p projekt", "q ende"}
	return " " + styleDim.Render(strings.Join(keys, " · "))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
