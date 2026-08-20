package core

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type statsRepositoriesFunc func(context.Context, string, string) RepositoryFact[RepositoryOwnCommitSeries]

func (f statsRepositoriesFunc) OwnCommitsSince(ctx context.Context, dir, since string) RepositoryFact[RepositoryOwnCommitSeries] {
	return f(ctx, dir, since)
}

func TestBuildStatsExposesPartialCommitCoverageAndKnownSubtotal(t *testing.T) {
	const commitTimestamp int64 = 1700000000
	now := time.Unix(1700003600, 0).In(time.Local)
	state := &State{Projects: []Project{
		{Name: "Readable", Path: "/readable"},
		{Name: "Blocked", Path: "/blocked"},
		{Name: "No repository"},
	}}
	repositories := statsRepositoriesFunc(func(_ context.Context, dir, since string) RepositoryFact[RepositoryOwnCommitSeries] {
		if since == "" {
			return repositoryUnknownFact[RepositoryOwnCommitSeries](repositoryProblemOwnCommitLog, errors.New("missing history boundary"))
		}
		switch dir {
		case "/readable":
			return repositoryKnownFact(RepositoryOwnCommitSeries{Timestamps: []int64{commitTimestamp}})
		case "/blocked":
			return repositoryUnknownFact[RepositoryOwnCommitSeries](repositoryProblemOwnCommitIdentity, errors.New("config denied"))
		default:
			return repositoryUnknownFact[RepositoryOwnCommitSeries](repositoryProblemOwnCommitLog, errors.New("unexpected repository"))
		}
	})

	stats := buildStatsWithRepositories(context.Background(), state, 7, nil, now, nil, repositories)
	coverage := stats.CommitCoverage
	if coverage.State != HistorySourcePartial || coverage.Repositories != 2 || coverage.AvailableRepositories != 1 {
		t.Fatalf("commit coverage = %#v", coverage)
	}
	if len(coverage.Problems) != 1 || coverage.Problems[0].Project != "Blocked" || coverage.Problems[0].Kind != statsCommitProblemIdentity {
		t.Fatalf("commit diagnostics = %#v", coverage.Problems)
	}
	if stats.Totals.Commits != 1 {
		t.Fatalf("known own-commit subtotal = %d, want 1", stats.Totals.Commits)
	}
	readable := findStatsProject(t, stats.Projects, "Readable")
	if readable.Commits != 1 || readable.CommitState != HistorySourceAvailable {
		t.Fatalf("readable project commits = %#v", readable)
	}
	blocked := findStatsProject(t, stats.Projects, "Blocked")
	if blocked.Commits != 0 || blocked.CommitState != HistorySourceUnavailable {
		t.Fatalf("blocked project looked like an exact zero: %#v", blocked)
	}
	if got := statsProjectCommitState(map[string]HistorySourceState{}, "No repository"); got != HistorySourceAbsent {
		t.Fatalf("project without repository state = %q", got)
	}
}

func TestBuildStatsConsumesNormalizedWorkHistory(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	codexHome := filepath.Join(root, "codex")
	indexDir := filepath.Join(root, "index")
	projectPath := filepath.Join(root, "project")
	if err := os.MkdirAll(projectPath, 0o700); err != nil {
		t.Fatal(err)
	}
	history, err := OpenWorkHistory(WorkHistoryConfig{HomeDir: home, CodexHome: codexHome, IndexDir: indexDir})
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	claudePromptAt := now.Add(-2 * time.Hour).UTC().Format(time.RFC3339Nano)
	claudeOutputAt := now.Add(-2*time.Hour + time.Minute).UTC().Format(time.RFC3339Nano)
	delegatedPromptAt := now.Add(-90 * time.Minute).UTC().Format(time.RFC3339Nano)
	delegatedOutputAt := now.Add(-89 * time.Minute).UTC().Format(time.RFC3339Nano)
	codexPromptAt := now.Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	codexOutputAt := now.Add(-time.Hour + time.Minute).UTC().Format(time.RFC3339Nano)
	codexUsageAt := now.Add(-time.Hour + 2*time.Minute).UTC().Format(time.RFC3339Nano)

	writeStatsFixture(t, filepath.Join(home, ".claude", "projects", "project", "claude-run.jsonl"), strings.Join([]string{
		"{malformed}",
		`{"type":"user","timestamp":"` + claudePromptAt + `","cwd":"` + projectPath + `","sessionId":"claude-run","message":{"content":"Claude statistics prompt"}}`,
		`{"type":"assistant","timestamp":"` + claudeOutputAt + `","cwd":"` + projectPath + `","sessionId":"claude-run","message":{"model":"claude-opus-4-8","content":"Claude statistics output","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":2,"cache_creation_input_tokens":1}}}`,
		`{"type":"user","timestamp":"` + delegatedPromptAt + `","cwd":"` + projectPath + `","sessionId":"claude-run","isSidechain":true,"message":{"content":"Delegated prompt must not enter primary statistics"}}`,
		`{"type":"assistant","timestamp":"` + delegatedOutputAt + `","cwd":"` + projectPath + `","sessionId":"claude-run","isSidechain":true,"message":{"model":"claude-opus-4-8","content":"Delegated output must not enter primary statistics","usage":{"input_tokens":1000,"output_tokens":500,"cache_read_input_tokens":200,"cache_creation_input_tokens":100}}}`,
	}, "\n")+"\n")
	writeStatsFixture(t, filepath.Join(codexHome, "sessions", "recent", "codex-run.jsonl"), strings.Join([]string{
		`{"type":"session_meta","timestamp":"` + codexPromptAt + `","payload":{"id":"codex-run","cwd":"` + projectPath + `","model":"gpt-5"}}`,
		`{"type":"event_msg","timestamp":"` + codexPromptAt + `","payload":{"type":"user_message","message":"Codex statistics prompt"}}`,
		`{"type":"response_item","timestamp":"` + codexOutputAt + `","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Codex statistics output"}]}}`,
		`{"type":"event_msg","timestamp":"` + codexUsageAt + `","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":20,"output_tokens":7,"cached_input_tokens":3}}}}`,
	}, "\n")+"\n")

	state := &State{
		Projects: []Project{{ID: ProjectID("project-id"), Name: "Durable project", Path: projectPath}},
		Agents: []Session{
			{ID: SessionID("claude-session"), Name: "Renamed Claude", ProjectID: ProjectID("project-id"), Project: "stale name", Dir: projectPath, AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "claude-run"}}},
			{ID: SessionID("codex-session"), Name: "Renamed Codex", ProjectID: ProjectID("project-id"), Project: "stale name", Dir: projectPath, AgentRuns: []AgentRunRef{{Vendor: AgentVendorCodex, ExternalID: "codex-run"}}},
		},
	}
	stats := buildStats(context.Background(), state, 7, history, now, nil)
	if stats.Err != "" {
		t.Fatalf("partial coverage became a fatal error: %s", stats.Err)
	}
	if stats.Totals.Prompts != 2 || stats.Totals.Turns != 2 || stats.Totals.Sessions != 2 {
		t.Fatalf("normalized totals = %#v", stats.Totals)
	}
	if stats.Totals.Input != 30 || stats.Totals.Output != 12 || stats.Totals.CacheRead != 5 || stats.Totals.CacheWrite != 1 || stats.Totals.Tokens != 48 {
		t.Fatalf("normalized token totals = %#v", stats.Totals)
	}
	wantClaudeCost := float64(10)*5/1_000_000 + float64(5)*25/1_000_000 + float64(2)*5*.1/1_000_000 + float64(1)*5*1.25/1_000_000
	if math.Abs(stats.Totals.Cost-wantClaudeCost) > 1e-12 || stats.Totals.CostState != StatsCostPartial {
		t.Fatalf("provider-aware cost = %#v, want Claude subtotal %f marked partial", stats.Totals, wantClaudeCost)
	}
	if len(stats.Projects) != 1 || stats.Projects[0].Name != "Durable project" || stats.Projects[0].Prompts != 2 || stats.Projects[0].Sessions != 2 || stats.Projects[0].Tokens != 48 || stats.Projects[0].Active != 2 {
		t.Fatalf("stable Registry project attribution = %#v", stats.Projects)
	}
	if math.Abs(stats.Projects[0].Cost-wantClaudeCost) > 1e-12 || stats.Projects[0].CostState != StatsCostPartial {
		t.Fatalf("project cost must expose a Claude-only subtotal: %#v", stats.Projects[0])
	}
	day := stats.Days[len(stats.Days)-1]
	if day.Prompts != 2 || day.Turns != 2 || day.Sessions != 2 {
		t.Fatalf("daily normalized activity = %#v", day)
	}
	if len(stats.Models) != 2 {
		t.Fatalf("provider-qualified models = %#v", stats.Models)
	}
	for _, model := range stats.Models {
		if model.Source == "" || model.Turns != 1 {
			t.Fatalf("model activity counted usage record as another turn: %#v", model)
		}
		if model.Provider == string(HistoryProviderCodex) && (model.Cost != 0 || model.CostState != StatsCostUnpriced) {
			t.Fatalf("Codex usage was priced as Claude: %#v", model)
		}
	}
	claude := findStatsProvider(t, stats.Providers, HistoryProviderClaude)
	if claude.State != HistorySourcePartial || claude.Prompts != 1 || claude.Turns != 1 || len(claude.Problems) != 1 {
		t.Fatalf("Claude partial coverage = %#v", claude)
	}
	codex := findStatsProvider(t, stats.Providers, HistoryProviderCodex)
	if codex.State != HistorySourceAvailable || codex.Prompts != 1 || codex.Turns != 1 || codex.Tokens != 30 {
		t.Fatalf("Codex provider activity = %#v", codex)
	}
}

func TestRegisteredStatsProjectsCountsParkedAndRuntimeAbsentSessions(t *testing.T) {
	now := time.Now()
	state := &State{
		Projects: []Project{{ID: ProjectID("project-id"), Name: "Durable project"}},
		Agents: []Session{
			{
				ID:        SessionID("parked"),
				Name:      "Parked session",
				ProjectID: ProjectID("project-id"),
				LaterAt:   now,
			},
			{
				ID:          SessionID("runtime-absent"),
				Name:        "Exited runtime still in Registry",
				ProjectID:   ProjectID("project-id"),
				RuntimeName: "no-longer-present",
			},
		},
	}

	registered := registeredStatsProjects(state)
	if got := registered["Durable project"]; got != 2 {
		t.Fatalf("registered session count = %d, want parked and runtime-absent Registry records", got)
	}
}

func TestModelCostOnlyPricesClaude(t *testing.T) {
	const (
		input      = int64(1_000_000)
		output     = int64(1_000_000)
		cacheRead  = int64(1_000_000)
		cacheWrite = int64(1_000_000)
	)
	cost, priced := modelCost(HistoryProviderClaude, "claude-sonnet-4-6", input, output, cacheRead, cacheWrite)
	if !priced || math.Abs(cost-(3+15+0.3+3.75)) > 1e-12 {
		t.Fatalf("Claude pricing = (%f, %v)", cost, priced)
	}
	if _, priced := modelCost(HistoryProviderClaude, "claude-sonnet-4-6-20260219", input, output, cacheRead, cacheWrite); !priced {
		t.Fatal("dated snapshot of a listed Claude model was not priced")
	}
	if cost, priced := modelCost(HistoryProviderClaude, "claude-unlisted", input, output, cacheRead, cacheWrite); priced || cost != 0 {
		t.Fatalf("unlisted Claude model inherited a fallback price: (%f, %v)", cost, priced)
	}
	if cost, priced := modelCost(HistoryProviderClaude, "claude-sonnet-4-6-experimental", input, output, cacheRead, cacheWrite); priced || cost != 0 {
		t.Fatalf("Claude model with an unknown suffix inherited a prefix price: (%f, %v)", cost, priced)
	}
	for _, provider := range []HistoryProvider{HistoryProviderCodex, HistoryProviderGemini, HistoryProviderCopilot} {
		if cost, priced := modelCost(provider, "claude-sonnet-4-6", input, output, cacheRead, cacheWrite); priced || cost != 0 {
			t.Errorf("%s inherited Claude pricing: (%f, %v)", provider, cost, priced)
		}
	}
}

func TestStatsCostStateMarksUnlistedClaudeModelsUnpriced(t *testing.T) {
	now := time.Now()
	usage := HistoryUsage{
		InputTokens:      historyKnown[int64](100),
		OutputTokens:     historyKnown[int64](50),
		CacheReadTokens:  historyKnown[int64](0),
		CacheWriteTokens: historyKnown[int64](0),
	}
	acc := newStatsAcc()
	acc.addEvent(HistoryEvent{
		Provider:   HistoryProviderClaude,
		Kind:       HistoryEventOutput,
		OccurredAt: historyKnown(now),
		Model:      historyKnown("claude-unlisted"),
		Usage:      usage,
	})
	day := acc.days[now.In(time.Local).Format(statsDateLayout)]
	if day == nil || day.Cost != 0 || day.costState() != StatsCostUnpriced {
		t.Fatalf("unlisted Claude model cost state = %#v", day)
	}

	acc.addEvent(HistoryEvent{
		Provider:   HistoryProviderClaude,
		Kind:       HistoryEventOutput,
		OccurredAt: historyKnown(now),
		Model:      historyKnown("claude-sonnet-4-6"),
		Usage:      usage,
	})
	if day.Cost <= 0 || day.costState() != StatsCostPartial {
		t.Fatalf("mixed listed and unlisted Claude cost state = %#v", day)
	}
}

func TestBuildStatsKeepsShapeWhenWorkHistoryFails(t *testing.T) {
	now := time.Now()
	stats := buildStats(context.Background(), &State{}, 3, nil, now, errors.New("index denied"))
	if !strings.Contains(stats.Err, "index denied") {
		t.Fatalf("history error = %q", stats.Err)
	}
	if len(stats.Days) != 3 || len(stats.Hours) != 24 || len(stats.Heatmap) != 7 {
		t.Fatalf("statistics transport shape was lost: %#v", stats)
	}
}

func writeStatsFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func findStatsProvider(t *testing.T, providers []StatsProvider, provider HistoryProvider) StatsProvider {
	t.Helper()
	for _, item := range providers {
		if item.Provider == string(provider) {
			return item
		}
	}
	t.Fatalf("provider %s missing: %#v", provider, providers)
	return StatsProvider{}
}

func findStatsProject(t *testing.T, projects []StatsProject, name string) StatsProject {
	t.Helper()
	for _, project := range projects {
		if project.Name == name {
			return project
		}
	}
	t.Fatalf("project %s missing: %#v", name, projects)
	return StatsProject{}
}
