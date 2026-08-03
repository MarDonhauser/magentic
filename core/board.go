package core

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type BoardTask struct {
	Text    string `json:"text"`
	Done    bool   `json:"done"`
	Section string `json:"section,omitempty"`
}

type BoardItem struct {
	ID       string      `json:"id"`
	Title    string      `json:"title"`
	Summary  string      `json:"summary,omitempty"`
	Path     string      `json:"path"`
	Kind     string      `json:"kind"`
	Column   string      `json:"column"`
	Total    int         `json:"total"`
	Done     int         `json:"done"`
	Specs    int         `json:"specs"`
	HasPlan  bool        `json:"hasPlan"`
	Updated  string      `json:"updated,omitempty"`
	Tasks    []BoardTask `json:"tasks,omitempty"`
	Agents   []string    `json:"agents,omitempty"`
	Branches []string    `json:"branches,omitempty"`
}

type Board struct {
	Project  string      `json:"project"`
	Kind     string      `json:"kind"`
	Root     string      `json:"root,omitempty"`
	Items    []BoardItem `json:"items"`
	Archived int         `json:"archived"`
	Specs    int         `json:"specs"`
	Err      string      `json:"err,omitempty"`
}

const (
	ColBacklog = "backlog"
	ColActive  = "active"
	ColReview  = "review"
	ColDone    = "done"
)

var taskLineRe = regexp.MustCompile(`^\s*[-*]\s+\[([ xX~/-])\]\s+(.*)$`)
var sectionRe = regexp.MustCompile(`^#{2,3}\s+(.*)$`)

func BuildBoard(s *State, projName string) Board {
	b := Board{Project: projName}
	proj := s.ProjectByName(projName)
	if proj == nil {
		b.Err = "Projekt nicht gefunden"
		return b
	}
	agents := liveAgentContext(s, projName)

	if root := filepath.Join(proj.Path, "openspec", "changes"); isDir(root) {
		b.Kind = "openspec"
		b.Root = root
		b.Items = collectBoardItems(root, "openspec", agents)
		b.Archived = countDirs(filepath.Join(root, "archive"))
		b.Specs = countDirs(filepath.Join(proj.Path, "openspec", "specs"))
		return b
	}
	if root := filepath.Join(proj.Path, "specs"); isDir(root) {
		b.Kind = "speckit"
		b.Root = root
		b.Items = collectBoardItems(root, "speckit", agents)
		return b
	}
	b.Kind = "none"
	return b
}

type agentCtx struct {
	name   string
	branch string
	dir    string
}

func liveAgentContext(s *State, projName string) []agentCtx {
	var out []agentCtx
	for _, a := range s.AgentsFor(projName) {
		if !a.LaterAt.IsZero() {
			continue
		}
		branch := ""
		if o, err := GitCmdCached(a.Dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
			branch = strings.TrimSpace(o)
		}
		out = append(out, agentCtx{name: a.Name, branch: branch, dir: a.Dir})
	}
	return out
}

func collectBoardItems(root, kind string, agents []agentCtx) []BoardItem {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var items []BoardItem
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "archive" || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		items = append(items, readBoardItem(filepath.Join(root, e.Name()), e.Name(), kind, agents))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Updated != items[j].Updated {
			return items[i].Updated > items[j].Updated
		}
		return items[i].ID < items[j].ID
	})
	return items
}

func readBoardItem(dir, id, kind string, agents []agentCtx) BoardItem {
	it := BoardItem{ID: id, Title: humanizeID(id), Path: dir, Kind: kind}
	docs := []string{"proposal.md", "spec.md", "design.md", "plan.md", "README.md"}
	for _, d := range docs {
		p := filepath.Join(dir, d)
		if !fileExists(p) {
			continue
		}
		if d == "plan.md" || d == "design.md" {
			it.HasPlan = true
		}
		if it.Summary == "" {
			title, summary := readDocHead(p)
			if title != "" {
				it.Title = title
			}
			it.Summary = summary
		}
	}
	it.Tasks = parseTasks(filepath.Join(dir, "tasks.md"))
	for _, t := range it.Tasks {
		it.Total++
		if t.Done {
			it.Done++
		}
	}
	it.Specs = countFilesRec(filepath.Join(dir, "specs"), ".md")
	if info, err := newestMTime(dir); err == nil {
		it.Updated = info.Format(time.RFC3339)
	}
	for _, a := range agents {
		if matchesItem(a, id, dir) {
			it.Agents = append(it.Agents, a.name)
			if a.branch != "" {
				it.Branches = appendUnique(it.Branches, a.branch)
			}
		}
	}
	it.Column = boardColumn(it)
	return it
}

func boardColumn(it BoardItem) string {
	if len(it.Agents) > 0 {
		return ColActive
	}
	switch {
	case it.Total == 0:
		return ColBacklog
	case it.Done == 0:
		return ColBacklog
	case it.Done >= it.Total:
		return ColReview
	default:
		return ColActive
	}
}

func matchesItem(a agentCtx, id, dir string) bool {
	slug := normalizeSlug(id)
	if slug == "" {
		return false
	}
	if normalizeSlug(a.branch) == slug || strings.Contains(normalizeSlug(a.branch), slug) {
		return true
	}
	if normalizeSlug(filepath.Base(a.dir)) == slug {
		return true
	}
	return normalizeSlug(a.name) == slug
}

func normalizeSlug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func parseTasks(path string) []BoardTask {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []BoardTask
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		if m := sectionRe.FindStringSubmatch(line); m != nil {
			section = strings.TrimSpace(m[1])
			continue
		}
		m := taskLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		text := strings.TrimSpace(m[2])
		if len(text) > 160 {
			text = text[:159] + "…"
		}
		out = append(out, BoardTask{
			Text:    text,
			Done:    m[1] == "x" || m[1] == "X",
			Section: section,
		})
	}
	return out
}

func readDocHead(path string) (title, summary string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	lines := strings.Split(string(data), "\n")
	var body []string
	inWhy := false
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if title == "" && strings.HasPrefix(t, "# ") {
			title = strings.TrimSpace(strings.TrimPrefix(t, "# "))
			continue
		}
		if strings.HasPrefix(t, "## ") {
			lower := strings.ToLower(t)
			inWhy = strings.Contains(lower, "why") || strings.Contains(lower, "warum") ||
				strings.Contains(lower, "summary") || strings.Contains(lower, "overview")
			continue
		}
		if !inWhy && len(body) > 0 {
			break
		}
		if t == "" || strings.HasPrefix(t, "<!--") {
			continue
		}
		body = append(body, t)
		if len(body) >= 4 {
			break
		}
	}
	summary = strings.Join(body, " ")
	if len(summary) > 320 {
		summary = summary[:319] + "…"
	}
	return title, summary
}

func humanizeID(id string) string {
	s := strings.NewReplacer("-", " ", "_", " ").Replace(id)
	parts := strings.Fields(s)
	for i, p := range parts {
		if i == 0 && len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

func appendUnique(list []string, v string) []string {
	for _, x := range list {
		if x == v {
			return list
		}
	}
	return append(list, v)
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

func countDirs(p string) int {
	entries, err := os.ReadDir(p)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			n++
		}
	}
	return n
}

func countFilesRec(root, ext string) int {
	n := 0
	filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(d.Name(), ext) {
			n++
		}
		return nil
	})
	return n
}

func newestMTime(dir string) (time.Time, error) {
	var newest time.Time
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, e := d.Info(); e == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	if newest.IsZero() {
		return newest, os.ErrNotExist
	}
	return newest, err
}
