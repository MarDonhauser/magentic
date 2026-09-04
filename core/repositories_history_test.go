package core

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type repositoriesRunnerFunc func(context.Context, string, ...string) (string, error)

func (run repositoriesRunnerFunc) Run(ctx context.Context, dir string, args ...string) (string, error) {
	return run(ctx, dir, args...)
}

func repositoryHistoryFixture(hash, parents, timestamp string) string {
	short := hash
	if len(short) > 7 {
		short = short[:7]
	}
	return hash + "\x1f" + short + "\x1f" + parents + "\x1fsubject\x1fauthor\x1f" + timestamp + "\x1fHEAD -> main\x1e"
}

func TestRepositoriesCommitHistoryRejectsSuccessfulMalformedOutput(t *testing.T) {
	hash := strings.Repeat("a", 40)
	tests := []struct {
		name string
		out  string
	}{
		{name: "truncated record", out: "malformed\x1e"},
		{name: "missing final terminator", out: strings.TrimSuffix(repositoryHistoryFixture(hash, "", "1700000000"), "\x1e")},
		{name: "invalid object ID", out: repositoryHistoryFixture(strings.Repeat("g", 40), "", "1700000000")},
		{name: "invalid parent object ID", out: repositoryHistoryFixture(hash, strings.Repeat("b", 39), "1700000000")},
		{name: "invalid timestamp", out: repositoryHistoryFixture(hash, "", "not-a-time")},
		{name: "negative timestamp", out: repositoryHistoryFixture(hash, "", "-1")},
		{name: "newline before terminator", out: strings.Replace(repositoryHistoryFixture(hash, "", "1700000000"), "\x1e", "\n\x1e", 1)},
		{name: "double final newline", out: repositoryHistoryFixture(hash, "", "1700000000") + "\n\n"},
		{name: "double inter-record newline", out: repositoryHistoryFixture(hash, "", "1700000000") + "\n\n" + repositoryHistoryFixture(strings.Repeat("b", 40), "", "1700000001")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repositories := newRepositories(repositoriesRunnerFunc(func(_ context.Context, _ string, args ...string) (string, error) {
				if len(args) > 0 && args[0] == "log" {
					return test.out, nil
				}
				return "", errors.New("unexpected command")
			}))
			fact := repositories.commitHistory(context.Background(), "/repo", 10)
			if fact.State != RepositoryUnknown || fact.Problem == nil || !strings.Contains(fact.Problem.Message, "malformed") {
				t.Fatalf("malformed successful log = %#v", fact)
			}
		})
	}
}

func TestRepositoriesCommitHistoryAndRefFactsAreValidated(t *testing.T) {
	hash := strings.Repeat("a", 40)
	parent := strings.Repeat("b", 40)
	runner := repositoriesRunnerFunc(func(_ context.Context, _ string, args ...string) (string, error) {
		switch args[0] {
		case "log":
			return repositoryHistoryFixture(hash, parent, "1700000000") + "\n", nil
		case "branch":
			return "main\ntopic\n", nil
		case "rev-list":
			return "2\t3\n", nil
		default:
			return "", errors.New("unexpected command")
		}
	})
	repositories := newRepositories(runner)
	history := repositories.commitHistory(context.Background(), "/repo", 10)
	if !history.Known() || len(history.Value) != 1 || history.Value[0].Timestamp != 1700000000 {
		t.Fatalf("history = %#v", history)
	}
	merged := repositories.mergedBranches(context.Background(), "/repo", "main")
	if !merged.Known() || !merged.Value["topic"] {
		t.Fatalf("merged branches = %#v", merged)
	}
	divergence := repositories.compareRefsFact(context.Background(), "/repo", "main", "topic")
	if !divergence.Known() || divergence.Value.Ahead != 3 || divergence.Value.Behind != 2 {
		t.Fatalf("divergence = %#v", divergence)
	}
}

func TestRepositoryCommitHistoryAcceptsSHA1AndSHA256ObjectIDs(t *testing.T) {
	for _, length := range []int{40, 64} {
		name := "sha1"
		if length == 64 {
			name = "sha256"
		}
		t.Run(name, func(t *testing.T) {
			hash := strings.Repeat("a", length)
			parent := strings.Repeat("b", length)
			commits, err := parseRepositoryCommitHistory(repositoryHistoryFixture(hash, parent, "1700000000"))
			if err != nil || len(commits) != 1 || commits[0].Hash != hash || len(commits[0].Parents) != 1 || commits[0].Parents[0] != parent {
				t.Fatalf("%d-hex history = %#v, %v", length, commits, err)
			}
		})
	}
}

func TestRepositoriesMergedBranchesRejectsMalformedSuccessfulOutput(t *testing.T) {
	tests := []struct {
		name string
		out  string
	}{
		{name: "truncated", out: "main"},
		{name: "leading whitespace", out: " main\n"},
		{name: "invalid ref", out: "topic..broken\n"},
		{name: "empty line", out: "main\n\n"},
		{name: "duplicate", out: "main\nmain\n"},
		{name: "unknown control", out: "topic\x00name\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repositories := newRepositories(repositoriesRunnerFunc(func(_ context.Context, _ string, args ...string) (string, error) {
				if len(args) > 0 && args[0] == "branch" {
					return test.out, nil
				}
				return "", errors.New("unexpected command")
			}))
			fact := repositories.mergedBranches(context.Background(), "/repo", "main")
			if fact.State != RepositoryUnknown || fact.Problem == nil || fact.Problem.Operation != "merged_branches" {
				t.Fatalf("malformed successful branch list became known: %#v", fact)
			}
		})
	}
}
