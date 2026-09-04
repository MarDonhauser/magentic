package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestBranchesForDirectoriesAnswersEveryDirectoryOnce hält fest, dass die
// Branch-Zuordnung an einer Stelle lebt: ein Verzeichnis in einem Worktree, das
// Projektverzeichnis selbst und ein fremdes Verzeichnis bekommen je eine
// Antwort, und die unbeantwortete trägt ihren Grund mit.
func TestBranchesForDirectoriesAnswersEveryDirectoryOnce(t *testing.T) {
	root := t.TempDir()
	projectPath := filepath.Join(root, "project")
	worktreePath := filepath.Join(root, "project-agents", "topic")
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
		{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(
			repositoriesTopologyWorktree{Path: projectPath, Head: "aaaa", Branch: "main"},
			repositoriesTopologyWorktree{Path: worktreePath, Head: "bbbb", Branch: "agent/topic"},
		)},
		{dir: projectPath, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("aaaa", "main")},
		{dir: worktreePath, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("bbbb", "agent/topic")},
		{dir: worktreePath, args: []string{"rev-list", "--left-right", "--count", "main...HEAD"}, output: "0\t1\n"},
	}}
	project := Project{ID: "project-id", Name: "demo", Path: projectPath, MainBranch: "main"}
	outside := filepath.Join(root, "outside")

	assignments := newRepositories(runner).BranchesForDirectories(context.Background(), project,
		[]string{filepath.Join(worktreePath, "internal", "pkg"), projectPath, outside})
	runner.assertDone()

	if !assignments.Known() || len(assignments.Assignments) != 3 {
		t.Fatalf("assignments = %#v", assignments)
	}
	if got := assignments.Assignments[0].Branch; got != "agent/topic" {
		t.Fatalf("directory inside a Worktree resolved to %q, want agent/topic", got)
	}
	if got := assignments.Assignments[1].Branch; got != "main" {
		t.Fatalf("project directory resolved to %q, want main", got)
	}
	third := assignments.Assignments[2]
	if third.Branch != "" || third.WorktreeIndex >= 0 || third.Known() || third.Problem == nil {
		t.Fatalf("foreign directory became an assignment: %#v", third)
	}
	worktree, checkedOut := assignments.WorktreeForBranch("agent/topic")
	if !checkedOut || !sameRepositoryPath(worktree.Path, worktreePath) {
		t.Fatalf("WorktreeForBranch = %#v, %v", worktree, checkedOut)
	}
	if _, checkedOut := assignments.WorktreeForBranch("feature"); checkedOut {
		t.Fatal("a branch without a Worktree must not resolve to one")
	}
}

// TestBranchesForDirectoriesKeepsMissingKnowledgeExplicit hält fest, dass ein
// Verzeichnis ohne lesbares Repository keine leere, sondern eine ausdrücklich
// unbekannte Zuordnung bekommt.
func TestBranchesForDirectoriesKeepsMissingKnowledgeExplicit(t *testing.T) {
	projectPath := t.TempDir()
	runner := repositoriesRunnerFunc(func(_ context.Context, _ string, args ...string) (string, error) {
		return "fatal: not a git repository", errRepositoriesNotRepository
	})
	project := Project{ID: "project-id", Name: "demo", Path: projectPath}

	assignments := newRepositories(runner).BranchesForDirectories(context.Background(), project, []string{projectPath})
	if assignments.Known() || assignments.Problem == nil || len(assignments.Assignments) != 1 {
		t.Fatalf("assignments = %#v", assignments)
	}
	if assignments.Assignments[0].State != assignments.State || assignments.Assignments[0].Problem == nil {
		t.Fatalf("assignment lost the reason: %#v", assignments.Assignments[0])
	}
}

// TestGraphFactsReadsHistoryMergeAndDivergenceTogether hält die eine
// Graph-Tatsache fest: begrenzter Verlauf, gemergte Branches und die Divergenz
// genau der Branches, die in diesem Verlauf sichtbar sind.
func TestGraphFactsReadsHistoryMergeAndDivergenceTogether(t *testing.T) {
	root := t.TempDir()
	projectPath := filepath.Join(root, "project")
	worktreePath := filepath.Join(root, "project-agents", "topic")
	newest := strings.Repeat("a", 40)
	middle := strings.Repeat("b", 40)
	oldest := strings.Repeat("c", 40)
	history := graphFactsCommitFixture(newest, middle, "1700000200", "HEAD -> main") +
		"\n" + graphFactsCommitFixture(middle, oldest, "1700000100", "agent/topic, feature") +
		"\n" + graphFactsCommitFixture(oldest, "", "1700000000", "stale")

	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
		{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(
			repositoriesTopologyWorktree{Path: projectPath, Head: "aaaa", Branch: "main"},
			repositoriesTopologyWorktree{Path: worktreePath, Head: "bbbb", Branch: "agent/topic"},
		)},
		{dir: projectPath, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("aaaa", "main")},
		{dir: worktreePath, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("bbbb", "agent/topic")},
		{dir: worktreePath, args: []string{"rev-list", "--left-right", "--count", "main...HEAD"}, output: "2\t3\n"},
		{dir: projectPath, args: []string{"log", "--all", "--date-order", "--max-count=3", "--format=" + repositoryCommitHistoryFormat}, output: history},
		{dir: projectPath, args: []string{"branch", "--merged", "main", "--format=%(refname:short)"}, output: "main\nfeature\n"},
		{dir: projectPath, args: []string{"rev-list", "--left-right", "--count", "main...feature"}, output: "1\t4\n"},
	}}
	repositories := newRepositories(runner)
	survey, err := repositories.Survey(context.Background(), []Project{{ID: "project-id", Name: "demo", Path: projectPath, MainBranch: "main"}})
	if err != nil {
		t.Fatalf("Survey() error = %v", err)
	}

	facts := repositories.GraphFacts(context.Background(), survey.Projects[0], 2)
	runner.assertDone()

	if facts.Main != "main" || !facts.Commits.Known() || len(facts.Commits.Value) != 2 || !facts.Truncated {
		t.Fatalf("history facts = %#v", facts)
	}
	if !facts.Merged.Known() || !facts.Merged.Value["feature"] || facts.Merged.Value["agent/topic"] {
		t.Fatalf("merged facts = %#v", facts.Merged)
	}
	// agent/topic ist ausgecheckt: seine Divergenz stammt aus dem Worktree und
	// wird nicht noch einmal erfragt. feature ist es nicht und wird verglichen.
	topic := facts.Divergence["agent/topic"]
	if !topic.Known() || topic.Value.Ahead != 3 || topic.Value.Behind != 2 {
		t.Fatalf("checked-out divergence = %#v", topic)
	}
	feature := facts.Divergence["feature"]
	if !feature.Known() || feature.Value.Ahead != 4 || feature.Value.Behind != 1 {
		t.Fatalf("compared divergence = %#v", feature)
	}
	if _, compared := facts.Divergence["main"]; compared {
		t.Fatal("the main branch must not be compared with itself")
	}
	if _, compared := facts.Divergence["stale"]; compared {
		t.Fatal("a branch beyond the requested limit must not be compared")
	}
}

func graphFactsCommitFixture(hash, parents, timestamp, decorations string) string {
	return hash + "\x1f" + hash[:7] + "\x1f" + parents + "\x1fsubject\x1fauthor\x1f" + timestamp + "\x1f" + decorations + "\x1e"
}

// TestWorktreePathsSeparatesNoRepositoryFromUnreadable hält fest, dass der
// Completion-Adapter Kenntnis liefert statt eines bool: kein Repository ist
// etwas anderes als ein fehlgeschlagener Aufruf.
func TestWorktreePathsSeparatesNoRepositoryFromUnreadable(t *testing.T) {
	tracked := repositoriesRunnerFunc(func(_ context.Context, _ string, args ...string) (string, error) {
		if len(args) > 2 {
			return "untracked.go\x00", nil
		}
		return "tracked.go\x00untracked.go\x00", nil
	})
	fact := newRepositories(tracked).WorktreePaths(context.Background(), "/repo")
	if !fact.Known() || len(fact.Value) != 2 || fact.Value[0] != "tracked.go" {
		t.Fatalf("paths = %#v", fact)
	}

	absent := repositoriesRunnerFunc(func(_ context.Context, _ string, _ ...string) (string, error) {
		return "fatal: not a git repository", errRepositoriesNotRepository
	})
	if fact := newRepositories(absent).WorktreePaths(context.Background(), "/repo"); fact.State != RepositoryNotRepository {
		t.Fatalf("a directory without a repository = %#v", fact)
	}

	broken := repositoriesRunnerFunc(func(_ context.Context, _ string, _ ...string) (string, error) {
		return "", context.DeadlineExceeded
	})
	if fact := newRepositories(broken).WorktreePaths(context.Background(), "/repo"); fact.State != RepositoryUnknown {
		t.Fatalf("an unreadable repository = %#v", fact)
	}
}

// TestStatsCommitCoverageSeparatesNoRepositoryFromUnreadable hält fest, dass
// ein Projekt ohne Repository die Deckung nicht wie ein Lesefehler einfärbt.
func TestStatsCommitCoverageSeparatesNoRepositoryFromUnreadable(t *testing.T) {
	state := &State{Projects: []Project{{ID: "one", Name: "demo", Path: "/demo"}}}
	tests := []struct {
		name     string
		fact     RepositoryFact[RepositoryOwnCommitSeries]
		coverage HistorySourceState
	}{
		{
			name:     "kein Repository",
			fact:     repositoryNotRepositoryFact[RepositoryOwnCommitSeries]("own_commits", errRepositoriesNotRepository),
			coverage: HistorySourceAbsent,
		},
		{
			name:     "unlesbares Repository",
			fact:     repositoryUnknownFact[RepositoryOwnCommitSeries]("own_commits", context.DeadlineExceeded),
			coverage: HistorySourceUnavailable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := statsRepositoriesFunc(func(context.Context, string, string) RepositoryFact[RepositoryOwnCommitSeries] {
				return test.fact
			})
			collection := collectStatsCommitsWithRepositories(context.Background(), state, "2026-01-01", source)
			if collection.Coverage.State != test.coverage {
				t.Fatalf("coverage state = %q, want %q", collection.Coverage.State, test.coverage)
			}
			if collection.ProjectStates["demo"] != test.coverage {
				t.Fatalf("project state = %q, want %q", collection.ProjectStates["demo"], test.coverage)
			}
		})
	}
}

// TestOverviewDoesNotLetAnEmptyPresenceStandForKnowledge hält fest, dass eine
// fehlende Presence als fehlendes Ergebnis benannt wird, statt stillschweigend
// für eine gelesene Unkenntnis einzustehen.
func TestOverviewDoesNotLetAnEmptyPresenceStandForKnowledge(t *testing.T) {
	project := Project{ID: "project-id", Name: "demo", Path: "/demo"}
	state := &State{Projects: []Project{project}}
	survey := RepositoriesSurvey{Projects: []RepositoryProjectSurvey{{ID: project.ID, Name: project.Name, Path: project.Path}}}

	overview := buildOverviewFromSurvey(state, ObservationSnapshot{}, survey, nil)
	if len(overview.Projects) != 1 {
		t.Fatalf("overview = %#v", overview)
	}
	observed := overview.Projects[0]
	if observed.RepositoryKnowledge != RepositoryUnknown {
		t.Fatalf("knowledge = %q, want unknown", observed.RepositoryKnowledge)
	}
	said := false
	for _, problem := range observed.Problems {
		if problem.Operation == "survey" && strings.Contains(problem.Message, "presence") {
			said = true
		}
	}
	if !said {
		t.Fatalf("an empty presence stood in for knowledge without saying so: %#v", observed.Problems)
	}
}
