package core

import (
	"context"
	"errors"
	"testing"
)

type fakeBoardSpecifications struct {
	discovery SpecificationsDiscovery
	err       error
}

func (f fakeBoardSpecifications) Discover(context.Context, Project, SpecificationQuery) (SpecificationsDiscovery, error) {
	return f.discovery, f.err
}

type fakeBoardRepositories struct {
	assignments RepositoryBranchAssignments
	calls       int
}

func (f *fakeBoardRepositories) BranchesForDirectories(_ context.Context, _ Project, dirs []string) RepositoryBranchAssignments {
	f.calls++
	return f.assignments
}

// TestBoardTakesItsDependenciesInsteadOfBuildingThem hält den Gewinn des Seams
// fest: das Board konstruiert Specifications und Repositories nicht mehr
// selbst, sondern nimmt sie entgegen. Vorher war dieser Pfad von keinem Test
// erreichbar.
//
// Der Branch-Zuordnungspfad bleibt ungetestet, solange liveAgentContext
// Observe direkt ruft: ohne laufende Runtime gibt es keine lebende Session und
// damit keine Anfrage an Repositories. Der Test hält das ausdrücklich fest,
// statt es zu verdecken.
func TestBoardTakesItsDependenciesInsteadOfBuildingThem(t *testing.T) {
	state := &State{Projects: []Project{{ID: "p1", Name: "magentic", Path: "/tmp/magentic"}}}
	specifications := fakeBoardSpecifications{}
	repositories := &fakeBoardRepositories{}

	board := buildBoardUsing(state, "p1", SpecificationQuery{}, specifications, repositories)

	if board.Err != "" {
		t.Fatalf("Board meldete einen Fehler: %q", board.Err)
	}
	if board.Project != "magentic" {
		t.Errorf("Board.Project = %q", board.Project)
	}
	if repositories.calls != 0 {
		t.Errorf("ohne lebende Session wurde Repositories %d mal gefragt", repositories.calls)
	}
}

// TestBoardSurfacesSpecificationSourceFailure hält fest, dass ein Board ohne
// lesbare Quelle das ausdrücklich sagt, statt ein leeres Board zu zeigen.
func TestBoardSurfacesSpecificationSourceFailure(t *testing.T) {
	state := &State{Projects: []Project{{ID: "p1", Name: "magentic", Path: "/tmp/magentic"}}}

	board := buildBoardUsing(state, "p1", SpecificationQuery{},
		fakeBoardSpecifications{err: errors.New("Quelle nicht lesbar")},
		&fakeBoardRepositories{})

	if board.Kind != "none" || board.Err == "" {
		t.Fatalf("unlesbare Quelle ergab Kind=%q Err=%q", board.Kind, board.Err)
	}
}
