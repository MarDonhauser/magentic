package core

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type OvAgent struct {
	ID            SessionID `json:"id"`
	Name          string    `json:"name"`
	Tool          string    `json:"tool,omitempty"`
	Vendor        string    `json:"vendor,omitempty"`
	Status        string    `json:"status"`
	Label         string    `json:"label"`
	Detail        string    `json:"detail"`
	Age           string    `json:"age"`
	Worktree      bool      `json:"worktree"`
	Term          bool      `json:"term"`
	Phase         string    `json:"phase,omitempty"`
	PhaseLabel    string    `json:"phaseLabel,omitempty"`
	Deployed      bool      `json:"deployed"`
	Known         bool      `json:"known"`
	OwnDirty      int       `json:"ownDirty"`
	OwnCommits    int       `json:"ownCommits"`
	Branch        string    `json:"branch,omitempty"`
	Unread        bool      `json:"unread"`
	Dock          bool      `json:"dock"`
	HandoffSource bool      `json:"handoffSource"`
	HandoffTarget bool      `json:"handoffTarget"`

	Queued     []OvQueuedMessage `json:"queued,omitempty"`
	Automation *OvAutomation     `json:"automation,omitempty"`
	StatusLine *OvStatusLine     `json:"statusLine,omitempty"`
}

// OvStatusLine is what the vendor last reported about its run — the facts the
// agent's own status line would show, so the app can draw that line itself.
type OvStatusLine struct {
	Model          string  `json:"model,omitempty"`
	Effort         string  `json:"effort,omitempty"`
	ContextPercent int     `json:"contextPercent"`
	ContextWindow  int     `json:"contextWindow,omitempty"`
	ContextTokens  int     `json:"contextTokens,omitempty"`
	CostUSD        float64 `json:"costUsd"`
	Version        string  `json:"version,omitempty"`
	OutputStyle    string  `json:"outputStyle,omitempty"`
	FastMode       bool    `json:"fastMode"`
	Age            string  `json:"age"`
}

type OvAutomation struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Enabled      bool      `json:"enabled"`
	EveryMinutes int       `json:"everyMinutes"`
	NextRunAt    time.Time `json:"nextRunAt"`
	LastRunAt    time.Time `json:"lastRunAt,omitzero"`
}

// OvQueuedMessage projects one durably queued Outbox message for the UI. Stuck
// reports an attempt that is still queued: a delivered message is dequeued
// right away, so a lingering attempt marker means the outcome is unknown.
type OvQueuedMessage struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Preview string `json:"preview"`
	Age     string `json:"age"`
	Stuck   bool   `json:"stuck"`
}

// OvInboxEntry is one planned inbox entry joined with the names and the queued
// messages the UI needs. It derives no attention fact of its own: kind, wait,
// and excerpt come from the AttentionPlan unchanged.
type OvInboxEntry struct {
	SessionID         SessionID            `json:"sessionId"`
	Session           string               `json:"session"`
	Project           string               `json:"project"`
	Kind              AttentionWaitingKind `json:"kind"`
	WaitingSince      time.Time            `json:"waitingSince,omitzero"`
	WaitingSinceKnown bool                 `json:"waitingSinceKnown"`
	Age               string               `json:"age"`
	Excerpt           string               `json:"excerpt,omitempty"`
	ExcerptKnown      bool                 `json:"excerptKnown"`
	Queued            []OvQueuedMessage    `json:"queued,omitempty"`
	AwaitingDelivery  bool                 `json:"awaitingDelivery"`
}

// OvInbox projects the planned blocked inbox for a frontend. State says how
// much of the list is known, so an empty Entries slice is never read as proof
// that nothing waits.
type OvInbox struct {
	State   AttentionInboxState `json:"state"`
	Entries []OvInboxEntry      `json:"entries"`
}

type OvWorktree struct {
	Reference       WorktreeRef         `json:"reference,omitempty"`
	Location        string              `json:"location,omitempty"`
	Path            string              `json:"-"`
	ShortPath       string              `json:"-"`
	Branch          string              `json:"branch"`
	IsMain          bool                `json:"isMain"`
	Ahead           int                 `json:"ahead"`
	Behind          int                 `json:"behind"`
	Staged          int                 `json:"staged"`
	Modified        int                 `json:"modified"`
	Untracked       int                 `json:"untracked"`
	Conflicted      int                 `json:"conflicted"`
	Clean           bool                `json:"clean"`
	LastMsg         string              `json:"lastMsg"`
	CheckoutKnown   bool                `json:"checkoutKnown"`
	ChangesKnown    bool                `json:"changesKnown"`
	DivergenceKnown bool                `json:"divergenceKnown"`
	Problems        []RepositoryProblem `json:"problems,omitempty"`
	Agents          []OvAgent           `json:"agents"`
	Warnings        []string            `json:"warnings"`
}

type OvProject struct {
	ID                  ProjectID           `json:"id"`
	Name                string              `json:"name"`
	Path                string              `json:"path"`
	MainBranch          string              `json:"mainBranch"`
	HeadBranch          string              `json:"headBranch"`
	MainConfigured      bool                `json:"mainConfigured"`
	RepositoryKnowledge RepositoryKnowledge `json:"repositoryKnowledge"`
	MainBranchKnown     bool                `json:"mainBranchKnown"`
	HeadBranchKnown     bool                `json:"headBranchKnown"`
	WorktreesKnown      bool                `json:"worktreesKnown"`
	Problems            []RepositoryProblem `json:"problems,omitempty"`
	Worktrees           []OvWorktree        `json:"worktrees"`
}

// OvUsageWindow is one quota window as the UI renders it: a share, a name for
// the window, and when it refills.
type OvUsageWindow struct {
	Label   string  `json:"label"`
	Percent float64 `json:"percent"`
	Reset   string  `json:"reset,omitempty"`
}

// OvUsagePage carries one provider's limits. Providers without a readable
// source get no page at all rather than an empty one.
type OvUsagePage struct {
	Provider string          `json:"provider"`
	Label    string          `json:"label"`
	Windows  []OvUsageWindow `json:"windows"`
}

type OvLater struct {
	ID      SessionID `json:"id"`
	Name    string    `json:"name"`
	Project string    `json:"project"`
	Age     string    `json:"age"`
	Term    bool      `json:"term"`
	Tool    string    `json:"tool,omitempty"`
}

type Overview struct {
	GeneratedAt string         `json:"generatedAt"`
	Counts      map[string]int `json:"counts"`
	Usage       []OvUsagePage  `json:"usage"`
	Projects    []OvProject    `json:"projects"`
	Later       []OvLater      `json:"later"`
	Sidebar     []SidebarNode  `json:"sidebar"`
}

func statusKey(s AgentStatus) string {
	switch s {
	case StatusRunning:
		return "running"
	case StatusAgents:
		return "agents"
	case StatusShell:
		return "shell"
	case StatusBlocked:
		return "blocked"
	case StatusDone:
		return "done"
	case StatusIdle:
		return "idle"
	case StatusExited:
		return "exited"
	case StatusDead:
		return "dead"
	case StatusTerm:
		return "term"
	}
	return "unknown"
}

func agentAlive(s AgentStatus) bool {
	return s == StatusRunning || s == StatusAgents || s == StatusShell || s == StatusBlocked ||
		s == StatusDone || s == StatusIdle || s == StatusTerm
}

type overviewRepositories interface {
	Survey(context.Context, []Project) (RepositoriesSurvey, error)
}

// BuildOverviewFromObservation projects one coherent runtime snapshot into the
// Overview and obtains one coherent repository Survey for all Projects. It is
// read-only: Session discovery and Registry cleanup belong to Lifecycle, not
// to an Overview read.
func BuildOverviewFromObservation(s *State, snapshot ObservationSnapshot) Overview {
	return buildOverviewFromObservationUsing(s, snapshot, NewRepositories())
}

func buildOverviewFromObservationUsing(s *State, snapshot ObservationSnapshot, repositories overviewRepositories) Overview {
	if s == nil {
		s = &State{}
	}
	copyOfState := *s
	copyOfState.Agents = observationSessions(s.Agents)
	copyOfSnapshot := snapshot
	copyOfSnapshot.Sessions = append([]SessionObservation(nil), snapshot.Sessions...)
	for i := range s.Agents {
		if s.Agents[i].ID == "" && i < len(copyOfSnapshot.Sessions) && copyOfSnapshot.Sessions[i].SessionID == "" {
			copyOfSnapshot.Sessions[i].SessionID = copyOfState.Agents[i].ID
		}
	}
	survey, surveyErr := repositories.Survey(context.Background(), append([]Project(nil), copyOfState.Projects...))
	return buildOverviewFromSurvey(&copyOfState, copyOfSnapshot, survey, surveyErr)
}

func buildOverviewFromSurvey(s *State, snapshot ObservationSnapshot, survey RepositoriesSurvey, surveyErr error) Overview {
	observations := make(map[SessionID]SessionObservation, len(snapshot.Sessions))
	for _, observed := range snapshot.Sessions {
		if observed.SessionID != "" {
			observations[observed.SessionID] = observed
		}
	}
	generatedAt := snapshot.ObservedAt
	if generatedAt.IsZero() {
		generatedAt = time.Now()
	}
	ov := Overview{
		GeneratedAt: generatedAt.Local().Format("15:04:05"),
		Counts:      map[string]int{},
	}
	ov.Usage = usagePages()
	later := map[SessionID]bool{}
	for _, a := range s.Agents {
		if a.LaterAt.IsZero() {
			continue
		}
		later[a.ID] = true
		observed := observationForSession(a, observations)
		ov.Later = append(ov.Later, OvLater{ID: a.ID, Name: a.Name, Project: a.Project, Age: FormatAge(a.LaterAt), Term: a.IsTerm(), Tool: observed.Tool})
	}
	// Dock-Terminals bleiben aus den Zählern: sie sind Werkzeug, keine Sitzung,
	// und tauchen deshalb auch in der Sitzungsliste nicht auf.
	dock := map[SessionID]bool{}
	for _, a := range s.Agents {
		if a.IsDock() {
			dock[a.ID] = true
		}
	}
	for _, a := range s.Agents {
		if later[a.ID] || dock[a.ID] {
			continue
		}
		ov.Counts[statusKey(observationForSession(a, observations).Status)]++
	}
	assigned := map[SessionID]bool{}
	for id := range later {
		assigned[id] = true
	}
	statusLines := StatusReportsForSessions(readStatusReports(), s.Agents)
	agentOverview := func(a Agent, observed SessionObservation, branch string) OvAgent {
		agent := toOvAgent(a, observed, branch)
		if report, ok := statusLines[a.ID]; ok {
			agent.StatusLine = ovStatusLine(report)
		}
		return agent
	}

	for _, p := range s.Projects {
		repository, found := overviewRepositoryProject(p, survey.Projects)
		if !found {
			repository = unavailableOverviewRepositoryProject(p, surveyErr)
		}
		proj, repositoryWorktrees := projectOverviewFromRepository(p, repository)
		for i, worktree := range repositoryWorktrees {
			for _, a := range s.Agents {
				if assigned[a.ID] || !overviewSessionMayBelongToProject(a, p) {
					continue
				}
				matched, matchedWorktree := repositoryWorktreeForDirectory(repositoryWorktrees, a.Dir)
				if !matchedWorktree || !sameRepositoryPath(matched.Path, worktree.Path) {
					continue
				}
				assigned[a.ID] = true
				branch := ""
				if proj.Worktrees[i].CheckoutKnown {
					branch = proj.Worktrees[i].Branch
				}
				proj.Worktrees[i].Agents = append(proj.Worktrees[i].Agents, agentOverview(a, observationForSession(a, observations), branch))
			}
		}
		for _, a := range overviewSessionsForProject(s, p) {
			if assigned[a.ID] {
				continue
			}
			assigned[a.ID] = true
			branch := ""
			if proj.Worktrees[0].CheckoutKnown {
				branch = proj.Worktrees[0].Branch
			}
			proj.Worktrees[0].Agents = append(proj.Worktrees[0].Agents, agentOverview(a, observationForSession(a, observations), branch))
		}
		finishWarnings(&proj)
		ov.Projects = append(ov.Projects, proj)
	}

	var orphanWt OvWorktree
	hasOrphans := false
	for _, a := range s.Agents {
		if assigned[a.ID] {
			continue
		}
		if a.ProjectID != "" && s.ProjectByID(a.ProjectID) != nil {
			continue
		}
		if a.Project != "" && s.ProjectByName(a.Project) != nil {
			continue
		}
		hasOrphans = true
		orphanWt.Agents = append(orphanWt.Agents, agentOverview(a, observationForSession(a, observations), ""))
	}
	if hasOrphans {
		orphanWt.Branch = "—"
		orphanWt.IsMain = true
		ov.Projects = append(ov.Projects, OvProject{
			Name: "(ohne Projekt)", RepositoryKnowledge: RepositoryUnknown,
			Worktrees: []OvWorktree{orphanWt},
		})
	}

	for _, p := range ov.Projects {
		for _, wt := range p.Worktrees {
			if wt.ChangesKnown && !wt.Clean {
				ov.Counts["dirty"]++
			}
			if len(wt.Warnings) > 0 {
				ov.Counts["warnings"]++
			}
			for _, a := range wt.Agents {
				if a.Unread && !a.Dock {
					ov.Counts["unread"]++
				}
			}
		}
	}
	ov.Sidebar = SidebarLayout(s)
	return ov
}

func observationForSession(session Session, observations map[SessionID]SessionObservation) SessionObservation {
	if observed, found := observations[session.ID]; found {
		return observed
	}
	return SessionObservation{
		SessionID:    session.ID,
		Availability: ObservationUnavailable,
		Presence:     SessionPresenceUnknown,
		Status:       StatusUnknown,
		Attention:    AttentionUnknown,
		WorktreePath: session.Dir,
		Worktree:     session.Worktree,
		Occupancy:    OccupancyUnknown,
	}
}

func overviewRepositoryProject(project Project, surveyed []RepositoryProjectSurvey) (RepositoryProjectSurvey, bool) {
	if project.ID != "" {
		for _, repository := range surveyed {
			if repository.ID == project.ID {
				return repository, true
			}
		}
	}
	for _, repository := range surveyed {
		if repository.Name == project.Name && sameRepositoryPath(repository.Path, project.Path) {
			return repository, true
		}
	}
	return RepositoryProjectSurvey{}, false
}

func unavailableOverviewRepositoryProject(project Project, surveyErr error) RepositoryProjectSurvey {
	message := "Project is missing from repository Survey"
	if surveyErr != nil {
		message = surveyErr.Error()
	}
	problem := &RepositoryProblem{Operation: "survey", Message: message}
	return RepositoryProjectSurvey{
		ID: project.ID, Name: project.Name, Path: project.Path,
		Presence:   RepositoryUnknown,
		Problem:    problem,
		MainBranch: RepositoryFact[string]{State: RepositoryUnknown, Problem: problem},
		Worktrees:  RepositoryFact[[]RepositoryWorktree]{State: RepositoryUnknown, Problem: problem},
	}
}

func projectOverviewFromRepository(project Project, repository RepositoryProjectSurvey) (OvProject, []RepositoryWorktree) {
	knowledge := repository.Presence
	if knowledge == "" {
		knowledge = RepositoryUnknown
	}
	overview := OvProject{
		ID: project.ID, Name: project.Name, Path: project.Path,
		MainConfigured:      strings.TrimSpace(project.MainBranch) != "",
		RepositoryKnowledge: knowledge,
	}
	overview.Problems = appendOverviewProblem(overview.Problems, repository.Problem)
	if repository.MainBranch.Known() {
		overview.MainBranch = repository.MainBranch.Value
		overview.MainBranchKnown = true
	} else {
		overview.Problems = appendOverviewProblem(overview.Problems, repository.MainBranch.Problem)
	}

	var worktrees []RepositoryWorktree
	if repository.Worktrees.Known() {
		overview.WorktreesKnown = true
		worktrees = append([]RepositoryWorktree(nil), repository.Worktrees.Value...)
		for _, worktree := range worktrees {
			overview.Worktrees = append(overview.Worktrees, overviewWorktreeFromRepository(worktree))
		}
	} else {
		overview.Problems = appendOverviewProblem(overview.Problems, repository.Worktrees.Problem)
	}

	if len(worktrees) > 0 {
		head := worktrees[0]
		for _, worktree := range worktrees {
			if worktree.Main {
				head = worktree
				break
			}
		}
		if branch, known := overviewCheckoutLabel(head.Checkout); known {
			overview.HeadBranch = branch
			overview.HeadBranchKnown = true
		} else {
			overview.Problems = appendOverviewProblem(overview.Problems, head.Checkout.Problem)
		}
	}
	if len(overview.Worktrees) == 0 {
		problem := repository.Worktrees.Problem
		if repository.Worktrees.Known() {
			problem = &RepositoryProblem{Operation: "worktree_topology", Message: "repository Survey returned no Worktrees"}
			overview.Problems = appendOverviewProblem(overview.Problems, problem)
		}
		overview.Worktrees = append(overview.Worktrees, fallbackOverviewWorktree(project, repository, problem))
	}
	return overview, worktrees
}

func overviewWorktreeFromRepository(worktree RepositoryWorktree) OvWorktree {
	overview := OvWorktree{
		Reference: worktree.Reference, Location: worktree.Location,
		Path: worktree.Path, ShortPath: worktree.Location, IsMain: worktree.Main,
	}
	if branch, known := overviewCheckoutLabel(worktree.Checkout); known {
		overview.Branch = branch
		overview.CheckoutKnown = true
	} else {
		overview.Problems = appendOverviewProblem(overview.Problems, worktree.Checkout.Problem)
	}
	overview.Problems = appendOverviewProblem(overview.Problems, worktree.Head.Problem)
	if worktree.Changes.Known() {
		changes := worktree.Changes.Value
		overview.ChangesKnown = true
		overview.Staged = changes.Staged
		overview.Modified = changes.Modified
		overview.Untracked = changes.Untracked
		overview.Conflicted = changes.Conflicted
		overview.Clean = changes.Clean()
	} else {
		overview.Problems = appendOverviewProblem(overview.Problems, worktree.Changes.Problem)
	}
	if worktree.Divergence.Known() {
		overview.DivergenceKnown = true
		overview.Ahead = worktree.Divergence.Value.Ahead
		overview.Behind = worktree.Divergence.Value.Behind
	} else {
		overview.Problems = appendOverviewProblem(overview.Problems, worktree.Divergence.Problem)
	}
	return overview
}

func fallbackOverviewWorktree(project Project, repository RepositoryProjectSurvey, problem *RepositoryProblem) OvWorktree {
	branch := ""
	if repository.Presence == RepositoryNotRepository {
		branch = "(kein git)"
	}
	worktree := OvWorktree{
		Reference: repositoryWorktreeReference(project, project.Path),
		Location:  repositoryWorktreeLocation(project.Path, project.Path),
		Path:      project.Path, ShortPath: repositoryWorktreeLocation(project.Path, project.Path), Branch: branch, IsMain: true,
	}
	worktree.Problems = appendOverviewProblem(worktree.Problems, repository.Problem)
	worktree.Problems = appendOverviewProblem(worktree.Problems, problem)
	return worktree
}

func overviewCheckoutLabel(checkout RepositoryFact[RepositoryCheckout]) (string, bool) {
	if !checkout.Known() {
		return "", false
	}
	switch checkout.Value.Kind {
	case RepositoryBranchCheckout:
		return checkout.Value.Branch, checkout.Value.Branch != ""
	case RepositoryDetached:
		return "(detached)", true
	case RepositoryUnborn:
		return "(unborn)", true
	case RepositoryBare:
		return "(bare)", true
	default:
		return "", false
	}
}

func appendOverviewProblem(problems []RepositoryProblem, problem *RepositoryProblem) []RepositoryProblem {
	if problem == nil {
		return problems
	}
	for _, existing := range problems {
		if existing.Operation == problem.Operation && existing.Message == problem.Message {
			return problems
		}
	}
	return append(problems, *problem)
}

func overviewSessionMayBelongToProject(session Session, project Project) bool {
	if session.ProjectID != "" && project.ID != "" {
		return session.ProjectID == project.ID
	}
	if session.Project != "" {
		return session.Project == project.Name
	}
	return true
}

func overviewSessionsForProject(state *State, project Project) []Session {
	var sessions []Session
	for _, session := range state.Agents {
		if session.ProjectID != "" && project.ID != "" {
			if session.ProjectID == project.ID {
				sessions = append(sessions, session)
			}
			continue
		}
		if session.Project == project.Name {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

var readStatusReports = func() []StatusReport { return ReadStatusReports(StatusReportDir()) }

func ovStatusLine(report StatusReport) *OvStatusLine {
	percent := int(report.ContextPercent + 0.5)
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	return &OvStatusLine{
		Model: report.Model, Effort: report.Effort,
		ContextPercent: percent, ContextWindow: report.ContextWindow, ContextTokens: report.ContextTokens,
		CostUSD: report.CostUSD, Version: report.Version, OutputStyle: report.OutputStyle,
		FastMode: report.FastMode, Age: FormatAge(report.At),
	}
}

func toOvAgent(a Agent, observed SessionObservation, branch string) OvAgent {
	st := observed.Status
	lastActive := a.CreatedAt
	if observed.ActivityKnown {
		lastActive = observed.Activity
	}
	phase, phaseLabel := agentPhase(a, agentAlive(st))
	tool := observed.Tool
	handoffSource := handoffSourceCapable(a, observed)
	handoffTarget := handoffTargetCapable(a, observed)
	// Survey deliberately omits per-Session baseline deltas. Keep the legacy
	// fields explicitly unknown instead of rebuilding that Git meaning here.
	agent := OvAgent{
		ID:            a.ID,
		Name:          a.Name,
		Tool:          tool,
		Vendor:        string(a.SessionVendor()),
		Status:        statusKey(st),
		Label:         st.Label(),
		Detail:        observed.Detail,
		Age:           FormatAge(lastActive),
		Worktree:      a.Worktree,
		Term:          a.IsTerm(),
		Phase:         phase,
		PhaseLabel:    phaseLabel,
		Deployed:      agentAlive(st) && !a.DeployAt.IsZero() && time.Since(a.DeployAt) < 45*time.Minute,
		Known:         false,
		OwnDirty:      0,
		OwnCommits:    0,
		Branch:        branch,
		Unread:        observed.Unread,
		Dock:          a.IsDock(),
		HandoffSource: handoffSource,
		HandoffTarget: handoffTarget,
		Queued:        queuedMessagesOverview(a.Outbox),
	}
	if a.Automation != nil {
		agent.Automation = &OvAutomation{
			ID: a.Automation.ID, Name: a.Automation.Name, Enabled: a.Automation.Enabled,
			EveryMinutes: a.Automation.EveryMinutes, NextRunAt: a.Automation.NextRunAt,
			LastRunAt: a.Automation.LastRunAt,
		}
	}
	return agent
}

// BuildInbox joins the planned inbox with the Registry so an entry carries the
// Project and Session the developer knows it by, plus the messages that are
// still queued for it. The order the planner produced is kept as it is.
func BuildInbox(st *State, inbox AttentionInbox) OvInbox {
	out := OvInbox{State: inbox.State, Entries: []OvInboxEntry{}}
	if out.State == "" {
		out.State = AttentionInboxUnavailable
	}
	sessions := map[SessionID]Session{}
	if st != nil {
		for _, session := range st.Agents {
			sessions[session.ID] = session
		}
	}
	for _, planned := range inbox.Entries {
		entry := OvInboxEntry{
			SessionID:         planned.SessionID,
			Session:           string(planned.SessionID),
			Kind:              planned.Kind,
			WaitingSince:      planned.WaitingSince,
			WaitingSinceKnown: planned.WaitingSinceKnown,
			Age:               FormatAge(planned.WaitingSince),
			Excerpt:           planned.Excerpt,
			ExcerptKnown:      planned.ExcerptKnown,
		}
		if session, known := sessions[planned.SessionID]; known {
			entry.Session = session.Name
			entry.Project = session.Project
			entry.Queued = queuedMessagesOverview(session.Outbox)
			entry.AwaitingDelivery = len(entry.Queued) > 0
		}
		out.Entries = append(out.Entries, entry)
	}
	return out
}

const queuedMessagePreviewLimit = 80

func queuedMessagesOverview(outbox []QueuedMessage) []OvQueuedMessage {
	if len(outbox) == 0 {
		return nil
	}
	queued := make([]OvQueuedMessage, 0, len(outbox))
	for _, message := range outbox {
		queued = append(queued, OvQueuedMessage{
			ID:      message.ID,
			Kind:    string(message.Kind),
			Preview: queuedMessagePreview(message.Text),
			Age:     FormatAge(message.EnqueuedAt),
			Stuck:   !message.AttemptedAt.IsZero(),
		})
	}
	return queued
}

// queuedMessagePreview keeps the queue readable in one line per message.
func queuedMessagePreview(text string) string {
	preview := strings.Join(strings.Fields(text), " ")
	runes := []rune(preview)
	if len(runes) > queuedMessagePreviewLimit {
		preview = strings.TrimRight(string(runes[:queuedMessagePreviewLimit]), " ") + "…"
	}
	return preview
}

func unread(st AgentStatus, seenAt, lastActive time.Time) bool {
	return observationUnread(st, seenAt, lastActive, true)
}

var integrationBranches = map[string]bool{"dev": true, "main": true, "master": true, "develop": true}

func agentPhase(a Agent, alive bool) (string, string) {
	if !alive {
		return "", ""
	}
	switch a.Purpose {
	case SessionPurposeDeploy:
		return "deploy", ""
	case SessionPurposeMerge:
		return "merge", ""
	case SessionPurposeCleanup:
		return "cleanup", ""
	}
	// Legacy Registry records are translated here until every installation has
	// passed through the Registry migration.
	switch a.Kind {
	case "deploy":
		return "deploy", ""
	case "merge":
		return "merge", ""
	case "cleanup":
		return "cleanup", ""
	}
	// The old "committed" presentation required per-Session Git probes. Survey
	// intentionally omits history, so Overview leaves that phase unknown rather
	// than bypassing the Repositories Module.
	return "", ""
}

func finishWarnings(proj *OvProject) {
	for i := range proj.Worktrees {
		wt := &proj.Worktrees[i]
		alive := false
		unknown := false
		for _, a := range wt.Agents {
			if a.Status == "unknown" {
				unknown = true
			}
			if a.Status == "running" || a.Status == "agents" || a.Status == "shell" || a.Status == "blocked" ||
				a.Status == "done" || a.Status == "idle" || a.Status == "term" {
				alive = true
			}
		}
		if wt.ChangesKnown && !wt.Clean && !alive && !unknown {
			wt.Warnings = append(wt.Warnings, "uncommitted Änderungen, keine aktive Session")
		}
		if wt.DivergenceKnown && wt.Ahead > 0 && !alive && !unknown && wt.Branch != proj.MainBranch {
			word := "Commits"
			if wt.Ahead == 1 {
				word = "Commit"
			}
			wt.Warnings = append(wt.Warnings, fmt.Sprintf("%d %s nicht in %s", wt.Ahead, word, proj.MainBranch))
		}
	}
}

// usagePages collects every provider that can actually report its limits.
// Claude answers over its OAuth endpoint, Codex from its own rollout records,
// Copilot over GitHub's quota snapshot; Gemini has no readable source and is
// therefore absent.
func usagePages() []OvUsagePage {
	var pages []OvUsagePage
	if u := CachedUsage(); u.Err == "" && !u.FetchedAt.IsZero() {
		pages = append(pages, OvUsagePage{
			Provider: string(AgentVendorClaude),
			Label:    "Claude-Limits",
			Windows: []OvUsageWindow{
				{Label: "5h", Percent: u.FiveHour, Reset: u.FiveHourReset.Format("15:04")},
				{Label: "7d", Percent: u.SevenDay, Reset: ShortWeekday(u.SevenDayReset)},
			},
		})
	}
	if u := CachedCodexUsage(); u.Err == "" && len(u.Windows) > 0 {
		page := OvUsagePage{Provider: string(AgentVendorCodex), Label: "Codex-Limits"}
		for _, window := range u.Windows {
			entry := OvUsageWindow{Label: window.Label, Percent: window.Percent}
			if !window.Reset.IsZero() {
				if window.Reset.Sub(time.Now()) > 24*time.Hour {
					entry.Reset = ShortWeekday(window.Reset)
				} else {
					entry.Reset = window.Reset.Format("15:04")
				}
			}
			page.Windows = append(page.Windows, entry)
		}
		pages = append(pages, page)
	}
	if u := CachedCopilotUsage(); u.Err == "" && len(u.Windows) > 0 {
		page := OvUsagePage{Provider: string(AgentVendorCopilot), Label: "Copilot-Limits"}
		for _, window := range u.Windows {
			entry := OvUsageWindow{Label: window.Label, Percent: window.Percent}
			if !window.Reset.IsZero() {
				// Copilot rechnet monatlich ab, ein Wochentag oder eine Uhrzeit
				// würde den Zeitpunkt also falsch nahe erscheinen lassen.
				entry.Reset = window.Reset.Format("2.1.")
			}
			page.Windows = append(page.Windows, entry)
		}
		pages = append(pages, page)
	}
	return pages
}
