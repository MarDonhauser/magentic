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

func TestRepositoriesCommitHistoryRejectsSuccessfulMalformedOutput(t *testing.T) {
	tests := []struct {
		name string
		out  string
	}{
		{name: "truncated", out: "malformed\x1e"},
		{name: "invalid timestamp", out: "hash\x1fshort\x1f\x1fsubject\x1fauthor\x1fnot-a-time\x1fHEAD -> main\x1e"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repositories := newRepositories(repositoriesRunnerFunc(func(_ context.Context, _ string, args ...string) (string, error) {
				if len(args) > 0 && args[0] == "log" {
					return test.out, nil
				}
				return "", errors.New("unexpected command")
			}))
			fact := repositories.CommitHistory(context.Background(), "/repo", 10)
			if fact.State != RepositoryUnknown || fact.Problem == nil || !strings.Contains(fact.Problem.Message, "malformed") {
				t.Fatalf("malformed successful log = %#v", fact)
			}
		})
	}
}

func TestRepositoriesCommitHistoryAndRefFactsAreValidated(t *testing.T) {
	runner := repositoriesRunnerFunc(func(_ context.Context, _ string, args ...string) (string, error) {
		switch args[0] {
		case "log":
			return "hash\x1fshort\x1fparent\x1fsubject\x1fauthor\x1f1700000000\x1fHEAD -> main\x1e", nil
		case "branch":
			return "main\ntopic\n", nil
		case "rev-list":
			return "2\t3\n", nil
		default:
			return "", errors.New("unexpected command")
		}
	})
	repositories := newRepositories(runner)
	history := repositories.CommitHistory(context.Background(), "/repo", 10)
	if !history.Known() || len(history.Value) != 1 || history.Value[0].Timestamp != 1700000000 {
		t.Fatalf("history = %#v", history)
	}
	merged := repositories.MergedBranches(context.Background(), "/repo", "main")
	if !merged.Known() || !merged.Value["topic"] {
		t.Fatalf("merged branches = %#v", merged)
	}
	divergence := repositories.CompareRefs(context.Background(), "/repo", "main", "topic")
	if !divergence.Known() || divergence.Value.Ahead != 3 || divergence.Value.Behind != 2 {
		t.Fatalf("divergence = %#v", divergence)
	}
}
