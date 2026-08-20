package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"magentic/core"
)

func TestExtractURLs(t *testing.T) {
	in := "Siehe [Doku](https://example.com/a) und **https://foo.bar/x**, dann http://localhost:5173.\n" +
		"Code: `https://code.example/y` und (https://z.de/p). Kein Link: https:// oder http://x"
	want := []string{
		"https://example.com/a",
		"https://foo.bar/x",
		"http://localhost:5173",
		"https://code.example/y",
		"https://z.de/p",
	}
	got := extractURLs(in)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("extractURLs:\n got %v\nwant %v", got, want)
	}
}

func TestResolveWorktreeTargetDeniesUnknownRepositoryFacts(t *testing.T) {
	_, _, _, statePath := configureHistoryAppTest(t)
	unknownPath := filepath.Join(t.TempDir(), "missing-repository")
	writeAppState(t, statePath, core.State{Projects: []core.Project{{ID: core.ProjectID("unknown"), Name: "Unknown", Path: unknownPath}}})
	if _, _, err := resolveWorktreeTarget(context.Background(), "unknown", "wt_untrusted"); err == nil {
		t.Fatal("unknown repository topology authorized a Worktree reference")
	}
}

func TestTimelineUsesNormalizedWorkHistoryForAllProviders(t *testing.T) {
	home, codexHome, projectPath, statePath := configureHistoryAppTest(t)
	state := core.State{
		Projects: []core.Project{{ID: core.ProjectID("project-id"), Name: "Stable project", Path: projectPath}},
		Agents: []core.Session{
			{ID: core.SessionID("claude-session"), Name: "Claude agent", ProjectID: core.ProjectID("project-id"), Project: "stale", Dir: projectPath, AgentRuns: []core.AgentRunRef{{Vendor: core.AgentVendorClaude, ExternalID: "claude-run"}}},
			{ID: core.SessionID("codex-session"), Name: "Codex agent", ProjectID: core.ProjectID("project-id"), Project: "stale", Dir: projectPath, AgentRuns: []core.AgentRunRef{{Vendor: core.AgentVendorCodex, ExternalID: "codex-run"}}},
			{ID: core.SessionID("gemini-session"), Name: "Gemini agent", ProjectID: core.ProjectID("project-id"), Project: "stale", Dir: projectPath, AgentRuns: []core.AgentRunRef{{Vendor: core.AgentVendorGemini, ExternalID: "gemini-run"}}},
			{ID: core.SessionID("copilot-session"), Name: "Copilot agent", ProjectID: core.ProjectID("project-id"), Project: "stale", Dir: projectPath, AgentRuns: []core.AgentRunRef{{Vendor: core.AgentVendorCopilot, ExternalID: "copilot-run"}}},
		},
	}
	writeAppState(t, statePath, state)

	now := time.Now().UTC()
	claudeAt := now.Add(-4 * time.Minute).Format(time.RFC3339Nano)
	codexAt := now.Add(-3 * time.Minute).Format(time.RFC3339Nano)
	geminiAt := now.Add(-2 * time.Minute).Format(time.RFC3339Nano)
	copilotAt := now.Add(-time.Minute).Format(time.RFC3339Nano)
	writeAppFixture(t, filepath.Join(home, ".claude", "projects", "stable", "claude-run.jsonl"), "{malformed}\n"+`{"type":"user","timestamp":"`+claudeAt+`","cwd":"`+projectPath+`","sessionId":"claude-run","message":{"content":"Claude prompt"}}`+"\n")
	writeAppFixture(t, filepath.Join(codexHome, "sessions", "recent", "codex-run.jsonl"), strings.Join([]string{
		`{"type":"session_meta","timestamp":"` + codexAt + `","payload":{"id":"codex-run","cwd":"` + projectPath + `"}}`,
		`{"type":"event_msg","timestamp":"` + codexAt + `","payload":{"type":"user_message","message":"[Image #1] Codex prompt"}}`,
		`{"type":"response_item","timestamp":"` + codexAt + `","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"[Image #1] Codex prompt"}]}}`,
	}, "\n")+"\n")
	writeAppFixture(t, filepath.Join(home, ".gemini", "tmp", "stable", "chats", "session-gemini-run.json"), `{"sessionId":"gemini-run","messages":[{"type":"user","timestamp":"`+geminiAt+`","content":"Gemini prompt"}]}`)
	copilotDir := filepath.Join(home, ".copilot", "session-state", "copilot-run")
	writeAppFixture(t, filepath.Join(copilotDir, "workspace.yaml"), "cwd: "+projectPath+"\n")
	writeAppFixture(t, filepath.Join(copilotDir, "events.jsonl"), strings.Join([]string{
		`{"id":"child","type":"user.message","timestamp":"` + copilotAt + `","data":{"content":"delegated prompt","parentAgentTaskId":"child-run"}}`,
		`{"id":"primary","type":"user.message","timestamp":"` + copilotAt + `","data":{"content":"Copilot prompt"}}`,
	}, "\n")+"\n")

	got, err := (&App{}).Timeline()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 4 {
		t.Fatalf("Timeline entries = %d, want four normalized primary prompts: %#v", len(got.Entries), got)
	}
	bySource := map[string]TimelineEntry{}
	for _, entry := range got.Entries {
		bySource[entry.Source] = entry
		if entry.Project != "Stable project" {
			t.Fatalf("Timeline used stale name/path inference: %#v", entry)
		}
	}
	if len(got.Sources) != 4 {
		t.Fatalf("Timeline coverage = %#v, want all four providers", got.Sources)
	}
	want := map[string]struct{ agent, text string }{
		timelineSourceClaude:  {"Claude agent", "Claude prompt"},
		timelineSourceCodex:   {"Codex agent", "Codex prompt"},
		timelineSourceGemini:  {"Gemini agent", "Gemini prompt"},
		timelineSourceCopilot: {"Copilot agent", "Copilot prompt"},
	}
	for source, expected := range want {
		entry, ok := bySource[source]
		if !ok || entry.Agent != expected.agent || entry.Text != expected.text {
			t.Fatalf("%s entry = %#v", source, entry)
		}
	}
	if strings.Contains(strings.Join([]string{got.Entries[0].Text, got.Entries[1].Text, got.Entries[2].Text, got.Entries[3].Text}, "|"), "delegated") {
		t.Fatalf("Timeline included delegated coding-agent work: %#v", got)
	}
	hits, err := (&App{}).SearchTranscripts("prompt")
	if err != nil {
		t.Fatal(err)
	}
	providers := map[string]bool{}
	for _, hit := range hits {
		providers[hit.Provider] = true
		if !hit.ProjectKnown || hit.Project != "Stable project" {
			t.Fatalf("search attribution = %#v", hit)
		}
	}
	for _, provider := range []string{"Claude Code", "Codex", "Gemini CLI", "GitHub Copilot"} {
		if !providers[provider] {
			t.Fatalf("SearchTranscripts omitted %s: %#v", provider, hits)
		}
	}
}

func TestStoredLinksAndSearchUseWorkHistory(t *testing.T) {
	home, _, projectPath, statePath := configureHistoryAppTest(t)
	state := core.State{
		Projects: []core.Project{{ID: core.ProjectID("project-id"), Name: "Search project", Path: projectPath}},
		Agents: []core.Session{{
			ID: core.SessionID("session-id"), Name: "history-cutover-test-session", ProjectID: core.ProjectID("project-id"), Project: "stale", Dir: projectPath,
			AgentRuns: []core.AgentRunRef{{Vendor: core.AgentVendorClaude, ExternalID: "claude-run"}},
		}},
	}
	writeAppState(t, statePath, state)
	now := time.Now().UTC()
	writeAppFixture(t, filepath.Join(home, ".claude", "projects", "stable", "claude-run.jsonl"), strings.Join([]string{
		`{"type":"user","timestamp":"` + now.Add(-time.Minute).Format(time.RFC3339Nano) + `","cwd":"` + projectPath + `","sessionId":"claude-run","message":{"content":"find this normalized phrase"}}`,
		`{"type":"assistant","timestamp":"` + now.Format(time.RFC3339Nano) + `","cwd":"` + projectPath + `","sessionId":"claude-run","message":{"content":"See https://example.test/result"}}`,
	}, "\n")+"\n")

	hits, err := (&App{}).SearchTranscripts("normalized phrase")
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 1 || hits[0].Project != "Search project" || hits[0].Role != "user" || !strings.Contains(hits[0].Full, "normalized phrase") {
		t.Fatalf("normalized search hits = %#v", hits)
	}
	links, err := (&App{}).SessionLinks("history-cutover-test-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 1 || links[0].URL != "https://example.test/result" || links[0].Time == "" {
		t.Fatalf("normalized stored links = %#v", links)
	}
}

func configureHistoryAppTest(t *testing.T) (home, codexHome, projectPath, statePath string) {
	t.Helper()
	root := t.TempDir()
	home = filepath.Join(root, "home")
	codexHome = filepath.Join(root, "configured-codex")
	projectPath = filepath.Join(root, "project")
	statePath = filepath.Join(root, "config", "state.json")
	if err := os.MkdirAll(projectPath, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("MAGENTIC_STATE", statePath)
	return home, codexHome, projectPath, statePath
}

func writeAppState(t *testing.T, path string, state core.State) {
	t.Helper()
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	writeAppFixture(t, path, string(data))
}

func writeAppFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
