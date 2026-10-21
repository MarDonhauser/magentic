package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"magentic/core"
)

// cliAgentHost is the agent-host mode: the Magentic binary running as one
// managed Session's process owner. The daemon starts it with the Session's
// identity, its working directory, and the token it recorded for this host
// before starting it (per ADR 0003). It owns the vendor process for as long
// as it runs, independent of any daemon connecting or disconnecting.
func cliAgentHost(args []string) {
	fs := flag.NewFlagSet("agent-host", flag.ExitOnError)
	sessionID := fs.String("session", "", "Session-ID, deren Agent-Prozess dieser Host besitzt")
	dir := fs.String("dir", "", "Arbeitsverzeichnis für den Agent-Prozess")
	mode := fs.String("mode", "resume", "\"new\" für eine frische Konversation, sonst wird fortgesetzt")
	fs.Parse(args)

	if *sessionID == "" || *dir == "" {
		fmt.Fprintln(os.Stderr, "magentic agent-host --session <id> --dir <pfad>")
		os.Exit(2)
	}
	token := core.AgentHostToken(os.Getenv("MAGENTIC_AGENT_HOST_TOKEN"))
	if token == "" {
		fmt.Fprintln(os.Stderr, "MAGENTIC_AGENT_HOST_TOKEN fehlt — der Daemon muss das beim Start des Hosts setzen")
		os.Exit(2)
	}

	// An omp host is fully described by the record the daemon wrote before
	// starting it; the Session is registered only after its runtime is up,
	// so it cannot be looked up here yet.
	if record, found, recordErr := core.NewManagedHostRegistry().RecordFor(core.SessionID(*sessionID)); recordErr == nil && found && len(record.LaunchArgv) > 0 {
		os.Exit(runOmpAgentHost(record, token, *dir))
	}

	state, err := core.LoadState()
	if err != nil {
		fmt.Fprintln(os.Stderr, "State konnte nicht geladen werden:", err)
		os.Exit(1)
	}
	session := state.SessionByID(core.SessionID(*sessionID))
	if session == nil {
		fmt.Fprintf(os.Stderr, "Session %q nicht gefunden\n", *sessionID)
		os.Exit(1)
	}
	if session.SessionRuntime() != core.RuntimeManaged {
		fmt.Fprintf(os.Stderr, "Session %q nutzt nicht den managed Runtime\n", *sessionID)
		os.Exit(1)
	}
	if session.SessionVendor() != core.AgentVendorClaude {
		fmt.Fprintf(os.Stderr, "Agent-Vendor %q hat keinen agent-host\n", session.SessionVendor())
		os.Exit(1)
	}

	installedVersion, versionErr := installedClaudeCLIVersion()
	if versionErr != nil {
		fmt.Fprintln(os.Stderr, "Claude-Code-Version konnte nicht ermittelt werden:", versionErr)
		os.Exit(1)
	}
	if ok, reason := core.VerifyClaudeManagedRuntimeVersion(installedVersion); !ok {
		fmt.Fprintln(os.Stderr, reason)
		os.Exit(1)
	}

	host, err := core.StartAgentHost(session.ID, token)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer host.Close()

	run, hasRun := session.AgentRun(core.AgentVendorClaude)
	if !hasRun {
		fmt.Fprintf(os.Stderr, "Session %q hat keine gespeicherte Claude-Run-Referenz\n", *sessionID)
		os.Exit(1)
	}
	argv, err := core.ClaudeManagedArgv(agentApproveMCPConfigPath(session.ID), &run, *mode)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := host.StartVendorProcess("claude", argv, *dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	fmt.Printf("Agent-Host für Session %q hört auf %s\n", *sessionID, host.Path())
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
}

// installedClaudeCLIVersion reports the version Claude Code's own --version
// flag prints, trimmed. It is a Seam only so tests can control it; production
// always asks the real binary.
var installedClaudeCLIVersion = func() (string, error) {
	out, err := exec.Command("claude", "--version").Output()
	if err != nil {
		return "", err
	}
	return firstLine(string(out)), nil
}

// runOmpAgentHost owns one omp Session's process. It launches exactly the
// argument list the daemon recorded before starting this host, and only after
// the installed omp's approval gate is verified; anything it cannot prove
// refuses the Session with the reason and starts nothing.
func runOmpAgentHost(record core.ManagedHostRecord, token core.AgentHostToken, dir string) int {
	if record.Token != token {
		fmt.Fprintf(os.Stderr, "für Session %q ist kein passender Host-Intent verzeichnet\n", record.SessionID)
		return 1
	}
	if !core.OmpLaunchArgvHasGate(record.LaunchArgv) {
		fmt.Fprintf(os.Stderr, "die verzeichneten Startargumente von Session %q erzwingen das Freigabe-Gate nicht\n", record.SessionID)
		return 1
	}
	if cwd := core.OmpLaunchCwd(record.LaunchArgv); cwd != dir {
		fmt.Fprintf(os.Stderr, "Arbeitsverzeichnis %q weicht vom verzeichneten %q ab\n", dir, cwd)
		return 1
	}

	// The verification must not run in any Project or in the Session's own
	// directory; a missing state only narrows what it can rule out.
	roots := []string{dir}
	if state, err := core.LoadState(); err == nil {
		for _, project := range state.Projects {
			roots = append(roots, project.Path)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	result, err := core.EnsureInstalledOmpGateVerified(ctx, roots)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if result.Outcome != core.OmpVerifyOutcomeVerified {
		fmt.Fprintln(os.Stderr, core.OmpGateRefusal(result))
		return 1
	}

	host, err := core.StartAgentHost(record.SessionID, token)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer host.Close()
	if _, err := host.StartOmpProcess(record.LaunchArgv, dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	fmt.Printf("Agent-Host für Session %q hört auf %s\n", record.SessionID, host.Path())
	signals, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-signals.Done():
	case <-host.Done():
	}
	return 0
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}

// agentApproveMCPConfigPath is where the --mcp-config file wiring in the
// agent-approve MCP server for sessionID is written. The agent-approve mode
// (task 5.1) generates this file; the agent-host only needs its path.
func agentApproveMCPConfigPath(sessionID core.SessionID) string {
	return core.AgentHostSocketPath(sessionID) + ".mcp.json"
}
