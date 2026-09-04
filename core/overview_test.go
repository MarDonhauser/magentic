package core

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type recordingOverviewRepositories struct {
	survey   RepositoriesSurvey
	err      error
	calls    int
	projects []Project
}

func (r *recordingOverviewRepositories) Survey(_ context.Context, projects []Project) (RepositoriesSurvey, error) {
	r.calls++
	r.projects = append([]Project(nil), projects...)
	return r.survey, r.err
}

func TestOvWorktreeJSONExposesOnlyOpaqueReferenceAndLocation(t *testing.T) {
	data, err := json.Marshal(OvWorktree{
		Reference: "wt_opaque", Location: "project-agents/one",
		Path: "/Users/example/project-agents/one", ShortPath: "legacy",
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["reference"] != "wt_opaque" || decoded["location"] != "project-agents/one" {
		t.Fatalf("opaque Worktree projection missing from JSON: %s", data)
	}
	for _, forbidden := range []string{"path", "shortPath", "Path", "ShortPath"} {
		if _, leaked := decoded[forbidden]; leaked {
			t.Fatalf("private Worktree path %q leaked into JSON: %s", forbidden, data)
		}
	}
}

func TestUnread(t *testing.T) {
	seen := time.Now().Add(-1 * time.Hour)
	after := seen.Add(10 * time.Minute)
	before := seen.Add(-10 * time.Minute)

	cases := []struct {
		name   string
		status AgentStatus
		active time.Time
		want   bool
	}{
		{"idle mit Aktivität nach dem Blick", StatusIdle, after, true},
		{"idle ohne neue Aktivität", StatusIdle, before, false},
		{"wartet und war aktiv", StatusBlocked, after, true},
		{"beendet nach dem Blick", StatusExited, after, true},
		{"läuft gerade", StatusRunning, after, false},
		{"Background-Agents laufen", StatusAgents, after, false},
		{"Terminal", StatusTerm, after, false},
		{"tot", StatusDead, after, false},
	}
	for _, c := range cases {
		if got := unread(c.status, seen, c.active); got != c.want {
			t.Errorf("%s: %v, erwartet %v", c.name, got, c.want)
		}
	}
}

func TestUnreadNieGesehen(t *testing.T) {
	if !unread(StatusIdle, time.Time{}, time.Now()) {
		t.Fatal("nie gesehene Session mit Aktivität muss ungelesen sein")
	}
}

func gitInit(t *testing.T, dir string, args ...[]string) {
	t.Helper()
	for _, a := range args {
		cmd := exec.Command("git", append([]string{"-C", dir}, a...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v — %s", a, err, out)
		}
	}
}

func TestSessionNameHint(t *testing.T) {
	dir := t.TempDir()
	gitInit(t, dir,
		[]string{"init", "-q", "-b", "main"},
		[]string{"config", "user.email", "t@example.com"},
		[]string{"config", "user.name", "Test"},
		[]string{"commit", "-q", "--allow-empty", "-m", "init"},
	)
	if got := SessionNameHint(dir, "projekt"); got != "projekt" {
		t.Fatalf("auf main erwartet Fallback %q, bekam %q", "projekt", got)
	}

	gitInit(t, dir, []string{"checkout", "-q", "-b", "agent/neue-suche"})
	if got := SessionNameHint(dir, "projekt"); got != "neue-suche" {
		t.Fatalf("erwartet %q, bekam %q", "neue-suche", got)
	}

	gitInit(t, dir, []string{"checkout", "-q", "-b", "dev"})
	if got := SessionNameHint(dir, "projekt"); got != "projekt" {
		t.Fatalf("dev ist ein Integrationsbranch, erwartet Fallback, bekam %q", got)
	}

	if got := SessionNameHint("", "projekt"); got != "projekt" {
		t.Fatalf("ohne Verzeichnis erwartet Fallback, bekam %q", got)
	}
	if got := SessionNameHint(t.TempDir(), "projekt"); got != "projekt" {
		t.Fatalf("ohne Repo erwartet Fallback, bekam %q", got)
	}
}

func TestOverviewProjectsCoherentObservationFactsAndStableIDs(t *testing.T) {
	dir := t.TempDir()
	activeAt := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	state := &State{
		Projects: []Project{{ID: "project-1", Name: "NAVI", Path: dir, MainBranch: "main"}},
		Agents: []Session{{
			ID: "session-1", Name: "one", ProjectID: "project-1", Project: "NAVI", Dir: dir,
			Purpose: SessionPurposeCleanup, CreatedAt: activeAt.Add(-time.Hour),
		}},
	}
	snapshot := ObservationSnapshot{
		ObservedAt:   activeAt,
		Availability: ObservationAvailable,
		Sessions: []SessionObservation{{
			SessionID: "session-1", Availability: ObservationAvailable,
			Presence: SessionPresencePresent, Status: StatusBlocked,
			Content: "content intentionally does not encode the detail", ContentKnown: true,
			Activity: activeAt, ActivityKnown: true, Tool: AgentToolCodex,
			Detail: "coherent detail", Attention: AttentionNeedsInput, Unread: true,
			Occupancy: OccupancyOccupied,
		}},
	}
	repositories := &recordingOverviewRepositories{survey: RepositoriesSurvey{
		ObservedAt: activeAt,
		Projects: []RepositoryProjectSurvey{{
			ID: "project-1", Name: "NAVI", Path: dir, Presence: RepositoryKnown,
			MainBranch: repositoryKnownFact("main"),
			Worktrees: repositoryKnownFact([]RepositoryWorktree{{
				Path: dir, Main: true,
				Checkout:   repositoryKnownFact(RepositoryCheckout{Kind: RepositoryBranchCheckout, Branch: "main"}),
				Head:       repositoryKnownFact("head-1"),
				Changes:    repositoryKnownFact(RepositoryWorkingChanges{Modified: 2, Conflicted: 1}),
				Divergence: repositoryKnownFact(RepositoryDivergence{Base: "main"}),
			}}),
		}},
	}}

	got := buildOverviewFromObservationUsing(state, snapshot, repositories)
	if repositories.calls != 1 || len(repositories.projects) != 1 || repositories.projects[0].ID != "project-1" {
		t.Fatalf("Overview must obtain exactly one Survey for all Projects: calls=%d projects=%#v", repositories.calls, repositories.projects)
	}
	if len(got.Projects) != 1 || got.Projects[0].ID != "project-1" {
		t.Fatalf("stable Project identity missing: %#v", got.Projects)
	}
	project := got.Projects[0]
	if project.RepositoryKnowledge != RepositoryKnown || !project.MainBranchKnown ||
		!project.HeadBranchKnown || !project.WorktreesKnown || project.MainBranch != "main" || project.HeadBranch != "main" {
		t.Fatalf("Project repository facts were not projected: %#v", project)
	}
	worktree := project.Worktrees[0]
	if !worktree.CheckoutKnown || !worktree.ChangesKnown || !worktree.DivergenceKnown ||
		worktree.Branch != "main" || worktree.Modified != 2 || worktree.Conflicted != 1 || worktree.Clean {
		t.Fatalf("Worktree repository facts were not projected: %#v", worktree)
	}
	agent := got.Projects[0].Worktrees[0].Agents[0]
	if agent.ID != "session-1" || agent.Name != "one" {
		t.Fatalf("stable Session identity missing: %#v", agent)
	}
	if agent.Status != "blocked" || agent.Tool != AgentToolCodex || agent.Detail != "coherent detail" || !agent.Unread {
		t.Fatalf("Overview recomputed or lost Observation facts: %#v", agent)
	}
	if agent.Phase != "cleanup" {
		t.Fatalf("Session purpose was lost during projection: %#v", agent)
	}
	if agent.Branch != "main" || agent.Known || agent.OwnDirty != 0 || agent.OwnCommits != 0 {
		t.Fatalf("Session repository projection bypassed Survey semantics: %#v", agent)
	}
	if got.Counts["blocked"] != 1 || got.Counts["unread"] != 1 || got.Counts["dirty"] != 1 {
		t.Fatalf("Overview counts do not match Observation: %#v", got.Counts)
	}
}

func TestOverviewUnavailableObservationIsReadOnlyAndDoesNotWarnAsDead(t *testing.T) {
	dir := t.TempDir()
	state := &State{
		Projects: []Project{{ID: "project-1", Name: "NAVI", Path: dir, MainBranch: "main"}},
		Agents:   []Session{{ID: "session-1", Name: "one", ProjectID: "project-1", Project: "NAVI", Dir: dir}},
	}
	snapshot := ObservationSnapshot{
		Availability: ObservationUnavailable,
		Sessions: []SessionObservation{{
			SessionID: "session-1", Availability: ObservationUnavailable,
			Presence: SessionPresenceUnknown, Status: StatusUnknown,
			Attention: AttentionUnknown, Occupancy: OccupancyUnknown,
		}},
	}
	repositories := &recordingOverviewRepositories{err: errors.New("repository process unavailable")}

	got := buildOverviewFromObservationUsing(state, snapshot, repositories)
	if repositories.calls != 1 {
		t.Fatalf("Survey calls = %d, want 1", repositories.calls)
	}
	if len(state.Agents) != 1 || state.Agents[0].ID != "session-1" {
		t.Fatalf("Overview mutated the Registry-shaped input: %#v", state.Agents)
	}
	if got.Counts["unknown"] != 1 {
		t.Fatalf("unavailable tmux was not preserved as unknown: %#v", got.Counts)
	}
	project := got.Projects[0]
	if project.RepositoryKnowledge != RepositoryUnknown || project.MainBranchKnown ||
		project.HeadBranchKnown || project.WorktreesKnown || len(project.Problems) == 0 {
		t.Fatalf("repository failure was collapsed into values: %#v", project)
	}
	wt := project.Worktrees[0]
	if wt.CheckoutKnown || wt.ChangesKnown || wt.DivergenceKnown || wt.Clean {
		t.Fatalf("unknown Worktree facts were collapsed into clean/zero claims: %#v", wt)
	}
	if got.Counts["dirty"] != 0 {
		t.Fatalf("unknown changes were counted as dirty: %#v", got.Counts)
	}
	if len(wt.Warnings) != 0 {
		t.Fatalf("unknown Session was treated as dead: %#v", wt.Warnings)
	}
}

func TestOverviewPreservesPartialWorktreeKnowledge(t *testing.T) {
	dir := t.TempDir()
	statusProblem := &RepositoryProblem{Operation: "status", Message: "status failed"}
	divergenceProblem := &RepositoryProblem{Operation: "divergence", Message: "main branch unavailable"}
	state := &State{Projects: []Project{{ID: "project-1", Name: "NAVI", Path: dir}}}
	repositories := &recordingOverviewRepositories{survey: RepositoriesSurvey{Projects: []RepositoryProjectSurvey{{
		ID: "project-1", Name: "NAVI", Path: dir, Presence: RepositoryKnown,
		MainBranch: repositoryKnownFact("main"),
		Worktrees: repositoryKnownFact([]RepositoryWorktree{{
			Path: dir, Main: true,
			Checkout:   repositoryKnownFact(RepositoryCheckout{Kind: RepositoryBranchCheckout, Branch: "feature"}),
			Head:       repositoryKnownFact("head-1"),
			Changes:    RepositoryFact[RepositoryWorkingChanges]{State: RepositoryUnknown, Problem: statusProblem},
			Divergence: RepositoryFact[RepositoryDivergence]{State: RepositoryUnknown, Problem: divergenceProblem},
		}}),
	}}}}

	got := buildOverviewFromObservationUsing(state, ObservationSnapshot{}, repositories)
	project := got.Projects[0]
	if project.RepositoryKnowledge != RepositoryKnown || !project.MainBranchKnown || !project.HeadBranchKnown || !project.WorktreesKnown {
		t.Fatalf("known Project facts were lost: %#v", project)
	}
	worktree := project.Worktrees[0]
	if !worktree.CheckoutKnown || worktree.Branch != "feature" || worktree.ChangesKnown || worktree.DivergenceKnown {
		t.Fatalf("partial Worktree knowledge was collapsed: %#v", worktree)
	}
	if worktree.Clean || got.Counts["dirty"] != 0 || len(worktree.Warnings) != 0 {
		t.Fatalf("unknown changes/divergence produced clean, dirty, or warning claims: worktree=%#v counts=%#v", worktree, got.Counts)
	}
	if len(worktree.Problems) != 2 {
		t.Fatalf("partial Worktree problems missing: %#v", worktree.Problems)
	}
}

func TestOverviewProjectsQueuedOutboxMessages(t *testing.T) {
	session := Session{ID: "session-1", Name: "hera", RuntimeName: "mgt-hera"}
	session.Outbox = []QueuedMessage{
		{
			ID: "m1", Kind: QueuedMessageKindMessage, Text: "bitte\nweiter   machen",
			EnqueuedAt: time.Now().Add(-2 * time.Minute),
		},
		{
			ID: "m2", Kind: QueuedMessageKindHandoff, Text: strings.Repeat("ü", 120),
			EnqueuedAt: time.Now().Add(-time.Minute), AttemptedAt: time.Now().Add(-30 * time.Second),
		},
	}
	observed := SessionObservation{
		SessionID: session.ID, Availability: ObservationAvailable, Presence: SessionPresencePresent,
		Status: StatusRunning, Tool: AgentToolClaude, Content: "arbeitet", ContentKnown: true,
	}

	queued := toOvAgent(session, observed, "", SessionResumability{}).Queued
	if len(queued) != 2 {
		t.Fatalf("queued projection = %+v, want both Outbox messages in order", queued)
	}
	if queued[0].ID != "m1" || queued[0].Kind != string(QueuedMessageKindMessage) ||
		queued[0].Preview != "bitte weiter machen" || queued[0].Stuck {
		t.Fatalf("first queued message = %+v", queued[0])
	}
	if queued[0].Age == "" {
		t.Fatalf("queued message lost its age: %+v", queued[0])
	}
	if !queued[1].Stuck || queued[1].Kind != string(QueuedMessageKindHandoff) {
		t.Fatalf("attempted message is not reported as stuck: %+v", queued[1])
	}
	if want := strings.Repeat("ü", 80) + "…"; queued[1].Preview != want {
		t.Fatalf("preview = %q, want %q", queued[1].Preview, want)
	}
	if len(toOvAgent(Session{ID: "session-2", Name: "leer"}, observed, "", SessionResumability{}).Queued) != 0 {
		t.Fatal("an empty Outbox produced a queued projection")
	}
}

// TestOverviewEmitsLivenessAsAFact hält fest, dass Lebendigkeit und Arbeit
// Tatsachen des Overviews sind und nicht aus dem Status-String abgeleitet
// werden müssen. Vier Oberflächen leiteten sie vorher mit vier verschiedenen
// Mengen ab; keine davon kannte "resumable".
func TestOverviewEmitsLivenessAsAFact(t *testing.T) {
	cases := []struct {
		status  AgentStatus
		live    bool
		working bool
	}{
		{StatusRunning, true, true},
		{StatusAgents, true, true},
		{StatusShell, true, true},
		{StatusTerm, true, true},
		{StatusBlocked, true, false},
		{StatusIdle, true, false},
		{StatusDone, true, false},
		{StatusExited, false, false},
		{StatusDead, false, false},
		{StatusUnknown, false, false},
	}
	for _, c := range cases {
		if got := agentAlive(c.status); got != c.live {
			t.Errorf("agentAlive(%v) = %v, erwartet %v", c.status, got, c.live)
		}
		if got := agentWorking(c.status); got != c.working {
			t.Errorf("agentWorking(%v) = %v, erwartet %v", c.status, got, c.working)
		}
		if agentWorking(c.status) && !agentAlive(c.status) {
			t.Errorf("Status %v arbeitet, ohne zu leben", c.status)
		}
	}
}
