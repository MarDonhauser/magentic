package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMatchesItemRequiresDurableSpecificationReference(t *testing.T) {
	reference := makeSpecificationRef(Project{ID: "project-1"}, SpecificationSpecKit, "reqspec-v2-default-activation", false)
	cases := []struct {
		name      string
		agent     agentCtx
		candidate SpecificationRef
		want      bool
	}{
		{"exact Reference", agentCtx{specificationRef: reference}, reference, true},
		{"same slug in Branch", agentCtx{branch: "agent/reqspec-v2-default-activation"}, reference, false},
		{"same slug in Worktree", agentCtx{dir: "/tmp/req.pilot-agents/reqspec-v2-default-activation"}, reference, false},
		{"same slug in Session name", agentCtx{name: "reqspec-v2-default-activation"}, reference, false},
		{"different Reference", agentCtx{specificationRef: makeSpecificationRef(Project{ID: "project-1"}, SpecificationSpecKit, "other", false)}, reference, false},
		{"empty candidate", agentCtx{specificationRef: reference}, "", false},
	}
	for _, c := range cases {
		if got := matchesItem(c.agent, c.candidate); got != c.want {
			t.Errorf("%s: %v, erwartet %v", c.name, got, c.want)
		}
	}
}

func TestLiveSpecificationSessionsRequiresKnownLiveObservation(t *testing.T) {
	reference := makeSpecificationRef(Project{ID: "project-1"}, SpecificationSpecKit, "login", false)
	sessions := []Session{
		{ID: "live", Name: "live", SpecificationRef: reference},
		{ID: "shell", Name: "shell", SpecificationRef: reference},
		{ID: "dead", Name: "dead", SpecificationRef: reference},
		{ID: "unknown", Name: "unknown", SpecificationRef: reference},
		{ID: "legacy", Name: "login", SpecificationRef: ""},
	}
	snapshot := ObservationSnapshot{Sessions: []SessionObservation{
		{SessionID: "live", Availability: ObservationAvailable, Presence: SessionPresencePresent, Status: StatusIdle},
		{SessionID: "shell", Availability: ObservationAvailable, Presence: SessionPresencePresent, Status: StatusShell},
		{SessionID: "dead", Availability: ObservationAvailable, Presence: SessionPresenceAbsent, Status: StatusDead},
		{SessionID: "unknown", Availability: ObservationUnavailable, Presence: SessionPresenceUnknown, Status: StatusUnknown},
		{SessionID: "legacy", Availability: ObservationAvailable, Presence: SessionPresencePresent, Status: StatusRunning},
	}}

	live, problems := liveSpecificationSessions(sessions, snapshot)
	if len(live) != 2 || live[0].ID != "live" || live[1].ID != "shell" {
		t.Fatalf("liveSpecificationSessions() = %#v, want only the known-live linked Sessions", live)
	}
	if len(problems) != 1 {
		t.Fatalf("liveSpecificationSessions() problems = %#v, want one unknown-runtime diagnostic", problems)
	}
}

func TestBoardSurfacesUnknownRuntimeFromMalformedPaneList(t *testing.T) {
	reference := makeSpecificationRef(Project{ID: "project-1"}, SpecificationSpecKit, "login", false)
	session := Session{
		ID: "session-1", Name: "login", RuntimeName: "mgt-login", SpecificationRef: reference,
	}
	snapshot := malformedListPanesObservation(t, []Session{session})

	live, problems := liveSpecificationSessions([]Session{session}, snapshot)
	if len(live) != 0 {
		t.Fatalf("unknown runtime was presented as a live Board Session: %#v", live)
	}
	if len(problems) != 1 || !strings.Contains(problems[0], "Laufzeit unbekannt") {
		t.Fatalf("Board did not surface unknown runtime knowledge: %#v", problems)
	}
}

func TestBoardPreservesUnknownSpecificationStage(t *testing.T) {
	reference := makeSpecificationRef(Project{ID: "project-1"}, SpecificationSpecKit, "login", false)
	item := boardItemFromSpecification(Specification{
		Reference: reference,
		ID:        "login",
		Lifecycle: SpecificationLifecycle{Stage: SpecificationStageUnknown},
	}, []agentCtx{{name: "slug-collision", branch: "agent/login"}})
	if item.Column != ColUnknown {
		t.Fatalf("Board column = %q, want %q", item.Column, ColUnknown)
	}
	if len(item.Agents) != 0 {
		t.Fatalf("slug collision falsely marked Specification active: %#v", item.Agents)
	}
}

func TestHumanizeID(t *testing.T) {
	if got := humanizeID("001-measure-e2e-fidelity"); got != "001 measure e2e fidelity" {
		t.Fatalf("%q", got)
	}
}

func TestBuildBoardDetectsFormat(t *testing.T) {
	root := t.TempDir()
	change := filepath.Join(root, "openspec", "changes", "add-login")
	if err := os.MkdirAll(change, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(change, "tasks.md"), []byte("- [x] a\n- [ ] b\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "openspec", "changes", "archive", "alt"), 0o755)

	st := &State{Projects: []Project{{ID: "project-demo", Name: "demo", Path: root}}}
	b := BuildBoard(st, st.Projects[0].ID)
	if b.Kind != "openspec" {
		t.Fatalf("Kind %q, erwartet openspec", b.Kind)
	}
	if len(b.Items) != 1 {
		t.Fatalf("%d Items, erwartet 1", len(b.Items))
	}
	if b.Items[0].Total != 2 || b.Items[0].Done != 1 {
		t.Fatalf("Fortschritt %d/%d, erwartet 1/2", b.Items[0].Done, b.Items[0].Total)
	}
	if b.Items[0].Column != ColActive {
		t.Fatalf("Spalte %q, erwartet %q", b.Items[0].Column, ColActive)
	}
	if b.Archived != 1 {
		t.Fatalf("%d archiviert, erwartet 1", b.Archived)
	}
}

func TestBuildBoardCollectsEverySpecSystem(t *testing.T) {
	root := t.TempDir()
	mk := func(parts ...string) string {
		dir := filepath.Join(append([]string{root}, parts...)...)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	os.WriteFile(filepath.Join(mk("openspec", "changes", "add-login"), "tasks.md"), []byte("- [ ] a\n"), 0o644)
	os.WriteFile(filepath.Join(mk("specs", "001-checkout"), "tasks.md"), []byte("- [x] a\n"), 0o644)
	os.WriteFile(filepath.Join(mk(".kiro", "specs", "search"), "tasks.md"), []byte("- [ ] a\n"), 0o644)
	os.WriteFile(filepath.Join(mk(".agent-os", "specs", "2025-01-01-billing"), "tasks.md"), []byte("- [ ] a\n"), 0o644)

	st := &State{Projects: []Project{{ID: "project-demo", Name: "demo", Path: root}}}
	b := BuildBoard(st, st.Projects[0].ID)

	if len(b.Sources) != 4 {
		t.Fatalf("%d Quellen, erwartet 4: %+v", len(b.Sources), b.Sources)
	}
	if len(b.Items) != 4 {
		t.Fatalf("%d Items, erwartet 4", len(b.Items))
	}
	seen := map[string]string{}
	for _, it := range b.Items {
		if it.Key == "" {
			t.Fatalf("Item %q ohne Key", it.ID)
		}
		if prev, dup := seen[it.Key]; dup {
			t.Fatalf("Key %q doppelt (%s und %s)", it.Key, prev, it.Kind)
		}
		seen[it.Key] = it.Kind
	}
	kinds := map[string]bool{}
	for _, s := range b.Sources {
		kinds[s.Kind] = true
	}
	for _, want := range []string{"openspec", "speckit", "kiro", "agent-os"} {
		if !kinds[want] {
			t.Fatalf("Quelle %q fehlt: %+v", want, b.Sources)
		}
	}
}

func TestBuildBoardSkipsEmptySource(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "specs"), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "specs", "notes.md"), []byte("# lose Datei\n"), 0o644)

	st := &State{Projects: []Project{{ID: "project-demo", Name: "demo", Path: root}}}
	if b := BuildBoard(st, st.Projects[0].ID); b.Kind != "none" || len(b.Sources) != 0 {
		t.Fatalf("Kind %q mit %d Quellen, erwartet none/0", b.Kind, len(b.Sources))
	}
}

func TestBuildBoardNoSpecs(t *testing.T) {
	st := &State{Projects: []Project{{ID: "project-empty", Name: "leer", Path: t.TempDir()}}}
	if b := BuildBoard(st, st.Projects[0].ID); b.Kind != "none" {
		t.Fatalf("Kind %q, erwartet none", b.Kind)
	}
}
