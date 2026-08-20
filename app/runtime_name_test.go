package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"magentic/core"

	"github.com/creack/pty"
)

const (
	customSourceRuntime = "mgt-legacy-source-runtime"
	customTargetRuntime = "mgt-legacy-target-runtime"
)

func customRuntimeAgents() (core.Agent, core.Agent) {
	source, target := defaultHandoffAgents()
	source.RuntimeName = customSourceRuntime
	target.RuntimeName = customTargetRuntime
	return source, target
}

func installCustomRuntimeTmux(t *testing.T, paneContent, sourceCommand, targetCommand string) string {
	t.Helper()
	logPath := installHandoffFakeTmux(t, paneContent, sourceCommand, targetCommand)
	t.Setenv("MAGENTIC_HANDOFF_SOURCE_RUNTIME", customSourceRuntime)
	t.Setenv("MAGENTIC_HANDOFF_TARGET_RUNTIME", customTargetRuntime)
	return logPath
}

func TestDiscoverNewUsesRegisteredRuntimeNameAsTmuxIdentity(t *testing.T) {
	installCustomRuntimeTmux(t, "ready", "claude", "claude")
	source, target := customRuntimeAgents()
	state := &core.State{Agents: []core.Agent{source, target}}

	if discovery := core.DiscoverNew(context.Background(), state); len(discovery.Sessions) != 0 {
		t.Fatalf("registered custom runtimes were rediscovered from display names: %#v", discovery.Sessions)
	}
}

func TestSendSkillTargetsRegisteredRuntimeName(t *testing.T) {
	logPath := installCustomRuntimeTmux(t, "Bereit\nshift+tab to cycle", "claude", "claude")
	source, target := customRuntimeAgents()
	handoffTestState(t, source, target)

	if err := newHandoffTestApp().SendSkill(string(source.ID), "/review "); err != nil {
		t.Fatal(err)
	}
	assertLiteralTmuxTarget(t, logPath, core.TargetPane(customSourceRuntime))
	assertNoTmuxTarget(t, logPath, core.TargetPane(core.SessionName(source.Name)))
}

func TestHandoffUsesCustomSourceAndTargetRuntimeNames(t *testing.T) {
	logPath := installCustomRuntimeTmux(t, "Bereit\nshift+tab to cycle", "claude", "claude")
	source, target := customRuntimeAgents()
	handoffTestState(t, source, target)

	if err := newHandoffTestApp().HandoffSession(string(source.ID), string(target.ID)); err != nil {
		t.Fatal(err)
	}
	assertLiteralTmuxTarget(t, logPath, core.TargetPane(customTargetRuntime))
	assertNoTmuxTarget(t, logPath, core.TargetPane(core.SessionName(target.Name)))

	wantSourceID := `Magentic-SessionID: "` + string(source.ID) + `"`
	wantSourceReference := `RuntimeName: "` + customSourceRuntime + `"`
	wantSourcePane := `tmux-Pane-Ziel: "` + core.TargetPane(customSourceRuntime) + `"`
	for _, call := range parseFakeTmuxCalls(t, logPath) {
		if len(call) != 5 || call[0] != "send-keys" || call[3] != "-l" {
			continue
		}
		if !strings.Contains(call[4], wantSourceID) || !strings.Contains(call[4], wantSourceReference) || !strings.Contains(call[4], wantSourcePane) {
			t.Fatalf("handoff prompt did not preserve source RuntimeName:\n%s", call[4])
		}
		if strings.Contains(call[4], `RuntimeName: "`+core.SessionName(source.Name)+`"`) {
			t.Fatalf("handoff prompt recomputed runtime identity from display name:\n%s", call[4])
		}
		return
	}
	t.Fatal("no literal handoff prompt was sent")
}

func TestSessionPreviewAndLinksUseRegisteredRuntimeName(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("CODEX_HOME", filepath.Join(root, "codex"))
	logPath := installCustomRuntimeTmux(t, "custom runtime https://example.test/live", "claude", "claude")
	source, target := customRuntimeAgents()
	handoffTestState(t, source, target)

	app := newHandoffTestApp()
	preview := app.SessionPreview(string(source.ID))
	if !preview.ContentKnown || !strings.Contains(preview.Content, "custom runtime") {
		t.Fatalf("SessionPreview() = %#v", preview)
	}
	links, err := app.SessionLinks(string(source.ID))
	if err != nil {
		t.Fatal(err)
	}
	if len(links.Links) != 1 || links.Links[0].URL != "https://example.test/live" {
		t.Fatalf("SessionLinks() = %#v", links)
	}
	assertNoTmuxTarget(t, logPath, core.TargetPane(core.SessionName(source.Name)))
}

func TestOpenTermBuildsAttachCommandFromRegisteredRuntimeName(t *testing.T) {
	installCustomRuntimeTmux(t, "ready", "claude", "claude")
	source, target := customRuntimeAgents()
	handoffTestState(t, source, target)

	wantStop := errors.New("stop before starting PTY")
	var commandArgs []string
	app := newHandoffTestApp()
	app.startTerm = func(command *exec.Cmd, _ *pty.Winsize) (*os.File, error) {
		commandArgs = append([]string(nil), command.Args...)
		return nil, wantStop
	}
	err := app.OpenTerm(string(source.ID), source.Name, 120, 40)
	if !errors.Is(err, wantStop) {
		t.Fatalf("OpenTerm() error = %v, want injected stop", err)
	}
	wantTarget := core.TargetSession(customSourceRuntime)
	if len(commandArgs) != 4 || commandArgs[0] != "tmux" || commandArgs[1] != "attach-session" || commandArgs[2] != "-t" || commandArgs[3] != wantTarget {
		t.Fatalf("attach command = %#v, want tmux attach-session -t %q", commandArgs, wantTarget)
	}
	if commandArgs[3] == core.TargetSession(core.SessionName(source.Name)) {
		t.Fatal("OpenTerm recomputed runtime identity from display name")
	}
}

func TestOpenTermDoesNotCollapseObservationFailureToMissingSession(t *testing.T) {
	installCustomRuntimeTmux(t, "ready", "claude", "claude")
	t.Setenv("MAGENTIC_HANDOFF_LIST_FAIL", "1")
	source, target := customRuntimeAgents()
	handoffTestState(t, source, target)

	starts := 0
	app := newHandoffTestApp()
	app.startTerm = func(*exec.Cmd, *pty.Winsize) (*os.File, error) {
		starts++
		return nil, errors.New("must not start")
	}
	err := app.OpenTerm(string(source.ID), source.Name, 120, 40)
	if err == nil || !strings.Contains(err.Error(), "nicht verlässlich geprüft") || strings.Contains(err.Error(), "existiert nicht mehr") {
		t.Fatalf("OpenTerm() error = %v, want explicit unknown runtime fact", err)
	}
	if starts != 0 {
		t.Fatalf("OpenTerm crossed PTY Seam %d times despite unavailable Observation", starts)
	}
}

func assertLiteralTmuxTarget(t *testing.T, logPath, want string) {
	t.Helper()
	for _, call := range parseFakeTmuxCalls(t, logPath) {
		if len(call) == 5 && call[0] == "send-keys" && call[2] == want && call[3] == "-l" {
			return
		}
	}
	t.Fatalf("no literal send-keys call targeted %q: %#v", want, parseFakeTmuxCalls(t, logPath))
}

func assertTmuxTarget(t *testing.T, logPath, command, want string) {
	t.Helper()
	for _, call := range parseFakeTmuxCalls(t, logPath) {
		if len(call) == 0 || call[0] != command {
			continue
		}
		for i := 1; i+1 < len(call); i++ {
			if call[i] == "-t" && call[i+1] == want {
				return
			}
		}
	}
	t.Fatalf("no %s call targeted %q: %#v", command, want, parseFakeTmuxCalls(t, logPath))
}

func assertNoTmuxTarget(t *testing.T, logPath, unwanted string) {
	t.Helper()
	for _, call := range parseFakeTmuxCalls(t, logPath) {
		for _, arg := range call {
			if arg == unwanted {
				t.Fatalf("tmux call used recomputed runtime target %q: %#v", unwanted, call)
			}
		}
	}
}
