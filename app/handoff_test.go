package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"magentic/core"
)

func installHandoffFakeTmux(t *testing.T, paneContent string) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tmux-args.log")
	script := `#!/bin/sh
for arg in "$@"; do
  printf '%s\037' "$arg" >> "$MAGENTIC_HANDOFF_TMUX_LOG"
done
printf '\036' >> "$MAGENTIC_HANDOFF_TMUX_LOG"

case "$1" in
  list-panes)
    printf '%ssource\tclaude\t1\n' "$MAGENTIC_HANDOFF_SESSION_PREFIX"
    printf '%starget\tclaude\t1\n' "$MAGENTIC_HANDOFF_SESSION_PREFIX"
    ;;
  capture-pane)
    printf '%s\n' "$MAGENTIC_HANDOFF_PANE_CONTENT"
    ;;
esac
`
	tmuxPath := filepath.Join(dir, "tmux")
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAGENTIC_HANDOFF_TMUX_LOG", logPath)
	t.Setenv("MAGENTIC_HANDOFF_PANE_CONTENT", paneContent)
	t.Setenv("MAGENTIC_HANDOFF_SESSION_PREFIX", core.SessionPrefix)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func handoffTestState(t *testing.T) core.Agent {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("MAGENTIC_STATE", statePath)
	source := core.Agent{
		Name:      "source",
		Project:   "magentic",
		Dir:       "/work/magentic-agents/source",
		SessionID: "11111111-2222-4333-8444-555555555555",
	}
	st := &core.State{Agents: []core.Agent{
		source,
		{Name: "target", Project: "magentic", Dir: "/work/magentic", SessionID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"},
	}}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	return source
}

func parseFakeTmuxCalls(t *testing.T, path string) [][]string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	for _, callRaw := range strings.Split(string(raw), "\x1e") {
		callRaw = strings.TrimSuffix(callRaw, "\x1f")
		if callRaw == "" {
			continue
		}
		calls = append(calls, strings.Split(callRaw, "\x1f"))
	}
	return calls
}

func TestAppHandoffSessionSendsPromptAsOneLiteralTmuxArgument(t *testing.T) {
	logPath := installHandoffFakeTmux(t, "Bereit\nshift+tab to cycle")
	source := handoffTestState(t)

	if err := (&App{}).HandoffSession("source", "target"); err != nil {
		t.Fatalf("HandoffSession() error = %v", err)
	}

	wantPrompt := core.BuildSessionHandoffPrompt(source, core.AgentToolClaude)
	var literalCall, enterCall []string
	for _, call := range parseFakeTmuxCalls(t, logPath) {
		if len(call) == 5 && call[0] == "send-keys" && call[3] == "-l" {
			literalCall = call
		}
		if len(call) == 4 && call[0] == "send-keys" && call[3] == "Enter" {
			enterCall = call
		}
	}
	if literalCall == nil {
		t.Fatalf("kein literal send-keys-Aufruf gefunden: %#v", parseFakeTmuxCalls(t, logPath))
	}
	wantTarget := core.TargetPane(core.SessionName("target"))
	if literalCall[2] != wantTarget || literalCall[4] != wantPrompt {
		t.Fatalf("literal send-keys = %#v, want target %q and prompt %q", literalCall, wantTarget, wantPrompt)
	}
	if enterCall == nil || enterCall[2] != wantTarget {
		t.Fatalf("kein Enter-Aufruf für Ziel-Session gefunden: %#v", parseFakeTmuxCalls(t, logPath))
	}
}

func TestAppHandoffSessionRejectsBlockedTargetWithoutSending(t *testing.T) {
	logPath := installHandoffFakeTmux(t, "Do you want to continue? (y/n)")
	handoffTestState(t)

	err := (&App{}).HandoffSession("source", "target")
	if err == nil || !strings.Contains(err.Error(), "wartet auf eine Antwort") {
		t.Fatalf("HandoffSession() error = %v, want blocked error", err)
	}
	for _, call := range parseFakeTmuxCalls(t, logPath) {
		if len(call) > 0 && call[0] == "send-keys" {
			t.Fatalf("blockiertes Ziel erhielt dennoch Eingabe: %#v", call)
		}
	}
}
