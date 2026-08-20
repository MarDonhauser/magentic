package core

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRegistryDiscoveryRuntime struct {
	listed     []string
	listErr    error
	facts      map[string]registryRuntimeSessionFact
	inspectErr map[string]error
}

func (f fakeRegistryDiscoveryRuntime) ListSessions(context.Context) ([]string, error) {
	return append([]string(nil), f.listed...), f.listErr
}

func (f fakeRegistryDiscoveryRuntime) InspectSession(_ context.Context, runtimeName string) (registryRuntimeSessionFact, error) {
	if err := f.inspectErr[runtimeName]; err != nil {
		return registryRuntimeSessionFact{RuntimeName: runtimeName}, err
	}
	return f.facts[runtimeName], nil
}

func TestDiscoverNewKeepsCompleteFactsAndReportsUnknownRuntimes(t *testing.T) {
	projectRoot := filepath.Join(t.TempDir(), "project")
	goodRuntime := SessionName("good")
	brokenRuntime := SessionName("broken")
	runtime := fakeRegistryDiscoveryRuntime{
		listed: []string{goodRuntime, brokenRuntime},
		facts: map[string]registryRuntimeSessionFact{
			goodRuntime: {RuntimeName: goodRuntime, Directory: projectRoot, CreatedUnix: "1710000000"},
		},
		inspectErr: map[string]error{brokenRuntime: errors.New("display-message failed")},
	}
	state := &State{Projects: []Project{{ID: "project-id", Name: "project", Path: projectRoot}}}

	discovery := discoverNewWithRuntime(context.Background(), state, runtime)

	if discovery.Availability != RegistryDiscoveryPartial || len(discovery.Sessions) != 1 || len(discovery.Problems) != 1 {
		t.Fatalf("discovery did not preserve partial knowledge: %#v", discovery)
	}
	discovered := discovery.Sessions[0]
	if discovered.Name != "good" || discovered.RuntimeName != goodRuntime || discovered.ProjectID != "project-id" || discovered.Project != "project" {
		t.Fatalf("complete runtime fact mapped incorrectly: %#v", discovered)
	}
	if discovery.Problems[0].RuntimeName != brokenRuntime || discovery.Problems[0].Operation != "inspect-session" ||
		!strings.Contains(discovery.Problems[0].Message, "display-message failed") {
		t.Fatalf("failed runtime diagnostics were lost: %#v", discovery.Problems)
	}
}

func TestDiscoverNewListFailureIsUnavailable(t *testing.T) {
	discovery := discoverNewWithRuntime(context.Background(), &State{}, fakeRegistryDiscoveryRuntime{
		listErr: errors.New("tmux server unavailable"),
	})

	if discovery.Availability != RegistryDiscoveryUnavailable || len(discovery.Sessions) != 0 || len(discovery.Problems) != 1 {
		t.Fatalf("list failure was not preserved as unavailable: %#v", discovery)
	}
	if discovery.Problems[0].Operation != "list-sessions" || discovery.Err() == nil {
		t.Fatalf("list failure diagnostics were lost: %#v", discovery.Problems)
	}
}

func TestDiscoverNewRejectsDisplayNameCollisionWithDifferentRuntimeIdentity(t *testing.T) {
	runtimeName := SessionName("topic")
	state := &State{Agents: []Session{{ID: "registered", Name: "topic", RuntimeName: "opaque-topic", Dir: "/elsewhere"}}}
	discovery := discoverNewWithRuntime(context.Background(), state, fakeRegistryDiscoveryRuntime{
		listed: []string{runtimeName},
		facts: map[string]registryRuntimeSessionFact{
			runtimeName: {RuntimeName: runtimeName, Directory: "/work/topic", CreatedUnix: "1710000000"},
		},
	})

	if discovery.Availability != RegistryDiscoveryPartial || len(discovery.Sessions) != 0 || len(discovery.Problems) != 1 {
		t.Fatalf("different RuntimeName was adopted through a reused display name: %#v", discovery)
	}
	if discovery.Problems[0].Operation != "resolve-session-identity" || discovery.Err() == nil {
		t.Fatalf("identity conflict diagnostics were lost: %#v", discovery.Problems)
	}
}

func TestRegisterDiscoveredRejectsReusedDisplayNameWithoutRegistryMutation(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("MAGENTIC_STATE", statePath)
	registry := OpenRegistry(statePath)
	registered := Session{
		ID: "registered", Name: "topic", RuntimeName: "opaque-topic", Dir: "/elsewhere",
	}
	if _, err := registry.Change(context.Background(), RegisterSession(registered)); err != nil {
		t.Fatal(err)
	}
	before, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := before.State()
	runtimeName := SessionName(registered.Name)
	runtime := fakeRegistryDiscoveryRuntime{
		listed: []string{runtimeName},
		facts: map[string]registryRuntimeSessionFact{
			runtimeName: {RuntimeName: runtimeName, Directory: "/work/topic", CreatedUnix: "1710000000"},
		},
	}

	err = registerDiscoveredWithRuntime(context.Background(), &state, runtime)
	if err == nil || !strings.Contains(err.Error(), "resolve-session-identity") {
		t.Fatalf("display-name collision error = %v", err)
	}
	after, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := after.State()
	if after.Revision() != before.Revision() || len(got.Agents) != 1 || got.Agents[0].ID != registered.ID || got.Agents[0].RuntimeName != registered.RuntimeName {
		t.Fatalf("display-name collision mutated Registry: before=%#v after=%#v", before.State(), got)
	}
}

func TestRegistryDiscoveryTmuxParsersRejectMalformedSuccess(t *testing.T) {
	for _, output := range []string{"", "mgt-external", "\n", "mgt-external\n\n", "mgt-external\r\n", " mgt-external\n", "mgt-external\tother\n"} {
		if sessions, err := parseRegistryDiscoverySessionList(output); err == nil {
			t.Fatalf("parseRegistryDiscoverySessionList(%q) = %#v, want malformed-success error", output, sessions)
		}
	}
	if sessions, err := parseRegistryDiscoverySessionList("user-shell\nmgt-external\n"); err != nil || len(sessions) != 2 {
		t.Fatalf("terminated list rejected: sessions=%#v err=%v", sessions, err)
	}

	for _, output := range []string{"", "/work/project", "\n", "/work/project\n\n", "/work/project\r\n", " /work/project\n", "/work/proj\tect\n"} {
		if scalar, err := parseRegistryDiscoveryScalar(output); err == nil {
			t.Fatalf("parseRegistryDiscoveryScalar(%q) = %q, want malformed-success error", output, scalar)
		}
	}
	if scalar, err := parseRegistryDiscoveryScalar("/work/project\n"); err != nil || scalar != "/work/project" {
		t.Fatalf("terminated scalar rejected: scalar=%q err=%v", scalar, err)
	}
}

func TestRegisterDiscoveredRejectsIncompleteRuntimeFactsWithoutRegistryMutation(t *testing.T) {
	for _, test := range []struct {
		name       string
		fact       registryRuntimeSessionFact
		inspectErr error
		listErr    error
		operation  string
	}{
		{
			name:      "empty successful list",
			listErr:   errors.New("successful tmux response is empty"),
			operation: "list-sessions",
		},
		{
			name:      "unterminated successful list",
			listErr:   errors.New("successful tmux response is unterminated"),
			operation: "list-sessions",
		},
		{
			name:       "display error",
			inspectErr: errors.New("display-message failed"),
			operation:  "inspect-session",
		},
		{
			name:      "malformed timestamp",
			fact:      registryRuntimeSessionFact{Directory: "/work/project", CreatedUnix: "not-a-timestamp"},
			operation: "parse-session-created",
		},
		{
			name:      "signed timestamp",
			fact:      registryRuntimeSessionFact{Directory: "/work/project", CreatedUnix: "+1710000000"},
			operation: "parse-session-created",
		},
		{
			name:      "timestamp with surrounding whitespace",
			fact:      registryRuntimeSessionFact{Directory: "/work/project", CreatedUnix: " 1710000000"},
			operation: "parse-session-created",
		},
		{
			name:      "empty directory",
			fact:      registryRuntimeSessionFact{CreatedUnix: "1710000000"},
			operation: "validate-pane-directory",
		},
		{
			name:      "unclean directory",
			fact:      registryRuntimeSessionFact{Directory: "/work/project/../project", CreatedUnix: "1710000000"},
			operation: "validate-pane-directory",
		},
		{
			name:      "directory with control character",
			fact:      registryRuntimeSessionFact{Directory: "/work/proj\tect", CreatedUnix: "1710000000"},
			operation: "validate-pane-directory",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "state.json")
			t.Setenv("MAGENTIC_STATE", statePath)
			registry := OpenRegistry(statePath)
			if _, err := registry.Change(context.Background(), RegisterProject(Project{
				ID: "project-id", Name: "project", Path: "/work/project",
			})); err != nil {
				t.Fatal(err)
			}
			before, err := registry.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			state := before.State()
			runtimeName := SessionName("external")
			test.fact.RuntimeName = runtimeName
			runtime := fakeRegistryDiscoveryRuntime{
				listed:     []string{runtimeName},
				listErr:    test.listErr,
				facts:      map[string]registryRuntimeSessionFact{runtimeName: test.fact},
				inspectErr: map[string]error{runtimeName: test.inspectErr},
			}

			err = registerDiscoveredWithRuntime(context.Background(), &state, runtime)
			if err == nil || !strings.Contains(err.Error(), test.operation) {
				t.Fatalf("registerDiscoveredWithRuntime() error = %v, want %s diagnostic", err, test.operation)
			}
			after, err := registry.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if after.Revision() != before.Revision() || len(after.State().Agents) != 0 {
				t.Fatalf("incomplete runtime fact mutated Registry: before=%#v after=%#v", before.State(), after.State())
			}
		})
	}
}
