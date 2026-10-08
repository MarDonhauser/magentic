package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// ompHostStartTimeout bounds how long starting an omp Session waits for its
// agent host. The first start of a new omp build includes the approval-gate
// verification, which runs a real model turn.
var ompHostStartTimeout = 12 * time.Minute

// ompInitialDeliveryTimeout bounds how long an initial prompt waits for omp's
// echo before its delivery is left unconfirmed.
var ompInitialDeliveryTimeout = 30 * time.Second

// ompLifecycleRuntime starts, finds and stops the agent host that owns an omp
// Session's process. It never touches tmux: the host is its own process,
// detached from the daemon so that closing every interface or restarting the
// daemon leaves it running, and addressed only by its recorded socket and
// token.
type ompLifecycleRuntime struct {
	registry *ManagedHostRegistry
	// spawn starts the agent-host process for a Session. It is a Seam so
	// tests can stand in for the Magentic binary; exited reports the host
	// process's end together with what it wrote.
	spawn func(session Session, token AgentHostToken) (exited <-chan string, err error)
}

// defaultOmpLifecycleRuntime is the omp runtime every Lifecycle uses. It is a
// variable so tests can replace it without starting a host.
var defaultOmpLifecycleRuntime lifecycleRuntime = ompLifecycleRuntime{}

func (r ompLifecycleRuntime) hosts() *ManagedHostRegistry {
	if r.registry != nil {
		return r.registry
	}
	return NewManagedHostRegistry()
}

// Exists reports whether a host confirmed by its recorded token owns the
// Session. A record without a confirmable host is not a running Session.
func (r ompLifecycleRuntime) Exists(_ context.Context, session Session) (bool, error) {
	record, found, err := r.hosts().RecordFor(session.ID)
	if err != nil || !found {
		return false, err
	}
	return ConnectAgentHost(record.SocketPath, record.Token) == nil, nil
}

// Start records the host's intent with its exact launch before any process
// exists (ADR 0003), then starts the host and waits until it confirms its
// identity and omp's handshake. A host that exits first — omp missing, an unverified gate, a
// refused handshake — fails the start with the reason it gave.
func (r ompLifecycleRuntime) Start(ctx context.Context, session Session, mode string) error {
	if info, err := os.Stat(session.Dir); err != nil || !info.IsDir() {
		return fmt.Errorf("Session directory %q is unavailable", session.Dir)
	}
	var run *AgentRunRef
	if existing, ok := session.AgentRun(AgentVendorOmp); ok {
		run = &existing
	} else {
		mode = "new"
	}
	argv, err := OmpArgv(session, run, mode)
	if err != nil {
		return err
	}
	token := NewAgentHostToken()
	socketPath := AgentHostSocketPath(session.ID)
	registry := r.hosts()
	if err := registry.RecordLaunchIntent(session.ID, socketPath, token, argv); err != nil {
		return err
	}
	spawn := r.spawn
	if spawn == nil {
		spawn = spawnAgentHostProcess
	}
	exited, err := spawn(session, token)
	if err != nil {
		return fmt.Errorf("Agent-Host konnte nicht gestartet werden: %w", err)
	}

	deadline := time.NewTimer(ompHostStartTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		// The socket answers before omp has sent its ready frame; the
		// Session is usable only once the host reports a live process whose
		// handshake it confirmed.
		if state, err := QueryAgentHostState(socketPath, token); err == nil && state.Alive && state.ProtocolVersion > 0 {
			return registry.MarkStarted(session.ID)
		}
		select {
		case output := <-exited:
			return fmt.Errorf("omp-Session nicht gestartet: %s", strings.TrimSpace(output))
		case <-deadline.C:
			return fmt.Errorf("der Agent-Host meldete sich nicht binnen %s", ompHostStartTimeout)
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// Stop asks the recorded host to stop its process and exit, then forgets it.
// A host that is already gone is simply forgotten; one that answers with a
// different token is neither stopped nor forgotten.
func (r ompLifecycleRuntime) Stop(_ context.Context, session Session) error {
	registry := r.hosts()
	record, found, err := registry.RecordFor(session.ID)
	if err != nil || !found {
		return err
	}
	if err := ShutdownAgentHost(record.SocketPath, record.Token); err != nil {
		if errors.Is(err, ErrAgentHostForeign) {
			return err
		}
		return registry.Forget(session.ID)
	}
	deadline := time.Now().Add(agentHostStopGrace + 2*time.Second)
	for time.Now().Before(deadline) {
		if errors.Is(ConnectAgentHost(record.SocketPath, record.Token), ErrAgentHostUnreachable) {
			return registry.Forget(session.ID)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("der Agent-Host von %s beendete sich nicht", session.Name)
}

// Rename has nothing to do: an omp host is addressed by the Session's
// identity, not by its runtime name.
func (ompLifecycleRuntime) Rename(context.Context, Session, string) error { return nil }

// DeliverInitial hands the initial prompt to the host and confirms it only on
// omp's echo. Without an echo in time the delivery stays unconfirmed rather
// than being assumed.
func (r ompLifecycleRuntime) DeliverInitial(ctx context.Context, session Session, prompt string) (bool, error) {
	record, found, err := r.hosts().RecordFor(session.ID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, fmt.Errorf("für %s ist kein Agent-Host verzeichnet", session.Name)
	}
	messageID := "initial-" + string(session.ID)
	if err := DeliverAgentHostPrompt(record.SocketPath, record.Token, messageID, prompt); err != nil {
		return false, err
	}
	deadline := time.Now().Add(ompInitialDeliveryTimeout)
	for time.Now().Before(deadline) {
		state, err := QueryAgentHostState(record.SocketPath, record.Token)
		if err == nil {
			if state.TurnKnown && state.Turn.MessageID == messageID {
				return true, nil
			}
			if state.FailedMessageID == messageID {
				return false, fmt.Errorf("omp lehnte den Start-Prompt ab: %s", state.DeliveryFailure)
			}
		}
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return false, nil
}

// spawnAgentHostProcess starts `magentic agent-host` for one Session in its
// own session, so it is not a child the daemon or an interface takes down
// when it exits. The token travels in the environment, never on the command
// line. The host's output is collected so a refusal can be reported.
func spawnAgentHostProcess(session Session, token AgentHostToken) (<-chan string, error) {
	binary, err := os.Executable()
	if err != nil {
		return nil, err
	}
	logPath := AgentHostSocketPath(session.ID) + ".log"
	if err := os.MkdirAll(dirOf(logPath), 0o700); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(binary, "agent-host", "--session", string(session.ID), "--dir", session.Dir)
	cmd.Env = append(os.Environ(), "MAGENTIC_AGENT_HOST_TOKEN="+string(token))
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return nil, err
	}
	exited := make(chan string, 1)
	go func() {
		_ = cmd.Wait()
		logFile.Close()
		output, _ := os.ReadFile(logPath)
		exited <- string(output)
	}()
	return exited, nil
}

func dirOf(path string) string {
	if i := strings.LastIndex(path, string(os.PathSeparator)); i > 0 {
		return path[:i]
	}
	return "."
}
