package main

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"magentic/core"
)

func resolveWorktreeTarget(ctx context.Context, projectID, reference string) (*core.State, core.RepositoryWorktreeTarget, error) {
	st, err := core.LoadState()
	if err != nil {
		return nil, core.RepositoryWorktreeTarget{}, err
	}
	id := core.ProjectID(strings.TrimSpace(projectID))
	project := st.ProjectByID(id)
	if project == nil {
		return nil, core.RepositoryWorktreeTarget{}, fmt.Errorf("unbekannte ProjectID: %s", id)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	target, err := core.NewRepositories().ResolveWorktree(ctx, *project, core.WorktreeRef(reference))
	if err != nil {
		return nil, core.RepositoryWorktreeTarget{}, fmt.Errorf("Worktree konnte nicht frisch aufgelöst werden: %w", err)
	}
	return st, target, nil
}

func (a *App) WorktreeDiff(projectID, reference string) (string, error) {
	_, target, err := resolveWorktreeTarget(a.ctx, projectID, reference)
	if err != nil {
		return "", err
	}
	fact := core.NewRepositories().WorktreeDiff(a.ctx, target.Worktree)
	if !fact.Known() {
		message := "Worktree-Diff ist derzeit nicht verfügbar"
		if fact.Problem != nil && strings.TrimSpace(fact.Problem.Message) != "" {
			message = fact.Problem.Message
		}
		return "", fmt.Errorf("%s", message)
	}
	out := fact.Value
	const cap = 400_000
	if len(out) > cap {
		out = out[:cap] + "\n… (gekürzt)"
	}
	return out, nil
}

const sessionRuntimeSource = "runtime"

type SessionPreviewResult struct {
	Content      string         `json:"content"`
	ContentKnown bool           `json:"contentKnown"`
	Source       TimelineSource `json:"source"`
}

func (a *App) SessionPreview(sessionID string) SessionPreviewResult {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return SessionPreviewResult{Source: TimelineSource{
			Source: sessionRuntimeSource, State: string(core.HistorySourceUnavailable),
			Problems: []string{err.Error()},
		}}
	}
	observed, source := sessionRuntimeObservation(a.observationFor([]core.Session{session}, true), session.ID)
	result := SessionPreviewResult{
		ContentKnown: observed.Presence == core.SessionPresencePresent && observed.ContentKnown,
		Source:       source,
	}
	if result.ContentKnown {
		result.Content = core.LastLines(strings.TrimRight(observed.Content, "\n"), 16)
	}
	return result
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

type SessionLinksResult struct {
	Links   []LinkInfo       `json:"links"`
	Sources []TimelineSource `json:"sources"`
}

func (a *App) SessionLinks(sessionID string) (SessionLinksResult, error) {
	st, session, err := loadSessionByID(sessionID)
	if err != nil {
		return SessionLinksResult{}, err
	}
	seen := map[string]bool{}
	var out []LinkInfo
	var sources []TimelineSource
	add := func(l LinkInfo) {
		if seen[l.URL] {
			return
		}
		seen[l.URL] = true
		out = append(out, l)
	}
	history, historyErr := core.OpenWorkHistory(core.WorkHistoryConfig{})
	if historyErr != nil {
		sources = append(sources, unavailableTimelineSource("work-history", historyErr))
	} else {
		page, err := history.Links(context.Background(), core.NewHistoryAssociations(*st), core.HistoryLinkQuery{
			Events: core.HistoryEventQuery{
				SessionKeys: []string{string(session.ID)},
				Kinds:       []core.HistoryEventKind{core.HistoryEventOutput},
			},
			Distinct: true,
			Limit:    40,
		})
		if err != nil {
			sources = append(sources, unavailableTimelineSource("work-history", err))
		} else {
			sources = append(sources, historyCoverageSources(page.Meta.Coverage)...)
			for _, link := range page.Links {
				when := ""
				if link.OccurredAt.State == core.HistoryFactKnown {
					when = link.OccurredAt.Value.In(time.Local).Format("02.01. 15:04")
				}
				add(LinkInfo{URL: link.URL, Time: when})
			}
		}
	}
	observed, runtimeSource := sessionRuntimeObservation(a.observationFor([]core.Session{session}, true), session.ID)
	sources = append(sources, runtimeSource)
	if observed.Presence == core.SessionPresencePresent && observed.ContentKnown {
		for _, u := range extractURLs(observed.Content) {
			add(LinkInfo{URL: u})
		}
	}
	if len(out) > 40 {
		out = out[:40]
	}
	return SessionLinksResult{Links: out, Sources: sources}, nil
}

type SearchHit struct {
	Project            string `json:"project"`
	ProjectKnown       bool   `json:"projectKnown"`
	AttributionProblem string `json:"attributionProblem,omitempty"`
	Provider           string `json:"provider"`
	Role               string `json:"role"`
	Time               string `json:"time"`
	TimeRaw            string `json:"timeRaw"`
	Snippet            string `json:"snippet"`
	Full               string `json:"full"`
}

// SearchResult keeps known hits and source coverage from one WorkHistory page
// together, so an empty known subtotal is never mistaken for an exact zero.
type SearchResult struct {
	Hits    []SearchHit      `json:"hits"`
	Sources []TimelineSource `json:"sources"`
}

func (a *App) SearchTranscripts(query string) (SearchResult, error) {
	query = strings.TrimSpace(query)
	if len(query) < 3 {
		return SearchResult{}, fmt.Errorf("mindestens 3 Zeichen")
	}
	st, err := core.LoadState()
	if err != nil {
		return SearchResult{}, err
	}
	history, err := core.OpenWorkHistory(core.WorkHistoryConfig{})
	if err != nil {
		return SearchResult{}, err
	}
	page, err := history.Events(context.Background(), core.NewHistoryAssociations(*st), core.HistoryEventQuery{
		Kinds: []core.HistoryEventKind{core.HistoryEventPrompt, core.HistoryEventOutput},
		Text:  query,
		Limit: 80,
	})
	if err != nil {
		return SearchResult{}, err
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
		project := "Projekt unbekannt"
		projectKnown := false
		attributionProblem := event.Attribution.ProjectName.Reason
		if event.Attribution.ProjectName.State == core.HistoryFactKnown && event.Attribution.ProjectName.Value != "" {
			project = event.Attribution.ProjectName.Value
			projectKnown = true
			attributionProblem = ""
		} else if event.Attribution.ProjectName.State == core.HistoryFactKnown {
			project = "ohne Projekt"
			projectKnown = true
			attributionProblem = ""
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
			Project: project, ProjectKnown: projectKnown, AttributionProblem: attributionProblem,
			Provider: event.Provider.Label(), Role: role, Time: displayTime, TimeRaw: timeRaw,
			Snippet: snippetAround(text, idx, len(qLower)), Full: capStr(text, 6000),
		})
	}
	return SearchResult{Hits: hits, Sources: historyCoverageSources(page.Meta.Coverage)}, nil
}

type TimelineEntry struct {
	Agent              string `json:"agent"`
	Project            string `json:"project"`
	ProjectKnown       bool   `json:"projectKnown"`
	AttributionProblem string `json:"attributionProblem,omitempty"`
	Source             string `json:"source"`
	Day                string `json:"day"`
	Time               string `json:"time"`
	TimeRaw            string `json:"timeRaw"`
	Text               string `json:"text"`
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

func unavailableTimelineSource(source string, err error) TimelineSource {
	problem := "Quelle ist derzeit nicht lesbar"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		problem = err.Error()
	}
	return TimelineSource{
		Source: source, State: string(core.HistorySourceUnavailable), Problems: []string{problem},
	}
}

func sessionRuntimeObservation(snapshot core.ObservationSnapshot, sessionID core.SessionID) (core.SessionObservation, TimelineSource) {
	source := TimelineSource{Source: sessionRuntimeSource, State: string(core.HistorySourceUnavailable)}
	for _, problem := range snapshot.Problems {
		if problem.SessionID != "" && problem.SessionID != sessionID {
			continue
		}
		message := strings.TrimSpace(problem.Message)
		if operation := strings.TrimSpace(problem.Operation); operation != "" {
			if message != "" {
				message = operation + ": " + message
			} else {
				message = operation
			}
		}
		if message != "" {
			source.Problems = append(source.Problems, message)
		}
	}
	for _, observed := range snapshot.Sessions {
		if observed.SessionID != sessionID {
			continue
		}
		switch {
		case observed.Availability == core.ObservationUnavailable:
			source.State = string(core.HistorySourceUnavailable)
		case observed.Availability == core.ObservationPartial:
			source.State = string(core.HistorySourcePartial)
		case observed.Presence == core.SessionPresenceAbsent:
			source.State = string(core.HistorySourceAbsent)
		case observed.Presence == core.SessionPresencePresent && observed.ContentKnown:
			source.State = string(core.HistorySourceAvailable)
		default:
			source.State = string(core.HistorySourcePartial)
		}
		if (source.State == string(core.HistorySourcePartial) || source.State == string(core.HistorySourceUnavailable)) && len(source.Problems) == 0 {
			source.Problems = append(source.Problems, "Laufzeitinhalt ist nicht vollständig bekannt")
		}
		return observed, source
	}
	if len(source.Problems) == 0 {
		source.Problems = append(source.Problems, "Session fehlt in der Laufzeitbeobachtung")
	}
	return core.SessionObservation{SessionID: sessionID}, source
}

func historyCoverageSources(coverage []core.HistoryProviderCoverage) []TimelineSource {
	sources := make([]TimelineSource, 0, len(coverage))
	for _, coverage := range coverage {
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
		sources = append(sources, source)
	}
	return sources
}

var tlWeekdays = map[time.Weekday]string{
	time.Monday: "Mo", time.Tuesday: "Di", time.Wednesday: "Mi",
	time.Thursday: "Do", time.Friday: "Fr", time.Saturday: "Sa", time.Sunday: "So",
}

func (a *App) Timeline() (TimelineResult, error) {
	st, err := core.LoadState()
	if err != nil {
		return TimelineResult{}, err
	}
	history, err := core.OpenWorkHistory(core.WorkHistoryConfig{})
	if err != nil {
		return TimelineResult{}, err
	}
	page, err := history.Events(context.Background(), core.NewHistoryAssociations(*st), core.HistoryEventQuery{
		Since:    time.Now().AddDate(0, 0, -7),
		Kinds:    []core.HistoryEventKind{core.HistoryEventPrompt},
		Lineages: []core.HistoryLineage{core.HistoryLineagePrimary},
		Limit:    150,
	})
	if err != nil {
		return TimelineResult{}, err
	}
	return TimelineResult{Entries: timelineEntries(page), Sources: historyCoverageSources(page.Meta.Coverage)}, nil
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
