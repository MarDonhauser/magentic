package main

import (
	"os"
	"path/filepath"
	"reflect"
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

func TestScanCodexPrompts(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "rollout.jsonl")
	data := "" +
		`{"type":"session_meta","timestamp":"2026-08-20T08:00:00Z","payload":{"cwd":"/work/reqpilot","session_id":"codex-1"}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-08-20T08:01:00Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions\\n\\n<INSTRUCTIONS>…</INSTRUCTIONS>"},{"type":"input_text","text":"<environment_context>…</environment_context>"}]}}` + "\n" +
		`{"type":"response_item","timestamp":"2026-08-20T08:02:00Z","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<image name=x>"},{"type":"input_text","text":"</image>"},{"type":"input_text","text":"[Image #1] Bitte Codex ergänzen"}]}}` + "\n" +
		`{"type":"event_msg","timestamp":"2026-08-20T08:02:00Z","payload":{"type":"user_message","message":"[Image #1] Bitte Codex ergänzen"}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := newTimelineContext(&core.State{
		Projects: []core.Project{{Name: "ReqPilot", Path: "/work/reqpilot"}},
		Agents:   []core.Agent{{Name: "codex-agent", Project: "ReqPilot", Dir: "/work/reqpilot", SessionID: "codex-1"}},
	})
	got := scanCodexPrompts(path, ctx, time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC))
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1: %#v", len(got), got)
	}
	if got[0].Source != timelineSourceCodex || got[0].Project != "ReqPilot" || got[0].Agent != "codex-agent" || got[0].Text != "Bitte Codex ergänzen" {
		t.Fatalf("unexpected entry: %#v", got[0])
	}
}

func TestScanGeminiPrompts(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, sha256Path("/work/gemini-project"), "chats")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(projectDir, "session-test.json")
	data := `{"sessionId":"gemini-1","messages":[` +
		`{"type":"user","timestamp":"2026-08-20T09:00:00Z","content":"Gemini-Aufgabe"},` +
		`{"type":"gemini","timestamp":"2026-08-20T09:00:01Z","content":"Antwort"}]}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := newTimelineContext(&core.State{Projects: []core.Project{{Name: "Gemini-Projekt", Path: "/work/gemini-project"}}})
	got := scanGeminiPrompts(path, root, ctx, time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC))
	if len(got) != 1 || got[0].Source != timelineSourceGemini || got[0].Project != "Gemini-Projekt" || got[0].Text != "Gemini-Aufgabe" {
		t.Fatalf("unexpected entries: %#v", got)
	}
}

func TestScanCopilotPromptsSkipsChildAgents(t *testing.T) {
	sessionDir := filepath.Join(t.TempDir(), "copilot-1")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sessionDir, "workspace.yaml"), []byte("cwd: /work/copilot-project\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data := "" +
		`{"type":"user.message","timestamp":"2026-08-20T10:00:00Z","data":{"content":"Subagent-Auftrag","parentAgentTaskId":"child-1"}}` + "\n" +
		`{"type":"user.message","timestamp":"2026-08-20T10:01:00Z","data":{"content":"","transformedContent":"Copilot-Aufgabe\n<system_reminder>interne Hinweise</system_reminder>"}}` + "\n"
	path := filepath.Join(sessionDir, "events.jsonl")
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := newTimelineContext(&core.State{Projects: []core.Project{{Name: "Copilot-Projekt", Path: "/work/copilot-project"}}})
	got := scanCopilotPrompts(path, ctx, time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC))
	if len(got) != 1 || got[0].Source != timelineSourceCopilot || got[0].Project != "Copilot-Projekt" || got[0].Text != "Copilot-Aufgabe" {
		t.Fatalf("unexpected entries: %#v", got)
	}
}

func TestTimelineCollectsConfiguredCodexHome(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, "custom-codex")
	sessionDir := filepath.Join(codexHome, "sessions", "recent")
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	data := `{"type":"session_meta","timestamp":"` + now.Add(-time.Minute).Format(time.RFC3339Nano) + `","payload":{"cwd":"/work/codex"}}` + "\n" +
		`{"type":"event_msg","timestamp":"` + now.Format(time.RFC3339Nano) + `","payload":{"type":"user_message","message":"Prompt aus Codex"}}` + "\n"
	if err := os.WriteFile(filepath.Join(sessionDir, "rollout.jsonl"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("MAGENTIC_STATE", filepath.Join(home, "state.json"))

	got, err := (&App{}).Timeline()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Source != timelineSourceCodex || got[0].Text != "Prompt aus Codex" {
		t.Fatalf("unexpected entries: %#v", got)
	}
}
