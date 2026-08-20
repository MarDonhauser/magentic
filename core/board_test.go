package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTasks(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "tasks.md")
	content := `## 1. Vorbereitung

- [x] 1.1 Reachability prüfen
      mehrzeilige Fortsetzung, die keine eigene Task ist
- [ ] 1.2 Route ergänzen

## 2. Backend

* [X] 2.1 Profile durchreichen
- [ ] 2.2 Contracts umstellen
kein Task
- [-] 2.3 verworfen
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	tasks := parseTasks(p)
	if len(tasks) != 5 {
		t.Fatalf("%d Tasks, erwartet 5: %+v", len(tasks), tasks)
	}
	if !tasks[0].Done || tasks[0].Section != "1. Vorbereitung" {
		t.Fatalf("erste Task falsch: %+v", tasks[0])
	}
	if tasks[1].Done {
		t.Fatalf("offene Task als erledigt geparst: %+v", tasks[1])
	}
	if !tasks[2].Done || tasks[2].Section != "2. Backend" {
		t.Fatalf("Großes X / Sternchen-Liste falsch geparst: %+v", tasks[2])
	}
	if tasks[4].Done {
		t.Fatalf("[-] darf nicht als erledigt zählen: %+v", tasks[4])
	}
}

func TestParseTasksMissingFile(t *testing.T) {
	if tasks := parseTasks(filepath.Join(t.TempDir(), "fehlt.md")); tasks != nil {
		t.Fatalf("erwartet nil, bekam %+v", tasks)
	}
}

func TestBoardColumn(t *testing.T) {
	cases := []struct {
		name string
		it   BoardItem
		want string
	}{
		{"ohne Tasks", BoardItem{}, ColBacklog},
		{"nichts erledigt", BoardItem{Total: 5}, ColBacklog},
		{"teilweise erledigt", BoardItem{Total: 5, Done: 2}, ColActive},
		{"alles erledigt", BoardItem{Total: 5, Done: 5}, ColReview},
		{"laufende Session schlägt alles", BoardItem{Total: 5, Done: 5, Agents: []string{"hera"}}, ColActive},
	}
	for _, c := range cases {
		if got := boardColumn(c.it); got != c.want {
			t.Errorf("%s: %q, erwartet %q", c.name, got, c.want)
		}
	}
}

func TestMatchesItemRequiresDurableSpecificationReference(t *testing.T) {
	reference := SpecificationRef("specification:v1:project-1:spec-kit:current:reqspec-v2-default-activation")
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
		{"different Reference", agentCtx{specificationRef: "specification:v1:project-1:spec-kit:current:other"}, reference, false},
		{"empty candidate", agentCtx{specificationRef: reference}, "", false},
	}
	for _, c := range cases {
		if got := matchesItem(c.agent, c.candidate); got != c.want {
			t.Errorf("%s: %v, erwartet %v", c.name, got, c.want)
		}
	}
}

func TestLiveSpecificationSessionsRequiresKnownLiveObservation(t *testing.T) {
	reference := SpecificationRef("specification:v1:project-1:spec-kit:current:login")
	sessions := []Session{
		{ID: "live", Name: "live", SpecificationRef: reference},
		{ID: "dead", Name: "dead", SpecificationRef: reference},
		{ID: "unknown", Name: "unknown", SpecificationRef: reference},
		{ID: "legacy", Name: "login", SpecificationRef: ""},
	}
	snapshot := ObservationSnapshot{Sessions: []SessionObservation{
		{SessionID: "live", Availability: ObservationAvailable, Presence: SessionPresencePresent, Status: StatusIdle},
		{SessionID: "dead", Availability: ObservationAvailable, Presence: SessionPresenceAbsent, Status: StatusDead},
		{SessionID: "unknown", Availability: ObservationUnavailable, Presence: SessionPresenceUnknown, Status: StatusUnknown},
		{SessionID: "legacy", Availability: ObservationAvailable, Presence: SessionPresencePresent, Status: StatusRunning},
	}}

	live, problems := liveSpecificationSessions(sessions, snapshot)
	if len(live) != 1 || live[0].ID != "live" {
		t.Fatalf("liveSpecificationSessions() = %#v, want only the known-live linked Session", live)
	}
	if len(problems) != 1 {
		t.Fatalf("liveSpecificationSessions() problems = %#v, want one unknown-runtime diagnostic", problems)
	}
}

func TestBoardPreservesUnknownSpecificationStage(t *testing.T) {
	reference := SpecificationRef("specification:v1:project-1:spec-kit:current:login")
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

func TestReadDocHead(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "proposal.md")
	content := `# ReqSpec v2 Default Activation

## Why

Die typisierte v2-Ebene ist implementiert, aber vom Produkt aus nicht
erreichbar, weil die Export-Route nie ein Profil durchreicht.

## What Changes

- Route bekommt einen Profil-Parameter
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	title, summary := readDocHead(p)
	if title != "ReqSpec v2 Default Activation" {
		t.Fatalf("Titel %q", title)
	}
	if summary == "" || len(summary) > 320 {
		t.Fatalf("Summary unbrauchbar: %q", summary)
	}
	if got := summary[:12]; got != "Die typisier" {
		t.Fatalf("Summary beginnt mit %q — erwartet den Why-Abschnitt", got)
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

	st := &State{Projects: []Project{{Name: "demo", Path: root}}}
	b := BuildBoard(st, "demo")
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

	st := &State{Projects: []Project{{Name: "demo", Path: root}}}
	b := BuildBoard(st, "demo")

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

	st := &State{Projects: []Project{{Name: "demo", Path: root}}}
	if b := BuildBoard(st, "demo"); b.Kind != "none" || len(b.Sources) != 0 {
		t.Fatalf("Kind %q mit %d Quellen, erwartet none/0", b.Kind, len(b.Sources))
	}
}

func TestBuildBoardNoSpecs(t *testing.T) {
	st := &State{Projects: []Project{{Name: "leer", Path: t.TempDir()}}}
	if b := BuildBoard(st, "leer"); b.Kind != "none" {
		t.Fatalf("Kind %q, erwartet none", b.Kind)
	}
}
