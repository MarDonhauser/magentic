package main

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"magentic/core"
)

type deploymentRemoteReaderFunc func(context.Context, string, string) core.RepositoryFact[string]

func (f deploymentRemoteReaderFunc) RemoteURL(ctx context.Context, dir, remote string) core.RepositoryFact[string] {
	return f(ctx, dir, remote)
}

func TestDiscoverAzdoOrgProjectsSurfacesPartialRemoteCoverage(t *testing.T) {
	projects := []core.Project{
		{Name: "Known", Path: "/known"},
		{Name: "Blocked", Path: "/blocked"},
		{Name: "No checkout"},
	}
	reader := deploymentRemoteReaderFunc(func(_ context.Context, dir, remote string) core.RepositoryFact[string] {
		if remote != "origin" {
			return core.RepositoryFact[string]{State: core.RepositoryUnknown}
		}
		switch dir {
		case "/known":
			return core.RepositoryFact[string]{State: core.RepositoryKnown, Value: "git@ssh.dev.azure.com:v3/acme/platform/repository"}
		case "/blocked":
			return core.RepositoryFact[string]{
				State:   core.RepositoryUnknown,
				Problem: &core.RepositoryProblem{Operation: "remote_url", Message: "git unavailable"},
			}
		default:
			return core.RepositoryFact[string]{State: core.RepositoryUnknown}
		}
	})

	pairs, coverage := discoverAzdoOrgProjects(context.Background(), projects, reader)
	if want := [][2]string{{"acme", "platform"}}; !reflect.DeepEqual(pairs, want) {
		t.Fatalf("Azure remotes = %#v, want %#v", pairs, want)
	}
	if coverage.State != core.HistorySourcePartial || coverage.Projects != 2 || coverage.AvailableProjects != 1 {
		t.Fatalf("remote coverage = %#v", coverage)
	}
	if len(coverage.Problems) != 1 || coverage.Problems[0].Project != "Blocked" || coverage.Problems[0].Message != "git unavailable" {
		t.Fatalf("remote problems = %#v", coverage.Problems)
	}
}

func TestDiscoverAzdoOrgProjectsDistinguishesKnownAbsenceFromUnavailable(t *testing.T) {
	tests := []struct {
		name     string
		projects []core.Project
		fact     core.RepositoryFact[string]
		state    core.HistorySourceState
		problems int
	}{
		{
			name:     "known non Azure remote",
			projects: []core.Project{{Name: "Local", Path: "/local"}},
			fact:     core.RepositoryFact[string]{State: core.RepositoryKnown, Value: "../local-repository"},
			state:    core.HistorySourceAvailable,
		},
		{
			name:     "remote unavailable",
			projects: []core.Project{{Name: "Blocked", Path: "/blocked"}},
			fact: core.RepositoryFact[string]{
				State:   core.RepositoryUnknown,
				Problem: &core.RepositoryProblem{Operation: "remote_url", Message: errors.New("git denied").Error()},
			},
			state:    core.HistorySourceUnavailable,
			problems: 1,
		},
		{name: "no repositories", projects: []core.Project{{Name: "Unconfigured"}}, state: core.HistorySourceAbsent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := deploymentRemoteReaderFunc(func(context.Context, string, string) core.RepositoryFact[string] { return test.fact })
			pairs, coverage := discoverAzdoOrgProjects(context.Background(), test.projects, reader)
			if len(pairs) != 0 || coverage.State != test.state || len(coverage.Problems) != test.problems {
				t.Fatalf("discovery = pairs %#v, coverage %#v", pairs, coverage)
			}
		})
	}
}
