package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"magentic/core"
)

type rowKind int

const (
	rowProject rowKind = iota
	rowAgent
	rowSep
	rowHint
)

func selectableRow(k rowKind) bool {
	return k != rowSep && k != rowHint
}

const orphanKey = "\x00orphans"

type treeRow struct {
	kind    rowKind
	project *Project
	agent   Agent
	label   string
}

type inputKind int

const (
	inputNone inputKind = iota
	inputNewSession
	inputNewWorktree
	inputNewTerm
	inputAddProject
	inputRename
	inputZgStart
	inputZgStop
)

type pollResult struct {
	observation       core.ObservationSnapshot
	observed          map[tuiSessionKey]core.SessionObservation
	repositories      core.RepositoriesSurvey
	repositoryProblem string
	inspections       map[tuiSessionKey]core.RepositoryInspection
	inspectionProblem map[tuiSessionKey]string
	discovered        []Agent
	diskState         *State
	zeitgeist         ZgInfo
}

// tuiSessionKey keeps durable Session identity through display-name changes.
// The name fallback is confined to legacy in-process fixtures without IDs.
type tuiSessionKey string

func sessionKey(a Agent) tuiSessionKey {
	if a.ID != "" {
		return tuiSessionKey("id\x00" + string(a.ID))
	}
	return tuiSessionKey("name\x00" + a.Name)
}

func sameSession(a, b Agent) bool {
	if a.ID != "" && b.ID != "" {
		return a.ID == b.ID
	}
	return a.Name == b.Name
}

type tuiObservationReader func(context.Context, []core.Session) core.ObservationSnapshot

type tuiRepositoryReader interface {
	Survey(context.Context, []core.Project) (core.RepositoriesSurvey, error)
	Inspect(context.Context, core.RepositoryInspectRequest) (core.RepositoryInspection, error)
}

type tickMsg time.Time
type pollMsg pollResult
type previewMsg struct {
	key          tuiSessionKey
	observation  core.SessionObservation
	availability core.ObservationAvailability
	problems     []core.ObservationProblem
}
type attachDoneMsg struct{ err error }
type usageTickMsg time.Time
type usageMsg UsageInfo

func usageTick() tea.Cmd {
	return tea.Tick(5*time.Minute, func(t time.Time) tea.Msg { return usageTickMsg(t) })
}

func fetchUsageCmd() tea.Cmd {
	return func() tea.Msg { return usageMsg(CachedUsage()) }
}

type model struct {
	state          *State
	cursor         int
	collapsed      map[string]bool
	input          textinput.Model
	inputKind      inputKind
	pendingProject string
	renameFrom     string
	confirmKill    bool
	confirmRmProj  bool
	notifyPending  map[string]AgentStatus
	poll           pollResult
	flash          string
	flashIsErr     bool
	flashTime      time.Time
	width          int
	height         int
	pollBusy       bool
	previewPending bool
	usage          UsageInfo
}

func newModel(s *State) model {
	reconcile(s)
	return model{state: s, collapsed: map[string]bool{}, notifyPending: map[string]AgentStatus{}}
}

func reconcile(s *State) {
	if agents := discoverNew(s); len(agents) > 0 {
		if changed, err := OpenRegistry(StatePath()).Change(context.Background(), AddDiscoveredSessions(agents)); err == nil {
			*s = *changed.Snapshot.MutableState()
		}
	}
}

func (m *model) handleStatusChanges(old map[tuiSessionKey]core.SessionObservation) {
	if old == nil {
		return
	}
	for _, session := range m.state.Agents {
		key := sessionKey(session)
		observed, found := m.poll.observed[key]
		st := StatusUnknown
		if found {
			st = observed.Status
		}
		pendingKey := string(key)
		if pending, ok := m.notifyPending[pendingKey]; ok && st != StatusUnknown {
			delete(m.notifyPending, pendingKey)
			if st == pending {
				notifyDesktop("magentic · "+session.Name, "Agent ist fertig — bereit für den nächsten Prompt", "Ping")
			}
		}
		previous, seen := old[key]
		if !seen || previous.Status == st {
			continue
		}
		prev := previous.Status
		if st == StatusBlocked && (prev == StatusRunning || prev == StatusAgents || prev == StatusShell || prev == StatusIdle) {
			notifyDesktop("magentic · "+session.Name, "Agent wartet auf deine Eingabe", "Glass")
		} else if (prev == StatusRunning || prev == StatusAgents || prev == StatusShell) && st == StatusIdle {
			m.notifyPending[pendingKey] = StatusIdle
		}
	}
}

func (m model) orphanAgents() []Agent {
	var out []Agent
	for _, a := range m.state.Agents {
		if a.Project == "" || m.state.ProjectByName(a.Project) == nil {
			out = append(out, a)
		}
	}
	return out
}

func (m model) sortAgents(agents []Agent) []Agent {
	sort.SliceStable(agents, func(i, j int) bool {
		return statusRank(m.statusFor(agents[i])) < statusRank(m.statusFor(agents[j]))
	})
	return agents
}

func (m model) rows() []treeRow {
	var rows []treeRow
	for i := range m.state.Projects {
		p := &m.state.Projects[i]
		rows = append(rows, treeRow{kind: rowProject, project: p})
		if m.collapsed[p.Name] {
			continue
		}
		for _, a := range m.sortAgents(m.state.AgentsFor(p.Name)) {
			rows = append(rows, treeRow{kind: rowAgent, agent: a, project: p})
		}
	}
	if orphans := m.orphanAgents(); len(orphans) > 0 {
		rows = append(rows, treeRow{kind: rowProject, project: nil})
		if !m.collapsed[orphanKey] {
			for _, a := range m.sortAgents(orphans) {
				rows = append(rows, treeRow{kind: rowAgent, agent: a})
			}
		}
	}
	return rows
}

func (m *model) moveCursor(delta int) {
	rows := m.rows()
	i := m.cursor
	for {
		i += delta
		if i < 0 || i >= len(rows) {
			return
		}
		if selectableRow(rows[i].kind) {
			m.cursor = i
			return
		}
	}
}

func (m *model) ensureSelectable() {
	rows := m.rows()
	if len(rows) == 0 {
		m.cursor = 0
		return
	}
	if m.cursor >= len(rows) {
		m.cursor = len(rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	if !selectableRow(rows[m.cursor].kind) {
		m.moveCursor(-1)
		if !selectableRow(rows[m.cursor].kind) {
			m.moveCursor(1)
		}
	}
}

func (m model) selectedRow() *treeRow {
	rows := m.rows()
	if len(rows) == 0 || m.cursor >= len(rows) {
		return nil
	}
	r := rows[m.cursor]
	return &r
}

func (m model) selectedAgent() *Agent {
	if r := m.selectedRow(); r != nil && r.kind == rowAgent {
		return &r.agent
	}
	return nil
}

func (m model) contextProject() *Project {
	r := m.selectedRow()
	if r == nil {
		return nil
	}
	if r.project != nil {
		return r.project
	}
	if r.kind == rowAgent && r.agent.Project != "" {
		return m.state.ProjectByName(r.agent.Project)
	}
	return nil
}

func (m *model) selectAgent(name string) {
	for i, r := range m.rows() {
		if r.kind == rowAgent && r.agent.Name == name {
			m.cursor = i
			return
		}
	}
}

func (m *model) setFlash(msg string, isErr bool) {
	m.flash = msg
	m.flashIsErr = isErr
	m.flashTime = time.Now()
}

func tick() tea.Cmd {
	return tea.Tick(2*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

const tuiPollTimeout = 5 * time.Second

func pollCmd(state State, selected *Agent) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tuiPollTimeout)
		defer cancel()
		res := collectPollModuleFacts(ctx, state, selected, core.Observe, core.NewRepositories())
		res.discovered = discoverNew(&state)
		res.zeitgeist = zeitgeistInfo()
		if disk, err := LoadState(); err == nil {
			res.diskState = disk
		}
		return pollMsg(res)
	}
}

// collectPollModuleFacts is the TUI's private Adapter over the Observation and
// Repositories Modules. Tests replace the two local-substitutable dependencies;
// production callers receive one coherent Observation and one repository pass.
func collectPollModuleFacts(
	ctx context.Context,
	state State,
	selected *Agent,
	observe tuiObservationReader,
	repositories tuiRepositoryReader,
) pollResult {
	result := pollResult{
		observed:          make(map[tuiSessionKey]core.SessionObservation, len(state.Agents)),
		inspections:       make(map[tuiSessionKey]core.RepositoryInspection),
		inspectionProblem: make(map[tuiSessionKey]string),
	}

	prepared := prepareObservationSessions(state.Agents)
	result.observation = observe(ctx, prepared)
	observedByID := make(map[core.SessionID]core.SessionObservation, len(result.observation.Sessions))
	for _, observation := range result.observation.Sessions {
		observedByID[observation.SessionID] = observation
	}
	for i, session := range state.Agents {
		if observation, ok := observedByID[prepared[i].ID]; ok {
			result.observed[sessionKey(session)] = observation
		}
	}

	survey, err := repositories.Survey(ctx, state.Projects)
	if err != nil {
		result.repositoryProblem = err.Error()
	} else {
		result.repositories = survey
	}

	for _, session := range state.Agents {
		_, representedBySurvey := surveyedWorktree(state, result.repositories, session)
		needsInspection := session.BaseCommit != "" || !representedBySurvey ||
			(selected != nil && sameSession(session, *selected))
		if !needsInspection {
			continue
		}
		request := core.RepositoryInspectRequest{
			Directory:  session.Dir,
			MainBranch: surveyedMainBranch(state, result.repositories, session),
		}
		if session.BaseCommit != "" {
			request.Against = &core.RepositoryBaseline{
				Directory:  session.Dir,
				Head:       session.BaseCommit,
				DirtyPaths: append([]string(nil), session.BaseDirty...),
			}
		}
		inspection, inspectErr := repositories.Inspect(ctx, request)
		key := sessionKey(session)
		if inspectErr != nil {
			result.inspectionProblem[key] = inspectErr.Error()
			continue
		}
		result.inspections[key] = inspection
	}
	return result
}

func prepareObservationSessions(sessions []Agent) []core.Session {
	prepared := append([]core.Session(nil), sessions...)
	used := make(map[core.SessionID]bool, len(prepared))
	for _, session := range prepared {
		if session.ID != "" {
			used[session.ID] = true
		}
	}
	for i := range prepared {
		if prepared[i].ID != "" {
			continue
		}
		for suffix := 0; ; suffix++ {
			candidate := core.SessionID(fmt.Sprintf("__tui_fixture_%d_%d", i, suffix))
			if !used[candidate] {
				prepared[i].ID = candidate
				used[candidate] = true
				break
			}
		}
	}
	return prepared
}

func surveyProject(state State, survey core.RepositoriesSurvey, session Agent) (core.RepositoryProjectSurvey, bool) {
	var project *Project
	if session.ProjectID != "" {
		project = state.ProjectByID(session.ProjectID)
	}
	if project == nil && session.Project != "" {
		project = state.ProjectByName(session.Project)
	}
	if project == nil {
		return core.RepositoryProjectSurvey{}, false
	}
	return surveyedProject(survey, *project)
}

func surveyedProject(survey core.RepositoriesSurvey, project Project) (core.RepositoryProjectSurvey, bool) {
	for _, repository := range survey.Projects {
		if project.ID != "" && repository.ID != "" && repository.ID == project.ID {
			return repository, true
		}
	}
	for _, repository := range survey.Projects {
		if repository.Name == project.Name || samePath(repository.Path, project.Path) {
			return repository, true
		}
	}
	return core.RepositoryProjectSurvey{}, false
}

func surveyedWorktree(state State, survey core.RepositoriesSurvey, session Agent) (core.RepositoryWorktree, bool) {
	repository, ok := surveyProject(state, survey, session)
	if !ok || repository.Presence != core.RepositoryKnown || !repository.Worktrees.Known() {
		return core.RepositoryWorktree{}, false
	}
	for _, worktree := range repository.Worktrees.Value {
		if samePath(worktree.Path, session.Dir) {
			return worktree, true
		}
	}
	return core.RepositoryWorktree{}, false
}

func surveyedMainBranch(state State, survey core.RepositoriesSurvey, session Agent) string {
	if repository, ok := surveyProject(state, survey, session); ok && repository.MainBranch.Known() {
		return repository.MainBranch.Value
	}
	if session.ProjectID != "" {
		if project := state.ProjectByID(session.ProjectID); project != nil {
			return project.MainBranch
		}
	}
	if project := state.ProjectByName(session.Project); project != nil {
		return project.MainBranch
	}
	return ""
}

func samePath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func (m model) observationFor(session Agent) (core.SessionObservation, bool) {
	observation, ok := m.poll.observed[sessionKey(session)]
	return observation, ok
}

func (m model) statusFor(session Agent) AgentStatus {
	if observation, ok := m.observationFor(session); ok {
		return observation.Status
	}
	return StatusUnknown
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.pollNow(), tick(), fetchUsageCmd(), usageTick())
}

func (m model) pollNow() tea.Cmd {
	return pollCmd(*m.state, m.selectedAgent())
}

func (m *model) previewNow() tea.Cmd {
	a := m.selectedAgent()
	if a == nil || m.previewPending {
		return nil
	}
	m.previewPending = true
	return previewObservationCmd(*a, core.Observe)
}

func previewObservationCmd(session Agent, observe tuiObservationReader) tea.Cmd {
	return func() tea.Msg {
		prepared := prepareObservationSessions([]Agent{session})
		snapshot := observe(context.Background(), prepared)
		message := previewMsg{
			key:          sessionKey(session),
			availability: snapshot.Availability,
			problems:     append([]core.ObservationProblem(nil), snapshot.Problems...),
		}
		for _, observation := range snapshot.Sessions {
			if observation.SessionID == prepared[0].ID {
				message.observation = observation
				break
			}
		}
		return message
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tickMsg:
		if m.pollBusy {
			return m, tick()
		}
		m.pollBusy = true
		return m, tea.Batch(m.pollNow(), tick())
	case previewMsg:
		m.previewPending = false
		if a := m.selectedAgent(); a != nil && sessionKey(*a) == msg.key {
			if m.poll.observed == nil {
				m.poll.observed = make(map[tuiSessionKey]core.SessionObservation)
			}
			m.poll.observed[msg.key] = msg.observation
			return m, nil
		}
		return m, m.previewNow()
	case usageTickMsg:
		return m, tea.Batch(fetchUsageCmd(), usageTick())
	case usageMsg:
		m.usage = UsageInfo(msg)
		return m, nil
	case pollMsg:
		m.pollBusy = false
		oldObservations := m.poll.observed
		var selName string
		if a := m.selectedAgent(); a != nil {
			selName = a.Name
		}
		m.poll = pollResult(msg)
		m.handleStatusChanges(oldObservations)
		if m.poll.diskState != nil {
			m.state = m.poll.diskState
		}
		if len(m.poll.discovered) > 0 {
			if changed, err := OpenRegistry(StatePath()).Change(context.Background(), AddDiscoveredSessions(m.poll.discovered)); err == nil {
				m.state = changed.Snapshot.MutableState()
			}
		}
		if selName != "" {
			m.selectAgent(selName)
		}
		return m, nil
	case attachDoneMsg:
		return m, m.pollNow()
	case tea.MouseMsg:
		if m.inputKind != inputNone || m.confirmKill || m.confirmRmProj {
			return m, nil
		}
		return m.updateMouse(msg)
	case tea.KeyMsg:
		if m.inputKind != inputNone {
			return m.updateInput(msg)
		}
		if m.confirmKill || m.confirmRmProj {
			return m.updateConfirm(msg)
		}
		return m.updateNormal(msg)
	}
	return m, nil
}

func (m model) updateMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Button {
	case tea.MouseButtonWheelUp:
		m.moveCursor(-1)
		return m, m.previewNow()
	case tea.MouseButtonWheelDown:
		m.moveCursor(1)
		return m, m.previewNow()
	case tea.MouseButtonLeft:
		if msg.Action != tea.MouseActionPress {
			return m, nil
		}
		return m.handleClick(msg.X, msg.Y)
	case tea.MouseButtonNone:
		return m, nil
	}
	return m, nil
}

func (m model) handleClick(x, y int) (tea.Model, tea.Cmd) {
	treeW := m.treeWidth()
	rows := m.rows()
	idx := y - 2
	if x < treeW {
		if idx < 0 || idx >= len(rows) {
			return m, nil
		}
		r := rows[idx]
		if !selectableRow(r.kind) {
			return m, nil
		}
		if r.kind == rowProject {
			m.cursor = idx
			key := orphanKey
			if r.project != nil {
				key = r.project.Name
			}
			m.collapsed[key] = !m.collapsed[key]
			m.ensureSelectable()
			return m, m.previewNow()
		}
		if m.cursor == idx {
			return m.attach()
		}
		m.cursor = idx
		return m, m.previewNow()
	}
	if m.selectedAgent() != nil {
		_, detailW, innerH := m.layout()
		_, previewStart := m.detailContent(detailW-4, innerH)
		if previewStart >= 0 && idx >= previewStart && idx < innerH {
			return m.attach()
		}
	}
	return m, nil
}

func (m model) maxAgentNameLen() int {
	n := 8
	for _, a := range m.state.Agents {
		if l := len([]rune(a.Name)); l > n {
			n = l
		}
	}
	if n > 18 {
		n = 18
	}
	return n
}

func (m model) treeWidth() int {
	w := m.maxAgentNameLen() + 27
	for _, p := range m.state.Projects {
		if l := len([]rune(p.Name)) + 14; l > w {
			w = l
		}
	}
	if w < 32 {
		w = 32
	}
	if cap := m.width * 55 / 100; m.width > 0 && w > cap {
		w = cap
	}
	return w
}

func (m model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "up", "k":
		m.moveCursor(-1)
		return m, m.previewNow()
	case "down", "j":
		m.moveCursor(1)
		return m, m.previewNow()
	case "enter", " ", "a":
		r := m.selectedRow()
		if r == nil {
			return m, nil
		}
		if r.kind == rowProject {
			key := orphanKey
			if r.project != nil {
				key = r.project.Name
			}
			m.collapsed[key] = !m.collapsed[key]
			m.ensureSelectable()
			return m, nil
		}
		return m.attach()
	case "n":
		return m.startInput(inputNewSession)
	case "w":
		return m.startInput(inputNewWorktree)
	case "T":
		return m.startInput(inputNewTerm)
	case "r":
		if a := m.selectedAgent(); a != nil {
			m.renameFrom = a.Name
			return m.startInput(inputRename)
		}
		return m, nil
	case "x":
		r := m.selectedRow()
		if r == nil {
			return m, nil
		}
		if r.kind == rowAgent {
			m.confirmKill = true
		} else if r.kind == rowProject && r.project != nil {
			m.confirmRmProj = true
		}
		return m, nil
	case "p":
		return m.startInput(inputAddProject)
	case "d":
		return m.sendSkillToSelected("/done ")
	case "D":
		return m.sendSkillToSelected("/deploy ")
	case "z":
		if m.poll.zeitgeist.Active {
			return m.startInput(inputZgStop)
		}
		if !m.poll.zeitgeist.Exists {
			m.setFlash("Zeitgeist-Daten nicht gefunden (~/.zeitgeist/data.json)", true)
			return m, nil
		}
		return m.startInput(inputZgStart)
	case "Z":
		return m.zgTogglePause()
	case "g":
		return m, m.pollNow()
	}
	return m, nil
}

func (m model) zgTogglePause() (tea.Model, tea.Cmd) {
	zg := m.poll.zeitgeist
	if !zg.Active {
		m.setFlash("Kein Zeitgeist-Timer aktiv — z startet einen", true)
		return m, nil
	}
	if zg.State == "running" {
		if err := zeitgeistPause(); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		m.setFlash("⏸ Zeitgeist-Timer pausiert", false)
	} else {
		if err := zeitgeistResume(); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		m.setFlash("▶ Zeitgeist-Timer läuft weiter", false)
	}
	return m, m.pollNow()
}

func (m model) zgStart(ref string) (tea.Model, tea.Cmd) {
	if ref == "" {
		m.setFlash("Kein Projekt angegeben", true)
		return m, nil
	}
	p, err := zeitgeistStart(ref)
	if err != nil {
		msg := err.Error()
		var names []string
		for _, pr := range m.poll.zeitgeist.Projects {
			names = append(names, pr.Name)
		}
		if len(names) > 0 {
			msg += " — Projekte: " + strings.Join(names, ", ")
		}
		m.setFlash(msg, true)
		return m, nil
	}
	m.setFlash(fmt.Sprintf("▶ Zeitgeist-Timer läuft: %s (%s/h)", p.Name, formatEuro(p.Rate)), false)
	return m, m.pollNow()
}

func (m model) zgStop(note string) (tea.Model, tea.Cmd) {
	s, err := zeitgeistStop(note)
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	m.setFlash(fmt.Sprintf("■ %s abgeschlossen: %s — %s", s.Project, formatDurShort(s.DurationSec), formatEuro(s.Earnings)), false)
	return m, m.pollNow()
}

func (m model) sendSkillToSelected(cmd string) (tea.Model, tea.Cmd) {
	label := strings.TrimSpace(cmd)
	a := m.selectedAgent()
	if a == nil {
		if label == "/deploy" {
			if p := m.contextProject(); p != nil {
				return m.startSkillSession(p, cmd)
			}
		}
		m.setFlash("Erst einen Agent auswählen ("+label+" läuft in dessen Session)", true)
		return m, nil
	}
	if a.IsTerm() {
		m.setFlash(a.Name+" ist eine Terminal-Session — dort läuft kein Claude", true)
		return m, nil
	}
	sn := tmuxSessionName(a.Name)
	st := m.statusFor(*a)
	if !TmuxHasSession(sn) || st == StatusExited || st == StatusDead {
		m.setFlash("Claude läuft in dieser Session nicht mehr", true)
		return m, nil
	}
	if st == StatusBlocked {
		m.setFlash(a.Name+" wartet auf eine Antwort — erst den Dialog beantworten (⏎)", true)
		return m, nil
	}
	sendSlashCommand(sn, cmd)
	m.setFlash(label+" an "+a.Name+" gesendet", false)
	return m, m.pollNow()
}

func (m model) startSkillSession(p *Project, cmd string) (tea.Model, tea.Cmd) {
	kind := ""
	if strings.TrimSpace(cmd) == "/deploy" {
		kind = "deploy"
	}
	name, err := startSkillAgent(m.state, p.Path, cmd, kind, cmd+" "+p.Name)
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	m.collapsed[p.Name] = false
	m.selectAgent(name)
	m.setFlash(fmt.Sprintf("Session %q gestartet — %s wird getippt", name, strings.TrimSpace(cmd)), false)
	return m, m.pollNow()
}

func (m model) startInput(kind inputKind) (tea.Model, tea.Cmd) {
	needProject := kind == inputNewSession || kind == inputNewWorktree ||
		(kind == inputNewTerm && m.selectedAgent() == nil)
	if needProject {
		p := m.contextProject()
		if p == nil {
			m.setFlash("Kein Projekt gewählt — erst mit p ein Projekt anlegen bzw. eins auswählen", true)
			return m, nil
		}
		m.pendingProject = p.Name
	}
	ti := textinput.New()
	ti.CharLimit = 500
	switch kind {
	case inputNewSession:
		ti.Prompt = fmt.Sprintf("Neuer Agent in %s (leer = auto): ", m.pendingProject)
	case inputNewWorktree:
		ti.Prompt = fmt.Sprintf("Neuer Agent im Worktree von %s (leer = auto): ", m.pendingProject)
	case inputNewTerm:
		where := m.pendingProject
		if a := m.selectedAgent(); a != nil {
			where = shortPath(a.Dir)
		}
		ti.Prompt = fmt.Sprintf("Neues Terminal in %s (leer = auto): ", where)
	case inputAddProject:
		ti.Prompt = "Projektpfad: "
		ti.SetValue("~/Projects/")
	case inputRename:
		ti.Prompt = "Neuer Name: "
		ti.SetValue(m.renameFrom)
	case inputZgStart:
		ti.Prompt = "▶ Zeitgeist-Timer starten — Projekt: "
		ti.SetValue(m.poll.zeitgeist.LastProject)
	case inputZgStop:
		ti.Prompt = "■ Zeitgeist-Timer stoppen — Notiz (leer = ohne): "
	}
	ti.Focus()
	m.input = ti
	m.inputKind = kind
	return m, textinput.Blink
}

func (m model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.inputKind = inputNone
		return m, nil
	case "enter":
		kind := m.inputKind
		value := strings.TrimSpace(m.input.Value())
		m.inputKind = inputNone
		return m.commitInput(kind, value)
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m model) commitInput(kind inputKind, value string) (tea.Model, tea.Cmd) {
	switch kind {
	case inputNewSession, inputNewWorktree:
		return m.createAgent(kind == inputNewWorktree, value, "")
	case inputNewTerm:
		return m.createTermAgent(value)
	case inputAddProject:
		return m.addProject(value)
	case inputRename:
		return m.renameAgent(value)
	case inputZgStart:
		return m.zgStart(value)
	case inputZgStop:
		return m.zgStop(value)
	}
	return m, nil
}

func (m model) createTermAgent(name string) (tea.Model, tea.Cmd) {
	a := m.selectedAgent()
	if a == nil {
		return m.createAgent(false, name, KindTerm)
	}
	n, err := createTermSessionFor(m.state, a.Name, name)
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	m.selectAgent(n)
	m.setFlash(fmt.Sprintf("Terminal %q geöffnet (%s)", n, shortPath(a.Dir)), false)
	return m, m.pollNow()
}

func (m model) createAgent(worktree bool, name, kind string) (tea.Model, tea.Cmd) {
	proj := m.state.ProjectByName(m.pendingProject)
	if proj == nil {
		m.setFlash("Projekt nicht gefunden", true)
		return m, nil
	}
	if name == "" {
		hint := proj.Name
		if kind == KindTerm {
			hint = "term " + proj.Name
		}
		name = PickAgentName(m.state, hint)
	} else {
		name = sanitizeName(name)
	}
	if name == "" || m.state.HasAgent(name) || TmuxHasSession(tmuxSessionName(name)) {
		m.setFlash(fmt.Sprintf("Name %q ist ungültig oder schon vergeben", name), true)
		return m, nil
	}
	var (
		created string
		err     error
	)
	if kind == KindTerm {
		created, err = createTermSession(m.state, proj.Name, worktree, name)
	} else {
		created, err = createAgentSession(m.state, proj.Name, worktree, name)
	}
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	name = created
	m.collapsed[proj.Name] = false
	m.selectAgent(name)
	label := "Session"
	switch {
	case kind == KindTerm:
		label = "Terminal"
	case worktree:
		label = "Worktree-Session"
	}
	m.setFlash(fmt.Sprintf("Agent %q gestartet (%s in %s)", name, label, proj.Name), false)
	return m, m.pollNow()
}

func (m model) addProject(path string) (tea.Model, tea.Cmd) {
	if path == "" {
		return m, nil
	}
	if strings.HasPrefix(path, "~") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		m.setFlash("Verzeichnis nicht gefunden: "+path, true)
		return m, nil
	}
	name := filepath.Base(path)
	if m.state.ProjectByName(name) != nil {
		m.setFlash(fmt.Sprintf("Projekt %q existiert schon", name), true)
		return m, nil
	}
	changed, err := OpenRegistry(StatePath()).Change(context.Background(), RegisterProject(Project{Name: name, Path: path}))
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	m.state = changed.Snapshot.MutableState()
	for i, r := range m.rows() {
		if r.kind == rowProject && r.project != nil && r.project.Name == name {
			m.cursor = i
		}
	}
	m.setFlash(fmt.Sprintf("Projekt %q hinzugefügt", name), false)
	return m, m.pollNow()
}

func (m model) renameAgent(newName string) (tea.Model, tea.Cmd) {
	newName = sanitizeName(newName)
	if newName == "" || newName == m.renameFrom {
		return m, nil
	}
	if m.state.HasAgent(newName) || TmuxHasSession(tmuxSessionName(newName)) {
		m.setFlash(fmt.Sprintf("Name %q ist schon vergeben", newName), true)
		return m, nil
	}
	old := tmuxSessionName(m.renameFrom)
	if TmuxHasSession(old) {
		if _, err := tmux("rename-session", "-t", targetSession(old), tmuxSessionName(newName)); err != nil {
			m.setFlash("tmux rename: "+err.Error(), true)
			return m, nil
		}
	}
	session := m.state.AgentByName(m.renameFrom)
	if session == nil {
		m.setFlash("Session nicht gefunden", true)
		return m, nil
	}
	changed, err := OpenRegistry(StatePath()).Change(context.Background(), RenameRegisteredSession(session.ID, session.Name, newName))
	if err != nil {
		// The external rename may already have applied. Keep that explicit to
		// the user; a later Registry retry is safer than silently reverting it.
		m.setFlash("Registry nach tmux rename: "+err.Error(), true)
		return m, nil
	}
	m.state = changed.Snapshot.MutableState()
	m.setFlash(fmt.Sprintf("%s → %s", m.renameFrom, newName), false)
	return m, m.pollNow()
}

func (m model) updateConfirm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	yes := msg.String() == "y" || msg.String() == "enter"
	if m.confirmKill {
		m.confirmKill = false
		if !yes {
			return m, nil
		}
		a := m.selectedAgent()
		if a == nil {
			return m, nil
		}
		note := ""
		if a.Worktree {
			note = " — Worktree bleibt unter " + shortPath(a.Dir)
		}
		if err := removeSession(m.state, a.Name); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		m.ensureSelectable()
		m.setFlash(fmt.Sprintf("Agent %q beendet%s", a.Name, note), false)
		return m, m.pollNow()
	}
	if m.confirmRmProj {
		m.confirmRmProj = false
		if !yes {
			return m, nil
		}
		r := m.selectedRow()
		if r == nil || r.project == nil {
			return m, nil
		}
		p := r.project
		if len(m.state.AgentsFor(p.Name)) > 0 {
			m.setFlash("Projekt hat noch Agents — erst alle beenden (x)", true)
			return m, nil
		}
		changed, err := OpenRegistry(StatePath()).Change(context.Background(), RemoveProject(p.ID, p.Name))
		if err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		m.state = changed.Snapshot.MutableState()
		m.ensureSelectable()
		m.setFlash(fmt.Sprintf("Projekt %q entfernt (Dateien bleiben unberührt)", p.Name), false)
		return m, m.pollNow()
	}
	return m, nil
}

func (m model) attach() (tea.Model, tea.Cmd) {
	a := m.selectedAgent()
	if a == nil {
		return m, nil
	}
	sn := tmuxSessionName(a.Name)
	if !TmuxHasSession(sn) {
		m.setFlash("Session existiert nicht mehr — mit x entfernen oder n neu starten", true)
		return m, nil
	}
	tmux("set-option", "-w", "-t", sn+":", "window-size", "latest")
	if os.Getenv("TMUX") != "" {
		if err := exec.Command("tmux", "switch-client", "-t", targetSession(sn)).Run(); err != nil {
			m.setFlash("tmux switch-client: "+err.Error(), true)
		}
		return m, nil
	}
	cmd := exec.Command("tmux", "attach-session", "-t", targetSession(sn))
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return attachDoneMsg{err} })
}
