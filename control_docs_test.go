package main

import (
	"bytes"
	"os"
	"strconv"
	"strings"
	"testing"

	"magentic/core"
)

func TestReadmeDocumentsTheImplementedControlSurface(t *testing.T) {
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatalf("README.md nicht lesbar: %v", err)
	}
	document := string(readme)
	if !strings.Contains(document, "## Steuer-API") {
		t.Fatal("Die README beschreibt die Steuer-API nicht")
	}
	for _, verb := range core.ControlVerbs() {
		name := strings.TrimPrefix(string(verb), "session.")
		if !strings.Contains(document, "magentic session "+name) {
			t.Fatalf("Die README nennt das Verb %q nicht", name)
		}
	}
	// A verb the README documents but the code does not have would be just as
	// wrong as a missing one.
	for _, line := range strings.Split(document, "\n") {
		index := strings.Index(line, "magentic session ")
		if index < 0 {
			continue
		}
		fields := strings.Fields(line[index:])
		if len(fields) < 3 {
			continue
		}
		name := strings.Trim(fields[2], "`*.,")
		if name == "<verb>" {
			continue
		}
		if !core.KnownControlVerb(core.ControlVerb("session." + name)) {
			t.Fatalf("Die README nennt das Verb %q, das es nicht gibt", name)
		}
	}
	for _, fact := range []string{"MAGENTIC_ENV", "MAGENTIC_SOCKET", "MAGENTIC_SESSION_ID", "MAGENTIC_PROJECT_ID"} {
		if !strings.Contains(document, fact) {
			t.Fatalf("Die README nennt %q nicht", fact)
		}
	}
	if !strings.Contains(document, "magentic skill install") {
		t.Fatal("Die README erklärt die Installation der Agent-Anleitung nicht")
	}
	if !strings.Contains(document, "**Agent Control**") {
		t.Fatal("Die Architektur-Sektion nennt das Agent-Control-Modul nicht")
	}
}

// TestControlCLIFlagsMatchTheSpecDefaults holds the CLI's actual flag.FlagSet
// wiring against core.ControlVerbSpecs(): every verb parsed with no arguments
// must produce exactly the ControlArgs its Flags' Defaults declare, and any
// After step. A spec Default that drifted from what the CLI registers would
// otherwise only show up as a silently wrong runtime default.
func TestControlCLIFlagsMatchTheSpecDefaults(t *testing.T) {
	for _, spec := range core.ControlVerbSpecs() {
		name := strings.TrimPrefix(string(spec.Verb), "session.")
		t.Run(name, func(t *testing.T) {
			var expected core.ControlArgs
			for _, flag := range spec.Flags {
				switch flag.Kind {
				case core.ControlFlagString:
					flag.SetString(&expected, flag.Default)
				case core.ControlFlagBool:
					flag.SetBool(&expected, flag.Default == "true")
				case core.ControlFlagInt:
					value := 0
					if flag.Default != "" {
						parsed, err := strconv.Atoi(flag.Default)
						if err != nil {
							t.Fatalf("Default von %q ist keine Zahl: %q", flag.Name, flag.Default)
						}
						value = parsed
					}
					flag.SetInt(&expected, value)
				}
			}
			if spec.After != nil {
				spec.After(&expected, nil)
			}

			var stdout, stderr bytes.Buffer
			request, machine, _, code := parseControlInvocation(
				controlCLI{stdout: &stdout, stderr: &stderr}, name, nil)
			if code != controlExitOK {
				t.Fatalf("Parsen ohne Argumente scheiterte: Exit-Code %d (%s)", code, stderr.String())
			}
			if machine {
				t.Fatal("--json ist ohne Argumente nicht gesetzt")
			}
			if request.Verb != spec.Verb {
				t.Fatalf("Verb = %q, want %q", request.Verb, spec.Verb)
			}
			if request.Args != expected {
				t.Fatalf("Args = %+v, want %+v (aus den Flag-Defaults der Spec)", request.Args, expected)
			}
		})
	}
}

func TestPinnedOccupantDecisionIsRecorded(t *testing.T) {
	const path = "docs/adr/0008-pin-the-awaited-session-occupant.md"
	adr, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Die ADR fehlt: %v", err)
	}
	for _, needle := range []string{"SessionID", "RuntimeName", "AgentRunRef", "occupant-replaced"} {
		if !strings.Contains(string(adr), needle) {
			t.Fatalf("Die ADR nennt %q nicht", needle)
		}
	}
	readme, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(readme), path) {
		t.Fatalf("Die README verweist nicht auf %s", path)
	}
}
