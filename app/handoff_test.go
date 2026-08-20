package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"magentic/core"
)

func installHandoffFakeTmux(t *testing.T, paneContent, sourceCommand, targetCommand string) string {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "tmux-args.log")
	listCountPath := filepath.Join(dir, "list-count")
	script := `#!/bin/sh
for arg in "$@"; do
  printf '%s\037' "$arg" >> "$MAGENTIC_HANDOFF_TMUX_LOG"
done
printf '\036' >> "$MAGENTIC_HANDOFF_TMUX_LOG"

case "$1" in
  list-panes)
	count=0
	if [ -f "$MAGENTIC_HANDOFF_LIST_COUNT" ]; then
	  read -r count < "$MAGENTIC_HANDOFF_LIST_COUNT"
	fi
	count=$((count + 1))
	printf '%s\n' "$count" > "$MAGENTIC_HANDOFF_LIST_COUNT"
	target_command="$MAGENTIC_HANDOFF_TARGET_COMMAND"
	if [ -n "$MAGENTIC_HANDOFF_TARGET_COMMAND_AFTER" ] && [ "$count" -ge "${MAGENTIC_HANDOFF_TARGET_SWITCH_AT:-2}" ]; then
	  target_command="$MAGENTIC_HANDOFF_TARGET_COMMAND_AFTER"
	fi
    printf '%ssource\t%s\t1\n' "$MAGENTIC_HANDOFF_SESSION_PREFIX" "$MAGENTIC_HANDOFF_SOURCE_COMMAND"
    printf '%starget\t%s\t1\n' "$MAGENTIC_HANDOFF_SESSION_PREFIX" "$target_command"
    ;;
  capture-pane)
    printf '%s\n' "$MAGENTIC_HANDOFF_PANE_CONTENT"
    ;;
  send-keys)
	if [ -n "$MAGENTIC_HANDOFF_SEND_DELAY" ]; then
	  sleep "$MAGENTIC_HANDOFF_SEND_DELAY"
	fi
	;;
esac
`
	tmuxPath := filepath.Join(dir, "tmux")
	if err := os.WriteFile(tmuxPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MAGENTIC_HANDOFF_TMUX_LOG", logPath)
	t.Setenv("MAGENTIC_HANDOFF_LIST_COUNT", listCountPath)
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
	source.SessionID = "stale-claude-session-id"
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
	if strings.Contains(literalCall[4], source.SessionID) {
		t.Fatalf("Codex-Terminal-Handoff enthält alte Claude-ID: %s", literalCall[4])
	}
	for _, want := range []string{
		`Tool: "codex"`,
		`Gespeicherte Provider-/CLI-Session-ID: "(nicht gespeichert`,
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

func TestAppHandoffSessionRejectsUnknownLiveTargetDespiteStoredSessionID(t *testing.T) {
	logPath := installHandoffFakeTmux(t, "Ready", "claude", "node")
	source, target := defaultHandoffAgents()
	handoffTestState(t, source, target)

	err := (&App{}).HandoffSession("source", "target")
	if err == nil || !strings.Contains(err.Error(), "unterstütztes KI-Tool") {
		t.Fatalf("HandoffSession() error = %v, want unsupported live target error", err)
	}
	assertNoHandoffSend(t, logPath)
}

func TestAppHandoffSessionRevalidatesLiveToolBeforeLiteralSend(t *testing.T) {
	logPath := installHandoffFakeTmux(t, "Bereit\nshift+tab to cycle", "claude", "claude")
	t.Setenv("MAGENTIC_HANDOFF_TARGET_COMMAND_AFTER", "bash")
	t.Setenv("MAGENTIC_HANDOFF_TARGET_SWITCH_AT", "2")
	source, target := defaultHandoffAgents()
	handoffTestState(t, source, target)

	err := (&App{}).HandoffSession("source", "target")
	if err == nil || !strings.Contains(err.Error(), "kein unterstütztes KI-Tool mehr") {
		t.Fatalf("HandoffSession() error = %v, want live revalidation error", err)
	}
	assertNoHandoffSend(t, logPath)
}

func TestAppHandoffSessionRevalidatesLiveToolBeforeEnter(t *testing.T) {
	logPath := installHandoffFakeTmux(t, "Bereit\nshift+tab to cycle", "claude", "claude")
	t.Setenv("MAGENTIC_HANDOFF_TARGET_COMMAND_AFTER", "bash")
	t.Setenv("MAGENTIC_HANDOFF_TARGET_SWITCH_AT", "3")
	source, target := defaultHandoffAgents()
	handoffTestState(t, source, target)

	err := (&App{}).HandoffSession("source", "target")
	if err == nil || !strings.Contains(err.Error(), "kein unterstütztes KI-Tool mehr") {
		t.Fatalf("HandoffSession() error = %v, want live revalidation error", err)
	}
	literal, enter := handoffSendCounts(t, logPath)
	if literal != 1 || enter != 0 {
		t.Fatalf("send-keys counts = literal %d, Enter %d; want 1, 0", literal, enter)
	}
}

func TestAppHandoffSessionDeduplicatesPendingPromptPerTarget(t *testing.T) {
	logPath := installHandoffFakeTmux(t, "Bereit\nshift+tab to cycle", "claude", "claude")
	t.Setenv("MAGENTIC_HANDOFF_SEND_DELAY", "0.2")
	source, target := defaultHandoffAgents()
	handoffTestState(t, source, target)

	firstDone := make(chan error, 1)
	go func() {
		firstDone <- (&App{}).HandoffSession("source", "target")
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		literal, _ := handoffSendCounts(t, logPath)
		if literal > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("first handoff did not reach literal send")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := (&App{}).HandoffSession("source", "target"); err != nil {
		t.Fatalf("deduplicated HandoffSession() error = %v", err)
	}
	if err := <-firstDone; err != nil {
		t.Fatalf("first HandoffSession() error = %v", err)
	}
	literal, enter := handoffSendCounts(t, logPath)
	if literal != 1 || enter != 1 {
		t.Fatalf("send-keys counts = literal %d, Enter %d; want one deduplicated handoff", literal, enter)
	}
}

func handoffSendCounts(t *testing.T, logPath string) (literal, enter int) {
	t.Helper()
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return 0, 0
	} else if err != nil {
		t.Fatal(err)
	}
	for _, call := range parseFakeTmuxCalls(t, logPath) {
		if len(call) == 5 && call[0] == "send-keys" && call[3] == "-l" {
			literal++
		}
		if len(call) == 4 && call[0] == "send-keys" && call[3] == "Enter" {
			enter++
		}
	}
	return literal, enter
}

func assertNoHandoffSend(t *testing.T, logPath string) {
	t.Helper()
	for _, call := range parseFakeTmuxCalls(t, logPath) {
		if len(call) > 0 && call[0] == "send-keys" {
			t.Fatalf("abgelehnte Session erhielt dennoch Eingabe: %#v", call)
		}
	}
}
