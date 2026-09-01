package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sidebarFixture(t *testing.T) (*Registry, State) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	seed := State{
		Schema: registrySchemaVersion,
		Projects: []Project{
			{ID: "p1", Name: "navi", Path: "/workspace/navi", MainBranch: "main"},
			{ID: "p2", Name: "magentic", Path: "/workspace/magentic", MainBranch: "main"},
		},
		Agents: []Session{
			{ID: "s1", Name: "navi", ProjectID: "p1", Project: "navi", Dir: "/workspace/navi"},
			{ID: "s2", Name: "magentic", ProjectID: "p2", Project: "magentic", Dir: "/workspace/magentic"},
			{ID: "s3", Name: "magentic-2", ProjectID: "p2", Project: "magentic", Dir: "/workspace/magentic"},
		},
	}
	raw, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	registry := OpenRegistry(path)
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return registry, snapshot.State()
}

func layoutShape(nodes []SidebarNode) []string {
	shape := make([]string, 0, len(nodes))
	for _, node := range nodes {
		entry := string(node.Kind) + ":" + node.Ref
		if len(node.Children) > 0 {
			entry += "("
			for i, child := range layoutShape(node.Children) {
				if i > 0 {
					entry += ","
				}
				entry += child
			}
			entry += ")"
		}
		shape = append(shape, entry)
	}
	return shape
}

func mustChange(t *testing.T, registry *Registry, change RegistryChange) State {
	t.Helper()
	result, err := registry.Change(context.Background(), change)
	if err != nil {
		t.Fatal(err)
	}
	return result.Snapshot.State()
}

func assertShape(t *testing.T, state State, want ...string) {
	t.Helper()
	got := layoutShape(SidebarLayout(&state))
	if len(got) != len(want) {
		t.Fatalf("Anordnung: %v, erwartet %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Anordnung: %v, erwartet %v", got, want)
		}
	}
}

func TestSidebarDefaultsPlaceSessionsUnderTheirProject(t *testing.T) {
	_, state := sidebarFixture(t)
	assertShape(t, state, "project:p1(session:s1)", "project:p2(session:s2,session:s3)")
}

func TestSidebarMigrationKeepsTheExistingProjectOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	legacy := []byte(`{
  "schema": 2,
  "projects": [{"id":"p2","name":"magentic","path":"/workspace/magentic"},
               {"id":"p1","name":"navi","path":"/workspace/navi"}],
  "agents": []
}`)
	if err := os.WriteFile(path, legacy, 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := OpenRegistry(path).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	if state.Schema != registrySchemaVersion {
		t.Fatalf("Schema nicht angehoben: %d", state.Schema)
	}
	assertShape(t, state, "project:p2", "project:p1")
	if _, err := os.Stat(path + ".pre-registry-v3.bak"); err != nil {
		t.Fatalf("keine Sicherung vor der Migration: %v", err)
	}
}

func TestSidebarSessionMovesIntoADivider(t *testing.T) {
	registry, _ := sidebarFixture(t)
	state := mustChange(t, registry, AddDivider("d1", "Recherche"))
	divider := state.SidebarDivider("d1")
	if divider == nil || divider.Name != "Recherche" || !divider.TopLevel() {
		t.Fatalf("Divider nicht angelegt: %+v", state.Sidebar)
	}
	state = mustChange(t, registry, MoveSidebarItem(SidebarSlotSession, "s3", SidebarSlotDivider, "d1",
		[]SidebarRef{{Kind: SidebarSlotSession, Ref: "s3"}}))
	assertShape(t, state, "project:p1(session:s1)", "project:p2(session:s2)", "divider:d1(session:s3)")
}

func TestSidebarReordersSessionsWithinTheirProject(t *testing.T) {
	registry, _ := sidebarFixture(t)
	state := mustChange(t, registry, MoveSidebarItem(SidebarSlotSession, "s3", SidebarSlotProject, "p2",
		[]SidebarRef{{Kind: SidebarSlotSession, Ref: "s3"}, {Kind: SidebarSlotSession, Ref: "s2"}}))
	assertShape(t, state, "project:p1(session:s1)", "project:p2(session:s3,session:s2)")
}

func TestSidebarReordersTheTopLevelAndKeepsDividerState(t *testing.T) {
	registry, _ := sidebarFixture(t)
	mustChange(t, registry, AddDivider("d1", "Recherche"))
	mustChange(t, registry, SetDividerCollapsed("d1", true))
	state := mustChange(t, registry, MoveSidebarItem(SidebarSlotProject, "p2", "", "", []SidebarRef{
		{Kind: SidebarSlotProject, Ref: "p2"},
		{Kind: SidebarSlotDivider, Ref: "d1"},
		{Kind: SidebarSlotProject, Ref: "p1"},
	}))
	assertShape(t, state, "project:p2(session:s2,session:s3)", "divider:d1", "project:p1(session:s1)")
	if divider := state.SidebarDivider("d1"); divider == nil || !divider.Collapsed || divider.Name != "Recherche" {
		t.Fatalf("Divider hat Namen oder Zustand verloren: %+v", divider)
	}
}

func TestSidebarRemovingADividerLeavesItsContentInPlace(t *testing.T) {
	registry, _ := sidebarFixture(t)
	mustChange(t, registry, AddDivider("d1", "Recherche"))
	mustChange(t, registry, MoveSidebarItem(SidebarSlotSession, "s3", SidebarSlotDivider, "d1",
		[]SidebarRef{{Kind: SidebarSlotSession, Ref: "s3"}}))
	state := mustChange(t, registry, RemoveDivider("d1"))
	assertShape(t, state, "project:p1(session:s1)", "project:p2(session:s2)", "session:s3")
}

func TestSidebarRejectsIllegalPlacements(t *testing.T) {
	registry, _ := sidebarFixture(t)
	mustChange(t, registry, AddDivider("d1", "Recherche"))
	cases := []struct {
		name   string
		change RegistryChange
	}{
		{"Session in ein fremdes Projekt", MoveSidebarItem(SidebarSlotSession, "s3", SidebarSlotProject, "p1",
			[]SidebarRef{{Kind: SidebarSlotSession, Ref: "s3"}})},
		{"Divider in einen Divider", MoveSidebarItem(SidebarSlotDivider, "d1", SidebarSlotDivider, "d1",
			[]SidebarRef{{Kind: SidebarSlotDivider, Ref: "d1"}})},
		{"Projekt in ein Projekt", MoveSidebarItem(SidebarSlotProject, "p1", SidebarSlotProject, "p2",
			[]SidebarRef{{Kind: SidebarSlotProject, Ref: "p1"}})},
		{"unbekannte Session", MoveSidebarItem(SidebarSlotSession, "weg", SidebarSlotDivider, "d1",
			[]SidebarRef{{Kind: SidebarSlotSession, Ref: "weg"}})},
		{"unbekannter Divider als Ziel", MoveSidebarItem(SidebarSlotSession, "s3", SidebarSlotDivider, "weg",
			[]SidebarRef{{Kind: SidebarSlotSession, Ref: "s3"}})},
		{"verschobener Eintrag fehlt in der Sortierung", MoveSidebarItem(SidebarSlotSession, "s3", SidebarSlotDivider, "d1",
			[]SidebarRef{{Kind: SidebarSlotSession, Ref: "s2"}})},
		{"doppelter Eintrag in der Sortierung", MoveSidebarItem(SidebarSlotSession, "s3", SidebarSlotDivider, "d1",
			[]SidebarRef{{Kind: SidebarSlotSession, Ref: "s3"}, {Kind: SidebarSlotSession, Ref: "s3"}})},
		{"Divider ohne Namen", AddDivider("d2", "   ")},
		{"Divider mit vergebener ID", AddDivider("d1", "Zweiter")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := registry.Change(context.Background(), tc.change); err == nil {
				t.Fatal("ungültige Änderung wurde angenommen")
			}
		})
	}
}

func TestSidebarForgetsPlacementsOfVanishedEntries(t *testing.T) {
	registry, _ := sidebarFixture(t)
	mustChange(t, registry, AddDivider("d1", "Recherche"))
	mustChange(t, registry, MoveSidebarItem(SidebarSlotSession, "s3", SidebarSlotDivider, "d1",
		[]SidebarRef{{Kind: SidebarSlotSession, Ref: "s3"}}))
	state := mustChange(t, registry, RemoveSession("s3", "magentic-2"))
	if state.SidebarSlotFor(SidebarSlotSession, "s3") != nil {
		t.Fatalf("Ablage der beendeten Session blieb stehen: %+v", state.Sidebar)
	}
	assertShape(t, state, "project:p1(session:s1)", "project:p2(session:s2)", "divider:d1")
}

func TestSidebarLeavesSessionsClosedForLaterOutOfTheList(t *testing.T) {
	registry, _ := sidebarFixture(t)
	state := mustChange(t, registry, MarkSessionLater("s3", "magentic-2", time.Now()))
	assertShape(t, state, "project:p1(session:s1)", "project:p2(session:s2)")
}
