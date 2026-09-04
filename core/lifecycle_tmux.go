package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// Der tmux-Adapter am Runtime-Seam. Er steht in einer eigenen Datei, weil er
// Transport ist und nicht Lifecycle: was hier liegt, wird beim Wechsel der
// Runtime ersetzt, alles andere in lifecycle.go bleibt.
//
// Am Seam sitzt genau ein Adapter. exactLifecycleRuntime ist kein zweiter,
// sondern der verpflichtende prüfende Dekorator davor: er hält die
// Genauigkeitsregel für RuntimeNames, bevor irgendein externer Prozess
// adressiert wird.

type lifecycleCommandRunner func(context.Context, string, ...string) ([]byte, error)

type tmuxLifecycleRuntime struct {
	command lifecycleCommandRunner
}

func (r tmuxLifecycleRuntime) combinedOutput(ctx context.Context, name string, args ...string) ([]byte, error) {
	if r.command != nil {
		return r.command(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (r tmuxLifecycleRuntime) Exists(ctx context.Context, session Session) (bool, error) {
	out, err := r.combinedOutput(ctx, "tmux", "has-session", "-t", TargetSession(session.TmuxName()))
	if err == nil {
		return true, nil
	}
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && tmuxTargetKnownAbsent(out) {
		return false, nil
	}
	message := strings.TrimSpace(string(out))
	if message == "" {
		return false, fmt.Errorf("observe tmux Session %q: %w", session.TmuxName(), err)
	}
	return false, fmt.Errorf("observe tmux Session %q: %w: %s", session.TmuxName(), err, message)
}

func tmuxTargetKnownAbsent(output []byte) bool {
	message := strings.TrimSuffix(string(output), "\n")
	if !singleLineTmuxDiagnostic(message) {
		return false
	}
	if tmuxServerKnownAbsent(message) {
		return true
	}
	detail, found := strings.CutPrefix(message, "can't find session: ")
	return found && detail != "" && strings.TrimSpace(detail) == detail
}

// tmux meldet einen fehlenden Server je nach errno verschieden: bei ECONNREFUSED
// (Socket-Datei ohne Server) "no server running on …", bei ENOENT (Socket-Datei
// weg, etwa nach einem Reboot) "error connecting to … (No such file or directory)".
func tmuxServerKnownAbsent(output string) bool {
	message := strings.TrimSuffix(output, "\n")
	if !singleLineTmuxDiagnostic(message) {
		return false
	}
	if socket, found := strings.CutPrefix(message, "no server running on "); found {
		return socket != "" && strings.TrimSpace(socket) == socket
	}
	if detail, found := strings.CutPrefix(message, "error connecting to "); found {
		socket, found := strings.CutSuffix(detail, " (No such file or directory)")
		return found && socket != "" && strings.TrimSpace(socket) == socket
	}
	return false
}

func singleLineTmuxDiagnostic(message string) bool {
	return message != "" && !strings.ContainsAny(message, "\r\n") && strings.TrimSpace(message) == message
}

func (tmuxLifecycleRuntime) Start(ctx context.Context, session Session, mode string) error {
	if info, err := os.Stat(session.Dir); err != nil || !info.IsDir() {
		return fmt.Errorf("Session directory %q is unavailable", session.Dir)
	}
	if !session.IsTerm() {
		// The binary check happens before the tmux Session exists, so a
		// missing program leaves nothing behind to clean up.
		provider, err := resolveSessionProvider(session)
		if err != nil {
			return err
		}
		if !providerBinaryAvailable(provider) {
			return fmt.Errorf("%s ist nicht installiert (%s nicht im PATH)", provider.Vendor(), provider.Binary())
		}
	}
	args := tmuxNewSessionArgs(session)
	if out, err := exec.CommandContext(ctx, "tmux", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("tmux new-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	TmuxConfigureUX()
	if session.IsTerm() {
		return nil
	}
	command, err := startCommandForSession(session, mode)
	if err != nil {
		return err
	}
	if _, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", TargetPane(session.TmuxName()), "-l", command).CombinedOutput(); err != nil {
		return fmt.Errorf("start coding agent: %w", err)
	}
	if _, err := exec.CommandContext(ctx, "tmux", "send-keys", "-t", TargetPane(session.TmuxName()), "Enter").CombinedOutput(); err != nil {
		return fmt.Errorf("submit coding-agent command: %w", err)
	}
	return nil
}

// tmuxNewSessionArgs builds the command that creates a Session runtime. Every
// provisioned runtime carries the Magentic environment marker.
func tmuxNewSessionArgs(session Session) []string {
	args := []string{"new-session", "-d", "-s", session.TmuxName(), "-c", session.Dir, "-x", "220", "-y", "50"}
	return append(args, controlEnvironmentArgs(session)...)
}

func (tmuxLifecycleRuntime) Stop(ctx context.Context, session Session) error {
	out, err := exec.CommandContext(ctx, "tmux", "kill-session", "-t", TargetSession(session.TmuxName())).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux kill-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (tmuxLifecycleRuntime) Rename(ctx context.Context, session Session, targetRuntime string) error {
	out, err := exec.CommandContext(
		ctx, "tmux", "rename-session", "-t", TargetSession(session.TmuxName()), targetRuntime,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("tmux rename-session: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (tmuxLifecycleRuntime) DeliverInitial(_ context.Context, session Session, prompt string) (bool, error) {
	if session.IsTerm() {
		return false, errors.New("initial coding prompt cannot be delivered to a terminal Session")
	}
	provider, err := resolveSessionProvider(session)
	if err != nil {
		return false, err
	}
	// enqueuePrompt confirms only in-process scheduling. The durable state
	// therefore remains delivery_unknown until a future observation can prove
	// acceptance; reconciliation intentionally does not submit it again.
	if err := enqueuePrompt(promptTargetForSession(session), prompt, true, provider.Tool(), true, true, false, nil); err != nil {
		return false, err
	}
	return false, nil
}
