package core

import (
	"context"
	"strings"
	"testing"
)

func controlEnvironmentMap(session Session) map[string]string {
	variables := map[string]string{}
	for _, variable := range ControlRuntimeEnvironment(session) {
		name, value, _ := strings.Cut(variable, "=")
		variables[name] = value
	}
	return variables
}

func TestControlRuntimeEnvironmentCarriesTheMarker(t *testing.T) {
	t.Setenv("MAGENTIC_SOCKET", "/tmp/magentic/control.sock")
	agent := Session{
		ID: "session-a", Name: "review", ProjectID: "projekt-a", Dir: "/tmp/alpha",
		Vendor: AgentVendorClaude, SessionKind: SessionKindCodingAgent,
	}
	terminal := Session{
		ID: "session-t", Name: "shell", ProjectID: "projekt-a", Dir: "/tmp/alpha",
		SessionKind: SessionKindTerminal,
	}
	for _, session := range []Session{agent, terminal} {
		variables := controlEnvironmentMap(session)
		if variables[ControlEnvMarker] != "1" {
			t.Fatalf("%s: Marker = %q, want \"1\"", session.Name, variables[ControlEnvMarker])
		}
		if variables[ControlEnvSocket] != "/tmp/magentic/control.sock" {
			t.Fatalf("%s: Sitzungs-Adresse = %q", session.Name, variables[ControlEnvSocket])
		}
		if variables[ControlEnvSessionID] != string(session.ID) || variables[ControlEnvProjectID] != "projekt-a" {
			t.Fatalf("%s: Identitäten = %+v", session.Name, variables)
		}
		if variables[ControlEnvWorktree] != "0" {
			t.Fatalf("%s: Worktree-Angabe = %q, want \"0\"", session.Name, variables[ControlEnvWorktree])
		}
		if _, present := variables[ControlEnvWorktreeDir]; present {
			t.Fatalf("%s: ohne Worktree wurde ein Worktree-Verzeichnis gemeldet", session.Name)
		}
	}

	inWorktree := agent
	inWorktree.Worktree = true
	inWorktree.Dir = "/tmp/alpha-agents/review"
	variables := controlEnvironmentMap(inWorktree)
	if variables[ControlEnvWorktree] != "1" || variables[ControlEnvWorktreeDir] != "/tmp/alpha-agents/review" {
		t.Fatalf("Worktree-Angaben = %+v", variables)
	}

	// The marker reaches the runtime through the command that creates it.
	args := strings.Join(tmuxNewSessionArgs(inWorktree), " ")
	if !strings.Contains(args, "-e "+ControlEnvMarker+"=1") {
		t.Fatalf("Der Marker fehlt im tmux-Aufruf: %s", args)
	}
}

func TestAdoptedRuntimeCarriesNoMarker(t *testing.T) {
	_, runtime, registry, _ := lifecycleHarness(t)
	project := registerLifecycleProject(t, registry)
	adopted := Session{
		ID: SessionID(NewUUID()), Name: "fremd", ProjectID: project.ID, Project: project.Name,
		Dir: project.Path, RuntimeName: "mgt-fremd", Vendor: AgentVendorClaude,
	}
	if _, err := registry.Change(context.Background(), RegisterSession(adopted)); err != nil {
		t.Fatalf("Adoption fehlgeschlagen: %v", err)
	}
	// Adoption registers an already running runtime. Nothing was provisioned,
	// so its processes were never given the marker.
	if runtime.startCalls != 0 {
		t.Fatalf("Für eine adoptierte Session wurde ein Runtime gestartet (%d)", runtime.startCalls)
	}

	// An occupant of such a runtime therefore presents no marker facts, and the
	// control API answers not-managed rather than an identity.
	service, _, _ := controlTestService(controlDispatchState())
	response := service.Dispatch(context.Background(), ControlRequest{Verb: ControlSessionWhoami})
	if response.Outcome != ControlNotManaged {
		t.Fatalf("Ergebnis = %q (%s)", response.Outcome, response.Message)
	}
}

func TestControlWhoamiResolvesMarkerFacts(t *testing.T) {
	service, _, _ := controlTestService(controlDispatchState())
	response := service.Dispatch(context.Background(), ControlRequest{
		Verb: ControlSessionWhoami,
		Args: ControlArgs{Marker: ControlMarker{SessionID: "session-c", ProjectID: "projekt-a"}},
	})
	if response.Outcome != ControlOK {
		t.Fatalf("Ergebnis = %q (%s)", response.Outcome, response.Message)
	}
	if response.Result.SessionID != "session-c" || response.Result.Session.ProjectID != "projekt-a" {
		t.Fatalf("Antwort = %+v", response.Result)
	}
	if response.Result.Session.Vendor == "" || response.Result.Dir == "" {
		t.Fatalf("Die Antwort nennt Agent-Art oder Verzeichnis nicht: %+v", response.Result.Session)
	}

	tests := []struct {
		name   string
		marker ControlMarker
	}{
		{"ohne Angaben", ControlMarker{}},
		{"unbekannte SessionID", ControlMarker{SessionID: "session-fremd"}},
		{"widersprüchliches Projekt", ControlMarker{SessionID: "session-c", ProjectID: "projekt-b"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := service.Dispatch(context.Background(), ControlRequest{
				Verb: ControlSessionWhoami, Args: ControlArgs{Marker: test.marker},
			})
			if response.Outcome != ControlNotManaged {
				t.Fatalf("Ergebnis = %q (%s)", response.Outcome, response.Message)
			}
			if response.Result != nil && response.Result.SessionID != "" {
				t.Fatalf("Es wurde eine fremde Identität geantwortet: %+v", response.Result)
			}
		})
	}
}

func TestControlNeverWidensAnAgentRequest(t *testing.T) {
	service, _, _ := controlTestService(controlDispatchState())
	// The caller sits in projekt-a and addresses a name that only exists in
	// projekt-b without naming that Project.
	response := service.Dispatch(context.Background(), ControlRequest{
		Verb: ControlSessionOutput,
		Args: ControlArgs{Session: "session-b", Project: "alpha", Marker: ControlMarker{SessionID: "session-c"}},
	})
	if response.Outcome != ControlNotFound {
		t.Fatalf("fremdes Projekt = %q (%s)", response.Outcome, response.Message)
	}

	// A verb that needs a Session never falls back to the caller's own.
	for _, verb := range []ControlVerb{ControlSessionOutput, ControlSessionSend, ControlSessionKill, ControlSessionWait} {
		t.Run(string(verb), func(t *testing.T) {
			response := service.Dispatch(context.Background(), ControlRequest{
				Verb: verb, Args: ControlArgs{Text: "weiter", Marker: ControlMarker{SessionID: "session-c"}},
			})
			if response.Outcome != ControlNoTarget {
				t.Fatalf("Ergebnis = %q (%s)", response.Outcome, response.Message)
			}
			if response.Result != nil && response.Result.SessionID != "" {
				t.Fatalf("Die eigene Session wurde eingesetzt: %+v", response.Result)
			}
		})
	}
}
