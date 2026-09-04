package core

import (
	"context"
	"sort"
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
	Truncate bool `json:"truncated"`
	// Availability trägt die Kenntnis über das Repository selbst,
	// HistoryAvailability die über den gelesenen Verlauf. Ein unlesbarer
	// Verlauf macht ein vorhandenes Repository nicht unbekannt.
	Availability        RepositoryKnowledge `json:"availability"`
	HistoryAvailability RepositoryKnowledge `json:"historyAvailability"`
	Problems     []RepositoryProblem `json:"problems,omitempty"`
	Err          string              `json:"err,omitempty"`
}

func BuildGitGraph(s *State, projectID ProjectID, limit int) GitGraph {
	return buildGitGraphUsing(s, projectID, limit, NewRepositories())
}

func buildGitGraphUsing(s *State, projectID ProjectID, limit int, repositories *Repositories) GitGraph {
	g := GitGraph{ProjectID: projectID, Availability: RepositoryUnknown, HistoryAvailability: RepositoryUnknown}
	if s == nil || projectID == "" {
		g.Err = "Projekt nicht gefunden"
		return g
	}
	registered := s.ProjectByID(projectID)
	if registered == nil {
		g.Err = "Projekt nicht gefunden"
		return g
	}
	project := *registered
	g.Project = project.Name
	if limit <= 0 || limit > 400 {
		limit = 120
	}
	ctx := context.Background()
	survey, surveyErr := repositories.Survey(ctx, []Project{project})
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

	// Die Branch-Zuordnung kommt aus Repositories; der Graph leitet sie nicht
	// noch einmal aus den Worktrees ab.
	assignments := repositoryBranchAssignments(repository, nil)
	worktreeByBranch := assignments.ByBranch
	wtByBranch := map[string]string{}
	for branch, worktree := range worktreeByBranch {
		wtByBranch[branch] = worktree.Path
	}
	agentsByDir := map[string][]string{}
	for _, a := range s.Agents {
		if a.ProjectID != project.ID {
			continue
		}
		if !a.LaterAt.IsZero() {
			continue
		}
		agentsByDir[a.Dir] = append(agentsByDir[a.Dir], a.Name)
	}

	facts := repositories.GraphFacts(ctx, repository, limit)
	g.HistoryAvailability = facts.Commits.State
	if !facts.Commits.Known() {
		// Die gelesene Presence bleibt stehen: dass der Verlauf fehlt, sagt
		// nichts darüber, ob das Verzeichnis ein Repository ist.
		g.Err = "Git-Verlauf konnte nicht gelesen werden"
		if facts.Commits.Problem != nil {
			g.Problems = append(g.Problems, *facts.Commits.Problem)
		}
		return g
	}
	commits := graphCommits(facts.Commits.Value, wtByBranch, agentsByDir)
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
	g.Truncate = facts.Truncated
	assignLanes(commits)
	g.Commits = commits
	for _, c := range commits {
		if c.Lane+1 > g.Lanes {
			g.Lanes = c.Lane + 1
		}
	}
	g.Branches = collectGraphBranches(facts, commits, wtByBranch, agentsByDir)
	g.Problems = append(g.Problems, facts.Problems...)
	for i := range g.Branches {
		if worktree, known := worktreeByBranch[g.Branches[i].Name]; known {
			g.Branches[i].WorktreeRef = worktree.Reference
			g.Branches[i].WorktreeLocation = worktree.Location
		}
	}
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

// parseRefs übernimmt die von Repositories zerlegte Dekoration; der Graph
// gibt ihr nur seine eigene Darstellung.
func parseRefs(raw string) []GraphRef {
	var refs []GraphRef
	for _, ref := range parseRepositoryDecorations(raw) {
		refs = append(refs, GraphRef{Name: ref.Name, Kind: ref.Kind, Current: ref.Current})
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

func collectGraphBranches(facts RepositoryGraphFacts, commits []GraphCommit, wtByBranch map[string]string, agentsByDir map[string][]string) []GraphBranch {
	main := facts.Main
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
	merged := facts.Merged
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
			if divergence := facts.Divergence[name]; divergence.Known() {
				b.Ahead = divergence.Value.Ahead
				b.Behind = divergence.Value.Behind
				b.DivergenceKnown = true
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
	return out
}
