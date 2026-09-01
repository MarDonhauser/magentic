package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"magentic/core"
)

type fakeTUIRepositories struct {
	survey          core.RepositoriesSurvey
	surveyErr       error
	surveyCalls     int
	inspectRequests []core.RepositoryInspectRequest
	inspections     map[string]core.RepositoryInspection
	inspectErrors   map[string]error
}

func (f *fakeTUIRepositories) Survey(_ context.Context, _ []core.Project) (core.RepositoriesSurvey, error) {
	f.surveyCalls++
	return f.survey, f.surveyErr
}

func (f *fakeTUIRepositories) Inspect(_ context.Context, request core.RepositoryInspectRequest) (core.RepositoryInspection, error) {
	f.inspectRequests = append(f.inspectRequests, request)
	if err := f.inspectErrors[request.Directory]; err != nil {
		return core.RepositoryInspection{}, err
	}
	return f.inspections[request.Directory], nil
}

func tuiKnownFact[T any](value T) core.RepositoryFact[T] {
	return core.RepositoryFact[T]{State: core.RepositoryKnown, Value: value}
}

func TestCollectPollModuleFactsUsesOneObservationAndSurveyPass(t *testing.T) {
	project := Project{ID: "project-1", Name: "magentic", Path: "/repo", MainBranch: "main"}
	baseline := Agent{
		ID: "session-1", ProjectID: project.ID, Project: project.Name,
		Name: "baseline", RuntimeName: "mgt-baseline", Dir: "/repo-wt",
		BaseCommit: "base-head", BaseDirty: []string{"already-dirty.txt"},
	}
	selected := Agent{
		ID: "session-2", ProjectID: project.ID, Project: project.Name,
		Name: "selected", RuntimeName: "mgt-selected", Dir: "/repo",
	}
	state := State{Projects: []Project{project}, Agents: []Agent{baseline, selected}}

	observeCalls := 0
	observe := func(_ context.Context, sessions []core.Session) core.ObservationSnapshot {
		observeCalls++
		if len(sessions) != 2 || sessions[0].ID != baseline.ID || sessions[1].ID != selected.ID {
			t.Fatalf("Observe received wrong Sessions: %#v", sessions)
		}
		return core.ObservationSnapshot{
			Availability: core.ObservationAvailable,
			Sessions: []core.SessionObservation{
				{SessionID: sessions[0].ID, Availability: core.ObservationAvailable, Presence: core.SessionPresencePresent, Status: StatusRunning},
				{SessionID: sessions[1].ID, Availability: core.ObservationAvailable, Presence: core.SessionPresencePresent, Status: StatusIdle, Content: "ready", ContentKnown: true},
			},
		}
	}
	repositories := &fakeTUIRepositories{
		survey: core.RepositoriesSurvey{Projects: []core.RepositoryProjectSurvey{{
			ID: project.ID, Name: project.Name, Path: project.Path, Presence: core.RepositoryKnown,
			MainBranch: tuiKnownFact("main"),
			Worktrees: tuiKnownFact([]core.RepositoryWorktree{
				{Path: "/repo", Main: true},
				{Path: "/repo-wt"},
			}),
		}}},
		inspections: map[string]core.RepositoryInspection{
			"/repo-wt": {Directory: "/repo-wt", Presence: core.RepositoryKnown},
			"/repo":    {Directory: "/repo", Presence: core.RepositoryKnown},
		},
		inspectErrors: map[string]error{},
	}

	result := collectPollModuleFacts(context.Background(), state, &selected, observe, repositories)

	if observeCalls != 1 {
		t.Fatalf("Observe calls = %d, want 1", observeCalls)
	}
	if repositories.surveyCalls != 1 {
		t.Fatalf("Survey calls = %d, want 1", repositories.surveyCalls)
	}
	if len(repositories.inspectRequests) != 2 {
		t.Fatalf("Inspect requests = %#v, want baseline Session and selected Session", repositories.inspectRequests)
	}
	requests := map[string]core.RepositoryInspectRequest{}
	for _, request := range repositories.inspectRequests {
		requests[request.Directory] = request
	}
	if request := requests[baseline.Dir]; request.Against == nil || request.Against.Head != baseline.BaseCommit ||
		len(request.Against.DirtyPaths) != 1 || request.MainBranch != "main" {
		t.Fatalf("baseline Inspect request = %#v", request)
	}
	if request := requests[selected.Dir]; request.Against != nil || request.MainBranch != "main" {
		t.Fatalf("selected Inspect request = %#v", request)
	}
	if got := result.observed[sessionKey(selected)]; got.Status != StatusIdle || got.Content != "ready" {
		t.Fatalf("selected Observation = %#v", got)
	}
}

func TestCollectPollModuleFactsAssignsCopyOnlyIDToLegacyFixture(t *testing.T) {
	session := Agent{Name: "legacy", RuntimeName: "mgt-legacy", Dir: "/not-a-repo"}
	state := State{Agents: []Agent{session}}
	var observedID core.SessionID
	observe := func(_ context.Context, sessions []core.Session) core.ObservationSnapshot {
		observedID = sessions[0].ID
		return core.ObservationSnapshot{Sessions: []core.SessionObservation{{
			SessionID: sessions[0].ID, Availability: core.ObservationUnavailable,
			Presence: core.SessionPresenceUnknown, Status: StatusUnknown,
		}}}
	}
	repositories := &fakeTUIRepositories{
		surveyErr:     errors.New("git unavailable"),
		inspections:   map[string]core.RepositoryInspection{},
		inspectErrors: map[string]error{session.Dir: errors.New("git unavailable")},
	}

	result := collectPollModuleFacts(context.Background(), state, &session, observe, repositories)

	if observedID == "" {
		t.Fatal("Observe received an ID-less compatibility fixture")
	}
	if state.Agents[0].ID != "" {
		t.Fatalf("Registry copy was mutated with ephemeral ID %q", state.Agents[0].ID)
	}
	if got := result.observed[sessionKey(session)].Status; got != StatusUnknown {
		t.Fatalf("status = %v, want explicitly unknown", got)
	}
}

func TestUnavailableRuntimeRendersUnknownInsteadOfDead(t *testing.T) {
	session := Agent{ID: "session-1", Name: "one", RuntimeName: "mgt-one", Dir: "/repo"}
	state := State{Agents: []Agent{session}}
	m := model{
		state: &state,
		poll: pollResult{observed: map[tuiSessionKey]core.SessionObservation{
			sessionKey(session): {
				SessionID: session.ID, Availability: core.ObservationUnavailable,
				Presence: core.SessionPresenceUnknown, Status: StatusUnknown,
			},
		}},
	}

	header := ansi.Strip(m.renderHeader())
	if !strings.Contains(header, "1 unbekannt") {
		t.Fatalf("header does not expose unknown runtime state: %q", header)
	}
	if strings.Contains(header, "1 aus") {
		t.Fatalf("unavailable runtime was counted as dead: %q", header)
	}

	var lines []string
	m.addAgentGit(&session, 80, func(line string) { lines = append(lines, ansi.Strip(line)) })
	gitText := strings.Join(lines, "\n")
	if !strings.Contains(gitText, "Git-Status unbekannt") || strings.Contains(gitText, "kein Git-Repo") {
		t.Fatalf("unknown repository rendered as a negative fact: %q", gitText)
	}
}

func TestPartialSessionDeltaNeverRendersCleanOrZero(t *testing.T) {
	session := Agent{ID: "session-1", Name: "one", Dir: "/repo", BaseCommit: "base"}
	state := State{Agents: []Agent{session}}
	m := model{
		state: &state,
		poll: pollResult{
			inspections: map[tuiSessionKey]core.RepositoryInspection{
				sessionKey(session): {
					Presence:   core.RepositoryKnown,
					Checkout:   tuiKnownFact(core.RepositoryCheckout{Kind: core.RepositoryBranchCheckout, Branch: "main"}),
					Changes:    tuiKnownFact(core.RepositoryWorkingChanges{}),
					Divergence: core.RepositoryFact[core.RepositoryDivergence]{State: core.RepositoryUnknown},
					Delta: &core.RepositoryBaselineDelta{
						Paths:   core.RepositoryFact[[]string]{State: core.RepositoryUnknown},
						Commits: tuiKnownFact(0),
					},
				},
			},
		},
	}

	var lines []string
	m.addAgentGit(&session, 80, func(line string) { lines = append(lines, ansi.Strip(line)) })
	text := strings.Join(lines, "\n")
	if !strings.Contains(text, "Dateien unbekannt") {
		t.Fatalf("partial delta is not explicit: %q", text)
	}
	if strings.Contains(text, "nichts geändert") || strings.Contains(text, "0 Commit") {
		t.Fatalf("partial delta rendered as clean/zero: %q", text)
	}
	if got := m.sessionChangeMark(session); got != sessionChangesUnknown {
		t.Fatalf("tree mark = %v, want unknown", got)
	}
}

func TestPreviewUsesObservationFact(t *testing.T) {
	session := Agent{ID: "session-1", Name: "one", RuntimeName: "mgt-one"}
	calls := 0
	cmd := previewObservationCmd(session, func(_ context.Context, sessions []core.Session) core.ObservationSnapshot {
		calls++
		return core.ObservationSnapshot{
			Availability: core.ObservationPartial,
			Sessions: []core.SessionObservation{{
				SessionID: sessions[0].ID, Availability: core.ObservationPartial,
				Presence: core.SessionPresencePresent, Status: StatusUnknown, ContentKnown: false,
			}},
		}
	})

	message, ok := cmd().(previewMsg)
	if !ok {
		t.Fatalf("preview command returned %T", cmd())
	}
	if calls != 1 {
		t.Fatalf("Observe calls = %d, want 1", calls)
	}
	if message.key != sessionKey(session) || message.observation.ContentKnown ||
		message.observation.Presence != core.SessionPresencePresent {
		t.Fatalf("preview message = %#v", message)
	}
}

// The runtime pass and the Git pass now run on separate cadences. Each message
// must leave the other Module's facts in place, otherwise the slower pass would
// blank the panel it does not own.
func TestSeparatePollPassesKeepEachOthersFacts(t *testing.T) {
	session := Agent{ID: "session-1", Name: "one", RuntimeName: "mgt-one", Dir: "/repo"}
	state := State{Agents: []Agent{session}}
	key := sessionKey(session)
	m := model{state: &state, collapsed: map[string]bool{},
		attention: core.NewAttentionPlanner(core.AttentionPlannerConfig{})}

	updated, _ := m.Update(repositoryMsg(pollResult{
		repositories: core.RepositoriesSurvey{Projects: []core.RepositoryProjectSurvey{{
			Name: "repo", Path: "/repo", Presence: core.RepositoryKnown,
		}}},
		inspections: map[tuiSessionKey]core.RepositoryInspection{
			key: {Directory: "/repo", Presence: core.RepositoryKnown},
		},
		inspectionProblem: map[tuiSessionKey]string{},
	}))
	updated, _ = updated.(model).Update(observationMsg(pollResult{
		observation: core.ObservationSnapshot{Availability: core.ObservationAvailable},
		observed: map[tuiSessionKey]core.SessionObservation{
			key: {SessionID: session.ID, Availability: core.ObservationAvailable,
				Presence: core.SessionPresencePresent, Status: StatusRunning},
		},
	}))

	after := updated.(model)
	if got := after.statusFor(session); got != StatusRunning {
		t.Fatalf("status after repository pass = %v, want running", got)
	}
	if _, ok := after.poll.inspections[key]; !ok {
		t.Fatal("runtime pass discarded the repository inspection")
	}
	if after.repoBusy || after.pollBusy {
		t.Fatalf("busy flags not cleared: repo=%v poll=%v", after.repoBusy, after.pollBusy)
	}

	updated, _ = after.Update(repositoryMsg(pollResult{
		inspections:       map[tuiSessionKey]core.RepositoryInspection{},
		inspectionProblem: map[tuiSessionKey]string{},
	}))
	if got := updated.(model).statusFor(session); got != StatusRunning {
		t.Fatalf("repository pass discarded the Observation: status = %v", got)
	}
}
