package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

var (
	colAccent  = lipgloss.Color("205")
	colRunning = lipgloss.Color("42")
	colAgents  = lipgloss.Color("44")
	colBlocked = lipgloss.Color("214")
	colIdle    = lipgloss.Color("245")
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
)

func statusStyle(s AgentStatus) lipgloss.Style {
	switch s {
	case StatusRunning:
		return lipgloss.NewStyle().Foreground(colRunning)
	case StatusAgents:
		return lipgloss.NewStyle().Foreground(colAgents)
	case StatusBlocked:
		return lipgloss.NewStyle().Foreground(colBlocked).Bold(true)
	case StatusDead:
		return lipgloss.NewStyle().Foreground(colDead)
	default:
		return lipgloss.NewStyle().Foreground(colIdle)
	}
}

func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	return ansi.Truncate(s, w, "…")
}

func pad(s string, w int) string {
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

func (m model) View() string {
	if m.width == 0 {
		return "starte…"
	}
	treeW, detailW, innerH := m.layout()

	header := m.renderHeader()
	detailContent := m.renderDetails(detailW-4, innerH)
	tree := m.renderPanel(m.renderTree(treeW-4, innerH), treeW-2, innerH, true)
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
	for _, st := range m.poll.statuses {
		counts[st]++
	}
	title := styleTitle.Render(" ⚡ magentic ")
	agentsSeg := ""
	if counts[StatusAgents] > 0 {
		agentsSeg = fmt.Sprintf("%s %d agents   ", styleAgents.Render("◍"), counts[StatusAgents])
	}
	stats := fmt.Sprintf("%s %d läuft   %s%s %d wartet   %s %d idle   %s %d aus",
		styleOK.Render("●"), counts[StatusRunning],
		agentsSeg,
		styleWarn.Render("◆"), counts[StatusBlocked],
		styleDim.Render("○"), counts[StatusIdle],
		styleErr.Render("✗"), counts[StatusExited]+counts[StatusDead])
	gap := m.width - lipgloss.Width(title) - lipgloss.Width(stats) - 1
	if gap < 1 {
		gap = 1
	}
	return title + strings.Repeat(" ", gap) + stats
}

func (m model) projectLine(r treeRow, w int) string {
	name := "(ohne projekt)"
	count := len(m.orphanAgents())
	key := orphanKey
	dirty := ""
	if r.project != nil {
		name = r.project.Name
		count = len(m.state.AgentsFor(r.project.Name))
		key = r.project.Name
		if git, ok := m.poll.git[r.project.Path]; ok && git.IsRepo {
			if git.Clean() {
				dirty = styleDim.Render(" ✓")
			} else {
				dirty = styleWarn.Render(" ±")
			}
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
	st := m.poll.statuses[a.Name]
	icon := statusStyle(st).Render(st.Icon())
	nameW := m.maxAgentNameLen()
	name := pad(trunc(a.Name, nameW), nameW+1)
	status := statusStyle(st).Render(pad(st.Label(), 8))
	lastActive := a.CreatedAt
	if act, ok := m.poll.activity[a.Name]; ok {
		lastActive = act
	}
	age := pad(formatAge(lastActive), 7)
	marks := "  "
	if sc, ok := m.poll.session[a.Name]; ok {
		if len(sc.Files) == 0 && sc.Commits == 0 {
			marks = styleDim.Render("✓ ")
		} else {
			marks = styleWarn.Render("± ")
		}
	}
	wt := " "
	if a.Worktree {
		wt = styleDim.Render("⑂")
	}
	return fmt.Sprintf("  %s %s%s%s%s%s", icon, name, status, age, marks, wt)
}

func (m model) renderTree(w, h int) string {
	rows := m.rows()
	var lines []string
	if len(rows) == 0 {
		lines = []string{styleDim.Render("Keine Projekte."), "", styleDim.Render("p = Projekt hinzufügen")}
	}
	for i, r := range rows {
		var line string
		switch r.kind {
		case rowProject:
			line = m.projectLine(r, w)
		case rowAgent:
			line = m.agentLine(r.agent, w)
		case rowSep:
			line = styleSection.Render(r.label) + " " + styleDim.Render(strings.Repeat("─", max(0, w-len([]rune(r.label))-1)))
			lines = append(lines, trunc(line, w))
			continue
		case rowHint:
			lines = append(lines, trunc(styleDim.Render(" "+r.label), w))
			continue
		case rowTodo:
			line = m.todoLine(r.todoIdx, w)
		}
		if i == m.cursor {
			plain := ansi.Strip(line)
			line = styleSel.Render(pad(plain, w))
		}
		lines = append(lines, trunc(line, w))
	}
	usage := m.usageLines(w)
	if len(usage) > 0 && len(lines)+len(usage) < h {
		for len(lines) < h-len(usage) {
			lines = append(lines, "")
		}
		lines = append(lines, usage...)
	}
	return strings.Join(lines, "\n")
}

func (m model) todoDetail(idx, w int) []string {
	t := m.state.Todos[idx]
	lines := []string{styleSection.Render("Todo"), ""}
	wrapped := lipgloss.NewStyle().Width(w).Render(t.Text)
	for _, l := range strings.Split(wrapped, "\n") {
		lines = append(lines, styleText.Render(l))
	}
	lines = append(lines, "")
	proj := t.Project
	if proj == "" {
		proj = "— (wird beim Starten abgefragt)"
	}
	lines = append(lines,
		styleDim.Render("Projekt: ")+styleText.Render(proj),
		styleDim.Render("Notiert: "+formatAgeWord(t.CreatedAt)),
		"",
		styleDim.Render("⏎  Session daraus starten (Text landet im Eingabefeld)"),
		styleDim.Render("e  bearbeiten"),
		styleDim.Render("x  löschen"),
	)
	return lines
}

func (m model) todoLine(idx, w int) string {
	if idx >= len(m.state.Todos) {
		return ""
	}
	t := m.state.Todos[idx]
	tag := ""
	if t.Project != "" {
		tag = styleDim.Render(" [" + t.Project + "]")
	}
	tagW := lipgloss.Width(tag)
	return " • " + styleText.Render(trunc(t.Text, w-4-tagW)) + tag
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
	lines, _ := m.detailContent(w, h)
	return strings.Join(lines, "\n")
}

func (m model) detailContent(w, h int) ([]string, int) {
	if m.hoverTodo >= 0 && m.hoverTodo < len(m.state.Todos) {
		return m.todoDetail(m.hoverTodo, w), -1
	}
	if r := m.selectedRow(); r != nil && r.kind == rowTodo && r.todoIdx < len(m.state.Todos) {
		return m.todoDetail(r.todoIdx, w), -1
	}
	a := m.selectedAgent()
	proj := m.contextProject()
	var lines []string
	previewStart := -1
	add := func(s string) { lines = append(lines, trunc(s, w)) }

	if a == nil && proj == nil {
		return []string{
			styleDim.Render("Nichts ausgewählt."),
			"",
			styleDim.Render("p  Projekt hinzufügen"),
			styleDim.Render("n  neue Claude-Session"),
			styleDim.Render("t  Todo notieren"),
		}, -1
	}

	if a != nil {
		projName := a.Project
		if projName == "" {
			projName = "—"
		}
		add(styleTitle.Render(a.Name) + styleDim.Render(" · "+projName))
		st := m.poll.statuses[a.Name]
		wtNote := ""
		if a.Worktree {
			wtNote = styleDim.Render(" · ⑂ worktree")
		}
		active := ""
		if act, ok := m.poll.activity[a.Name]; ok {
			active = " · aktiv " + formatAgeWord(act)
		}
		add(statusStyle(st).Render(st.Icon()+" "+st.Label()) + styleDim.Render(" · seit "+formatAge(a.CreatedAt)+active) + wtNote)
		if d := m.poll.details[a.Name]; d != "" {
			add(styleAgents.Render("◍ " + d))
		}
		add(styleDim.Render(shortPath(a.Dir)))
		add("")
		m.addAgentGit(a, w, add)
	} else {
		add(styleTitle.Render(proj.Name))
		add(styleDim.Render(shortPath(proj.Path)))
		add("")
		m.addRepoGit(proj.Path, add)
	}
	add("")

	if a != nil && m.poll.preview != "" {
		remaining := h - len(lines) - 1
		if remaining > 3 {
			previewStart = len(lines)
			label := "Terminal · klick zum Öffnen "
			add(styleSection.Render("Terminal") + styleDim.Render(" · klick zum Öffnen "+strings.Repeat("─", max(0, w-len([]rune(label))-9))))
			pv := strings.Split(strings.TrimRight(m.poll.preview, "\n"), "\n")
			if len(pv) > remaining {
				pv = pv[len(pv)-remaining:]
			}
			for _, l := range pv {
				add(styleDim.Render(strings.ReplaceAll(l, "\t", "  ")))
			}
		}
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines, previewStart
}

func (m model) addAgentGit(a *Agent, w int, add func(string)) {
	git, ok := m.poll.git[a.Dir]
	if !ok || !git.IsRepo {
		add(styleSection.Render("Git"))
		add(" " + styleDim.Render("kein Git-Repo"))
		return
	}
	sc := m.poll.session[a.Name]
	label := "Git · diese Session"
	if !sc.Known {
		label = "Git · gesamt (Session-Start unbekannt)"
	}
	add(styleSection.Render(label))
	ab := ""
	if git.Ahead > 0 {
		ab += fmt.Sprintf(" ↑%d", git.Ahead)
	}
	if git.Behind > 0 {
		ab += fmt.Sprintf(" ↓%d", git.Behind)
	}
	add(" " + styleText.Render(git.Branch) + styleWarn.Render(ab))
	if len(sc.Files) == 0 && sc.Commits == 0 {
		add(" " + styleOK.Render("✓ nichts geändert"))
		return
	}
	summary := []string{}
	if sc.Commits > 0 {
		word := "Commits"
		if sc.Commits == 1 {
			word = "Commit"
		}
		summary = append(summary, fmt.Sprintf("%d %s", sc.Commits, word))
	}
	if len(sc.Files) > 0 {
		word := "Dateien"
		if len(sc.Files) == 1 {
			word = "Datei"
		}
		summary = append(summary, fmt.Sprintf("%d %s geändert", len(sc.Files), word))
	}
	add(" " + styleWarn.Render("± "+strings.Join(summary, " · ")))
	maxFiles := 6
	for i, f := range sc.Files {
		if i == maxFiles {
			add("   " + styleDim.Render(fmt.Sprintf("… +%d weitere", len(sc.Files)-maxFiles)))
			break
		}
		add("   " + styleDim.Render(trunc(f, w-4)))
	}
}

func (m model) addRepoGit(dir string, add func(string)) {
	add(styleSection.Render("Git"))
	git, ok := m.poll.git[dir]
	if !ok || !git.IsRepo {
		add(" " + styleDim.Render("kein Git-Repo"))
		return
	}
	ab := ""
	if git.Ahead > 0 {
		ab += fmt.Sprintf(" ↑%d", git.Ahead)
	}
	if git.Behind > 0 {
		ab += fmt.Sprintf(" ↓%d", git.Behind)
	}
	add(" " + styleText.Render(git.Branch) + styleWarn.Render(ab))
	if git.Clean() {
		add(" " + styleOK.Render("✓ sauber"))
	} else {
		parts := []string{}
		if git.Staged > 0 {
			parts = append(parts, fmt.Sprintf("%d staged", git.Staged))
		}
		if git.Modified > 0 {
			parts = append(parts, fmt.Sprintf("%d geändert", git.Modified))
		}
		if git.Untracked > 0 {
			parts = append(parts, fmt.Sprintf("%d neu", git.Untracked))
		}
		add(" " + styleWarn.Render("± "+strings.Join(parts, " · ")))
	}
	if git.LastMsg != "" {
		add(" " + styleDim.Render("⌥ "+git.LastMsg))
	}
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
	if r := m.selectedRow(); r != nil && r.kind == rowTodo {
		keys := []string{"⏎/klick session daraus starten", "e bearbeiten", "x löschen", "t neues todo", "q ende"}
		return " " + styleDim.Render(strings.Join(keys, " · "))
	}
	keys := []string{"n neu", "w worktree", "⏎ attach", "t todo", "d done", "D deploy", "r name", "x kill", "p projekt", "q ende"}
	return " " + styleDim.Render(strings.Join(keys, " · "))
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
