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
}

type OvWorktree struct {
	Path            string              `json:"path"`
	ShortPath       string              `json:"ShortPath"`
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
	ID                  ProjectID            `json:"id"`
	Name                string               `json:"name"`
	Path                string               `json:"path"`
	MainBranch          string               `json:"mainBranch"`
	HeadBranch          string               `json:"headBranch"`
	MainConfigured      bool                 `json:"mainConfigured"`
	RepositoryKnowledge RepositoryKnowledge  `json:"repositoryKnowledge"`
	MainBranchKnown     bool                 `json:"mainBranchKnown"`
	HeadBranchKnown     bool                 `json:"headBranchKnown"`
	WorktreesKnown      bool                 `json:"worktreesKnown"`
	Problems            []RepositoryProblem  `json:"problems,omitempty"`
	Worktrees           []OvWorktree         `json:"worktrees"`
}

type OvUsage struct {
	FiveHour      float64 `json:"fiveHour"`
	FiveHourReset string  `json:"fiveHourReset"`
	SevenDay      float64 `json:"sevenDay"`
	SevenDayReset string  `json:"sevenDayReset"`
}

type OvLater struct {
	Name    string `json:"name"`
	Project string `json:"project"`
	Age     string `json:"age"`
	Term    bool   `json:"term"`
	Tool    string `json:"tool,omitempty"`
}

type Overview struct {
	GeneratedAt string         `json:"generatedAt"`
	Counts      map[string]int `json:"counts"`
	Usage       *OvUsage       `json:"usage"`
	Projects    []OvProject    `json:"projects"`
	Later       []OvLater      `json:"later"`
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
	return s == StatusRunning || s == StatusAgents || s == StatusShell || s == StatusBlocked || s == StatusIdle || s == StatusTerm
}

func BuildOverview(s *State) Overview {
	if s == nil {
		return buildOverviewFromObservation(&State{}, ObservationSnapshot{})
	}
	sessions := observationSessions(s.Agents)
	copyOfState := *s
	copyOfState.Agents = sessions
	return buildOverviewFromObservation(&copyOfState, Observe(context.Background(), sessions))
}

func BuildOverviewFrom(s *State, statuses map[string]AgentStatus, contents map[string]string, activity map[string]time.Time) Overview {
	return BuildOverviewWithToolsFrom(s, statuses, contents, activity, nil)
}

func BuildOverviewWithToolsFrom(s *State, statuses map[string]AgentStatus, contents map[string]string, activity map[string]time.Time, tools map[string]string) Overview {
	if s == nil {
		return buildOverviewFromObservation(&State{}, ObservationSnapshot{})
	}
	sessions, snapshot := legacyObservationSnapshot(s.Agents, statuses, contents, activity, tools)
	copyOfState := *s
	copyOfState.Agents = sessions
	return buildOverviewFromObservation(&copyOfState, snapshot)
}

// BuildOverviewFromObservation projects one coherent runtime snapshot into the
// Overview. It is read-only: Session discovery and Registry cleanup belong to
// Lifecycle, not to an Overview read.
func BuildOverviewFromObservation(s *State, snapshot ObservationSnapshot) Overview {
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
	return buildOverviewFromObservation(&copyOfState, copyOfSnapshot)
}

func buildOverviewFromObservation(s *State, snapshot ObservationSnapshot) Overview {
	gitCache := map[string]GitInfo{}
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
	if u := CachedUsage(); u.Err == "" && !u.FetchedAt.IsZero() {
		ov.Usage = &OvUsage{
			FiveHour:      u.FiveHour,
			FiveHourReset: u.FiveHourReset.Format("15:04"),
			SevenDay:      u.SevenDay,
			SevenDayReset: ShortWeekday(u.SevenDayReset),
		}
	}
	later := map[SessionID]bool{}
	for _, a := range s.Agents {
		if a.LaterAt.IsZero() {
			continue
		}
		later[a.ID] = true
		observed := observationForSession(a, observations)
		ov.Later = append(ov.Later, OvLater{Name: a.Name, Project: a.Project, Age: FormatAge(a.LaterAt), Term: a.IsTerm(), Tool: observed.Tool})
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

	for _, p := range s.Projects {
		proj := OvProject{ID: p.ID, Name: p.Name, Path: p.Path}
		wts := CollectWorktreesCached(p.Path)
		if len(wts) == 0 {
			wts = []WorktreeInfo{{Path: p.Path, Branch: ""}}
		}
		proj.HeadBranch = wts[0].Branch
		proj.MainBranch = proj.HeadBranch
		if p.MainBranch != "" {
			proj.MainBranch = p.MainBranch
			proj.MainConfigured = true
		}
		for i, wt := range wts {
			owt := buildWorktree(s, observations, assigned, wt, i == 0, proj.MainBranch, gitCache)
			proj.Worktrees = append(proj.Worktrees, owt)
		}
		for _, a := range s.AgentsFor(p.Name) {
			if assigned[a.ID] {
				continue
			}
			assigned[a.ID] = true
			proj.Worktrees[0].Agents = append(proj.Worktrees[0].Agents, toOvAgent(a, observationForSession(a, observations), proj.MainBranch, gitCache))
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
		if a.Project != "" && s.ProjectByName(a.Project) != nil {
			continue
		}
		hasOrphans = true
		orphanWt.Agents = append(orphanWt.Agents, toOvAgent(a, observationForSession(a, observations), "", gitCache))
	}
	if hasOrphans {
		orphanWt.Branch = "—"
		orphanWt.IsMain = true
		orphanWt.Clean = true
		ov.Projects = append(ov.Projects, OvProject{Name: "(ohne Projekt)", Worktrees: []OvWorktree{orphanWt}})
	}

	for _, p := range ov.Projects {
		for _, wt := range p.Worktrees {
			if !wt.Clean {
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

func cachedGit(cache map[string]GitInfo, dir string) GitInfo {
	if gi, ok := cache[dir]; ok {
		return gi
	}
	gi := CollectGitInfoCached(dir)
	cache[dir] = gi
	return gi
}

func buildWorktree(s *State, observations map[SessionID]SessionObservation, assigned map[SessionID]bool, wt WorktreeInfo, isMain bool, mainBranch string, gitCache map[string]GitInfo) OvWorktree {
	git := cachedGit(gitCache, wt.Path)
	owt := OvWorktree{
		Path:      wt.Path,
		ShortPath: ShortPath(wt.Path),
		Branch:    wt.Branch,
		IsMain:    isMain,
		Staged:    git.Staged,
		Modified:  git.Modified,
		Untracked: git.Untracked,
		Clean:     git.Clean(),
		LastMsg:   git.LastMsg,
	}
	if owt.Branch == "" {
		if git.IsRepo {
			owt.Branch = git.Branch
		} else {
			owt.Branch = "(kein git)"
			owt.Clean = true
		}
	}
	if owt.Branch != mainBranch && mainBranch != "" && mainBranch != "(detached)" && git.IsRepo {
		owt.Ahead, owt.Behind = AheadBehindCached(wt.Path, mainBranch)
	}
	for _, a := range s.Agents {
		if a.Dir == wt.Path && !assigned[a.ID] {
			assigned[a.ID] = true
			owt.Agents = append(owt.Agents, toOvAgent(a, observationForSession(a, observations), mainBranch, gitCache))
		}
	}
	return owt
}

func toOvAgent(a Agent, observed SessionObservation, mainBranch string, gitCache map[string]GitInfo) OvAgent {
	st := observed.Status
	lastActive := a.CreatedAt
	if observed.ActivityKnown {
		lastActive = observed.Activity
	}
	phase, phaseLabel := agentPhase(a, mainBranch, agentAlive(st))
	var sc SessionChanges
	branch := ""
	if gi := cachedGit(gitCache, a.Dir); gi.IsRepo {
		sc = CollectSessionChangesCached(a, gi)
		branch = gi.Branch
	}
	tool := observed.Tool
	handoffCapable := len(a.AgentRuns) > 0 || strings.TrimSpace(a.SessionID) != "" || (tool != "" && tool != AgentToolBash)
	return OvAgent{
		ID:            a.ID,
		Name:          a.Name,
		Tool:          tool,
		Status:        statusKey(st),
		Label:         st.Label(),
		Detail:        observed.Detail,
		Age:           FormatAge(lastActive),
		Worktree:      a.Worktree,
		Term:          a.IsTerm(),
		Phase:         phase,
		PhaseLabel:    phaseLabel,
		Deployed:      agentAlive(st) && !a.DeployAt.IsZero() && time.Since(a.DeployAt) < 45*time.Minute,
		Known:         sc.Known,
		OwnDirty:      len(sc.Files),
		OwnCommits:    sc.Commits,
		Branch:        branch,
		Unread:        observed.Unread,
		Dock:          a.IsDock(),
		HandoffSource: handoffCapable,
		HandoffTarget: handoffCapable,
	}
}

func unread(st AgentStatus, seenAt, lastActive time.Time) bool {
	return observationUnread(st, seenAt, lastActive, true)
}

var integrationBranches = map[string]bool{"dev": true, "main": true, "master": true, "develop": true}

func agentPhase(a Agent, mainBranch string, alive bool) (string, string) {
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
	if a.BaseCommit == "" {
		return "", ""
	}
	branch, err := GitCmdCached(a.Dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", ""
	}
	branch = strings.TrimSpace(branch)
	if branch == "" || (branch != mainBranch && !integrationBranches[branch]) {
		return "", ""
	}
	cnt, err := GitCmdCached(a.Dir, "rev-list", "--count", a.BaseCommit+"..HEAD")
	if err != nil {
		return "", ""
	}
	if n := strings.TrimSpace(cnt); n == "" || n == "0" {
		return "", ""
	}
	tsRaw, err := GitCmdCached(a.Dir, "log", "-1", "--format=%ct")
	if err != nil {
		return "", ""
	}
	ts, _ := strconv.ParseInt(strings.TrimSpace(tsRaw), 10, 64)
	if ts == 0 || time.Since(time.Unix(ts, 0)) > 15*time.Minute {
		return "", ""
	}
	return "committed", branch
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
			if a.Status == "running" || a.Status == "agents" || a.Status == "shell" || a.Status == "blocked" || a.Status == "idle" || a.Status == "term" {
				alive = true
			}
		}
		if !wt.Clean && !alive && !unknown {
			wt.Warnings = append(wt.Warnings, "uncommitted Änderungen, keine aktive Session")
		}
		if wt.Ahead > 0 && !alive && !unknown && wt.Branch != proj.MainBranch {
			word := "Commits"
			if wt.Ahead == 1 {
				word = "Commit"
			}
			wt.Warnings = append(wt.Warnings, fmt.Sprintf("%d %s nicht in %s", wt.Ahead, word, proj.MainBranch))
		}
	}
}
