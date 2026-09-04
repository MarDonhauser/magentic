package main

import (
	"context"
	"fmt"
	"os"
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
	observation        core.ObservationSnapshot
	observed           map[tuiSessionKey]core.SessionObservation
	resumable          map[tuiSessionKey]core.SessionResumability
	repositoryProblem  string
	projectInspections map[string]core.RepositoryInspection
	inspections        map[tuiSessionKey]core.RepositoryInspection
	inspectionProblem  map[tuiSessionKey]string
	discovery          core.RegistryDiscovery
	diskState          *State
	zeitgeist          ZgInfo
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
	SurveyTopology(context.Context, []core.Project) (core.RepositoriesSurvey, error)
	InspectAll(context.Context, []core.RepositoryInspectRequest) ([]core.RepositoryInspection, error)
}

type tickMsg time.Time
type repoTickMsg time.Time
type diskTickMsg time.Time

// observationMsg carries the cheap tmux pass, repositoryMsg the expensive git
// pass. Both are partially filled pollResult values that Update merges into the
// model's one coherent pollResult.
type observationMsg pollResult
type repositoryMsg pollResult

// diskMsg carries the slow file-backed facts: the Registry file on disk, the
// Zeitgeist file, and externally created tmux runtimes. They change rarely (a
// registry write, a timer action, a hand-made tmux session), so they ride
// along with the slow cadence instead of every 2s observation.
type diskMsg pollResult
type previewMsg struct {
	key          tuiSessionKey
	observation  core.SessionObservation
	availability core.ObservationAvailability
	problems     []core.ObservationProblem
}
type attachDoneMsg struct{ err error }

// previewDueMsg fires once the cursor has stood still long enough for a preview
// to be worth fetching.
type previewDueMsg struct{ generation int }

// previewDebounce is short enough to feel immediate on a single keypress and
// long enough that holding a key or scrolling produces one probe, not one per
// step.
const previewDebounce = 120 * time.Millisecond

type usageTickMsg time.Time
type usageMsg UsageInfo

func usageTick() tea.Cmd {
	return tea.Tick(5*time.Minute, func(t time.Time) tea.Msg { return usageTickMsg(t) })
}

func fetchUsageCmd() tea.Cmd {
	return func() tea.Msg { return usageMsg(CachedUsage()) }
}

type model struct {
	state             *State
	cursor            int
	collapsed         map[string]bool
	input             textinput.Model
	inputKind         inputKind
	pendingProject    core.ProjectID
	renameFrom        string
	confirmKill       bool
	confirmRmProj     bool
	attention         *core.AttentionPlanner
	poll              pollResult
	flash             string
	flashIsErr        bool
	flashTime         time.Time
	width             int
	height            int
	pollBusy          bool
	repoBusy          bool
	diskBusy          bool
	previewPending    bool
	previewGeneration int
	inbox             core.AttentionInbox
	inboxOpen         bool
	inboxCursor       int
	usage             UsageInfo
}

func newModel(s *State) model {
	reconcile(s)
	return model{
		state: s, collapsed: map[string]bool{},
		attention: core.NewAttentionPlanner(core.AttentionPlannerConfig{}),
	}
}

func reconcile(s *State) {
	discovery := discoverNew(context.Background(), s)
	if err := discovery.Err(); err != nil {
		core.Logf("TUI Session-Discovery unvollständig: %v", err)
		return
	}
	if len(discovery.Sessions) > 0 {
		if changed, err := OpenRegistry(StatePath()).AdoptDiscoveredSessions(context.Background(), discovery.Sessions); err == nil {
			*s = changed.Snapshot.State()
		} else {
			core.Logf("TUI Session-Discovery fehlgeschlagen: %v", err)
		}
	}
}

func (m *model) executeAttentionPlan() {
	if m.attention == nil {
		m.attention = core.NewAttentionPlanner(core.AttentionPlannerConfig{})
	}
	labels := make(map[core.SessionID]string, len(m.state.Agents))
	for _, session := range m.state.Agents {
		if observed, found := m.poll.observed[sessionKey(session)]; found && observed.SessionID != "" {
			labels[observed.SessionID] = session.Name
		}
	}
	// Ausgeschaltete Benachrichtigungen sind ein Signal an den Planner, keine
	// Regel der Oberfläche: sonst schweigt der Desktop und die TUI meldet sich
	// weiter, obwohl beide denselben Planner benutzen.
	quiet := core.AttentionQuietNone
	if !core.NotificationsEnabled() {
		quiet = core.AttentionQuietMuted
	}
	plan := m.attention.Plan(core.AttentionInput{
		Observation: m.poll.observation, SessionLabels: labels, Quiet: quiet, Now: time.Now(),
	})
	// Der Posteingang kommt aus genau diesem Plan; die TUI leitet nichts eigenes ab.
	m.inbox = plan.Inbox
	if m.inboxCursor >= len(m.inbox.Entries) {
		m.inboxCursor = 0
	}
	// Die TUI kann nur benachrichtigen; Badge, native Attention und
	// In-den-Vordergrund-Holen sind Absichten, die sie nicht bedient.
	core.ExecuteAttentionPlan(plan, core.AttentionExecutor{Notify: notifyDesktop})
}

// inboxRow joins one planned entry with the Session the TUI knows it by. The
// order of the plan is kept as it is.
type inboxRow struct {
	entry core.AttentionInboxEntry
	agent Agent
	known bool
}

func (m model) inboxRows() []inboxRow {
	byID := make(map[core.SessionID]Agent, len(m.state.Agents))
	for _, session := range m.state.Agents {
		id := session.ID
		if observed, found := m.poll.observed[sessionKey(session)]; found && observed.SessionID != "" {
			id = observed.SessionID
		}
		if id != "" {
			byID[id] = session
		}
	}
	rows := make([]inboxRow, 0, len(m.inbox.Entries))
	for _, entry := range m.inbox.Entries {
		row := inboxRow{entry: entry}
		if session, found := byID[entry.SessionID]; found {
			row.agent, row.known = session, true
		}
		rows = append(rows, row)
	}
	return rows
}

// selectInboxEntry moves the tree selection to the Session of the selected
// entry and closes the inbox.
func (m *model) selectInboxEntry() {
	rows := m.inboxRows()
	if m.inboxCursor < 0 || m.inboxCursor >= len(rows) {
		return
	}
	row := rows[m.inboxCursor]
	if !row.known {
		m.setFlash("Session ist nicht mehr registriert", true)
		return
	}
	m.inboxOpen = false
	if row.agent.Project != "" {
		m.collapsed[row.agent.Project] = false
	}
	m.selectAgent(row.agent.Name)
}

func (m *model) moveInboxCursor(delta int) {
	count := len(m.inbox.Entries)
	if count == 0 {
		m.inboxCursor = 0
		return
	}
	next := m.inboxCursor + delta
	if next < 0 || next >= count {
		return
	}
	m.inboxCursor = next
}

func (m model) rows() []treeRow {
	if m.state == nil {
		return nil
	}
	agents := m.state.Agents
	// One pass over the Sessions: bucket indices per Project, remember the
	// orphans, and resolve every status rank once. The previous shape asked
	// AgentsFor (one full scan plus one copy per Project) and then paid a map
	// lookup with a freshly built key per status comparison while sorting.
	ranks := make([]int, len(agents))
	for i := range agents {
		ranks[i] = statusRank(m.statusForIndex(agents[i]))
	}
	knownProjects := make(map[string]int, len(m.state.Projects))
	for i := range m.state.Projects {
		knownProjects[m.state.Projects[i].Name] = i
	}
	buckets := make(map[string][]int, len(m.state.Projects))
	var orphans []int
	for i := range agents {
		if name := agents[i].Project; name != "" {
			if _, ok := knownProjects[name]; ok {
				buckets[name] = append(buckets[name], i)
				continue
			}
		}
		orphans = append(orphans, i)
	}
	var rows []treeRow
	for i := range m.state.Projects {
		p := &m.state.Projects[i]
		rows = append(rows, treeRow{kind: rowProject, project: p})
		if m.collapsed[p.Name] {
			continue
		}
		rows = appendAgentRows(rows, agents, buckets[p.Name], ranks, p)
	}
	if len(orphans) > 0 {
		rows = append(rows, treeRow{kind: rowProject, project: nil})
		if !m.collapsed[orphanKey] {
			rows = appendAgentRows(rows, agents, orphans, ranks, nil)
		}
	}
	return rows
}

// statusForIndex resolves the rank input for one Session without callers
// paying sessionKey's allocation more than once per Session per tree build.
func (m model) statusForIndex(session Agent) AgentStatus {
	if observation, ok := m.poll.observed[sessionKey(session)]; ok {
		return observation.Status
	}
	return StatusUnknown
}

func appendAgentRows(rows []treeRow, agents []Agent, indices []int, ranks []int, project *Project) []treeRow {
	ordered := append([]int(nil), indices...)
	sort.SliceStable(ordered, func(i, j int) bool {
		return ranks[ordered[i]] < ranks[ordered[j]]
	})
	for _, index := range ordered {
		rows = append(rows, treeRow{kind: rowAgent, agent: agents[index], project: project})
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
	return tea.Tick(observationInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func repoTick() tea.Cmd {
	return tea.Tick(repositoryInterval, func(t time.Time) tea.Msg { return repoTickMsg(t) })
}

func diskTick() tea.Cmd {
	return tea.Tick(diskInterval, func(t time.Time) tea.Msg { return diskTickMsg(t) })
}

const (
	tuiPollTimeout = 5 * time.Second
	// The tmux pass is cheap enough to run continuously; one git pass costs
	// several hundred milliseconds across every Project and Worktree, so it runs
	// on its own slower cadence and additionally after every action. The
	// file-backed facts (Registry file, Zeitgeist file, external runtimes)
	// change rarely, so they share the slow cadence instead of burdening every
	// 2s observation with two file reads and an extra tmux listing.
	observationInterval = 2 * time.Second
	repositoryInterval  = 10 * time.Second
	diskInterval        = 10 * time.Second
)

func observeCmd(state State) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tuiPollTimeout)
		defer cancel()
		result := collectObservationFacts(ctx, state, core.Observe)
		// Hintergrund-Buchhaltung für den letzten bekannten Status je Session.
		// Ein fehlgeschlagener Pass verzögert nur die Zuletzt-gesehen-Anzeige,
		// deshalb bleibt ein Fehler hier still.
		_, _ = core.RecordObservationStatuses(ctx, core.OpenRegistry(core.StatePath()), result.observation)
		return observationMsg(result)
	}
}

func diskCmd(state State) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tuiPollTimeout)
		defer cancel()
		var res pollResult
		res.discovery = discoverNew(ctx, &state)
		res.zeitgeist = zeitgeistInfo()
		if disk, err := LoadState(); err == nil {
			res.diskState = disk
		}
		return diskMsg(res)
	}
}

func repositoryCmd(state State) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), tuiPollTimeout)
		defer cancel()
		return repositoryMsg(collectRepositoryFacts(ctx, state, core.NewRepositories()))
	}
}

// collectPollModuleFacts is the TUI's private Adapter over the Observation and
// Repositories Modules. Tests replace the two local-substitutable dependencies;
// production callers receive one coherent Observation and one repository pass.
func collectPollModuleFacts(
	ctx context.Context,
	state State,
	observe tuiObservationReader,
	repositories tuiRepositoryReader,
) pollResult {
	result := collectObservationFacts(ctx, state, observe)
	facts := collectRepositoryFacts(ctx, state, repositories)
	result.repositoryProblem = facts.repositoryProblem
	result.projectInspections = facts.projectInspections
	result.inspections = facts.inspections
	result.inspectionProblem = facts.inspectionProblem
	return result
}

// collectObservationFacts reads only the runtime side of one poll.
func collectObservationFacts(ctx context.Context, state State, observe tuiObservationReader) pollResult {
	result := pollResult{
		observed: make(map[tuiSessionKey]core.SessionObservation, len(state.Agents)),
	}
	prepared := prepareObservationSessions(state.Agents)
	result.observation = observe(ctx, prepared)
	observedByID := make(map[core.SessionID]core.SessionObservation, len(result.observation.Sessions))
	for _, observation := range result.observation.Sessions {
		observedByID[observation.SessionID] = observation
	}
	result.resumable = make(map[tuiSessionKey]core.SessionResumability, len(state.Agents))
	for i, session := range state.Agents {
		if observation, ok := observedByID[prepared[i].ID]; ok {
			result.observed[sessionKey(session)] = observation
			result.resumable[sessionKey(session)] = core.ResumabilityForSession(prepared[i], observation)
		}
	}
	return result
}

// collectRepositoryFacts reads only the Git side of one poll. It observes the
// checkouts the TUI actually shows — one per Project root and one per Session
// directory — instead of every Worktree a Project happens to have. Topology
// alone answers which branch counts as main, so the working tree is read once
// per directory no matter how many Sessions share it.
func collectRepositoryFacts(
	ctx context.Context,
	state State,
	repositories tuiRepositoryReader,
) pollResult {
	result := pollResult{
		projectInspections: make(map[string]core.RepositoryInspection, len(state.Projects)),
		inspections:        make(map[tuiSessionKey]core.RepositoryInspection, len(state.Agents)),
		inspectionProblem:  make(map[tuiSessionKey]string),
	}

	topology, err := repositories.SurveyTopology(ctx, state.Projects)
	if err != nil {
		result.repositoryProblem = err.Error()
	}

	requests := make([]core.RepositoryInspectRequest, 0, len(state.Projects)+len(state.Agents))
	for _, project := range state.Projects {
		requests = append(requests, core.RepositoryInspectRequest{
			Directory:  project.Path,
			MainBranch: topologyMainBranch(topology, project),
		})
	}
	for _, session := range state.Agents {
		request := core.RepositoryInspectRequest{
			Directory:  session.Dir,
			MainBranch: sessionMainBranch(state, topology, session),
		}
		if session.BaseCommit != "" {
			request.Against = &core.RepositoryBaseline{
				Directory:  session.Dir,
				Head:       session.BaseCommit,
				DirtyPaths: append([]string(nil), session.BaseDirty...),
			}
		}
		requests = append(requests, request)
	}

	inspections, inspectErr := repositories.InspectAll(ctx, requests)
	if inspectErr != nil {
		problem := inspectErr.Error()
		if result.repositoryProblem == "" {
			result.repositoryProblem = problem
		}
		for _, session := range state.Agents {
			result.inspectionProblem[sessionKey(session)] = problem
		}
		return result
	}
	for index, project := range state.Projects {
		result.projectInspections[repositoryDirectoryKey(project.Path)] = inspections[index]
	}
	for index, session := range state.Agents {
		result.inspections[sessionKey(session)] = inspections[len(state.Projects)+index]
	}
	return result
}

// repositoryDirectoryKey is the TUI's stable lookup for a checkout. Sessions and
// Projects reach the same inspection when they name the same directory.
func repositoryDirectoryKey(directory string) string {
	return filepath.Clean(strings.TrimSpace(directory))
}

func topologyMainBranch(topology core.RepositoriesSurvey, project Project) string {
	if repository, ok := surveyedProject(topology, project); ok && repository.MainBranch.Known() {
		return repository.MainBranch.Value
	}
	return strings.TrimSpace(project.MainBranch)
}

func sessionMainBranch(state State, topology core.RepositoriesSurvey, session Agent) string {
	if repository, ok := surveyProject(state, topology, session); ok && repository.MainBranch.Known() {
		return repository.MainBranch.Value
	}
	return ""
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

// resumabilityFor reads the absent-runtime reading for one Session from the
// last observation pass: resumable with its offered actions, dead with its
// reason, or unknown. Live Sessions and missing observations yield the zero
// reading.
func (m model) resumabilityFor(session Agent) (core.SessionObservation, core.SessionResumability, bool) {
	observation, ok := m.observationFor(session)
	if !ok {
		return core.SessionObservation{}, core.SessionResumability{}, false
	}
	res, ok := m.poll.resumable[sessionKey(session)]
	if !ok {
		return observation, core.SessionResumability{}, true
	}
	return observation, res, true
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.pollNow(), tick(), repoTick(), diskTick(), fetchUsageCmd(), usageTick())
}

// pollNow refreshes all Modules at once. Actions call it because they change
// runtime, repository and Registry facts together.
func (m model) pollNow() tea.Cmd {
	return tea.Batch(observeCmd(*m.state), repositoryCmd(*m.state), diskCmd(*m.state))
}

// previewNow schedules a preview instead of starting one. Scrolling moves the
// cursor far faster than tmux can answer, and every answer for a Session the
// cursor has already left is thrown away — so the probe waits until the
// selection stands still. The generation counter makes a timer that belongs to
// an older selection expire silently.
func (m *model) previewNow() tea.Cmd {
	if m.selectedAgent() == nil {
		return nil
	}
	m.previewGeneration++
	generation := m.previewGeneration
	return tea.Tick(previewDebounce, func(time.Time) tea.Msg {
		return previewDueMsg{generation: generation}
	})
}

// previewFetch starts the probe the settled selection asks for.
func (m *model) previewFetch() tea.Cmd {
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
		return m, tea.Batch(observeCmd(*m.state), tick())
	case repoTickMsg:
		if m.repoBusy {
			return m, repoTick()
		}
		m.repoBusy = true
		return m, tea.Batch(repositoryCmd(*m.state), repoTick())
	case diskTickMsg:
		if m.diskBusy {
			return m, diskTick()
		}
		m.diskBusy = true
		return m, tea.Batch(diskCmd(*m.state), diskTick())
	case previewDueMsg:
		if msg.generation != m.previewGeneration {
			return m, nil
		}
		return m, m.previewFetch()
	case previewMsg:
		m.previewPending = false
		if a := m.selectedAgent(); a != nil && sessionKey(*a) == msg.key {
			if m.poll.observed == nil {
				m.poll.observed = make(map[tuiSessionKey]core.SessionObservation)
			}
			m.poll.observed[msg.key] = msg.observation
			return m, nil
		}
		// The cursor moved while tmux answered. Going through the debounce again
		// keeps one rule: a probe only ever runs for a selection that stands
		// still, so a fast scroll cannot chain probes it will discard.
		return m, m.previewNow()
	case usageTickMsg:
		return m, tea.Batch(fetchUsageCmd(), usageTick())
	case usageMsg:
		m.usage = UsageInfo(msg)
		return m, nil
	case repositoryMsg:
		m.repoBusy = false
		m.poll.repositoryProblem = msg.repositoryProblem
		m.poll.projectInspections = msg.projectInspections
		m.poll.inspections = msg.inspections
		m.poll.inspectionProblem = msg.inspectionProblem
		return m, nil
	case observationMsg:
		m.pollBusy = false
		m.poll.observation = msg.observation
		m.poll.observed = msg.observed
		m.poll.resumable = msg.resumable
		// Die Steuer-API leitet ihre Ereignisse aus genau diesem Durchlauf ab.
		publishControlObservation(m.state.Agents, msg.observation)
		m.executeAttentionPlan()
		return m, nil
	case diskMsg:
		m.diskBusy = false
		var selName string
		if a := m.selectedAgent(); a != nil {
			selName = a.Name
		}
		m.poll.discovery = msg.discovery
		m.poll.diskState = msg.diskState
		m.poll.zeitgeist = msg.zeitgeist
		if m.poll.diskState != nil {
			m.state = m.poll.diskState
		}
		if discoveryErr := m.poll.discovery.Err(); discoveryErr != nil {
			m.setFlash("Session-Discovery unvollständig: "+discoveryErr.Error(), true)
		} else if len(m.poll.discovery.Sessions) > 0 {
			if changed, err := OpenRegistry(StatePath()).AdoptDiscoveredSessions(context.Background(), m.poll.discovery.Sessions); err == nil {
				state := changed.Snapshot.State()
				m.state = &state
			} else {
				m.setFlash("Session-Discovery fehlgeschlagen: "+err.Error(), true)
			}
		}
		if selName != "" {
			m.selectAgent(selName)
		}
		return m, nil
	case attachDoneMsg:
		return m, m.pollNow()
	case tea.MouseMsg:
		if m.inputKind != inputNone || m.confirmKill || m.confirmRmProj || m.inboxOpen {
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
		if m.inboxOpen {
			return m.updateInbox(msg)
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
	return m.treeWidthWithNameW(m.maxAgentNameLen())
}

func (m model) treeWidthWithNameW(nameW int) int {
	w := nameW + 27
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
	case "R":
		return m.resumeSelected()
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
	case "i":
		m.inboxOpen = true
		if m.inboxCursor >= len(m.inbox.Entries) {
			m.inboxCursor = 0
		}
		return m, nil
	}
	return m, nil
}

// updateInbox drives the read-only inbox: moving through the list, jumping to a
// Session, closing it again. Answering stays in the desktop app.
func (m model) updateInbox(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "ctrl+c":
		return m, tea.Quit
	case "esc", "i":
		m.inboxOpen = false
		return m, nil
	case "up", "k":
		m.moveInboxCursor(-1)
		return m, nil
	case "down", "j":
		m.moveInboxCursor(1)
		return m, nil
	case "enter", " ":
		m.selectInboxEntry()
		return m, m.previewNow()
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
	if err := sendSkillByID(a.ID, cmd); err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	m.setFlash(label+" an "+a.Name+" gesendet", false)
	return m, m.pollNow()
}

func (m model) startSkillSession(p *Project, cmd string) (tea.Model, tea.Cmd) {
	kind := ""
	if strings.TrimSpace(cmd) == "/deploy" {
		kind = "deploy"
	}
	name, err := startSkillAgent(m.state, p.ID, p.Path, cmd, kind, cmd+" "+p.Name)
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
		m.pendingProject = p.ID
	}
	pendingProjectName := string(m.pendingProject)
	if project := m.state.ProjectByID(m.pendingProject); project != nil {
		pendingProjectName = project.Name
	}
	ti := textinput.New()
	ti.CharLimit = 500
	switch kind {
	case inputNewSession:
		ti.Prompt = fmt.Sprintf("Neuer Agent in %s (leer = auto): ", pendingProjectName)
	case inputNewWorktree:
		ti.Prompt = fmt.Sprintf("Neuer Agent im Worktree von %s (leer = auto): ", pendingProjectName)
	case inputNewTerm:
		where := pendingProjectName
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
	n, err := createTermSessionForID(m.state, a.ID, name)
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	m.selectAgent(n)
	m.setFlash(fmt.Sprintf("Terminal %q geöffnet (%s)", n, shortPath(a.Dir)), false)
	return m, m.pollNow()
}

func (m model) createAgent(worktree bool, name, kind string) (tea.Model, tea.Cmd) {
	proj := m.state.ProjectByID(m.pendingProject)
	if proj == nil {
		m.setFlash(fmt.Sprintf("ProjectID %q existiert nicht mehr", m.pendingProject), true)
		return m, nil
	}
	var (
		created string
		err     error
	)
	if kind == KindTerm {
		created, err = createTermSession(m.state, proj.ID, worktree, name)
	} else {
		created, err = createAgentSession(m.state, proj.ID, worktree, name)
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
	state := changed.Snapshot.State()
	m.state = &state
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
	session := m.state.AgentByName(m.renameFrom)
	if session == nil {
		m.setFlash("Session nicht gefunden", true)
		return m, nil
	}
	_, err := core.OpenSessionLifecycle(core.SessionLifecycleConfig{}).Rename(
		context.Background(), session.ID, session.Name, newName,
	)
	if err != nil {
		m.setFlash("Session umbenennen: "+err.Error(), true)
		return m, nil
	}
	snapshot, err := OpenRegistry(StatePath()).Snapshot(context.Background())
	if err != nil {
		m.setFlash("Registry nach Umbenennen laden: "+err.Error(), true)
		return m, nil
	}
	state := snapshot.State()
	m.state = &state
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
		// Eine fortsetzbare Session wird nicht gekillt, sondern verworfen: Es
		// gibt keine Runtime mehr, nur noch den Eintrag — und der geht, ohne
		// Verzeichnis, Worktree oder Konversation anzufassen.
		if observation, res, _ := m.resumabilityFor(*a); res.Resumable {
			if err := discardSessionByID(a.ID, observation); err != nil {
				m.setFlash(err.Error(), true)
				return m, nil
			}
			latest, err := LoadState()
			if err != nil {
				m.setFlash("Registry nach Verwerfen laden: "+err.Error(), true)
				return m, nil
			}
			m.state = latest
			m.ensureSelectable()
			m.setFlash(fmt.Sprintf("Eintrag %q verworfen — Verzeichnis bleibt erhalten", a.Name), false)
			return m, m.pollNow()
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
		if err := core.OpenSessionLifecycle(core.SessionLifecycleConfig{}).RemoveProject(context.Background(), p.ID); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		state, err := LoadState()
		if err != nil {
			m.setFlash("Registry nach Projektentfernung laden: "+err.Error(), true)
			return m, nil
		}
		m.state = state
		m.ensureSelectable()
		m.setFlash(fmt.Sprintf("Projekt %q entfernt (Dateien bleiben unberührt)", p.Name), false)
		return m, m.pollNow()
	}
	return m, nil
}

// resumeSelected führt die erste angebotene Aktion einer Session ohne Runtime
// aus: Fortsetzen, frisch starten oder Shell neu starten. Alles andere meldet
// nur, dass es nichts fortzusetzen gibt.
func (m model) resumeSelected() (tea.Model, tea.Cmd) {
	a := m.selectedAgent()
	if a == nil {
		return m, nil
	}
	observation, res, _ := m.resumabilityFor(*a)
	actions := core.SessionActionsFor(*a, observation, res)
	if len(actions) == 0 {
		m.setFlash(fmt.Sprintf("%s ist nicht fortsetzbar", a.Name), true)
		return m, nil
	}
	switch actions[0].ID {
	case core.SessionActionResume:
		if err := resumeSessionByID(a.ID); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		m.setFlash(fmt.Sprintf("%s wird fortgesetzt", a.Name), false)
	case core.SessionActionResumeFresh:
		if err := resumeFreshSessionByID(a.ID); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		m.setFlash(fmt.Sprintf("%s startet frisch", a.Name), false)
	case core.SessionActionRestartShell:
		if _, err := createTermSessionForID(m.state, a.ID, ""); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
		m.setFlash("Shell neu gestartet", false)
	default:
		m.setFlash(fmt.Sprintf("%s ist nicht fortsetzbar", a.Name), true)
		return m, nil
	}
	return m, m.pollNow()
}

func (m model) attach() (tea.Model, tea.Cmd) {
	a := m.selectedAgent()
	if a == nil {
		return m, nil
	}
	latest, err := LoadState()
	if err != nil {
		m.setFlash("Session Registry: "+err.Error(), true)
		return m, nil
	}
	current := latest.SessionByID(a.ID)
	if current == nil {
		m.setFlash("Session existiert nicht mehr — Ansicht aktualisieren", true)
		return m, nil
	}
	observed := observeSessions(context.Background(), []core.Session{*current})
	if len(observed.Sessions) != 1 || observed.Sessions[0].SessionID != current.ID ||
		observed.Sessions[0].Presence == core.SessionPresenceUnknown {
		m.setFlash("Session-Laufzeit kann derzeit nicht verlässlich geprüft werden", true)
		return m, nil
	}
	if observed.Sessions[0].Presence == core.SessionPresenceAbsent {
		m.setFlash("Session existiert nicht mehr — mit x entfernen oder n neu starten", true)
		return m, nil
	}
	sn := current.TmuxName()
	core.PrepareSessionPresentation(sn)
	// Innerhalb eines angehängten Clients wird gewechselt, außerhalb angehängt.
	if os.Getenv("TMUX") != "" {
		if err := core.SwitchToSessionCommand(sn).Command().Run(); err != nil {
			m.setFlash("tmux switch-client: "+err.Error(), true)
		}
		return m, nil
	}
	cmd := core.AttachSessionCommand(sn, "").Command()
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg { return attachDoneMsg{err} })
}
