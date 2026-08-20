package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRepositoryCommitMatchesEffectiveIdentity(t *testing.T) {
	const email = "martin.donhauser@lhind.dlh.de"
	const name = "donhauser, martin"
	tests := []struct {
		name        string
		commitEmail string
		commitName  string
		email       string
		author      string
		want        bool
	}{
		{name: "email", commitEmail: email, commitName: "Someone", email: email, author: name, want: true},
		{name: "case folded email", commitEmail: "Martin.Donhauser@LHIND.dlh.de", commitName: "Someone", email: email, author: name, want: true},
		{name: "name", commitEmail: "private@example.com", commitName: "DONHAUSER, MARTIN", email: email, author: name, want: true},
		{name: "surrounding space", commitEmail: " " + email + " ", commitName: "Someone", email: email, author: name, want: true},
		{name: "other author", commitEmail: "kai@example.com", commitName: "Kai", email: email, author: name},
		{name: "no identity", commitEmail: email, commitName: name},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := repositoryCommitMatchesIdentity(test.commitEmail, test.commitName, test.email, test.author); got != test.want {
				t.Fatalf("repositoryCommitMatchesIdentity() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestRepositoriesOwnCommitsSinceRequiresKnownIdentity(t *testing.T) {
	tests := []struct {
		name  string
		steps []repositoriesRunnerStep
	}{
		{
			name: "identity absent",
			steps: []repositoriesRunnerStep{
				{dir: "/repo", args: []string{"config", "user.email"}},
				{dir: "/repo", args: []string{"config", "user.name"}},
			},
		},
		{
			name: "identity unavailable",
			steps: []repositoriesRunnerStep{
				{dir: "/repo", args: []string{"config", "user.email"}, err: errors.New("email denied")},
				{dir: "/repo", args: []string{"config", "user.name"}, err: errors.New("name denied")},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &repositoriesRecordingRunner{t: t, steps: test.steps}
			fact := newRepositories(runner).OwnCommitsSince(context.Background(), "/repo", "2026-01-01")
			runner.assertDone()
			if fact.State != RepositoryUnknown || fact.Problem == nil || fact.Problem.Operation != repositoryProblemOwnCommitIdentity {
				t.Fatalf("identity failure became an exact empty series: %#v", fact)
			}
		})
	}
}

func TestRepositoriesOwnCommitsSinceRejectsMalformedSuccessfulIdentity(t *testing.T) {
	tests := []struct {
		name     string
		emailOut string
		nameOut  string
	}{
		{name: "email missing terminator", emailOut: "me@example.com", nameOut: "Me\n"},
		{name: "email multiline", emailOut: "me@example.com\nother@example.com\n", nameOut: "Me\n"},
		{name: "name missing terminator", emailOut: "me@example.com\n", nameOut: "Me"},
		{name: "name multiline", emailOut: "me@example.com\n", nameOut: "Me\nOther\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
				{dir: "/repo", args: []string{"config", "user.email"}, output: test.emailOut},
				{dir: "/repo", args: []string{"config", "user.name"}, output: test.nameOut},
			}}
			fact := newRepositories(runner).OwnCommitsSince(context.Background(), "/repo", "2026-01-01")
			runner.assertDone()
			if fact.State != RepositoryUnknown || fact.Problem == nil || fact.Problem.Operation != repositoryProblemOwnCommitIdentity {
				t.Fatalf("malformed successful identity became known: %#v", fact)
			}
		})
	}
}

func TestRepositoriesOwnCommitsSinceKeepsLogFailureUnknown(t *testing.T) {
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
		{dir: "/repo", args: []string{"config", "user.email"}, output: "me@example.com\n"},
		{dir: "/repo", args: []string{"config", "user.name"}, err: errors.New("name not configured")},
		{dir: "/repo", args: []string{"log", "--all", "--since=2026-01-01", "--format=" + repositoryOwnCommitFormat}, err: errors.New("log denied")},
	}}
	fact := newRepositories(runner).OwnCommitsSince(context.Background(), "/repo", "2026-01-01")
	runner.assertDone()
	if fact.State != RepositoryUnknown || fact.Problem == nil || fact.Problem.Operation != repositoryProblemOwnCommitLog || !strings.Contains(fact.Problem.Message, "log denied") {
		t.Fatalf("log failure became an exact empty series: %#v", fact)
	}
}

func TestRepositoriesOwnCommitsSinceValidatesRecordsAndKeepsKnownSubtotal(t *testing.T) {
	tests := []struct {
		name           string
		log            string
		wantTimestamps []int64
	}{
		{name: "malformed record", log: "malformed\x1e"},
		{name: "missing terminator", log: "1700000000\x1fme@example.com\x1fMe\n", wantTimestamps: []int64{1700000000}},
		{name: "invalid timestamp", log: "1700000000\x1fme@example.com\x1fMe\x1enot-a-time\x1fme@example.com\x1fMe\x1e", wantTimestamps: []int64{1700000000}},
		{name: "negative timestamp", log: "-1\x1fme@example.com\x1fMe\x1e"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
				{dir: "/repo", args: []string{"config", "user.email"}, output: "me@example.com\n"},
				{dir: "/repo", args: []string{"config", "user.name"}, output: "Me\n"},
				{dir: "/repo", args: []string{"log", "--all", "--since=2026-01-01", "--format=" + repositoryOwnCommitFormat}, output: test.log},
			}}
			fact := newRepositories(runner).OwnCommitsSince(context.Background(), "/repo", "2026-01-01")
			runner.assertDone()
			if fact.State != RepositoryPartial || fact.Problem == nil || fact.Problem.Operation != repositoryProblemOwnCommitMalformed {
				t.Fatalf("malformed successful history became exact: %#v", fact)
			}
			if len(fact.Value.Timestamps) != len(test.wantTimestamps) {
				t.Fatalf("known subtotal = %v, want %v", fact.Value.Timestamps, test.wantTimestamps)
			}
			for i := range test.wantTimestamps {
				if fact.Value.Timestamps[i] != test.wantTimestamps[i] {
					t.Fatalf("known subtotal = %v, want %v", fact.Value.Timestamps, test.wantTimestamps)
				}
			}
		})
	}
}

func TestRepositoriesOwnCommitsSinceReturnsValidatedOwnSeries(t *testing.T) {
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
		{dir: "/repo", args: []string{"config", "user.email"}, output: "me@example.com\n"},
		{dir: "/repo", args: []string{"config", "user.name"}, output: "Me\n"},
		{dir: "/repo", args: []string{"log", "--all", "--since=2026-01-01", "--format=" + repositoryOwnCommitFormat}, output: "1700000002\x1fother@example.com\x1fOther\x1e\n1700000001\x1fME@example.com\x1fSomeone\x1e\n"},
	}}
	fact := newRepositories(runner).OwnCommitsSince(context.Background(), "/repo", "2026-01-01")
	runner.assertDone()
	if !fact.Known() || len(fact.Value.Timestamps) != 1 || fact.Value.Timestamps[0] != 1700000001 {
		t.Fatalf("OwnCommitsSince() = %#v", fact)
	}
}
