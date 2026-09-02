package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func controlAddressState() State {
	return State{
		Projects: []Project{
			{ID: "projekt-a", Name: "alpha", Path: "/tmp/alpha"},
			{ID: "projekt-b", Name: "beta", Path: "/tmp/beta"},
		},
		Agents: []Session{
			{ID: "session-a", Name: "review", ProjectID: "projekt-a", Project: "alpha", RuntimeName: "mgt-review", Dir: "/tmp/alpha"},
			{ID: "session-b", Name: "review", ProjectID: "projekt-b", Project: "beta", RuntimeName: "mgt-review-2", Dir: "/tmp/beta"},
			{ID: "session-c", Name: "einzig", ProjectID: "projekt-a", Project: "alpha", RuntimeName: "mgt-einzig", Dir: "/tmp/alpha"},
		},
	}
}

func TestResolveControlSessionByIdentity(t *testing.T) {
	state := controlAddressState()
	tests := []struct {
		name string
		args ControlArgs
		want SessionID
	}{
		{"SessionID", ControlArgs{Session: "session-b"}, "session-b"},
		{"eindeutiger Name", ControlArgs{Session: "einzig"}, "session-c"},
		{"Name mit Projekt-ID", ControlArgs{Session: "review", Project: "projekt-b"}, "session-b"},
		{"Name mit Projektnamen", ControlArgs{Session: "review", Project: "alpha"}, "session-a"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			session, failure := resolveControlSession(state, test.args)
			if failure != nil {
				t.Fatalf("Adressierung abgelehnt: %s", failure.Message)
			}
			if session.ID != test.want {
				t.Fatalf("SessionID = %q, want %q", session.ID, test.want)
			}
		})
	}
}

func TestResolveControlSessionAmbiguousBareName(t *testing.T) {
	state := controlAddressState()
	_, failure := resolveControlSession(state, ControlArgs{Session: "review"})
	if failure == nil {
		t.Fatal("ein doppelt vergebener Name wurde aufgelöst")
	}
	if failure.Outcome != ControlAmbiguous {
		t.Fatalf("Ergebnis = %q, want %q", failure.Outcome, ControlAmbiguous)
	}
	if len(failure.Candidates) != 2 {
		t.Fatalf("Kandidaten = %+v, want zwei", failure.Candidates)
	}
	for _, candidate := range failure.Candidates {
		if candidate.SessionID == "" || candidate.Project == "" {
			t.Fatalf("Kandidat nennt SessionID und Projekt nicht: %+v", candidate)
		}
	}
}

func TestResolveControlSessionRefusals(t *testing.T) {
	state := controlAddressState()
	tests := []struct {
		name string
		args ControlArgs
		want ControlOutcome
	}{
		{"unbekannte Session", ControlArgs{Session: "session-x"}, ControlNotFound},
		{"unbekannter Name", ControlArgs{Session: "fehlt"}, ControlNotFound},
		{"fremdes Projekt", ControlArgs{Session: "session-a", Project: "beta"}, ControlNotFound},
		{"unbekanntes Projekt", ControlArgs{Session: "review", Project: "gamma"}, ControlNotFound},
		{"ohne Adresse", ControlArgs{}, ControlNoTarget},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, failure := resolveControlSession(state, test.args)
			if failure == nil {
				t.Fatal("die Adressierung wurde akzeptiert")
			}
			if failure.Outcome != test.want {
				t.Fatalf("Ergebnis = %q (%s), want %q", failure.Outcome, failure.Message, test.want)
			}
		})
	}
}

// controlStubRepositories answers with a fixed checkout topology, so Worktree
// scoping is testable without a Git repository.
type controlStubRepositories struct {
	worktrees []RepositoryWorktree
	err       error
}

func (r controlStubRepositories) ResolveWorktree(_ context.Context, project Project, reference WorktreeRef) (RepositoryWorktreeTarget, error) {
	if r.err != nil {
		return RepositoryWorktreeTarget{}, r.err
	}
	for _, worktree := range r.worktrees {
		if worktree.Reference == reference {
			return RepositoryWorktreeTarget{Project: project, Worktree: worktree}, nil
		}
	}
	return RepositoryWorktreeTarget{}, errors.New("Worktree reference is stale or unknown")
}

func (r controlStubRepositories) Survey(_ context.Context, projects []Project) (RepositoriesSurvey, error) {
	if r.err != nil {
		return RepositoriesSurvey{}, r.err
	}
	survey := RepositoriesSurvey{}
	for _, project := range projects {
		survey.Projects = append(survey.Projects, RepositoryProjectSurvey{
			ID: project.ID, Name: project.Name, Path: project.Path,
			Presence:  RepositoryKnown,
			Worktrees: repositoryKnownFact(r.worktrees),
		})
	}
	return survey, nil
}

func controlWorktreeRepositories() controlStubRepositories {
	return controlStubRepositories{worktrees: []RepositoryWorktree{
		{Reference: "wt_main", Path: "/tmp/alpha", Main: true},
		{Reference: "wt_review", Path: "/tmp/alpha-agents/review"},
	}}
}

func TestResolveControlWorktreeScope(t *testing.T) {
	project := Project{ID: "projekt-a", Name: "alpha", Path: "/tmp/alpha"}
	repositories := controlWorktreeRepositories()

	scope, failure := resolveControlWorktree(context.Background(), repositories, project, ControlArgs{})
	if failure != nil || scope.Directory != "/tmp/alpha" || scope.Create {
		t.Fatalf("Projekt-Verzeichnis = %+v (%v)", scope, failure)
	}

	scope, failure = resolveControlWorktree(context.Background(), repositories, project, ControlArgs{NewWorktree: true})
	if failure != nil || !scope.Create || scope.Directory != "" {
		t.Fatalf("frischer Worktree = %+v (%v)", scope, failure)
	}

	scope, failure = resolveControlWorktree(context.Background(), repositories, project, ControlArgs{Worktree: "wt_review"})
	if failure != nil || scope.Directory != "/tmp/alpha-agents/review" {
		t.Fatalf("Worktree-Handle = %+v (%v)", scope, failure)
	}

	scope, failure = resolveControlWorktree(context.Background(), repositories, project, ControlArgs{Directory: "/tmp/alpha-agents/review"})
	if failure != nil || scope.Reference != "wt_review" {
		t.Fatalf("Verzeichnis im Projekt = %+v (%v)", scope, failure)
	}
}

func TestResolveControlWorktreeRejectsForeignDirectory(t *testing.T) {
	project := Project{ID: "projekt-a", Name: "alpha", Path: "/tmp/alpha"}
	repositories := controlWorktreeRepositories()

	_, failure := resolveControlWorktree(
		context.Background(), repositories, project, ControlArgs{Directory: "/tmp/beta"},
	)
	if failure == nil {
		t.Fatal("ein fremdes Verzeichnis wurde als Worktree akzeptiert")
	}
	if failure.Outcome != ControlContainment {
		t.Fatalf("Ergebnis = %q, want %q", failure.Outcome, ControlContainment)
	}

	_, failure = resolveControlWorktree(
		context.Background(), repositories, project, ControlArgs{Worktree: "wt_fremd"},
	)
	if failure == nil || failure.Outcome != ControlContainment {
		t.Fatalf("veraltetes Handle = %v", failure)
	}

	_, failure = resolveControlWorktree(
		context.Background(), controlStubRepositories{err: errors.New("git kaputt")}, project,
		ControlArgs{Directory: "/tmp/alpha"},
	)
	if failure == nil || failure.Outcome != ControlUnavailable {
		t.Fatalf("unlesbare Topologie = %v", failure)
	}
	if failure != nil && !strings.Contains(failure.Message, "nicht lesbar") {
		t.Fatalf("Begründung = %q", failure.Message)
	}
}
