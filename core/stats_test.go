package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestOwnCommit(t *testing.T) {
	const email = "martin.donhauser@lhind.dlh.de"
	const name = "donhauser, martin"

	cases := []struct {
		label  string
		cEmail string
		cName  string
		want   bool
	}{
		{"eigene Mail", "martin.donhauser@lhind.dlh.de", "DONHAUSER, MARTIN", true},
		{"Mail in anderer Schreibweise", "Martin.Donhauser@LHIND.dlh.de", "irgendwer", true},
		{"nur Name passt", "privat@example.com", "DONHAUSER, MARTIN", true},
		{"fremder Commit", "kai@example.com", "Kai Detmers", false},
		{"Leerzeichen drumherum", " martin.donhauser@lhind.dlh.de ", "x", true},
	}
	for _, test := range cases {
		if got := ownCommit(test.cEmail, test.cName, email, name); got != test.want {
			t.Errorf("%s: %v, erwartet %v", test.label, got, test.want)
		}
	}
}

func TestOwnCommitOhneIdentitaet(t *testing.T) {
	if !ownCommit("wer@auch.immer", "Wer Auch Immer", "", "") {
		t.Fatal("ohne konfigurierte Identität muss alles zählen")
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
	codexPromptAt := now.Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	codexOutputAt := now.Add(-time.Hour + time.Minute).UTC().Format(time.RFC3339Nano)
	codexUsageAt := now.Add(-time.Hour + 2*time.Minute).UTC().Format(time.RFC3339Nano)

	writeStatsFixture(t, filepath.Join(home, ".claude", "projects", "project", "claude-run.jsonl"), strings.Join([]string{
		"{malformed}",
		`{"type":"user","timestamp":"` + claudePromptAt + `","cwd":"` + projectPath + `","sessionId":"claude-run","message":{"content":"Claude statistics prompt"}}`,
		`{"type":"assistant","timestamp":"` + claudeOutputAt + `","cwd":"` + projectPath + `","sessionId":"claude-run","message":{"model":"claude-opus-4-8","content":"Claude statistics output","usage":{"input_tokens":10,"output_tokens":5,"cache_read_input_tokens":2,"cache_creation_input_tokens":1}}}`,
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
	if len(stats.Projects) != 1 || stats.Projects[0].Name != "Durable project" || stats.Projects[0].Prompts != 2 || stats.Projects[0].Sessions != 2 || stats.Projects[0].Tokens != 48 || stats.Projects[0].Active != 2 {
		t.Fatalf("stable Registry project attribution = %#v", stats.Projects)
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
