package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type OvAgent struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Label      string `json:"label"`
	Detail     string `json:"detail"`
	Age        string `json:"age"`
	Worktree   bool   `json:"worktree"`
	Phase      string `json:"phase,omitempty"`
	PhaseLabel string `json:"phaseLabel,omitempty"`
	Known      bool   `json:"known"`
	OwnDirty   int    `json:"ownDirty"`
	OwnCommits int    `json:"ownCommits"`
}

type OvWorktree struct {
	Path      string    `json:"path"`
	ShortPath string    `json:"ShortPath"`
	Branch    string    `json:"branch"`
	IsMain    bool      `json:"isMain"`
	Ahead     int       `json:"ahead"`
	Behind    int       `json:"behind"`
	Staged    int       `json:"staged"`
	Modified  int       `json:"modified"`
	Untracked int       `json:"untracked"`
	Clean     bool      `json:"clean"`
	LastMsg   string    `json:"lastMsg"`
	Agents    []OvAgent `json:"agents"`
	Warnings  []string  `json:"warnings"`
}

type OvProject struct {
	Name           string       `json:"name"`
	Path           string       `json:"path"`
	MainBranch     string       `json:"mainBranch"`
	HeadBranch     string       `json:"headBranch"`
	MainConfigured bool         `json:"mainConfigured"`
	Worktrees      []OvWorktree `json:"worktrees"`
}

type OvUsage struct {
	FiveHour      float64 `json:"fiveHour"`
	FiveHourReset string  `json:"fiveHourReset"`
	SevenDay      float64 `json:"sevenDay"`
	SevenDayReset string  `json:"sevenDayReset"`
}

type Overview struct {
	GeneratedAt string         `json:"generatedAt"`
	Counts      map[string]int `json:"counts"`
	Usage       *OvUsage       `json:"usage"`
	Projects    []OvProject    `json:"projects"`
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
	}
	return "unknown"
}

func agentAlive(s AgentStatus) bool {
	return s == StatusRunning || s == StatusAgents || s == StatusShell || s == StatusBlocked || s == StatusIdle
}

func BuildOverview(s *State) Overview {
	for _, a := range DiscoverNew(s) {
		s.AddAgent(a)
	}
	gitCache := map[string]GitInfo{}
	statuses, contents, activity := CollectStatuses(s.Agents)
	kept := s.Agents[:0]
	removed := false
	for _, a := range s.Agents {
		if statuses[a.Name] == StatusDead {
			removed = true
			continue
		}
		kept = append(kept, a)
	}
	s.Agents = kept
	if removed {
		s.Save()
	}
	ov := Overview{
		GeneratedAt: time.Now().Format("15:04:05"),
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
	for _, st := range statuses {
		ov.Counts[statusKey(st)]++
	}
	assigned := map[string]bool{}

	for _, p := range s.Projects {
		proj := OvProject{Name: p.Name, Path: p.Path}
		wts := CollectWorktrees(p.Path)
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
			owt := buildWorktree(s, statuses, activity, contents, assigned, wt, i == 0, proj.MainBranch, gitCache)
			proj.Worktrees = append(proj.Worktrees, owt)
		}
		for _, a := range s.AgentsFor(p.Name) {
			if assigned[a.Name] {
				continue
			}
			assigned[a.Name] = true
			proj.Worktrees[0].Agents = append(proj.Worktrees[0].Agents, toOvAgent(a, statuses, activity, contents, proj.MainBranch, gitCache))
		}
		finishWarnings(&proj, statuses, s)
		ov.Projects = append(ov.Projects, proj)
	}

	var orphanWt OvWorktree
	hasOrphans := false
	for _, a := range s.Agents {
		if assigned[a.Name] {
			continue
		}
		if a.Project != "" && s.ProjectByName(a.Project) != nil {
			continue
		}
		hasOrphans = true
		orphanWt.Agents = append(orphanWt.Agents, toOvAgent(a, statuses, activity, contents, "", gitCache))
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
		}
	}
	return ov
}

func cachedGit(cache map[string]GitInfo, dir string) GitInfo {
	if gi, ok := cache[dir]; ok {
		return gi
	}
	gi := CollectGitInfo(dir)
	cache[dir] = gi
	return gi
}

func buildWorktree(s *State, statuses map[string]AgentStatus, activity map[string]time.Time, contents map[string]string, assigned map[string]bool, wt WorktreeInfo, isMain bool, mainBranch string, gitCache map[string]GitInfo) OvWorktree {
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
		owt.Ahead, owt.Behind = AheadBehind(wt.Path, mainBranch)
	}
	for _, a := range s.Agents {
		if a.Dir == wt.Path && !assigned[a.Name] {
			assigned[a.Name] = true
			owt.Agents = append(owt.Agents, toOvAgent(a, statuses, activity, contents, mainBranch, gitCache))
		}
	}
	return owt
}

func toOvAgent(a Agent, statuses map[string]AgentStatus, activity map[string]time.Time, contents map[string]string, mainBranch string, gitCache map[string]GitInfo) OvAgent {
	st := statuses[a.Name]
	lastActive := a.CreatedAt
	if act, ok := activity[a.Name]; ok {
		lastActive = act
	}
	detail := ""
	switch st {
	case StatusAgents:
		detail = AgentsDetail(BackgroundAgentCount(LastLines(contents[a.Name], 25)))
	case StatusShell:
		detail = ShellDetail(BackgroundShellCount(LastLines(contents[a.Name], 25)))
	case StatusBlocked:
		detail = BlockedDetail(LastLines(contents[a.Name], 25))
	}
	phase, phaseLabel := agentPhase(a, mainBranch, agentAlive(st))
	var sc SessionChanges
	if gi := cachedGit(gitCache, a.Dir); gi.IsRepo {
		sc = CollectSessionChanges(a, gi)
	}
	return OvAgent{
		Name:       a.Name,
		Status:     statusKey(st),
		Label:      st.Label(),
		Detail:     detail,
		Age:        FormatAge(lastActive),
		Worktree:   a.Worktree,
		Phase:      phase,
		PhaseLabel: phaseLabel,
		Known:      sc.Known,
		OwnDirty:   len(sc.Files),
		OwnCommits: sc.Commits,
	}
}

var integrationBranches = map[string]bool{"dev": true, "main": true, "master": true, "develop": true}

func agentPhase(a Agent, mainBranch string, alive bool) (string, string) {
	if !alive {
		return "", ""
	}
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
	branch, err := GitCmd(a.Dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", ""
	}
	branch = strings.TrimSpace(branch)
	if branch == "" || (branch != mainBranch && !integrationBranches[branch]) {
		return "", ""
	}
	cnt, err := GitCmd(a.Dir, "rev-list", "--count", a.BaseCommit+"..HEAD")
	if err != nil {
		return "", ""
	}
	if n := strings.TrimSpace(cnt); n == "" || n == "0" {
		return "", ""
	}
	tsRaw, err := GitCmd(a.Dir, "log", "-1", "--format=%ct")
	if err != nil {
		return "", ""
	}
	ts, _ := strconv.ParseInt(strings.TrimSpace(tsRaw), 10, 64)
	if ts == 0 || time.Since(time.Unix(ts, 0)) > 15*time.Minute {
		return "", ""
	}
	return "committed", branch
}

func finishWarnings(proj *OvProject, statuses map[string]AgentStatus, s *State) {
	for i := range proj.Worktrees {
		wt := &proj.Worktrees[i]
		alive := false
		for _, a := range wt.Agents {
			if a.Status == "running" || a.Status == "agents" || a.Status == "blocked" || a.Status == "idle" {
				alive = true
			}
		}
		if !wt.Clean && !alive {
			wt.Warnings = append(wt.Warnings, "uncommitted Änderungen, keine aktive Session")
		}
		if wt.Ahead > 0 && !alive && wt.Branch != proj.MainBranch {
			word := "Commits"
			if wt.Ahead == 1 {
				word = "Commit"
			}
			wt.Warnings = append(wt.Warnings, fmt.Sprintf("%d %s nicht in %s", wt.Ahead, word, proj.MainBranch))
		}
	}
}
