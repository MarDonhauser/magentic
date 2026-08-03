package core

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type GraphRef struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Worktree string `json:"worktree,omitempty"`
	Current  bool   `json:"current,omitempty"`
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
	Name     string   `json:"name"`
	Lane     int      `json:"lane"`
	IsMain   bool     `json:"isMain"`
	Worktree string   `json:"worktree,omitempty"`
	Ahead    int      `json:"ahead"`
	Behind   int      `json:"behind"`
	Merged   bool     `json:"merged"`
	Agents   []string `json:"agents,omitempty"`
}

type GitGraph struct {
	Project  string        `json:"project"`
	Main     string        `json:"main"`
	Lanes    int           `json:"lanes"`
	Commits  []GraphCommit `json:"commits"`
	Branches []GraphBranch `json:"branches"`
	Truncate bool          `json:"truncated"`
	Err      string        `json:"err,omitempty"`
}

const graphFmt = "%H\x1f%h\x1f%P\x1f%s\x1f%an\x1f%ct\x1f%D\x1e"

func BuildGitGraph(s *State, projName string, limit int) GitGraph {
	g := GitGraph{Project: projName}
	proj := s.ProjectByName(projName)
	if proj == nil {
		g.Err = "Projekt nicht gefunden"
		return g
	}
	if limit <= 0 || limit > 400 {
		limit = 120
	}
	main := proj.MainBranch
	wts := CollectWorktreesCached(proj.Path)
	if main == "" && len(wts) > 0 {
		main = wts[0].Branch
	}
	g.Main = main

	wtByBranch := map[string]string{}
	for _, wt := range wts {
		if wt.Branch != "" {
			wtByBranch[wt.Branch] = wt.Path
		}
	}
	agentsByDir := map[string][]string{}
	for _, a := range s.AgentsFor(projName) {
		if !a.LaterAt.IsZero() {
			continue
		}
		agentsByDir[a.Dir] = append(agentsByDir[a.Dir], a.Name)
	}

	out, err := GitCmdCached(proj.Path, "log", "--all", "--date-order",
		"--max-count="+strconv.Itoa(limit+1), "--format="+graphFmt)
	if err != nil {
		g.Err = "git log fehlgeschlagen"
		return g
	}
	commits := parseGraphCommits(out, wtByBranch, agentsByDir)
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
	g.Branches = collectGraphBranches(proj.Path, main, commits, wtByBranch, agentsByDir)
	return g
}

func parseGraphCommits(out string, wtByBranch map[string]string, agentsByDir map[string][]string) []GraphCommit {
	var commits []GraphCommit
	for _, rec := range strings.Split(out, "\x1e") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}
		f := strings.Split(rec, "\x1f")
		if len(f) < 7 {
			continue
		}
		ts, _ := strconv.ParseInt(f[5], 10, 64)
		c := GraphCommit{
			Hash:    f[0],
			Short:   f[1],
			Subject: f[3],
			Author:  f[4],
			Time:    ts,
			Age:     FormatAge(time.Unix(ts, 0)),
		}
		if p := strings.Fields(f[2]); len(p) > 0 {
			c.Parents = p
			c.Merge = len(p) > 1
		}
		seen := map[string]bool{}
		for _, r := range parseRefs(f[6]) {
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

func aheadBehindRefs(dir, base, ref string) (ahead, behind int) {
	out, err := GitCmdCached(dir, "rev-list", "--left-right", "--count", base+"..."+ref)
	if err != nil {
		return 0, 0
	}
	fmt.Sscanf(strings.TrimSpace(out), "%d\t%d", &behind, &ahead)
	return ahead, behind
}

func collectGraphBranches(projPath, main string, commits []GraphCommit, wtByBranch map[string]string, agentsByDir map[string][]string) []GraphBranch {
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
	merged := map[string]bool{}
	if main != "" {
		if out, err := GitCmdCached(projPath, "branch", "--merged", main, "--format=%(refname:short)"); err == nil {
			for _, l := range strings.Split(out, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					merged[l] = true
				}
			}
		}
	}
	var out []GraphBranch
	for name, lane := range laneOf {
		b := GraphBranch{Name: name, Lane: lane, IsMain: name == main, Merged: merged[name]}
		if wt, ok := wtByBranch[name]; ok {
			b.Worktree = wt
			b.Agents = agentsByDir[wt]
		}
		if !b.IsMain && main != "" {
			b.Ahead, b.Behind = aheadBehindRefs(projPath, main, name)
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
