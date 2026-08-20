package core

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type StatsDay struct {
	Date       string  `json:"date"`
	Weekday    string  `json:"weekday"`
	Prompts    int     `json:"prompts"`
	Turns      int     `json:"turns"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cacheRead"`
	CacheWrite int64   `json:"cacheWrite"`
	Cost       float64 `json:"cost"`
	Sessions   int     `json:"sessions"`
	Commits    int     `json:"commits"`
}

type StatsProject struct {
	Name     string  `json:"name"`
	Tokens   int64   `json:"tokens"`
	Cost     float64 `json:"cost"`
	Prompts  int     `json:"prompts"`
	Sessions int     `json:"sessions"`
	Commits  int     `json:"commits"`
	Active   int     `json:"active"`
}

type StatsModel struct {
	Model      string  `json:"model"`
	Source     string  `json:"source,omitempty"`
	Turns      int     `json:"turns"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cacheRead"`
	CacheWrite int64   `json:"cacheWrite"`
	Cost       float64 `json:"cost"`
}

// StatsProvider exposes source coverage alongside the known activity subtotal.
// A partial source is therefore distinguishable from a provider with no work.
type StatsProvider struct {
	Provider string              `json:"provider"`
	Source   string              `json:"source"`
	State    HistorySourceState  `json:"state"`
	Prompts  int                 `json:"prompts"`
	Turns    int                 `json:"turns"`
	Tokens   int64               `json:"tokens"`
	Usage    HistoryUsageSummary `json:"usage"`
	Problems []HistoryProblem    `json:"problems,omitempty"`
}

type StatsTotals struct {
	Days       int     `json:"days"`
	Prompts    int     `json:"prompts"`
	Turns      int     `json:"turns"`
	Sessions   int     `json:"sessions"`
	Tokens     int64   `json:"tokens"`
	Input      int64   `json:"input"`
	Output     int64   `json:"output"`
	CacheRead  int64   `json:"cacheRead"`
	CacheWrite int64   `json:"cacheWrite"`
	Cost       float64 `json:"cost"`
	Commits    int     `json:"commits"`
	CacheHit   float64 `json:"cacheHit"`
	BusiestDay string  `json:"busiestDay"`
	Streak     int     `json:"streak"`
}

type Stats struct {
	Range     int             `json:"range"`
	Days      []StatsDay      `json:"days"`
	Projects  []StatsProject  `json:"projects"`
	Models    []StatsModel    `json:"models"`
	Providers []StatsProvider `json:"providers"`
	Heatmap   [][]int         `json:"heatmap"`
	Hours     []int           `json:"hours"`
	Totals    StatsTotals     `json:"totals"`
	Err       string          `json:"err,omitempty"`
}

const (
	statsDateLayout   = "2006-01-02"
	statsOtherProject = "sonstige"
)

var modelPrices = []struct {
	prefix  string
	in, out float64
}{
	{"claude-sonnet-4-6", 3.00, 15.00},
	{"claude-sonnet-4-5", 3.00, 15.00},
	{"claude-haiku-4-5", 1.00, 5.00},
	{"claude-opus-4-8", 5.00, 25.00},
	{"claude-opus-4-7", 5.00, 25.00},
	{"claude-opus-4-6", 5.00, 25.00},
	{"claude-opus-4-5", 5.00, 25.00},
	{"claude-mythos-5", 10.00, 50.00},
	{"claude-sonnet-5", 3.00, 15.00},
	{"claude-fable-5", 10.00, 50.00},
	{"claude-opus-5", 5.00, 25.00},
}

const (
	defaultPriceIn  = 5.00
	defaultPriceOut = 25.00
)

func modelPrice(model string) (float64, float64) {
	for _, price := range modelPrices {
		if strings.HasPrefix(model, price.prefix) {
			return price.in, price.out
		}
	}
	return defaultPriceIn, defaultPriceOut
}

func modelCost(model string, input, output, cacheRead, cacheWrite int64) float64 {
	in, out := modelPrice(model)
	const million = 1_000_000.0
	return float64(input)*in/million +
		float64(output)*out/million +
		float64(cacheWrite)*in*1.25/million +
		float64(cacheRead)*in*0.1/million
}

type statsAgg struct {
	Prompts    int
	Turns      int
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
	Cost       float64
}

func (a *statsAgg) add(other statsAgg) {
	a.Prompts += other.Prompts
	a.Turns += other.Turns
	a.Input += other.Input
	a.Output += other.Output
	a.CacheRead += other.CacheRead
	a.CacheWrite += other.CacheWrite
	a.Cost += other.Cost
}

func (a statsAgg) tokens() int64 {
	return a.Input + a.Output + a.CacheRead + a.CacheWrite
}

type statsSlot struct {
	statsAgg
	sessions map[string]bool
}

func newStatsSlot() *statsSlot { return &statsSlot{sessions: map[string]bool{}} }

type statsAcc struct {
	days     map[string]*statsSlot
	projects map[string]*statsSlot
	hours    [24]int
	heatmap  [7][24]int
}

func newStatsAcc() *statsAcc {
	return &statsAcc{days: map[string]*statsSlot{}, projects: map[string]*statsSlot{}}
}

func statsSlotFor(slots map[string]*statsSlot, key string) *statsSlot {
	slot := slots[key]
	if slot == nil {
		slot = newStatsSlot()
		slots[key] = slot
	}
	return slot
}

func (a *statsAcc) addEvent(event HistoryEvent) {
	if event.OccurredAt.State != HistoryFactKnown {
		return
	}
	local := event.OccurredAt.Value.In(time.Local)
	date := local.Format(statsDateLayout)
	day := statsSlotFor(a.days, date)
	project := statsEventProject(event)
	projectSlot := statsSlotFor(a.projects, project)
	if session := statsEventSession(event); session != "" {
		day.sessions[session] = true
		projectSlot.sessions[session] = true
	}

	var activity statsAgg
	switch event.Kind {
	case HistoryEventPrompt:
		activity.Prompts = 1
		a.hours[local.Hour()]++
		weekday := (int(local.Weekday()) + 6) % 7
		a.heatmap[weekday][local.Hour()]++
	case HistoryEventOutput:
		activity.Turns = 1
		fallthrough
	case HistoryEventUsage:
		activity.Input = knownHistoryValue(event.Usage.InputTokens)
		activity.Output = knownHistoryValue(event.Usage.OutputTokens)
		activity.CacheRead = knownHistoryValue(event.Usage.CacheReadTokens)
		activity.CacheWrite = knownHistoryValue(event.Usage.CacheWriteTokens)
		model := ""
		if event.Model.State == HistoryFactKnown {
			model = event.Model.Value
		}
		activity.Cost = modelCost(model, activity.Input, activity.Output, activity.CacheRead, activity.CacheWrite)
	default:
		return
	}
	day.add(activity)
	projectSlot.add(activity)
}

func statsEventProject(event HistoryEvent) string {
	if event.Attribution.ProjectName.State == HistoryFactKnown && event.Attribution.ProjectName.Value != "" {
		return event.Attribution.ProjectName.Value
	}
	return statsOtherProject
}

func statsEventSession(event HistoryEvent) string {
	if event.Attribution.SessionKey.State == HistoryFactKnown {
		return event.Attribution.SessionKey.Value
	}
	if event.ConversationID.State == HistoryFactKnown {
		return string(event.Provider) + "\x00" + event.ConversationID.Value
	}
	return ""
}

func knownHistoryValue(fact HistoryFact[int64]) int64 {
	if fact.State == HistoryFactKnown {
		return fact.Value
	}
	return 0
}

func statsWeekdayIndex(date string) int {
	parsed, err := time.ParseInLocation(statsDateLayout, date, time.Local)
	if err != nil {
		return -1
	}
	return (int(parsed.Weekday()) + 6) % 7
}

var statsWeekdayNames = [7]string{"Mo", "Di", "Mi", "Do", "Fr", "Sa", "So"}

// gitIdentity returns the identity used by this repository. Without the
// filter, shared repositories would attribute other people's commits here.
func gitIdentity(dir string) (email, name string) {
	if out, err := GitCmd(dir, "config", "user.email"); err == nil {
		email = strings.ToLower(strings.TrimSpace(out))
	}
	if out, err := GitCmd(dir, "config", "user.name"); err == nil {
		name = strings.ToLower(strings.TrimSpace(out))
	}
	return email, name
}

func commitsPerDay(dir, since string) map[string]int {
	email, name := gitIdentity(dir)
	out, err := GitCmd(dir, "log", "--all", "--since="+since, "--format=%ct%x1f%ae%x1f%an")
	if err != nil {
		return nil
	}
	days := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\x1f")
		if len(fields) < 3 || !ownCommit(fields[1], fields[2], email, name) {
			continue
		}
		seconds, err := strconv.ParseInt(fields[0], 10, 64)
		if err == nil {
			days[time.Unix(seconds, 0).In(time.Local).Format(statsDateLayout)]++
		}
	}
	return days
}

func ownCommit(commitEmail, commitName, email, name string) bool {
	if email == "" && name == "" {
		return true
	}
	if email != "" && strings.EqualFold(strings.TrimSpace(commitEmail), email) {
		return true
	}
	return name != "" && strings.EqualFold(strings.TrimSpace(commitName), name)
}

// BuildStats is the compatibility Interface used by the TUI and desktop.
// WorkHistory owns all coding-agent history semantics; this function combines
// its normalized facts with the independent Git commit metrics. Quota remains
// a separate concept and is intentionally not read here.
func BuildStats(state *State, days int) Stats {
	history, openErr := OpenWorkHistory(WorkHistoryConfig{})
	return buildStats(context.Background(), state, days, history, time.Now(), openErr)
}

func buildStats(ctx context.Context, state *State, days int, history *WorkHistory, now time.Time, historyErr error) Stats {
	if days <= 0 || days > 365 {
		days = 30
	}
	if state == nil {
		loaded, err := LoadState()
		if err != nil || loaded == nil {
			loaded = &State{}
		}
		state = loaded
	}

	result := Stats{Range: days}
	localNow := now.In(time.Local)
	last := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, time.Local)
	first := last.AddDate(0, 0, -(days - 1))
	before := last.AddDate(0, 0, 1)
	fromKey, toKey := first.Format(statsDateLayout), last.Format(statsDateLayout)

	commits := collectStatsCommits(state, fromKey)
	commitsByDay, commitsByProject := selectStatsCommits(commits, fromKey, toKey)

	var summary HistorySummary
	var events []HistoryEvent
	if historyErr == nil && history != nil {
		query := HistoryEventQuery{Since: first, Before: before}
		summary, events, historyErr = coherentStatsHistory(ctx, history, HistoryAssociationsFromState(state), query)
	}
	if historyErr != nil {
		result.Err = "Arbeitsverlauf nicht lesbar: " + historyErr.Error()
	}

	acc := newStatsAcc()
	for _, event := range events {
		acc.addEvent(event)
	}
	active := activeStatsProjects(state)

	var totals StatsTotals
	busiestScore := [2]int{-1, -1}
	activeDays := map[string]bool{}
	for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
		key := day.Format(statsDateLayout)
		slot := acc.days[key]
		if slot == nil {
			slot = newStatsSlot()
		}
		item := StatsDay{
			Date: key, Weekday: statsWeekdayNames[(int(day.Weekday())+6)%7],
			Prompts: slot.Prompts, Turns: slot.Turns,
			Input: slot.Input, Output: slot.Output, CacheRead: slot.CacheRead, CacheWrite: slot.CacheWrite,
			Cost: slot.Cost, Sessions: len(slot.sessions), Commits: commitsByDay[key],
		}
		result.Days = append(result.Days, item)
		totals.Cost += item.Cost
		totals.Commits += item.Commits
		if item.Prompts > 0 || item.Turns > 0 || item.Commits > 0 {
			totals.Days++
			activeDays[key] = true
		}
		if score := [2]int{item.Prompts, item.Turns}; score[0] > busiestScore[0] || score[0] == busiestScore[0] && score[1] > busiestScore[1] {
			busiestScore = score
			if score[0] > 0 || score[1] > 0 {
				totals.BusiestDay = key
			}
		}
	}

	// These totals come from WorkHistory.Summarize so every consumer shares
	// prompt, output, Session, and usage semantics, including explicit unknowns.
	totals.Prompts = summary.Totals.Prompts
	totals.Turns = summary.Totals.Outputs
	totals.Sessions = summary.Totals.Sessions
	totals.Input = summary.Totals.Usage.InputTokens.Value
	totals.Output = summary.Totals.Usage.OutputTokens.Value
	totals.CacheRead = summary.Totals.Usage.CacheReadTokens.Value
	totals.CacheWrite = summary.Totals.Usage.CacheWriteTokens.Value
	totals.Tokens = totals.Input + totals.Output + totals.CacheRead + totals.CacheWrite
	if base := totals.CacheRead + totals.Input + totals.CacheWrite; base > 0 {
		totals.CacheHit = float64(totals.CacheRead) / float64(base) * 100
	}
	cursor := last
	if !activeDays[cursor.Format(statsDateLayout)] {
		cursor = cursor.AddDate(0, 0, -1)
	}
	for !cursor.Before(first) && activeDays[cursor.Format(statsDateLayout)] {
		totals.Streak++
		cursor = cursor.AddDate(0, 0, -1)
	}
	result.Totals = totals

	result.Projects = buildStatsProjects(acc, state, active, commitsByProject)
	result.Models = buildStatsModels(summary)
	result.Providers = buildStatsProviders(summary)
	result.Heatmap = make([][]int, 7)
	for i := range result.Heatmap {
		result.Heatmap[i] = append([]int(nil), acc.heatmap[i][:]...)
	}
	result.Hours = append([]int(nil), acc.hours[:]...)
	if result.Projects == nil {
		result.Projects = []StatsProject{}
	}
	if result.Models == nil {
		result.Models = []StatsModel{}
	}
	if result.Providers == nil {
		result.Providers = []StatsProvider{}
	}
	return result
}

func coherentStatsHistory(ctx context.Context, history *WorkHistory, associations HistoryAssociations, query HistoryEventQuery) (HistorySummary, []HistoryEvent, error) {
	for attempt := 0; attempt < 3; attempt++ {
		summary, err := history.Summarize(ctx, associations, HistorySummaryQuery{Events: query, Location: time.Local})
		if err != nil {
			return HistorySummary{}, nil, err
		}
		events, revision, err := pagedStatsHistory(ctx, history, associations, query)
		if err != nil {
			return HistorySummary{}, nil, err
		}
		if revision == summary.Meta.Revision {
			return summary, events, nil
		}
	}
	return HistorySummary{}, nil, fmt.Errorf("work history changed repeatedly while statistics were read")
}

func pagedStatsHistory(ctx context.Context, history *WorkHistory, associations HistoryAssociations, query HistoryEventQuery) ([]HistoryEvent, uint64, error) {
	query.Limit = 1000
	query.Offset = 0
	var events []HistoryEvent
	var revision uint64
	for {
		page, err := history.Events(ctx, associations, query)
		if err != nil {
			return nil, 0, err
		}
		if revision == 0 {
			revision = page.Meta.Revision
		} else if page.Meta.Revision != revision {
			return nil, page.Meta.Revision, nil
		}
		events = append(events, page.Events...)
		if len(events) >= page.Total {
			return events, revision, nil
		}
		if len(page.Events) == 0 {
			return nil, 0, fmt.Errorf("work history pagination made no progress")
		}
		query.Offset += len(page.Events)
	}
}

func collectStatsCommits(state *State, since string) map[string]map[string]int {
	commits := map[string]map[string]int{}
	var mu sync.Mutex
	var wait sync.WaitGroup
	for _, project := range state.Projects {
		if project.Path == "" {
			continue
		}
		wait.Add(1)
		go func(project Project) {
			defer wait.Done()
			byDay := commitsPerDay(project.Path, since)
			if len(byDay) == 0 {
				return
			}
			mu.Lock()
			commits[project.Name] = byDay
			mu.Unlock()
		}(project)
	}
	wait.Wait()
	return commits
}

func selectStatsCommits(commits map[string]map[string]int, from, to string) (map[string]int, map[string]int) {
	byDay, byProject := map[string]int{}, map[string]int{}
	for project, days := range commits {
		for date, count := range days {
			if date >= from && date <= to {
				byDay[date] += count
				byProject[project] += count
			}
		}
	}
	return byDay, byProject
}

func activeStatsProjects(state *State) map[string]int {
	active := map[string]int{}
	for _, session := range state.Agents {
		name := session.Project
		if session.ProjectID != "" {
			if project := state.ProjectByID(session.ProjectID); project != nil {
				name = project.Name
			}
		}
		active[name]++
	}
	return active
}

func buildStatsProjects(acc *statsAcc, state *State, active, commits map[string]int) []StatsProject {
	seen := map[string]bool{}
	var projects []StatsProject
	for name, slot := range acc.projects {
		seen[name] = true
		projects = append(projects, StatsProject{
			Name: name, Tokens: slot.tokens(), Cost: slot.Cost, Prompts: slot.Prompts,
			Sessions: len(slot.sessions), Commits: commits[name], Active: active[name],
		})
	}
	for _, project := range state.Projects {
		if seen[project.Name] || commits[project.Name] == 0 && active[project.Name] == 0 {
			continue
		}
		projects = append(projects, StatsProject{Name: project.Name, Commits: commits[project.Name], Active: active[project.Name]})
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Tokens != projects[j].Tokens {
			return projects[i].Tokens > projects[j].Tokens
		}
		return projects[i].Name < projects[j].Name
	})
	return projects
}

func buildStatsModels(summary HistorySummary) []StatsModel {
	models := make([]StatsModel, 0, len(summary.Models))
	for _, model := range summary.Models {
		name := model.Model
		if name == "unknown" || name == "" {
			name = "unbekannt"
		}
		input := model.Usage.InputTokens.Value
		output := model.Usage.OutputTokens.Value
		cacheRead := model.Usage.CacheReadTokens.Value
		cacheWrite := model.Usage.CacheWriteTokens.Value
		models = append(models, StatsModel{
			Model: name, Source: model.Provider.Label(), Turns: model.Turns,
			Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite,
			Cost: modelCost(name, input, output, cacheRead, cacheWrite),
		})
	}
	sort.Slice(models, func(i, j int) bool {
		if models[i].Cost != models[j].Cost {
			return models[i].Cost > models[j].Cost
		}
		if models[i].Source != models[j].Source {
			return models[i].Source < models[j].Source
		}
		return models[i].Model < models[j].Model
	})
	return models
}

func buildStatsProviders(summary HistorySummary) []StatsProvider {
	problems := map[HistoryProvider][]HistoryProblem{}
	for _, coverage := range summary.Meta.Coverage {
		problems[coverage.Provider] = append([]HistoryProblem(nil), coverage.Problems...)
	}
	providers := make([]StatsProvider, 0, len(summary.Providers))
	for _, provider := range summary.Providers {
		usage := provider.Usage
		providers = append(providers, StatsProvider{
			Provider: string(provider.Provider), Source: provider.Provider.Label(), State: provider.State,
			Prompts: provider.Prompts, Turns: provider.Outputs,
			Tokens: usage.InputTokens.Value + usage.OutputTokens.Value + usage.CacheReadTokens.Value + usage.CacheWriteTokens.Value,
			Usage:  usage, Problems: problems[provider.Provider],
		})
	}
	return providers
}
