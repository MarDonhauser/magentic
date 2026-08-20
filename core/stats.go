package core

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// StatsCostState describes how much of an aggregate can be priced without
// pretending that every agent vendor shares Claude's public API prices.
type StatsCostState string

const (
	StatsCostPriced   StatsCostState = "priced"
	StatsCostPartial  StatsCostState = "partial"
	StatsCostUnpriced StatsCostState = "unpriced"
	StatsCostNone     StatsCostState = "none"
)

type StatsDay struct {
	Date       string         `json:"date"`
	Weekday    string         `json:"weekday"`
	Prompts    int            `json:"prompts"`
	Turns      int            `json:"turns"`
	Input      int64          `json:"input"`
	Output     int64          `json:"output"`
	CacheRead  int64          `json:"cacheRead"`
	CacheWrite int64          `json:"cacheWrite"`
	Cost       float64        `json:"cost"`
	CostState  StatsCostState `json:"costState"`
	Sessions   int            `json:"sessions"`
	Commits    int            `json:"commits"`
}

type StatsProject struct {
	Name        string             `json:"name"`
	Tokens      int64              `json:"tokens"`
	Cost        float64            `json:"cost"`
	CostState   StatsCostState     `json:"costState"`
	Prompts     int                `json:"prompts"`
	Sessions    int                `json:"sessions"`
	Commits     int                `json:"commits"`
	CommitState HistorySourceState `json:"commitState"`
	Active      int                `json:"active"`
}

type StatsModel struct {
	Model      string         `json:"model"`
	Provider   string         `json:"provider"`
	Source     string         `json:"source,omitempty"`
	Turns      int            `json:"turns"`
	Input      int64          `json:"input"`
	Output     int64          `json:"output"`
	CacheRead  int64          `json:"cacheRead"`
	CacheWrite int64          `json:"cacheWrite"`
	Cost       float64        `json:"cost"`
	CostState  StatsCostState `json:"costState"`
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

// StatsCommitProblem explains why a repository could not contribute an exact
// own-commit count. Project is kept separate so consumers can group or shorten
// diagnostics without parsing presentation text.
type StatsCommitProblem struct {
	Project string `json:"project"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// StatsCommitCoverage describes whether Commits is an exact total or only a
// known subtotal. A repository counts as available only when both its Git log
// and at least one local Git identity field can be read.
type StatsCommitCoverage struct {
	State                 HistorySourceState   `json:"state"`
	Repositories          int                  `json:"repositories"`
	AvailableRepositories int                  `json:"availableRepositories"`
	Problems              []StatsCommitProblem `json:"problems,omitempty"`
}

type StatsTotals struct {
	Days       int            `json:"days"`
	Prompts    int            `json:"prompts"`
	Turns      int            `json:"turns"`
	Sessions   int            `json:"sessions"`
	Tokens     int64          `json:"tokens"`
	Input      int64          `json:"input"`
	Output     int64          `json:"output"`
	CacheRead  int64          `json:"cacheRead"`
	CacheWrite int64          `json:"cacheWrite"`
	Cost       float64        `json:"cost"`
	CostState  StatsCostState `json:"costState"`
	Commits    int            `json:"commits"`
	CacheHit   float64        `json:"cacheHit"`
	BusiestDay string         `json:"busiestDay"`
	Streak     int            `json:"streak"`
}

type Stats struct {
	Range          int                 `json:"range"`
	Days           []StatsDay          `json:"days"`
	Projects       []StatsProject      `json:"projects"`
	Models         []StatsModel        `json:"models"`
	Providers      []StatsProvider     `json:"providers"`
	CommitCoverage StatsCommitCoverage `json:"commitCoverage"`
	Heatmap        [][]int             `json:"heatmap"`
	Hours          []int               `json:"hours"`
	Totals         StatsTotals         `json:"totals"`
	Err            string              `json:"err,omitempty"`
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

func modelPrice(model string) (float64, float64, bool) {
	for _, price := range modelPrices {
		if pricedModelName(model, price.prefix) {
			return price.in, price.out, true
		}
	}
	return 0, 0, false
}

func pricedModelName(model, listed string) bool {
	if model == listed {
		return true
	}
	// Anthropic snapshot IDs append an eight-digit release date to the listed
	// family name. Other suffixes are different models and stay unpriced.
	if len(model) != len(listed)+9 || !strings.HasPrefix(model, listed+"-") {
		return false
	}
	snapshot := model[len(listed)+1:]
	if !strings.HasPrefix(snapshot, "20") {
		return false
	}
	for i := range len(snapshot) {
		if snapshot[i] < '0' || snapshot[i] > '9' {
			return false
		}
	}
	return true
}

func modelCost(provider HistoryProvider, model string, input, output, cacheRead, cacheWrite int64) (float64, bool) {
	if provider != HistoryProviderClaude {
		return 0, false
	}
	in, out, known := modelPrice(model)
	if !known {
		return 0, false
	}
	const million = 1_000_000.0
	return float64(input)*in/million +
		float64(output)*out/million +
		float64(cacheWrite)*in*1.25/million +
		float64(cacheRead)*in*0.1/million, true
}

type statsAgg struct {
	Prompts     int
	Turns       int
	Input       int64
	Output      int64
	CacheRead   int64
	CacheWrite  int64
	Cost        float64
	costPriced  bool
	costUnknown bool
}

func (a *statsAgg) add(other statsAgg) {
	a.Prompts += other.Prompts
	a.Turns += other.Turns
	a.Input += other.Input
	a.Output += other.Output
	a.CacheRead += other.CacheRead
	a.CacheWrite += other.CacheWrite
	a.Cost += other.Cost
	a.costPriced = a.costPriced || other.costPriced
	a.costUnknown = a.costUnknown || other.costUnknown
}

func (a statsAgg) tokens() int64 {
	return a.Input + a.Output + a.CacheRead + a.CacheWrite
}

func (a statsAgg) costState() StatsCostState {
	switch {
	case a.costPriced && a.costUnknown:
		return StatsCostPartial
	case a.costPriced:
		return StatsCostPriced
	case a.costUnknown:
		return StatsCostUnpriced
	default:
		return StatsCostNone
	}
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
		knownUsage, unknownUsage := statsUsageFactState(event.Usage)
		if event.Provider == HistoryProviderClaude && knownUsage {
			activity.Cost, activity.costPriced = modelCost(event.Provider, model, activity.Input, activity.Output, activity.CacheRead, activity.CacheWrite)
		}
		// An assistant output represents potentially billable work even when its
		// adapter cannot expose token facts. Non-Claude activity is deliberately
		// unpriced: applying Claude's fallback here would manufacture precision.
		activity.costUnknown = event.Provider != HistoryProviderClaude ||
			unknownUsage ||
			(knownUsage && !activity.costPriced) ||
			(event.Kind == HistoryEventOutput && !knownUsage)
	default:
		return
	}
	day.add(activity)
	projectSlot.add(activity)
}

func statsUsageFactState(usage HistoryUsage) (known, unknown bool) {
	states := [...]HistoryFactState{
		usage.InputTokens.State,
		usage.OutputTokens.State,
		usage.CacheReadTokens.State,
		usage.CacheWriteTokens.State,
	}
	for _, state := range states {
		switch state {
		case HistoryFactKnown:
			known = true
		case HistoryFactUnknown:
			unknown = true
		}
	}
	return known, unknown
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

const (
	statsCommitProblemIdentity = "identity-unavailable"
	statsCommitProblemLog      = "log-unavailable"
)

type statsCommitRepositoryResult struct {
	Project  string
	Days     map[string]int
	State    HistorySourceState
	Problems []StatsCommitProblem
}

// gitIdentity returns the effective identity used by this repository. At
// least one field must be known: an absent identity is an unavailable source,
// never permission to count every author in a shared repository.
func gitIdentity(dir string) (email, name string, err error) {
	return gitIdentityWithGit(dir, GitCmd)
}

func gitIdentityWithGit(dir string, run gitRunner) (email, name string, err error) {
	emailOut, emailErr := run(dir, "config", "user.email")
	if emailErr == nil {
		email = strings.ToLower(strings.TrimSpace(emailOut))
	}
	nameOut, nameErr := run(dir, "config", "user.name")
	if nameErr == nil {
		name = strings.ToLower(strings.TrimSpace(nameOut))
	}
	if email != "" || name != "" {
		return email, name, nil
	}

	switch {
	case emailErr != nil && nameErr != nil:
		return "", "", fmt.Errorf("Git-Identität nicht lesbar (user.email: %v; user.name: %v)", emailErr, nameErr)
	case emailErr != nil:
		return "", "", fmt.Errorf("keine Git-Identität konfiguriert (user.email nicht lesbar: %v)", emailErr)
	case nameErr != nil:
		return "", "", fmt.Errorf("keine Git-Identität konfiguriert (user.name nicht lesbar: %v)", nameErr)
	default:
		return "", "", fmt.Errorf("keine Git-Identität konfiguriert")
	}
}

func commitsPerDay(dir, since string) statsCommitRepositoryResult {
	return commitsPerDayWithGit("", dir, since, GitCmd)
}

func commitsPerDayWithGit(project, dir, since string, run gitRunner) statsCommitRepositoryResult {
	result := statsCommitRepositoryResult{Project: project, State: HistorySourceUnavailable}
	email, name, err := gitIdentityWithGit(dir, run)
	if err != nil {
		result.Problems = []StatsCommitProblem{{
			Project: project,
			Kind:    statsCommitProblemIdentity,
			Message: err.Error(),
		}}
		return result
	}

	out, err := run(dir, "log", "--all", "--since="+since, "--format=%ct%x1f%ae%x1f%an")
	if err != nil {
		result.Problems = []StatsCommitProblem{{
			Project: project,
			Kind:    statsCommitProblemLog,
			Message: "Git-Verlauf nicht lesbar: " + err.Error(),
		}}
		return result
	}

	result.Days = map[string]int{}
	result.State = HistorySourceAvailable
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
			result.Days[time.Unix(seconds, 0).In(time.Local).Format(statsDateLayout)]++
		}
	}
	return result
}

func ownCommit(commitEmail, commitName, email, name string) bool {
	if email == "" && name == "" {
		return false
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
	return buildStatsWithGit(ctx, state, days, history, now, historyErr, GitCmd)
}

func buildStatsWithGit(ctx context.Context, state *State, days int, history *WorkHistory, now time.Time, historyErr error, run gitRunner) Stats {
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

	commits := collectStatsCommitsWithGit(state, fromKey, run)
	commitsByDay, commitsByProject := selectStatsCommits(commits.ByProject, fromKey, toKey)
	result.CommitCoverage = commits.Coverage

	var summary HistorySummary
	var events []HistoryEvent
	if historyErr == nil && history != nil {
		// Prompts, Turns, tokens, and costs use the same primary-lineage
		// population. Delegated work needs a separate aggregate before it can be
		// shown without turning subagent prompts into developer prompts.
		query := HistoryEventQuery{
			Since: first, Before: before,
			Lineages: []HistoryLineage{HistoryLineagePrimary},
		}
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
	var totalCosts statsAgg
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
			Cost: slot.Cost, CostState: slot.costState(), Sessions: len(slot.sessions), Commits: commitsByDay[key],
		}
		result.Days = append(result.Days, item)
		totalCosts.add(slot.statsAgg)
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
	totals.CostState = totalCosts.costState()
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
	result.Projects = buildStatsProjects(acc, state, active, commitsByProject, commits.ProjectStates)
	result.Models = buildStatsModels(summary)
	result.Providers = buildStatsProviders(summary)
	if statsHistoryCoverageIncomplete(summary.Meta.Coverage) {
		totals.CostState = partialStatsCostState(totals.CostState)
		for i := range result.Days {
			result.Days[i].CostState = partialStatsCostState(result.Days[i].CostState)
		}
		for i := range result.Projects {
			result.Projects[i].CostState = partialStatsCostState(result.Projects[i].CostState)
		}
		for i := range result.Models {
			result.Models[i].CostState = partialStatsCostState(result.Models[i].CostState)
		}
	}
	result.Totals = totals
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

func statsHistoryCoverageIncomplete(coverage []HistoryProviderCoverage) bool {
	for _, source := range coverage {
		if source.State == HistorySourcePartial || source.State == HistorySourceUnavailable {
			return true
		}
	}
	return false
}

func partialStatsCostState(state StatsCostState) StatsCostState {
	if state == StatsCostPriced {
		return StatsCostPartial
	}
	return state
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

type statsCommitCollection struct {
	ByProject     map[string]map[string]int
	ProjectStates map[string]HistorySourceState
	Coverage      StatsCommitCoverage
}

func collectStatsCommits(state *State, since string) statsCommitCollection {
	return collectStatsCommitsWithGit(state, since, GitCmd)
}

func collectStatsCommitsWithGit(state *State, since string, run gitRunner) statsCommitCollection {
	collection := statsCommitCollection{
		ByProject:     map[string]map[string]int{},
		ProjectStates: map[string]HistorySourceState{},
		Coverage:      StatsCommitCoverage{State: HistorySourceAbsent},
	}
	if state == nil {
		return collection
	}

	var repositories []Project
	for _, project := range state.Projects {
		if strings.TrimSpace(project.Path) != "" {
			repositories = append(repositories, project)
		}
	}
	collection.Coverage.Repositories = len(repositories)
	if len(repositories) == 0 {
		return collection
	}

	results := make(chan statsCommitRepositoryResult, len(repositories))
	for _, project := range repositories {
		go func(project Project) {
			results <- commitsPerDayWithGit(project.Name, project.Path, since, run)
		}(project)
	}

	for range repositories {
		result := <-results
		if result.State == HistorySourceAvailable {
			collection.Coverage.AvailableRepositories++
		}
		if previous, ok := collection.ProjectStates[result.Project]; ok {
			collection.ProjectStates[result.Project] = mergeStatsCommitStates(previous, result.State)
		} else {
			collection.ProjectStates[result.Project] = result.State
		}
		if len(result.Days) > 0 {
			days := collection.ByProject[result.Project]
			if days == nil {
				days = map[string]int{}
				collection.ByProject[result.Project] = days
			}
			for date, count := range result.Days {
				days[date] += count
			}
		}
		collection.Coverage.Problems = append(collection.Coverage.Problems, result.Problems...)
	}

	switch collection.Coverage.AvailableRepositories {
	case 0:
		collection.Coverage.State = HistorySourceUnavailable
	case collection.Coverage.Repositories:
		collection.Coverage.State = HistorySourceAvailable
	default:
		collection.Coverage.State = HistorySourcePartial
	}
	sort.Slice(collection.Coverage.Problems, func(i, j int) bool {
		left, right := collection.Coverage.Problems[i], collection.Coverage.Problems[j]
		if left.Project != right.Project {
			return left.Project < right.Project
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.Message < right.Message
	})
	return collection
}

func mergeStatsCommitStates(left, right HistorySourceState) HistorySourceState {
	if left == right {
		return left
	}
	if left == HistorySourceAbsent {
		return right
	}
	if right == HistorySourceAbsent {
		return left
	}
	return HistorySourcePartial
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

func buildStatsProjects(acc *statsAcc, state *State, active, commits map[string]int, commitStates map[string]HistorySourceState) []StatsProject {
	seen := map[string]bool{}
	var projects []StatsProject
	for name, slot := range acc.projects {
		seen[name] = true
		projects = append(projects, StatsProject{
			Name: name, Tokens: slot.tokens(), Cost: slot.Cost, CostState: slot.costState(), Prompts: slot.Prompts,
			Sessions: len(slot.sessions), Commits: commits[name], CommitState: statsProjectCommitState(commitStates, name), Active: active[name],
		})
	}
	for _, project := range state.Projects {
		commitState := statsProjectCommitState(commitStates, project.Name)
		if seen[project.Name] || commits[project.Name] == 0 && active[project.Name] == 0 && commitState != HistorySourcePartial && commitState != HistorySourceUnavailable {
			continue
		}
		projects = append(projects, StatsProject{
			Name: project.Name, Commits: commits[project.Name], CommitState: commitState, Active: active[project.Name],
		})
	}
	sort.Slice(projects, func(i, j int) bool {
		if projects[i].Tokens != projects[j].Tokens {
			return projects[i].Tokens > projects[j].Tokens
		}
		return projects[i].Name < projects[j].Name
	})
	return projects
}

func statsProjectCommitState(states map[string]HistorySourceState, project string) HistorySourceState {
	if state := states[project]; state != "" {
		return state
	}
	return HistorySourceAbsent
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
		knownUsage, unknownUsage := statsUsageSummaryState(model.Usage)
		cost, priced := modelCost(model.Provider, name, input, output, cacheRead, cacheWrite)
		costState := StatsCostNone
		switch {
		case priced && knownUsage && unknownUsage:
			costState = StatsCostPartial
		case priced && knownUsage:
			costState = StatsCostPriced
		case model.Turns > 0 || knownUsage || unknownUsage:
			costState = StatsCostUnpriced
		}
		models = append(models, StatsModel{
			Model: name, Provider: string(model.Provider), Source: model.Provider.Label(), Turns: model.Turns,
			Input: input, Output: output, CacheRead: cacheRead, CacheWrite: cacheWrite,
			Cost: cost, CostState: costState,
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

func statsUsageSummaryState(usage HistoryUsageSummary) (known, unknown bool) {
	measures := [...]HistoryMeasure{
		usage.InputTokens,
		usage.OutputTokens,
		usage.CacheReadTokens,
		usage.CacheWriteTokens,
	}
	for _, measure := range measures {
		known = known || measure.KnownEvents > 0
		unknown = unknown || measure.UnknownEvents > 0
	}
	return known, unknown
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
