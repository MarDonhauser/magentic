package core

import (
	"context"
	"sort"
	"strings"
	"time"
)

type GraphRef struct {
	Name             string      `json:"name"`
	Kind             string      `json:"kind"`
	Worktree         string      `json:"-"`
	WorktreeRef      WorktreeRef `json:"worktreeRef,omitempty"`
	WorktreeLocation string      `json:"worktreeLocation,omitempty"`
	Current          bool        `json:"current,omitempty"`
}

type GraphCommit struct {
	Hash    string     `json:"hash"`
	Short   string     `json:"short"`
	Parents []string   `json:"parents"`
	Subject string     `json:"subject"`
	Author  string     `json:"author"`
	Age     string     `json:"age"`
	Time    int64      `json:"time"`
	Lane    int        `json:"lane"`
	Merge   bool       `json:"merge"`
	Refs    []GraphRef `json:"refs"`
	Agents  []string   `json:"agents,omitempty"`
}

type GraphBranch struct {
	Name             string      `json:"name"`
	Lane             int         `json:"lane"`
	IsMain           bool        `json:"isMain"`
	Worktree         string      `json:"-"`
	WorktreeRef      WorktreeRef `json:"worktreeRef,omitempty"`
	WorktreeLocation string      `json:"worktreeLocation,omitempty"`
	Ahead            int         `json:"ahead"`
	Behind           int         `json:"behind"`
	DivergenceKnown  bool        `json:"divergenceKnown"`
	Merged           bool        `json:"merged"`
	MergedKnown      bool        `json:"mergedKnown"`
	Agents           []string    `json:"agents,omitempty"`
}

type GitGraph struct {
	ProjectID    ProjectID           `json:"projectId"`
	Project      string              `json:"project"`
	Main         string              `json:"main"`
	Lanes        int                 `json:"lanes"`
	Commits      []GraphCommit       `json:"commits"`
	Branches     []GraphBranch       `json:"branches"`
	Truncate     bool                `json:"truncated"`
	Availability RepositoryKnowledge `json:"availability"`
	Problems     []RepositoryProblem `json:"problems,omitempty"`
	Err          string              `json:"err,omitempty"`
}

func BuildGitGraph(s *State, projName string, limit int) GitGraph {
	return buildGitGraphUsing(s, projName, limit, NewRepositories())
}

func buildGitGraphUsing(s *State, projName string, limit int, repositories *Repositories) GitGraph {
	g := GitGraph{Project: projName, Availability: RepositoryUnknown}
	proj := s.ProjectByName(projName)
	if proj == nil {
		g.Err = "Projekt nicht gefunden"
		return g
	}
	g.ProjectID = proj.ID
	if limit <= 0 || limit > 400 {
		limit = 120
	}
	ctx := context.Background()
	survey, surveyErr := repositories.Survey(ctx, []Project{*proj})
	if surveyErr != nil {
		g.Err = "Repository-Status konnte nicht gelesen werden"
		g.Problems = append(g.Problems, RepositoryProblem{Operation: "survey", Message: surveyErr.Error()})
		return g
	}
	if len(survey.Projects) != 1 {
		g.Err = "Repository-Status ist unvollständig"
		return g
	}
	repository := survey.Projects[0]
	g.Availability = repository.Presence
	if repository.Problem != nil {
		g.Problems = append(g.Problems, *repository.Problem)
	}
	if !repository.Worktrees.Known() {
		g.Err = "Worktree-Status ist nicht verfügbar"
		if repository.Worktrees.Problem != nil {
			g.Problems = append(g.Problems, *repository.Worktrees.Problem)
		}
		return g
	}
	main := ""
	if repository.MainBranch.Known() {
		main = repository.MainBranch.Value
	} else if repository.MainBranch.Problem != nil {
		g.Problems = append(g.Problems, *repository.MainBranch.Problem)
	}
	g.Main = main

	wtByBranch := map[string]string{}
	worktreeByBranch := map[string]RepositoryWorktree{}
	divergenceByBranch := map[string]RepositoryFact[RepositoryDivergence]{}
	for _, wt := range repository.Worktrees.Value {
		if wt.Checkout.Known() && wt.Checkout.Value.Kind == RepositoryBranchCheckout && wt.Checkout.Value.Branch != "" {
			branch := wt.Checkout.Value.Branch
			wtByBranch[branch] = wt.Path
			worktreeByBranch[branch] = wt
			divergenceByBranch[branch] = wt.Divergence
		}
	}
	agentsByDir := map[string][]string{}
	for _, a := range s.AgentsFor(projName) {
		if !a.LaterAt.IsZero() {
			continue
		}
		agentsByDir[a.Dir] = append(agentsByDir[a.Dir], a.Name)
	}

	history := repositories.CommitHistory(ctx, proj.Path, limit+1)
	if !history.Known() {
		g.Availability = RepositoryUnknown
		g.Err = "Git-Verlauf konnte nicht gelesen werden"
		if history.Problem != nil {
			g.Problems = append(g.Problems, *history.Problem)
		}
		return g
	}
	commits := graphCommits(history.Value, wtByBranch, agentsByDir)
	for i := range commits {
		for j := range commits[i].Refs {
			worktree, known := worktreeByBranch[commits[i].Refs[j].Name]
			if !known {
				continue
			}
			commits[i].Refs[j].WorktreeRef = worktree.Reference
			commits[i].Refs[j].WorktreeLocation = worktree.Location
		}
	}
	if len(commits) > limit {
		commits = commits[:limit]
		g.Truncate = true
	}
	assignLanes(commits)
	g.Commits = commits
	for _, c := range commits {
		if c.Lane+1 > g.Lanes {
			g.Lanes = c.Lane + 1
		}
	}
	var branchProblems []RepositoryProblem
	g.Branches, branchProblems = collectGraphBranches(ctx, repositories, proj.Path, main, commits, wtByBranch, agentsByDir, divergenceByBranch)
	g.Problems = append(g.Problems, branchProblems...)
	for i := range g.Branches {
		if worktree, known := worktreeByBranch[g.Branches[i].Name]; known {
			g.Branches[i].WorktreeRef = worktree.Reference
			g.Branches[i].WorktreeLocation = worktree.Location
		}
	}
	g.Availability = RepositoryKnown
	return g
}

func graphCommits(history []RepositoryCommit, wtByBranch map[string]string, agentsByDir map[string][]string) []GraphCommit {
	commits := make([]GraphCommit, 0, len(history))
	for _, fact := range history {
		c := GraphCommit{
			Hash:    fact.Hash,
			Short:   fact.Short,
			Parents: append([]string(nil), fact.Parents...),
			Subject: fact.Subject,
			Author:  fact.Author,
			Time:    fact.Timestamp,
			Age:     FormatAge(time.Unix(fact.Timestamp, 0)),
		}
		c.Merge = len(c.Parents) > 1
		seen := map[string]bool{}
		for _, r := range parseRefs(fact.Decorations) {
			if wt, ok := wtByBranch[r.Name]; ok {
				r.Worktree = wt
				for _, ag := range agentsByDir[wt] {
					if !seen[ag] {
						seen[ag] = true
						c.Agents = append(c.Agents, ag)
					}
				}
			}
			c.Refs = append(c.Refs, r)
		}
		commits = append(commits, c)
	}
	return commits
}

func parseRefs(raw string) []GraphRef {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var refs []GraphRef
	for _, part := range strings.Split(raw, ", ") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		current := false
		if idx := strings.Index(part, " -> "); idx >= 0 {
			current = strings.HasPrefix(part, "HEAD")
			part = part[idx+4:]
		}
		switch {
		case part == "HEAD":
			refs = append(refs, GraphRef{Name: "HEAD", Kind: "head", Current: true})
		case strings.HasPrefix(part, "tag: "):
			refs = append(refs, GraphRef{Name: strings.TrimPrefix(part, "tag: "), Kind: "tag"})
		case strings.HasPrefix(part, "origin/"):
			refs = append(refs, GraphRef{Name: part, Kind: "remote"})
		default:
			refs = append(refs, GraphRef{Name: part, Kind: "branch", Current: current})
		}
	}
	return refs
}

// assignLanes verteilt die Commits auf Spuren: jede Spur wartet auf einen Hash,
// ein Commit übernimmt die erste Spur, die auf ihn zeigt, und gibt sie an seinen
// ersten Parent weiter. Weitere Parents öffnen neue Spuren (Abzweige), Spuren,
// die auf denselben Commit zeigen, laufen zusammen (Merge).
func assignLanes(commits []GraphCommit) {
	var lanes []string
	take := func(hash string) int {
		for i, want := range lanes {
			if want == hash {
				return i
			}
		}
		for i, want := range lanes {
			if want == "" {
				lanes[i] = hash
				return i
			}
		}
		lanes = append(lanes, hash)
		return len(lanes) - 1
	}
	for i := range commits {
		c := &commits[i]
		lane := take(c.Hash)
		for j := lane + 1; j < len(lanes); j++ {
			if lanes[j] == c.Hash {
				lanes[j] = ""
			}
		}
		c.Lane = lane
		if len(c.Parents) == 0 {
			lanes[lane] = ""
			continue
		}
		lanes[lane] = c.Parents[0]
		for _, p := range c.Parents[1:] {
			take(p)
		}
		for len(lanes) > 0 && lanes[len(lanes)-1] == "" {
			lanes = lanes[:len(lanes)-1]
		}
	}
}

func collectGraphBranches(ctx context.Context, repositories *Repositories, projPath, main string, commits []GraphCommit, wtByBranch map[string]string, agentsByDir map[string][]string, divergenceByBranch map[string]RepositoryFact[RepositoryDivergence]) ([]GraphBranch, []RepositoryProblem) {
	laneOf := map[string]int{}
	for _, c := range commits {
		for _, r := range c.Refs {
			if r.Kind == "branch" {
				if _, ok := laneOf[r.Name]; !ok {
					laneOf[r.Name] = c.Lane
				}
			}
		}
	}
	merged := RepositoryFact[map[string]bool]{State: RepositoryUnknown}
	if main != "" {
		merged = repositories.MergedBranches(ctx, projPath, main)
	}
	var problems []RepositoryProblem
	if merged.Problem != nil {
		problems = append(problems, *merged.Problem)
	}
	var out []GraphBranch
	for name, lane := range laneOf {
		b := GraphBranch{Name: name, Lane: lane, IsMain: name == main, MergedKnown: merged.Known()}
		if merged.Known() {
			b.Merged = merged.Value[name]
		}
		if wt, ok := wtByBranch[name]; ok {
			b.Worktree = wt
			b.Agents = agentsByDir[wt]
		}
		if !b.IsMain && main != "" {
			if divergence, exists := divergenceByBranch[name]; exists {
				if divergence.Known() {
					b.Ahead = divergence.Value.Ahead
					b.Behind = divergence.Value.Behind
					b.DivergenceKnown = true
				} else if divergence.Problem != nil {
					problems = append(problems, *divergence.Problem)
				}
			} else {
				divergence := repositories.CompareRefs(ctx, projPath, main, name)
				if divergence.Known() {
					b.Ahead = divergence.Value.Ahead
					b.Behind = divergence.Value.Behind
					b.DivergenceKnown = true
				} else if divergence.Problem != nil {
					problems = append(problems, *divergence.Problem)
				}
			}
		} else if b.IsMain {
			b.DivergenceKnown = true
			b.MergedKnown = true
		}
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsMain != out[j].IsMain {
			return out[i].IsMain
		}
		if out[i].Lane != out[j].Lane {
			return out[i].Lane < out[j].Lane
		}
		return out[i].Name < out[j].Name
	})
	return out, problems
}
