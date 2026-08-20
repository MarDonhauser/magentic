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
			head := wt.Head
			// Most repository tests use four-character mnemonic object IDs. The
			// porcelain fixture expands those shorthands to the full SHA-1 grammar.
			if len(head) == 4 {
				head = strings.Repeat(head, 10)
			}
			out += "HEAD " + head + "\n"
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
	if len(head) == 4 {
		head = strings.Repeat(head, 10)
	}
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
			"1 M. N... 100644 100644 100644 "+strings.Repeat("a", 40)+" "+strings.Repeat("b", 40)+" tracked file.go",
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

func TestRepositoriesRemoteURLReturnsExplicitFacts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "project")
	t.Run("known", func(t *testing.T) {
		runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{{
			dir: dir, args: []string{"remote", "get-url", "origin"},
			output: "git@ssh.dev.azure.com:v3/org/project/repository\n",
		}}}
		fact := newRepositories(runner).RemoteURL(context.Background(), dir, "origin")
		runner.assertDone()
		if !fact.Known() || fact.Value != "git@ssh.dev.azure.com:v3/org/project/repository" || fact.Problem != nil {
			t.Fatalf("RemoteURL() = %#v", fact)
		}
	})

	t.Run("command unavailable", func(t *testing.T) {
		runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{{
			dir: dir, args: []string{"remote", "get-url", "origin"}, err: errors.New("git unavailable"),
		}}}
		fact := newRepositories(runner).RemoteURL(context.Background(), dir, "origin")
		runner.assertDone()
		if fact.Known() || fact.State != RepositoryUnknown || fact.Problem == nil || fact.Value != "" {
			t.Fatalf("unavailable RemoteURL() = %#v", fact)
		}
	})

	t.Run("not repository", func(t *testing.T) {
		runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{{
			dir: dir, args: []string{"remote", "get-url", "origin"}, err: fmt.Errorf("%w: missing .git", errRepositoriesNotRepository),
		}}}
		fact := newRepositories(runner).RemoteURL(context.Background(), dir, "origin")
		runner.assertDone()
		if fact.State != RepositoryNotRepository || fact.Problem == nil {
			t.Fatalf("non-repository RemoteURL() = %#v", fact)
		}
	})
}

func TestRepositoryRemoteURLRejectsMalformedSuccessfulOutput(t *testing.T) {
	for _, malformed := range []string{
		"",
		"\n",
		"https://example.test/repo",
		" https://example.test/repo\n",
		"https://one.test/repo\nhttps://two.test/repo\n",
		"https://example.test/repo\x00suffix\n",
	} {
		if got, err := parseRepositoryRemoteURL(malformed); err == nil {
			t.Fatalf("malformed successful remote output became known: input=%q value=%q", malformed, got)
		}
	}

	dir := filepath.Join(t.TempDir(), "project")
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{{
		dir: dir, args: []string{"remote", "get-url", "origin"}, output: "\n",
	}}}
	fact := newRepositories(runner).RemoteURL(context.Background(), dir, "origin")
	runner.assertDone()
	if fact.Known() || fact.State != RepositoryUnknown || fact.Problem == nil {
		t.Fatalf("malformed RemoteURL() = %#v", fact)
	}
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

func TestRepositoriesSurveyRejectsMalformedSuccessfulDivergence(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	worktreePath := filepath.Join(filepath.Dir(projectPath), "project-agents", "topic")
	tests := []struct {
		name string
		out  string
	}{
		{name: "negative", out: "-1\t2\n"},
		{name: "explicit plus", out: "+1\t2\n"},
		{name: "wrong separator", out: "1 2\n"},
		{name: "missing terminator", out: "1\t2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
				{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(
					repositoriesTopologyWorktree{Path: projectPath, Head: "aaaa", Branch: "main"},
					repositoriesTopologyWorktree{Path: worktreePath, Head: "bbbb", Branch: "agent/topic"},
				)},
				{dir: projectPath, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("aaaa", "main")},
				{dir: worktreePath, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture("bbbb", "agent/topic")},
				{dir: worktreePath, args: []string{"rev-list", "--left-right", "--count", "main...HEAD"}, output: test.out},
			}}
			survey, err := newRepositories(runner).Survey(context.Background(), []Project{{Name: "demo", Path: projectPath}})
			if err != nil {
				t.Fatal(err)
			}
			runner.assertDone()
			if !survey.Projects[0].Worktrees.Known() || survey.Projects[0].Worktrees.Value[1].Divergence.State != RepositoryUnknown || survey.Projects[0].Worktrees.Value[1].Divergence.Problem == nil {
				t.Fatalf("malformed divergence became known: %#v", survey.Projects[0])
			}
		})
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

func TestRepositoriesTopologyAcceptsDocumentedVariants(t *testing.T) {
	root := t.TempDir()
	branchPath := filepath.Join(root, "branch")
	detachedPath := filepath.Join(root, "detached")
	unbornPath := filepath.Join(root, "unborn")
	barePath := filepath.Join(root, "bare.git")
	head := strings.Repeat("a", 40)
	zeroHead := strings.Repeat("0", 40)
	input := fmt.Sprintf(
		"worktree %s\nHEAD %s\nbranch refs/heads/feature/topic\nlocked \"repair needed\"\nprunable stale-gitdir\n\n"+
			"worktree %s\nHEAD %s\ndetached\n\n"+
			"worktree %s\nHEAD %s\nbranch refs/heads/main\n\n"+
			"worktree %s\nbare\n\n",
		branchPath, head, detachedPath, head, unbornPath, zeroHead, barePath,
	)

	got, err := parseRepositoriesTopology(input)
	if err != nil {
		t.Fatalf("parseRepositoriesTopology() error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("topology = %#v", got)
	}
	if branch := got[0]; branch.Path != branchPath || branch.Head != head || branch.Branch != "feature/topic" ||
		!branch.Locked || branch.LockReason != "repair needed" || !branch.Prunable || branch.Detached || branch.Bare || branch.Unborn {
		t.Fatalf("branch topology = %#v", branch)
	}
	if detached := got[1]; detached.Path != detachedPath || !detached.Detached || detached.Branch != "" || detached.Unborn || detached.Bare {
		t.Fatalf("detached topology = %#v", detached)
	}
	if unborn := got[2]; unborn.Path != unbornPath || !unborn.Unborn || unborn.Branch != "main" || !allZeroOID(unborn.Head) {
		t.Fatalf("unborn topology = %#v", unborn)
	} else if checkout := repositoryWorktreeFromTopology(root, unborn).Checkout; !checkout.Known() ||
		checkout.Value != (RepositoryCheckout{Kind: RepositoryUnborn, Branch: "main"}) {
		t.Fatalf("unborn checkout = %#v", checkout)
	}
	if bare := got[3]; bare.Path != barePath || !bare.Bare || bare.Head != "" || bare.Branch != "" {
		t.Fatalf("bare topology = %#v", bare)
	}
}

func TestRepositoriesTopologyRejectsMalformedSuccessfulOutput(t *testing.T) {
	root := filepath.Join(t.TempDir(), "repo")
	other := filepath.Join(filepath.Dir(root), "other")
	head := strings.Repeat("a", 40)
	validRecord := fmt.Sprintf("worktree %s\nHEAD %s\nbranch refs/heads/main\n", root, head)
	tests := []struct {
		name  string
		input string
	}{
		{name: "empty", input: ""},
		{name: "only worktree path", input: fmt.Sprintf("worktree %s\n\n", root)},
		{name: "truncated record", input: validRecord},
		{name: "unknown line", input: validRecord + "future-field value\n\n"},
		{name: "duplicate head", input: fmt.Sprintf("worktree %s\nHEAD %s\nHEAD %s\nbranch refs/heads/main\n\n", root, head, head)},
		{name: "duplicate checkout", input: validRecord + "detached\n\n"},
		{name: "duplicate locked", input: validRecord + "locked\nlocked again\n\n"},
		{name: "duplicate prunable", input: validRecord + "prunable\nprunable again\n\n"},
		{name: "head before worktree", input: fmt.Sprintf("HEAD %s\nworktree %s\nbranch refs/heads/main\n\n", head, root)},
		{name: "branch before head", input: fmt.Sprintf("worktree %s\nbranch refs/heads/main\nHEAD %s\n\n", root, head)},
		{name: "locked before checkout", input: fmt.Sprintf("worktree %s\nHEAD %s\nlocked\nbranch refs/heads/main\n\n", root, head)},
		{name: "missing record separator", input: validRecord + fmt.Sprintf("worktree %s\nHEAD %s\ndetached\n\n", other, head)},
		{name: "duplicate path", input: validRecord + "\n" + validRecord + "\n"},
		{name: "bare with head", input: fmt.Sprintf("worktree %s\nHEAD %s\nbare\n\n", root, head)},
		{name: "detached unborn", input: fmt.Sprintf("worktree %s\nHEAD %s\ndetached\n\n", root, strings.Repeat("0", 40))},
		{name: "invalid head", input: fmt.Sprintf("worktree %s\nHEAD not-an-object-id\nbranch refs/heads/main\n\n", root)},
		{name: "invalid branch", input: fmt.Sprintf("worktree %s\nHEAD %s\nbranch refs/tags/main\n\n", root, head)},
		{name: "unterminated quoted path", input: "worktree \"/tmp/repo\n" + "bare\n\n"},
		{name: "leading empty record", input: "\n" + validRecord + "\n"},
		{name: "extra trailing record separator", input: validRecord + "\n\n"},
		{name: "extra path delimiter", input: fmt.Sprintf("worktree  %s\nHEAD %s\nbranch refs/heads/main\n\n", root, head)},
		{name: "trailing path whitespace", input: fmt.Sprintf("worktree %s \nHEAD %s\nbranch refs/heads/main\n\n", root, head)},
		{name: "extra head delimiter", input: fmt.Sprintf("worktree %s\nHEAD  %s\nbranch refs/heads/main\n\n", root, head)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got, err := parseRepositoriesTopology(test.input); err == nil {
				t.Fatalf("malformed successful output became known topology: input=%q result=%#v", test.input, got)
			}
		})
	}
}

func TestRepositoriesSurveyDoesNotTreatMalformedTopologyAsKnown(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{{
		dir: projectPath, args: []string{"worktree", "list", "--porcelain"},
		output: fmt.Sprintf("worktree %s\n\n", projectPath),
	}}}
	survey, err := newRepositories(runner).Survey(context.Background(), []Project{{ID: "project-1", Name: "demo", Path: projectPath}})
	if err != nil {
		t.Fatal(err)
	}
	runner.assertDone()
	project := survey.Projects[0]
	if project.Presence != RepositoryUnknown || project.Worktrees.Known() || project.Worktrees.Problem == nil {
		t.Fatalf("malformed topology became known: %#v", project)
	}
}

func TestRepositoriesStatusRejectsMalformedSuccessfulOutput(t *testing.T) {
	head := strings.Repeat("a", 40)
	for _, malformed := range []string{
		"",
		"nonsense that is not porcelain\n",
		"# branch.oid aaaa\n",
		"# branch.oid aaaa\n# branch.head main\n1 malformed\n",
		"# branch.oid aaaa\n# branch.head main\n? \n",
		"# branch.oid " + head + "\n# branch.head topic..broken\n",
		"# branch.oid " + head + "\n# branch.head main",
		"# branch.oid (initial)\n# branch.head (detached)\n",
		"# branch.oid " + strings.Repeat("0", 40) + "\n# branch.head main\n",
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

func TestRepositoriesChangeDoesNotRemoveOnMalformedSuccessfulStatus(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	target := filepath.Join(filepath.Dir(projectPath), "project-agents", "topic")
	project := Project{Name: "demo", Path: projectPath}
	root := repositoriesTopologyWorktree{Path: projectPath, Head: "aaaa", Branch: "main"}
	topic := repositoriesTopologyWorktree{Path: target, Head: "bbbb", Branch: "agent/topic"}
	head := strings.Repeat("b", 40)
	oldOID := strings.Repeat("a", 40)
	tests := []struct {
		name   string
		status string
	}{
		{name: "invalid object ID", status: "# branch.oid garbage\n# branch.head agent/topic\n"},
		{name: "invalid branch", status: "# branch.oid " + head + "\n# branch.head topic..broken\n"},
		{name: "missing terminator", status: "# branch.oid " + head + "\n# branch.head agent/topic"},
		{name: "ordinary record without a change", status: repositoriesStatusFixture(head, "agent/topic", "1 .. N... 100644 100644 100644 "+oldOID+" "+head+" tracked.go")},
		{name: "rename record without rename status", status: repositoriesStatusFixture(head, "agent/topic", "2 M. N... 100644 100644 100644 "+oldOID+" "+head+" R100 tracked.go\told.go")},
		{name: "rename score disagrees with status", status: repositoriesStatusFixture(head, "agent/topic", "2 R. N... 100644 100644 100644 "+oldOID+" "+head+" C100 tracked.go\told.go")},
		{name: "invalid ordinary mode", status: repositoriesStatusFixture(head, "agent/topic", "1 M. N... 10064x 100644 100644 "+oldOID+" "+head+" tracked.go")},
		{name: "invalid ordinary submodule", status: repositoriesStatusFixture(head, "agent/topic", "1 M. SXYZ 100644 100644 100644 "+oldOID+" "+head+" tracked.go")},
		{name: "invalid unmerged status", status: repositoriesStatusFixture(head, "agent/topic", "u M. N... 100644 100644 100644 100644 "+oldOID+" "+head+" "+head+" tracked.go")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
				{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: repositoriesTopologyFixture(root, topic)},
				{dir: target, args: []string{"status", "--porcelain=v2", "--branch"}, output: test.status},
			}}
			result, err := newRepositories(runner).Change(context.Background(), RemoveManagedWorktreeChange(project, target))
			runner.assertDone()
			if err == nil || result.State != RepositoryUnknown || result.Changed || result.MayHaveApplied || result.Problem == nil {
				t.Fatalf("malformed status authorized removal: result=%#v error=%v", result, err)
			}
		})
	}
}

func TestRepositoriesChangeDoesNotRemoveOnMalformedSuccessfulTopology(t *testing.T) {
	projectPath := filepath.Join(t.TempDir(), "project")
	target := filepath.Join(filepath.Dir(projectPath), "project-agents", "topic")
	project := Project{Name: "demo", Path: projectPath}
	root := repositoriesTopologyFixture(
		repositoriesTopologyWorktree{Path: projectPath, Head: "aaaa", Branch: "main"},
	)
	topic := repositoriesTopologyFixture(
		repositoriesTopologyWorktree{Path: target, Head: "bbbb", Branch: "agent/topic"},
	)
	tests := []struct {
		name     string
		topology string
	}{
		{name: "leading path whitespace", topology: root + strings.Replace(topic, "worktree "+target, "worktree  "+target, 1)},
		{name: "trailing path whitespace", topology: root + strings.Replace(topic, "worktree "+target+"\n", "worktree "+target+" \n", 1)},
		{name: "extra record separator", topology: root + "\n" + topic},
		{name: "extra HEAD delimiter", topology: root + strings.Replace(topic, "HEAD ", "HEAD  ", 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
				{dir: projectPath, args: []string{"worktree", "list", "--porcelain"}, output: test.topology},
			}}
			result, err := newRepositories(runner).Change(context.Background(), RemoveManagedWorktreeChange(project, target))
			runner.assertDone()
			if err == nil || result.State != RepositoryUnknown || result.Changed || result.MayHaveApplied || result.Problem == nil {
				t.Fatalf("malformed topology authorized removal: result=%#v error=%v", result, err)
			}
		})
	}
}

func TestRepositoriesInspectCapturesAndComparesBaselineOnDemand(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "project-agents", "topic")
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
		{dir: dir, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture(headA, "agent/topic",
			"1 .M N... 100644 100644 100644 "+strings.Repeat("a", 40)+" "+strings.Repeat("a", 40)+" old file.go")},
		{dir: dir, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture(headB, "agent/topic",
			"1 .M N... 100644 100644 100644 "+strings.Repeat("a", 40)+" "+strings.Repeat("a", 40)+" old file.go",
			"? new file.go")},
		{dir: dir, args: []string{"rev-list", "--count", headA + "..HEAD"}, output: "2\n"},
	}}
	repositories := newRepositories(runner)
	initial, err := repositories.Inspect(context.Background(), RepositoryInspectRequest{Directory: dir})
	if err != nil {
		t.Fatalf("initial Inspect() error = %v", err)
	}
	if !initial.Baseline.Known() || initial.Baseline.Value.Head != headA || !reflect.DeepEqual(initial.Baseline.Value.DirtyPaths, []string{"old file.go"}) {
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

func TestRepositoriesBaselineDeltaRejectsMalformedSuccessfulCount(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "project")
	headA := strings.Repeat("a", 40)
	headB := strings.Repeat("b", 40)
	for _, malformed := range []string{"-1\n", "+1\n", "1", "1\n2\n"} {
		t.Run(fmt.Sprintf("%q", malformed), func(t *testing.T) {
			runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
				{dir: dir, args: []string{"status", "--porcelain=v2", "--branch"}, output: repositoriesStatusFixture(headB, "main")},
				{dir: dir, args: []string{"rev-list", "--count", headA + "..HEAD"}, output: malformed},
			}}
			inspection, err := newRepositories(runner).Inspect(context.Background(), RepositoryInspectRequest{
				Directory: dir,
				Against:   &RepositoryBaseline{Directory: dir, Head: headA},
			})
			runner.assertDone()
			if err != nil {
				t.Fatal(err)
			}
			if inspection.Delta == nil || inspection.Delta.Commits.State != RepositoryUnknown || inspection.Delta.Commits.Problem == nil {
				t.Fatalf("malformed count became known: %#v", inspection.Delta)
			}
		})
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
