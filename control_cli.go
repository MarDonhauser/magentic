package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"magentic/core"
)

// Exit codes of the control surface. They separate an address the caller got
// wrong from a request the surface refused or could not carry out.
const (
	controlExitOK         = 0
	controlExitRefused    = 1
	controlExitAddressing = 2
)

// controlCLI is the io a `magentic session` invocation writes to. The
// machine-readable document goes to stdout alone; everything else to stderr.
type controlCLI struct {
	stdout io.Writer
	stderr io.Writer
	// client is the only path this command has to Magentic. It reaches no
	// Registry, no tmux, and no Git on its own.
	client *core.ControlClient
}

// cliSession runs `magentic session <verb>`.
func cliSession(args []string) {
	os.Exit(runControlSession(controlCLI{stdout: os.Stdout, stderr: os.Stderr}, args))
}

func runControlSession(cli controlCLI, args []string) int {
	if len(args) == 0 {
		fmt.Fprintf(cli.stderr, "magentic session <verb> — bekannt sind: %s.\n", controlVerbNames())
		return controlExitAddressing
	}
	verb := args[0]
	switch verb {
	case "-h", "--help", "help":
		fmt.Fprint(cli.stderr, controlSessionHelp())
		return controlExitOK
	}
	request, machine, socket, failure := parseControlInvocation(cli, verb, args[1:])
	if failure != controlExitOK {
		return failure
	}
	if cli.client == nil {
		cli.client = core.NewControlClient(socket)
	}
	ctx := context.Background()
	if request.Verb == core.ControlSessionWatch {
		return renderControlWatch(cli, ctx, request, machine)
	}
	response := cli.client.Call(ctx, request)
	return renderControlResponse(cli, response, machine)
}

// parseControlInvocation marshals the arguments of one verb into a request.
func parseControlInvocation(cli controlCLI, verb string, args []string) (core.ControlRequest, bool, string, int) {
	set := flag.NewFlagSet("magentic session "+verb, flag.ContinueOnError)
	set.SetOutput(cli.stderr)
	machine := set.Bool("json", false, "Ein einzelnes JSON-Dokument auf die Standardausgabe schreiben")
	socket := set.String("socket", "", "Abweichende Sitzungs-Adresse der Steuer-API")
	var request core.ControlRequest
	switch verb {
	case "start":
		set.StringVar(&request.Args.Project, "project", "", "Projekt (ProjectID oder Projektname)")
		set.StringVar(&request.Args.Name, "name", "", "Name der neuen Session")
		vendor := set.String("vendor", "", "Agent-Art, etwa claude oder codex")
		terminal := set.Bool("terminal", false, "Eine Terminal-Session ohne Coding-Agent starten")
		set.StringVar(&request.Args.Worktree, "worktree", "", "Bestehender Worktree des Projekts (Handle)")
		set.BoolVar(&request.Args.NewWorktree, "new-worktree", false, "Einen frischen verwalteten Worktree anlegen")
		set.StringVar(&request.Args.Directory, "dir", "", "Verzeichnis, das zu einem Worktree des Projekts gehören muss")
		set.StringVar(&request.Args.Prompt, "prompt", "", "Erster Prompt an den Coding-Agent")
		if err := set.Parse(args); err != nil {
			return request, false, "", controlExitAddressing
		}
		request.Verb = core.ControlSessionStart
		request.Args.Vendor = core.AgentVendor(*vendor)
		if *terminal {
			request.Args.Kind = core.SessionKindTerminal
		}
	case "list":
		set.StringVar(&request.Args.Project, "project", "", "Nur Sessions dieses Projekts auflisten")
		set.StringVar(&request.Args.Worktree, "worktree", "", "Nur Sessions dieses Worktrees auflisten (Handle)")
		if err := set.Parse(args); err != nil {
			return request, false, "", controlExitAddressing
		}
		request.Verb = core.ControlSessionList
	case "send":
		set.StringVar(&request.Args.Session, "session", "", "SessionID oder Name")
		set.StringVar(&request.Args.Project, "project", "", "Projekt, das einen Namen eindeutig macht")
		set.StringVar(&request.Args.Text, "text", "", "Zu sendender Text")
		if err := set.Parse(args); err != nil {
			return request, false, "", controlExitAddressing
		}
		request.Verb = core.ControlSessionSend
		if request.Args.Text == "" && set.NArg() > 0 {
			request.Args.Text = strings.Join(set.Args(), " ")
		}
	case "output":
		set.StringVar(&request.Args.Session, "session", "", "SessionID oder Name")
		set.StringVar(&request.Args.Project, "project", "", "Projekt, das einen Namen eindeutig macht")
		set.IntVar(&request.Args.Lines, "lines", 0, "Nur so viele letzte Zeilen zurückgeben")
		if err := set.Parse(args); err != nil {
			return request, false, "", controlExitAddressing
		}
		request.Verb = core.ControlSessionOutput
	case "wait":
		set.StringVar(&request.Args.Session, "session", "", "SessionID oder Name")
		set.StringVar(&request.Args.Project, "project", "", "Projekt, das einen Namen eindeutig macht")
		set.StringVar(&request.Args.Until, "until", "done", "Wartebedingung: done oder waiting")
		seconds := set.Int("timeout", 0, "Zeitgrenze in Sekunden, 0 wartet ohne Grenze")
		if err := set.Parse(args); err != nil {
			return request, false, "", controlExitAddressing
		}
		request.Verb = core.ControlSessionWait
		request.Args.TimeoutMS = *seconds * 1000
	case "kill":
		set.StringVar(&request.Args.Session, "session", "", "SessionID oder Name")
		set.StringVar(&request.Args.Project, "project", "", "Projekt, das einen Namen eindeutig macht")
		if err := set.Parse(args); err != nil {
			return request, false, "", controlExitAddressing
		}
		request.Verb = core.ControlSessionKill
	case "whoami":
		if err := set.Parse(args); err != nil {
			return request, false, "", controlExitAddressing
		}
		request.Verb = core.ControlSessionWhoami
		request.Args.Marker = core.ControlMarkerFromEnvironment()
	case "watch":
		set.StringVar(&request.Args.Project, "project", "", "Nur Ereignisse dieses Projekts empfangen")
		set.StringVar(&request.Args.Session, "session", "", "Nur Ereignisse dieser Session empfangen")
		if err := set.Parse(args); err != nil {
			return request, false, "", controlExitAddressing
		}
		request.Verb = core.ControlSessionWatch
	default:
		fmt.Fprintf(cli.stderr, "Unbekanntes Verb %q — bekannt sind: %s.\n", verb, controlVerbNames())
		return request, false, "", controlExitAddressing
	}
	return request, *machine, *socket, controlExitOK
}

// renderControlResponse writes exactly one document in machine-readable mode
// and a readable line otherwise, and turns the outcome into an exit code.
func renderControlResponse(cli controlCLI, response core.ControlResponse, machine bool) int {
	if machine {
		encoded, err := json.Marshal(response)
		if err != nil {
			fmt.Fprintln(cli.stderr, err)
			return controlExitRefused
		}
		fmt.Fprintln(cli.stdout, string(encoded))
		return controlExitCode(response.Outcome)
	}
	renderControlText(cli, response)
	return controlExitCode(response.Outcome)
}

func controlExitCode(outcome core.ControlOutcome) int {
	switch {
	case core.ControlSuccessOutcome(outcome):
		return controlExitOK
	case core.ControlAddressingOutcome(outcome):
		return controlExitAddressing
	}
	return controlExitRefused
}

// renderControlText carries the same facts as the document, but it is not a
// parsing contract.
func renderControlText(cli controlCLI, response core.ControlResponse) {
	out := cli.stdout
	if !core.ControlSuccessOutcome(response.Outcome) {
		out = cli.stderr
	}
	result := response.Result
	switch {
	case result != nil && len(result.Sessions) > 0:
		for _, session := range result.Sessions {
			fmt.Fprintf(out, "%-24s %-16s %-12s %s\n",
				session.SessionID, session.Name, controlStatusText(session), session.Dir)
		}
	case result != nil && result.Content != "":
		fmt.Fprintln(out, result.Content)
	case result != nil && result.SessionID != "":
		fmt.Fprintf(out, "%s %s\n", response.Outcome, result.SessionID)
	default:
		fmt.Fprintln(out, response.Outcome)
	}
	if response.Message != "" {
		fmt.Fprintln(cli.stderr, response.Message)
	}
	if result != nil && len(result.Candidates) > 0 {
		for _, candidate := range result.Candidates {
			fmt.Fprintf(cli.stderr, "  %s  %s/%s\n", candidate.SessionID, candidate.Project, candidate.Name)
		}
	}
}

func controlStatusText(session core.ControlSessionView) string {
	if session.Status == "" {
		return string(session.Availability)
	}
	return string(session.Status)
}

func renderControlWatch(cli controlCLI, ctx context.Context, request core.ControlRequest, machine bool) int {
	response := cli.client.Watch(ctx, request, func(event core.ControlEvent) bool {
		if machine {
			encoded, err := json.Marshal(core.ControlEventMessage{ID: request.ID, Event: event})
			if err != nil {
				return false
			}
			fmt.Fprintln(cli.stdout, string(encoded))
			return true
		}
		fmt.Fprintf(cli.stdout, "%s %s → %s (%s)\n",
			event.SessionID, controlEventStateText(event.PreviousStatus, event.PreviousAvailability),
			controlEventStateText(event.Status, event.Availability), event.ObservedAt.Format("15:04:05"))
		return true
	})
	if response.Message != "" {
		fmt.Fprintln(cli.stderr, response.Message)
	}
	return controlExitCode(response.Outcome)
}

func controlEventStateText(status core.ControlStatus, availability core.ObservationAvailability) string {
	if status == "" {
		return string(availability)
	}
	return string(status)
}

func controlVerbNames() string {
	names := make([]string, 0, len(core.ControlVerbs()))
	for _, verb := range core.ControlVerbs() {
		names = append(names, strings.TrimPrefix(string(verb), "session."))
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// controlSessionHelp lists every verb of the control surface.
func controlSessionHelp() string {
	var help strings.Builder
	help.WriteString("magentic session — Sessions steuern, über die lokale Steuer-API\n\n")
	for _, line := range [][2]string{
		{"start", "Session in einem Projekt oder Worktree starten"},
		{"list", "Sessions mit ihrer Beobachtung auflisten"},
		{"send", "Text an den Coding-Agent einer Session senden"},
		{"output", "Sichtbaren Inhalt einer Session lesen"},
		{"wait", "Auf die gepinnte Belegung einer Session warten"},
		{"kill", "Runtime einer Session beenden, der Worktree bleibt"},
		{"whoami", "Eigene Session aus den Marker-Angaben auflösen"},
		{"watch", "Zustandswechsel als Ereignisstrom mitlesen"},
	} {
		fmt.Fprintf(&help, "  magentic session %-8s %s\n", line[0], line[1])
	}
	help.WriteString("\n  --json     Genau ein JSON-Dokument auf die Standardausgabe\n")
	help.WriteString("  --socket   Abweichende Sitzungs-Adresse der Steuer-API\n")
	return help.String()
}
