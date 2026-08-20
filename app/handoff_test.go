package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"magentic/core"
)

func installHandoffFakeTmux(t *testing.T, paneContent, sourceCommand, targetCommand string) string {
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
    printf '%ssource\t%s\t1\n' "$MAGENTIC_HANDOFF_SESSION_PREFIX" "$MAGENTIC_HANDOFF_SOURCE_COMMAND"
    printf '%starget\t%s\t1\n' "$MAGENTIC_HANDOFF_SESSION_PREFIX" "$MAGENTIC_HANDOFF_TARGET_COMMAND"
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
	t.Setenv("MAGENTIC_HANDOFF_SOURCE_COMMAND", sourceCommand)
	t.Setenv("MAGENTIC_HANDOFF_TARGET_COMMAND", targetCommand)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func handoffTestState(t *testing.T, source, target core.Agent) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("MAGENTIC_STATE", statePath)
	st := &core.State{Agents: []core.Agent{source, target}}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
}

func defaultHandoffAgents() (core.Agent, core.Agent) {
	return core.Agent{
			Name:      "source",
			Project:   "magentic",
			Dir:       "/work/magentic-agents/source",
			SessionID: "11111111-2222-4333-8444-555555555555",
		}, core.Agent{
			Name:      "target",
			Project:   "magentic",
			Dir:       "/work/magentic",
			SessionID: "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee",
		}
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
	logPath := installHandoffFakeTmux(t, "Bereit\nshift+tab to cycle", "claude", "claude")
	source, target := defaultHandoffAgents()
	handoffTestState(t, source, target)

	if err := (&App{}).HandoffSession("source", "target"); err != nil {
		t.Fatalf("HandoffSession() error = %v", err)
	}

	wantPrompt := core.BuildSessionHandoffPrompt(source, core.AgentToolClaude)
	wantInput := "\x1b[200~" + strings.ReplaceAll(wantPrompt, "\n", "\r") + "\x1b[201~"
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
	if literalCall[2] != wantTarget || literalCall[4] != wantInput {
		t.Fatalf("literal send-keys = %#v, want target %q and bracketed prompt %q", literalCall, wantTarget, wantInput)
	}
	if enterCall == nil || enterCall[2] != wantTarget {
		t.Fatalf("kein Enter-Aufruf für Ziel-Session gefunden: %#v", parseFakeTmuxCalls(t, logPath))
	}
}

func TestAppHandoffSessionRejectsBlockedTargetWithoutSending(t *testing.T) {
	logPath := installHandoffFakeTmux(t, "Do you want to continue? (y/n)", "claude", "claude")
	source, target := defaultHandoffAgents()
	handoffTestState(t, source, target)

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

func TestAppHandoffSessionAllowsCodexStartedInTerminalAsSource(t *testing.T) {
	logPath := installHandoffFakeTmux(t, "Bereit\nshift+tab to cycle", "codex", "claude")
	source, target := defaultHandoffAgents()
	source.Kind = core.KindTerm
	source.SessionID = ""
	source.Name = "source"
	handoffTestState(t, source, target)

	if err := (&App{}).HandoffSession("source", "target"); err != nil {
		t.Fatalf("HandoffSession() error = %v", err)
	}

	wantPrompt := core.BuildSessionHandoffPrompt(source, core.AgentToolCodex)
	wantInput := "\x1b[200~" + strings.ReplaceAll(wantPrompt, "\n", "\r") + "\x1b[201~"
	var literalCall []string
	for _, call := range parseFakeTmuxCalls(t, logPath) {
		if len(call) == 5 && call[0] == "send-keys" && call[3] == "-l" {
			literalCall = call
		}
	}
	if literalCall == nil || literalCall[4] != wantInput {
		t.Fatalf("Codex-Terminal-Handoff nicht literal gesendet: %#v", literalCall)
	}
	for _, want := range []string{
		`Tool: "codex"`,
		`Magentic-/tmux-Session-ID (Suchreferenz): "` + core.SessionName("source") + `"`,
		`${CODEX_HOME:-~/.codex}/sessions/**/rollout-*.jsonl`,
		`session_meta`,
		`payload.cwd`,
	} {
		if !strings.Contains(literalCall[4], want) {
			t.Errorf("Codex-Handoff-Prompt enthält %q nicht:\n%s", want, literalCall[4])
		}
	}
}

func TestAppHandoffSessionAllowsCodexStartedInTerminalAsTarget(t *testing.T) {
	logPath := installHandoffFakeTmux(t, "Codex ready", "claude", "codex")
	source, target := defaultHandoffAgents()
	target.Kind = core.KindTerm
	target.SessionID = ""
	handoffTestState(t, source, target)

	if err := (&App{}).HandoffSession("source", "target"); err != nil {
		t.Fatalf("HandoffSession() error = %v", err)
	}
	for _, call := range parseFakeTmuxCalls(t, logPath) {
		if len(call) == 5 && call[0] == "send-keys" && call[3] == "-l" && call[2] == core.TargetPane(core.SessionName("target")) {
			return
		}
	}
	t.Fatalf("kein literal Handoff an Codex-Terminal gefunden: %#v", parseFakeTmuxCalls(t, logPath))
}

func TestAppHandoffSessionRejectsPlainTerminalAsSource(t *testing.T) {
	logPath := installHandoffFakeTmux(t, "Shell ready", "zsh", "claude")
	source, target := defaultHandoffAgents()
	source.Kind = core.KindTerm
	source.SessionID = ""
	handoffTestState(t, source, target)

	err := (&App{}).HandoffSession("source", "target")
	if err == nil || !strings.Contains(err.Error(), "reines Terminal") {
		t.Fatalf("HandoffSession() error = %v, want plain terminal error", err)
	}
	assertNoHandoffSend(t, logPath)
}

func TestAppHandoffSessionRejectsPlainTerminalAsTarget(t *testing.T) {
	logPath := installHandoffFakeTmux(t, "Shell ready", "claude", "bash")
	source, target := defaultHandoffAgents()
	target.Kind = core.KindTerm
	target.SessionID = ""
	handoffTestState(t, source, target)

	err := (&App{}).HandoffSession("source", "target")
	if err == nil || !strings.Contains(err.Error(), "reines Terminal") {
		t.Fatalf("HandoffSession() error = %v, want plain terminal error", err)
	}
	assertNoHandoffSend(t, logPath)
}

func assertNoHandoffSend(t *testing.T, logPath string) {
	t.Helper()
	for _, call := range parseFakeTmuxCalls(t, logPath) {
		if len(call) > 0 && call[0] == "send-keys" {
			t.Fatalf("abgelehnte Session erhielt dennoch Eingabe: %#v", call)
		}
	}
}
