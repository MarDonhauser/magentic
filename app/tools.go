package main

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"magentic/core"
)

func knownWorktreePath(path string) bool {
	st, err := core.LoadState()
	if err != nil {
		return false
	}
	survey, err := core.NewRepositories().Survey(context.Background(), st.Projects)
	if err != nil {
		return false
	}
	target := filepath.Clean(path)
	for _, project := range survey.Projects {
		if project.Presence != core.RepositoryKnown || !project.Worktrees.Known() {
			continue
		}
		for _, worktree := range project.Worktrees.Value {
			if filepath.Clean(worktree.Path) == target {
				return true
			}
		}
	}
	return false
}

func (a *App) WorktreeDiff(path string) (string, error) {
	if !knownWorktreePath(path) {
		return "", fmt.Errorf("Pfad gehört zu keinem Projekt")
	}
	var b strings.Builder
	if status, err := core.GitCmd(path, "status", "--short"); err == nil && strings.TrimSpace(status) != "" {
		b.WriteString("── Status ──\n")
		b.WriteString(status)
		b.WriteString("\n")
	}
	if diff, err := core.GitCmd(path, "diff", "HEAD"); err == nil && strings.TrimSpace(diff) != "" {
		b.WriteString("── Diff (gegen HEAD) ──\n")
		b.WriteString(diff)
	}
	if untracked, err := core.GitCmd(path, "ls-files", "--others", "--exclude-standard"); err == nil {
		files := strings.Fields(strings.TrimSpace(untracked))
		if len(files) > 0 {
			b.WriteString("\n── Neue Dateien (untracked) ──\n")
			for _, f := range files {
				b.WriteString("+ " + f + "\n")
			}
		}
	}
	out := b.String()
	if out == "" {
		out = "Keine Änderungen."
	}
	const cap = 400_000
	if len(out) > cap {
		out = out[:cap] + "\n… (gekürzt)"
	}
	return out, nil
}

func (a *App) SessionPreview(name string) string {
	sn := core.SessionName(name)
	if !core.TmuxHasSession(sn) {
		return ""
	}
	return core.LastLines(strings.TrimRight(core.TmuxCapturePane(sn, 0), "\n"), 16)
}

type LinkInfo struct {
	URL  string `json:"url"`
	Time string `json:"time"`
}

var urlRe = regexp.MustCompile(`https?://[^\s<>"'\x60)\]}]+`)

func extractURLs(text string) []string {
	var out []string
	for _, m := range urlRe.FindAllString(text, -1) {
		m = strings.TrimRight(m, ".,;:!?*…")
		if u := strings.TrimPrefix(strings.TrimPrefix(m, "https://"), "http://"); len(u) < 3 {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (a *App) SessionLinks(name string) ([]LinkInfo, error) {
	st, err := core.LoadState()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []LinkInfo
	add := func(l LinkInfo) {
		if seen[l.URL] {
			return
		}
		seen[l.URL] = true
		out = append(out, l)
	}
	if session := st.AgentByName(name); session != nil {
		history, err := core.OpenWorkHistory(core.WorkHistoryConfig{})
		if err != nil {
			return nil, err
		}
		sessionKey := session.Name
		if session.ID != "" {
			sessionKey = string(session.ID)
		}
		page, err := history.Links(context.Background(), core.HistoryAssociationsFromState(st), core.HistoryLinkQuery{
			Events: core.HistoryEventQuery{
				SessionKeys: []string{sessionKey},
				Kinds:       []core.HistoryEventKind{core.HistoryEventOutput},
			},
			Distinct: true,
			Limit:    40,
		})
		if err != nil {
			return nil, err
		}
		for _, link := range page.Links {
			when := ""
			if link.OccurredAt.State == core.HistoryFactKnown {
				when = link.OccurredAt.Value.In(time.Local).Format("02.01. 15:04")
			}
			add(LinkInfo{URL: link.URL, Time: when})
		}
	}
	sn := core.SessionName(name)
	if core.TmuxHasSession(sn) {
		for _, u := range extractURLs(core.TmuxCapturePaneJoined(sn, 3000)) {
			add(LinkInfo{URL: u})
		}
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return out, nil
}

type SearchHit struct {
	Project string `json:"project"`
	Role    string `json:"role"`
	Time    string `json:"time"`
	TimeRaw string `json:"timeRaw"`
	Snippet string `json:"snippet"`
	Full    string `json:"full"`
}

func (a *App) SearchTranscripts(query string) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if len(query) < 3 {
		return nil, fmt.Errorf("mindestens 3 Zeichen")
	}
	st, err := core.LoadState()
	if err != nil {
		return nil, err
	}
	history, err := core.OpenWorkHistory(core.WorkHistoryConfig{})
	if err != nil {
		return nil, err
	}
	page, err := history.Events(context.Background(), core.HistoryAssociationsFromState(st), core.HistoryEventQuery{
		Providers: []core.HistoryProvider{core.HistoryProviderClaude},
		Kinds:     []core.HistoryEventKind{core.HistoryEventPrompt, core.HistoryEventOutput},
		Text:      query,
		Limit:     80,
	})
	if err != nil {
		return nil, err
	}
	qLower := strings.ToLower(query)
	hits := make([]SearchHit, 0, len(page.Events))
	for _, event := range page.Events {
		if event.Text.State != core.HistoryFactKnown {
			continue
		}
		text := event.Text.Value
		idx := strings.Index(strings.ToLower(text), qLower)
		if idx < 0 {
			continue
		}
		project := "ohne Projekt"
		if event.Attribution.ProjectName.State == core.HistoryFactKnown && event.Attribution.ProjectName.Value != "" {
			project = event.Attribution.ProjectName.Value
		}
		role := "assistant"
		if event.Role == core.HistoryRoleDeveloper {
			role = "user"
		}
		timeRaw, displayTime := "", ""
		if event.OccurredAt.State == core.HistoryFactKnown {
			timeRaw = event.OccurredAt.Value.UTC().Format(time.RFC3339Nano)
			displayTime = event.OccurredAt.Value.In(time.Local).Format("02.01. 15:04")
		}
		hits = append(hits, SearchHit{
			Project: project,
			Role:    role,
			Time:    displayTime,
			TimeRaw: timeRaw,
			Snippet: snippetAround(text, idx, len(qLower)),
			Full:    capStr(text, 6000),
		})
	}
	return hits, nil
}

type TimelineEntry struct {
	Agent   string `json:"agent"`
	Project string `json:"project"`
	Source  string `json:"source"`
	Day     string `json:"day"`
	Time    string `json:"time"`
	TimeRaw string `json:"timeRaw"`
	Text    string `json:"text"`
}

type TimelineSource struct {
	Source   string   `json:"source"`
	State    string   `json:"state"`
	Problems []string `json:"problems,omitempty"`
}

type TimelineResult struct {
	Entries []TimelineEntry  `json:"entries"`
	Sources []TimelineSource `json:"sources"`
}

var tlWeekdays = map[time.Weekday]string{
	time.Monday: "Mo", time.Tuesday: "Di", time.Wednesday: "Mi",
	time.Thursday: "Do", time.Friday: "Fr", time.Saturday: "Sa", time.Sunday: "So",
}

func (a *App) Timeline() (TimelineResult, error) {
	st, _ := core.LoadState()
	history, err := core.OpenWorkHistory(core.WorkHistoryConfig{})
	if err != nil {
		return TimelineResult{}, err
	}
	page, err := history.Events(context.Background(), core.HistoryAssociationsFromState(st), core.HistoryEventQuery{
		Since:    time.Now().AddDate(0, 0, -7),
		Kinds:    []core.HistoryEventKind{core.HistoryEventPrompt},
		Lineages: []core.HistoryLineage{core.HistoryLineagePrimary},
		Limit:    150,
	})
	if err != nil {
		return TimelineResult{}, err
	}
	result := TimelineResult{Entries: timelineEntries(page)}
	for _, coverage := range page.Meta.Coverage {
		source := TimelineSource{Source: string(coverage.Provider), State: string(coverage.State)}
		for _, problem := range coverage.Problems {
			message := strings.TrimSpace(problem.Message)
			if message == "" {
				message = strings.TrimSpace(problem.Kind)
			}
			if message != "" {
				source.Problems = append(source.Problems, message)
			}
		}
		result.Sources = append(result.Sources, source)
	}
	return result, nil
}

func snippetAround(text string, idx, qlen int) string {
	start := idx - 110
	if start < 0 {
		start = 0
	}
	end := idx + qlen + 110
	if end > len(text) {
		end = len(text)
	}
	for start > 0 && text[start]&0xC0 == 0x80 {
		start--
	}
	for end < len(text) && text[end]&0xC0 == 0x80 {
		end++
	}
	s := strings.ReplaceAll(text[start:end], "\n", " ")
	if start > 0 {
		s = "…" + s
	}
	if end < len(text) {
		s += "…"
	}
	return s
}

func formatTranscriptTime(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Local().Format("02.01. 15:04")
	}
	return ts
}

func capStr(s string, n int) string {
	if len(s) > n {
		return s[:n] + "\n… (gekürzt)"
	}
	return s
}
