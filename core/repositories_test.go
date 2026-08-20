package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type repositoriesRunnerStep struct {
	dir    string
	args   []string
	output string
	err    error
}

type repositoriesRecordingRunner struct {
	t     *testing.T
	steps []repositoriesRunnerStep
	calls int
}

func (r *repositoriesRecordingRunner) Run(_ context.Context, dir string, args ...string) (string, error) {
	r.t.Helper()
	if r.calls >= len(r.steps) {
		r.t.Fatalf("unexpected repository command %q in %q", args, dir)
	}
	step := r.steps[r.calls]
	r.calls++
	if dir != step.dir {
		r.t.Fatalf("repository command %d directory = %q, want %q", r.calls, dir, step.dir)
	}
	if !reflect.DeepEqual(args, step.args) {
		r.t.Fatalf("repository command %d args = %q, want %q", r.calls, args, step.args)
	}
	return step.output, step.err
}

func (r *repositoriesRecordingRunner) assertDone() {
	r.t.Helper()
	if r.calls != len(r.steps) {
		r.t.Fatalf("repository command budget used %d of %d commands", r.calls, len(r.steps))
	}
}

func repositoriesTopologyFixture(worktrees ...repositoriesTopologyWorktree) string {
	var out string
	for _, wt := range worktrees {
		out += "worktree " + wt.Path + "\n"
		if wt.Head != "" {
			out += "HEAD " + wt.Head + "\n"
		}
		if wt.Branch != "" {
			out += "branch refs/heads/" + wt.Branch + "\n"
		}
		if wt.Detached {
			out += "detached\n"
		}
		if wt.Bare {
			out += "bare\n"
		}
		if wt.Locked {
			out += "locked " + wt.LockReason + "\n"
		}
		if wt.Prunable {
			out += "prunable\n"
		}
		out += "\n"
	}
	return out
}

func repositoriesStatusFixture(head, branch string, records ...string) string {
	out := "# branch.oid " + head + "\n# branch.head " + branch + "\n"
	for _, record := range records {
		out += record + "\n"
	}
	return out
}

func TestRepositoriesSurveyHasBoundedCommandBudgetAndCoherentFacts(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	worktreePath := filepath.Join(filepath.Dir(projectPath), "project-agents", "topic")
	topology := repositoriesTopologyFixture(
		repositoriesTopologyWorktree{Path: projectPath, Head: "aaaa", Branch: "main"},
		repositoriesTopologyWorktree{Path: worktreePath, Head: "bbbb", Branch: "agent/topic"},
	)
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
		{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: topology},
		{dir: projectPath, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("aaaa", "main")},
		{dir: worktreePath, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("bbbb", "agent/topic",
			"1 M. N... 100644 100644 100644 aaaa bbbb tracked file.go",
			"? new file.go")},
		{dir: worktreePath, args: []string{"rev-list", "--left-right", "--count", "main...HEAD"}, output: "2\t3\n"},
	}}
	repositories := newRepositories(runner)
	observedAt := time.Date(2026, 8, 20, 10, 0, 0, 0, time.UTC)
	repositories.now = func() time.Time { return observedAt }

	survey, err := repositories.Survey(context.Background(), []Project{{Name: "demo", Path: projectPath}})
	if err != nil {
		t.Fatalf("Survey() error = %v", err)
	}
	runner.assertDone()
	if !survey.ObservedAt.Equal(observedAt) || len(survey.Projects) != 1 {
		t.Fatalf("Survey() = %#v", survey)
	}
	project := survey.Projects[0]
	if project.Presence != RepositoryKnown || !project.MainBranch.Known() || project.MainBranch.Value != "main" {
		t.Fatalf("Project facts = %#v", project)
	}
	if !project.Worktrees.Known() || len(project.Worktrees.Value) != 2 {
		t.Fatalf("Worktrees = %#v", project.Worktrees)
	}
	main, topic := project.Worktrees.Value[0], project.Worktrees.Value[1]
	if main.Reference == "" || topic.Reference == "" || main.Reference == topic.Reference ||
		main.Location == "" || topic.Location == "" || filepath.IsAbs(topic.Location) {
		t.Fatalf("opaque Worktree identity/location missing: main=%#v topic=%#v", main, topic)
	}
	if !main.Main || main.Divergence.Value != (RepositoryDivergence{Base: "main"}) {
		t.Fatalf("main Worktree = %#v", main)
	}
	if topic.Main || !topic.Changes.Known() || topic.Changes.Value.Staged != 1 || topic.Changes.Value.Untracked != 1 {
		t.Fatalf("topic Worktree changes = %#v", topic)
	}
	if want := []string{"new file.go", "tracked file.go"}; !reflect.DeepEqual(topic.Changes.Value.Paths, want) {
		t.Fatalf("topic paths = %q, want %q", topic.Changes.Value.Paths, want)
	}
	if !topic.Divergence.Known() || topic.Divergence.Value != (RepositoryDivergence{Base: "main", Ahead: 3, Behind: 2}) {
		t.Fatalf("topic divergence = %#v", topic.Divergence)
	}
}

func TestRepositoriesResolveWorktreeRefreshesAndKeepsPathsPrivate(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	project := Project{ID: "project-1", Name: "demo", Path: projectPath}
	topology := repositoriesTopologyFixture(repositoriesTopologyWorktree{Path: projectPath, Head: "aaaa", Branch: "main"})
	steps := []repositoriesRunnerStep{
		{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: topology},
		{dir: projectPath, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("aaaa", "main")},
		{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: topology},
		{dir: projectPath, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("aaaa", "main")},
	}
	runner := &repositoriesRecordingRunner{t: t, steps: steps}
	repositories := newRepositories(runner)
	survey, err := repositories.Survey(context.Background(), []Project{project})
	if err != nil {
		t.Fatal(err)
	}
	reference := survey.Projects[0].Worktrees.Value[0].Reference
	target, err := repositories.ResolveWorktree(context.Background(), project, reference)
	if err != nil {
		t.Fatalf("ResolveWorktree() error = %v", err)
	}
	runner.assertDone()
	if target.Worktree.Reference != reference || !sameRepositoryPath(target.Worktree.Path, projectPath) {
		t.Fatalf("ResolveWorktree() = %#v", target)
	}
	encoded, err := json.Marshal(target.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), projectPath) || strings.Contains(string(encoded), `"path"`) {
		t.Fatalf("RepositoryWorktree leaked its private path: %s", encoded)
	}
}

func TestRepositoriesWorktreeDiffKeepsFailureDistinctFromClean(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "topic")
	t.Run("known diff", func(t *testing.T) {
		runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
			{dir: dir, args: []string{"status", "--short"}, output: " M tracked.go\n?? new.go\n"},
			{dir: dir, args: []string{"diff", "HEAD"}, output: "diff --git a/tracked.go b/tracked.go\n"},
			{dir: dir, args: []string{"ls-files", "--others", "--exclude-standard"}, output: "new.go\n"},
		}}
		fact := newRepositories(runner).WorktreeDiff(context.Background(), RepositoryWorktree{Path: dir})
		runner.assertDone()
		if !fact.Known() || !strings.Contains(fact.Value, "tracked.go") || !strings.Contains(fact.Value, "+ new.go") {
			t.Fatalf("WorktreeDiff() = %#v", fact)
		}
	})

	t.Run("command failure", func(t *testing.T) {
		runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{{
			dir: dir, args: []string{"status", "--short"}, err: errors.New("git unavailable"),
		}}}
		fact := newRepositories(runner).WorktreeDiff(context.Background(), RepositoryWorktree{Path: dir})
		runner.assertDone()
		if fact.Known() || fact.Problem == nil || fact.Value == "Keine Änderungen." {
			t.Fatalf("WorktreeDiff failure became clean: %#v", fact)
		}
	})
}

func TestRepositoriesSurveyDistinguishesUnknownFromNotRepository(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	tests := []struct {
		name string
		err  error
		want RepositoryKnowledge
	}{
		{name: "unavailable", err: errors.New("git executable unavailable"), want: RepositoryUnknown},
		{name: "not repository", err: fmt.Errorf("%w: missing .git", errRepositoriesNotRepository), want: RepositoryNotRepository},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{{
				dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, err: tt.err,
			}}}
			survey, err := newRepositories(runner).Survey(context.Background(), []Project{{Name: "demo", Path: projectPath}})
			if err != nil {
				t.Fatalf("Survey() error = %v", err)
			}
			runner.assertDone()
			project := survey.Projects[0]
			if project.Presence != tt.want || project.Worktrees.State != tt.want || project.Problem == nil {
				t.Fatalf("Project facts = %#v, want state %q with a problem", project, tt.want)
			}
		})
	}
}

func TestRepositoriesSurveyKeepsKnownTopologyWhenDivergenceIsUnknown(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	worktreePath := filepath.Join(filepath.Dir(projectPath), "project-agents", "topic")
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
		{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(
			repositoriesTopologyWorktree{Path: projectPath, Head: "aaaa", Branch: "main"},
			repositoriesTopologyWorktree{Path: worktreePath, Head: "bbbb", Branch: "agent/topic"},
		)},
		{dir: projectPath, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("aaaa", "main")},
		{dir: worktreePath, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("bbbb", "agent/topic")},
		{dir: worktreePath, args: []string{"rev-list", "--left-right", "--count", "main...HEAD"}, err: errors.New("history unavailable")},
	}}
	survey, err := newRepositories(runner).Survey(context.Background(), []Project{{Name: "demo", Path: projectPath}})
	if err != nil {
		t.Fatalf("Survey() error = %v", err)
	}
	runner.assertDone()
	project := survey.Projects[0]
	if project.Presence != RepositoryKnown || !project.Worktrees.Known() {
		t.Fatalf("Project facts = %#v", project)
	}
	topic := project.Worktrees.Value[1]
	if topic.Divergence.State != RepositoryUnknown || topic.Divergence.Problem == nil {
		t.Fatalf("topic divergence = %#v", topic.Divergence)
	}
}

func TestRepositoriesTopologyPreservesCheckoutAndMaintenanceFacts(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	detachedPath := filepath.Join(filepath.Dir(projectPath), "project-agents", "detached")
	topology, err := parseRepositoriesTopology(repositoriesTopologyFixture(
		repositoriesTopologyWorktree{Path: projectPath, Bare: true},
		repositoriesTopologyWorktree{Path: detachedPath, Head: "bbbb", Detached: true, Locked: true, LockReason: "repair", Prunable: true},
	))
	if err != nil {
		t.Fatalf("parseRepositoriesTopology() error = %v", err)
	}
	if len(topology) != 2 {
		t.Fatalf("topology = %#v", topology)
	}
	bare := repositoryWorktreeFromTopology(projectPath, topology[0])
	detached := repositoryWorktreeFromTopology(projectPath, topology[1])
	if bare.Checkout.Value.Kind != RepositoryBare || !bare.Main {
		t.Fatalf("bare Worktree = %#v", bare)
	}
	if detached.Checkout.Value.Kind != RepositoryDetached || !detached.Locked || detached.LockReason != "repair" || !detached.Prunable {
		t.Fatalf("detached Worktree = %#v", detached)
	}

	unborn, err := parseRepositoriesStatus(repositoriesStatusFixture("(initial)", "topic", "? untracked file.go"))
	if err != nil {
		t.Fatalf("parseRepositoriesStatus() error = %v", err)
	}
	if unborn.Checkout.Kind != RepositoryUnborn || unborn.Checkout.Branch != "topic" || unborn.Head != "" {
		t.Fatalf("unborn status = %#v", unborn)
	}
}

func TestRepositoriesStatusRejectsMalformedSuccessfulOutput(t *testing.T) {
	for _, malformed := range []string{
		"",
		"nonsense that is not porcelain\n",
		"# branch.oid aaaa\n",
		"# branch.oid aaaa\n# branch.head main\n1 malformed\n",
		"# branch.oid aaaa\n# branch.head main\n? \n",
	} {
		if got, err := parseRepositoriesStatus(malformed); err == nil {
			t.Fatalf("malformed successful output became known clean: input=%q result=%#v", malformed, got)
		}
	}
}

func TestRepositoriesSurveyDoesNotTreatMalformedStatusAsClean(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
		{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(
			repositoriesTopologyWorktree{Path: projectPath, Head: "aaaa", Branch: "main"},
		)},
		{dir: projectPath, args: []string{"status", "--porcelain=v2", "--branch"}, output: "successful but malformed\n"},
	}}
	survey, err := newRepositories(runner).Survey(context.Background(), []Project{{ID: "project-1", Name: "demo", Path: projectPath}})
	if err != nil {
		t.Fatal(err)
	}
	runner.assertDone()
	worktree := survey.Projects[0].Worktrees.Value[0]
	if worktree.Changes.Known() || worktree.Changes.Problem == nil {
		t.Fatalf("malformed status authorized a known clean Worktree: %#v", worktree.Changes)
	}
}

func TestRepositoriesInspectCapturesAndComparesBaselineOnDemand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "project-agents", "topic")
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
		{dir: dir, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("aaaa", "agent/topic",
			"1 .M N... 100644 100644 100644 aaaa aaaa old file.go")},
		{dir: dir, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("bbbb", "agent/topic",
			"1 .M N... 100644 100644 100644 aaaa aaaa old file.go",
			"? new file.go")},
		{dir: dir, args: []string{"rev-list", "--count", "aaaa..HEAD"}, output: "2\n"},
	}}
	repositories := newRepositories(runner)
	initial, err := repositories.Inspect(context.Background(), RepositoryInspectRequest{Directory: dir})
	if err != nil {
		t.Fatalf("initial Inspect() error = %v", err)
	}
	if !initial.Baseline.Known() || initial.Baseline.Value.Head != "aaaa" || !reflect.DeepEqual(initial.Baseline.Value.DirtyPaths, []string{"old file.go"}) {
		t.Fatalf("initial baseline = %#v", initial.Baseline)
	}
	if initial.Divergence.State != RepositoryUnknown {
		t.Fatalf("initial divergence = %#v, want unknown without a requested main branch", initial.Divergence)
	}

	current, err := repositories.Inspect(context.Background(), RepositoryInspectRequest{Directory: dir, Against: &initial.Baseline.Value})
	if err != nil {
		t.Fatalf("current Inspect() error = %v", err)
	}
	runner.assertDone()
	if current.Delta == nil || !current.Delta.Paths.Known() || !reflect.DeepEqual(current.Delta.Paths.Value, []string{"new file.go"}) {
		t.Fatalf("baseline path delta = %#v", current.Delta)
	}
	if !current.Delta.Commits.Known() || current.Delta.Commits.Value != 2 {
		t.Fatalf("baseline commit delta = %#v", current.Delta.Commits)
	}
}

func TestRepositoriesInspectNeverTurnsFailureIntoClean(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "project")
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{{
		dir: dir, args: []string{"status", "--porcelain=v2", "--branch"}, err: errors.New("permission denied"),
	}}}
	inspection, err := newRepositories(runner).Inspect(context.Background(), RepositoryInspectRequest{Directory: dir})
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	runner.assertDone()
	if inspection.Presence != RepositoryUnknown || inspection.Changes.State != RepositoryUnknown || inspection.Changes.Problem == nil {
		t.Fatalf("inspection = %#v", inspection)
	}
	if inspection.Changes.Known() {
		t.Fatal("failed status was reported as known clean")
	}
}

func TestRepositoriesChangeIsIdempotent(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	target := filepath.Join(filepath.Dir(projectPath), "project-agents", "topic")
	project := Project{Name: "demo", Path: projectPath}

	t.Run("create existing managed Worktree", func(t *testing.T) {
		runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{{
			dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(
				repositoriesTopologyWorktree{Path: projectPath, Head: "aaaa", Branch: "main"},
				repositoriesTopologyWorktree{Path: target, Head: "bbbb", Branch: "agent/topic"},
			),
		}}}
		result, err := newRepositories(runner).Change(context.Background(), CreateManagedWorktreeChange(project, "topic"))
		if err != nil {
			t.Fatalf("Change(create) error = %v", err)
		}
		runner.assertDone()
		if result.State != RepositoryKnown || result.Changed {
			t.Fatalf("Change(create) = %#v", result)
		}
	})

	t.Run("remove absent managed Worktree", func(t *testing.T) {
		runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{{
			dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(
				repositoriesTopologyWorktree{Path: projectPath, Head: "aaaa", Branch: "main"},
			),
		}}}
		result, err := newRepositories(runner).Change(context.Background(), RemoveManagedWorktreeChange(project, target))
		if err != nil {
			t.Fatalf("Change(remove) error = %v", err)
		}
		runner.assertDone()
		if result.State != RepositoryKnown || result.Changed {
			t.Fatalf("Change(remove) = %#v", result)
		}
	})
}

func TestRepositoriesChangeChecksFreshPreconditionsAndPostconditions(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	target := filepath.Join(filepath.Dir(projectPath), "project-agents", "topic")
	project := Project{Name: "demo", Path: projectPath}
	root := repositoriesTopologyWorktree{Path: projectPath, Head: "aaaa", Branch: "main"}
	topic := repositoriesTopologyWorktree{Path: target, Head: "bbbb", Branch: "agent/topic"}

	t.Run("create", func(t *testing.T) {
		runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
			{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(root)},
			{dir: projectPath, args: []string{"worktree", "add", "-b", "agent/topic", target}},
			{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(root, topic)},
		}}
		result, err := newRepositories(runner).Change(context.Background(), CreateManagedWorktreeChange(project, "topic"))
		if err != nil {
			t.Fatalf("Change(create) error = %v", err)
		}
		runner.assertDone()
		if result.State != RepositoryKnown || !result.Changed || result.MayHaveApplied {
			t.Fatalf("Change(create) = %#v", result)
		}
	})

	t.Run("remove", func(t *testing.T) {
		runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
			{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(root, topic)},
			{dir: target, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("bbbb", "agent/topic")},
			{dir: projectPath, args: []string{"worktree", "remove", target}},
			{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(root)},
		}}
		result, err := newRepositories(runner).Change(context.Background(), RemoveManagedWorktreeChange(project, target))
		if err != nil {
			t.Fatalf("Change(remove) error = %v", err)
		}
		runner.assertDone()
		if result.State != RepositoryKnown || !result.Changed || result.MayHaveApplied {
			t.Fatalf("Change(remove) = %#v", result)
		}
	})

	t.Run("dirty remove is rejected before mutation", func(t *testing.T) {
		runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
			{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(root, topic)},
			{dir: target, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("bbbb", "agent/topic", "? scratch.txt")},
		}}
		_, err := newRepositories(runner).Change(context.Background(), RemoveManagedWorktreeChange(project, target))
		runner.assertDone()
		var changeErr *ManagedWorktreeChangeError
		if !errors.As(err, &changeErr) || changeErr.Kind != ManagedWorktreeDirty || changeErr.MayHaveApplied {
			t.Fatalf("Change(remove dirty) error = %#v", err)
		}
	})
}

func TestRepositoriesChangeRejectsNonManagedRemovalWithoutACommand(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	runner := &repositoriesRecordingRunner{t: t}
	_, err := newRepositories(runner).Change(context.Background(), RemoveManagedWorktreeChange(
		Project{Name: "demo", Path: projectPath}, filepath.Join(filepath.Dir(projectPath), "somewhere-else", "topic"),
	))
	runner.assertDone()
	var changeErr *ManagedWorktreeChangeError
	if !errors.As(err, &changeErr) || changeErr.Kind != ManagedWorktreeInvalid {
		t.Fatalf("Change(remove outside managed root) error = %#v", err)
	}
}

func TestRepositoriesChangeReportsUnknownPostcondition(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	target := filepath.Join(filepath.Dir(projectPath), "project-agents", "topic")
	root := repositoriesTopologyWorktree{Path: projectPath, Head: "aaaa", Branch: "main"}
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
		{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(root)},
		{dir: projectPath, args: []string{"worktree", "add", "-b", "agent/topic", target}},
		{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, err: errors.New("cannot refresh topology")},
	}}
	result, err := newRepositories(runner).Change(context.Background(), CreateManagedWorktreeChange(
		Project{Name: "demo", Path: projectPath}, "topic",
	))
	runner.assertDone()
	var changeErr *ManagedWorktreeChangeError
	if !errors.As(err, &changeErr) || changeErr.Kind != ManagedWorktreePostcondition || !changeErr.MayHaveApplied {
		t.Fatalf("Change(create) error = %#v", err)
	}
	if result.State != RepositoryUnknown || result.Changed || !result.MayHaveApplied || result.Problem == nil {
		t.Fatalf("Change(create) result = %#v", result)
	}
}

func TestRepositoryWorktreeForDirectoryPrefersContainingManagedWorktree(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	managedPath := filepath.Join(filepath.Dir(projectPath), "project-agents", "topic")
	worktrees := []RepositoryWorktree{{Path: projectPath, Main: true}, {Path: managedPath}}

	worktree, found := repositoryWorktreeForDirectory(worktrees, filepath.Join(managedPath, "internal", "package"))
	if !found || !sameRepositoryPath(worktree.Path, managedPath) {
		t.Fatalf("repositoryWorktreeForDirectory() = %#v, %v", worktree, found)
	}
	if _, found := repositoryWorktreeForDirectory(worktrees, filepath.Join(filepath.Dir(projectPath), "outside")); found {
		t.Fatal("unrelated directory was assigned to a Worktree")
	}
}
