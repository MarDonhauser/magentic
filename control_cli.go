package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
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

// parseControlInvocation marshals the arguments of one verb into a request,
// reading its flags from that verb's single ControlVerbSpec.
func parseControlInvocation(cli controlCLI, verb string, args []string) (core.ControlRequest, bool, string, int) {
	spec, known := controlVerbSpecByName(verb)
	if !known {
		fmt.Fprintf(cli.stderr, "Unbekanntes Verb %q — bekannt sind: %s.\n", verb, controlVerbNames())
		return core.ControlRequest{}, false, "", controlExitAddressing
	}
	set := flag.NewFlagSet("magentic session "+verb, flag.ContinueOnError)
	set.SetOutput(cli.stderr)
	machine := set.Bool("json", false, "Ein einzelnes JSON-Dokument auf die Standardausgabe schreiben")
	socket := set.String("socket", "", "Abweichende Sitzungs-Adresse der Steuer-API")

	strs := make([]*string, len(spec.Flags))
	bools := make([]*bool, len(spec.Flags))
	ints := make([]*int, len(spec.Flags))
	for i, flagSpec := range spec.Flags {
		switch flagSpec.Kind {
		case core.ControlFlagString:
			strs[i] = set.String(flagSpec.Name, flagSpec.Default, flagSpec.Usage)
		case core.ControlFlagBool:
			bools[i] = set.Bool(flagSpec.Name, flagSpec.Default == "true", flagSpec.Usage)
		case core.ControlFlagInt:
			def, _ := strconv.Atoi(flagSpec.Default)
			ints[i] = set.Int(flagSpec.Name, def, flagSpec.Usage)
		}
	}

	var request core.ControlRequest
	if err := set.Parse(args); err != nil {
		return request, false, "", controlExitAddressing
	}
	request.Verb = spec.Verb
	for i, flagSpec := range spec.Flags {
		switch flagSpec.Kind {
		case core.ControlFlagString:
			flagSpec.SetString(&request.Args, *strs[i])
		case core.ControlFlagBool:
			flagSpec.SetBool(&request.Args, *bools[i])
		case core.ControlFlagInt:
			flagSpec.SetInt(&request.Args, *ints[i])
		}
	}
	if spec.After != nil {
		spec.After(&request.Args, set.Args())
	}
	return request, *machine, *socket, controlExitOK
}

// controlVerbSpecByName resolves the CLI-local verb name ("start", not
// "session.start") against the shared ControlVerbSpecs declaration.
func controlVerbSpecByName(name string) (core.ControlVerbSpec, bool) {
	for _, spec := range core.ControlVerbSpecs() {
		if strings.TrimPrefix(string(spec.Verb), "session.") == name {
			return spec, true
		}
	}
	return core.ControlVerbSpec{}, false
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

// controlSessionHelp lists every verb of the control surface, generated from
// the same ControlVerbSpecs the flag parser reads.
func controlSessionHelp() string {
	var help strings.Builder
	help.WriteString("magentic session — Sessions steuern, über die lokale Steuer-API\n\n")
	for _, spec := range core.ControlVerbSpecs() {
		name := strings.TrimPrefix(string(spec.Verb), "session.")
		fmt.Fprintf(&help, "  magentic session %-8s %s\n", name, spec.Summary)
	}
	help.WriteString("\n  --json     Genau ein JSON-Dokument auf die Standardausgabe\n")
	help.WriteString("  --socket   Abweichende Sitzungs-Adresse der Steuer-API\n")
	return help.String()
}

// cliControlSkill installs the shipped agent instruction file into a Project.
// A second install replaces Magentic's section instead of duplicating it.
func cliControlSkill(args []string) {
	mode := ""
	if len(args) > 0 {
		mode = args[0]
	}
	if mode != "install" {
		fmt.Fprintln(os.Stderr, "magentic skill install [pfad] — die Agent-Anleitung in ein Projekt schreiben")
		os.Exit(1)
	}
	path := "."
	if len(args) > 1 {
		path = args[1]
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	state, err := core.LoadState()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var project core.Project
	for _, registered := range state.Projects {
		if registered.Path == absolute {
			project = registered
		}
	}
	if project.Path == "" {
		fmt.Fprintf(os.Stderr, "%s ist kein registriertes Projekt — zuerst »magentic add« aufrufen.\n", absolute)
		os.Exit(1)
	}
	changed, err := core.InstallControlSkill(project)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !changed {
		fmt.Printf("Nichts zu tun: %s trägt die Anleitung bereits so.\n", core.ControlSkillPath(project))
		return
	}
	fmt.Printf("Die Agent-Anleitung steht jetzt in %s.\n", core.ControlSkillPath(project))
}
