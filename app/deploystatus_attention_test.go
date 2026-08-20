package main

import (
	"testing"

	"magentic/core"
)

func TestDeploymentOutcomesTranslateOnlyObservedTransitions(t *testing.T) {
	previous := &DeployStatus{
		Builds: []BuildInfo{
			{URL: "build-ok", Repo: "web", Branch: "main", Status: "inProgress"},
			{URL: "build-stable", Repo: "api", Branch: "main", Status: "completed", Result: "failed"},
		},
		Apps: []ArgoApp{
			{Name: "checkout", Health: "Progressing"},
			{Name: "catalog", Health: "Healthy"},
		},
	}
	current := &DeployStatus{
		Builds: []BuildInfo{
			{URL: "build-ok", Repo: "web", Branch: "main", Status: "completed", Result: "succeeded"},
			{URL: "build-stable", Repo: "api", Branch: "main", Status: "completed", Result: "failed"},
			{URL: "build-new-failure", Repo: "worker", Branch: "feature", Status: "completed", Result: "failed"},
		},
		Apps: []ArgoApp{
			{Name: "checkout", Health: "Healthy"},
			{Name: "catalog", Health: "Degraded"},
		},
	}

	outcomes := deploymentOutcomes(previous, current)
	if len(outcomes) != 4 {
		t.Fatalf("deploymentOutcomes returned %d transitions: %#v", len(outcomes), outcomes)
	}
	want := map[core.AttentionDeploymentKind]bool{
		core.AttentionDeploymentBuildReady:  false,
		core.AttentionDeploymentBuildFailed: false,
		core.AttentionDeploymentAppHealthy:  false,
		core.AttentionDeploymentAppDegraded: false,
	}
	for _, outcome := range outcomes {
		if _, exists := want[outcome.Kind]; !exists {
			t.Fatalf("unexpected outcome %#v", outcome)
		}
		want[outcome.Kind] = true
		if outcome.Key == "" || outcome.Name == "" {
			t.Fatalf("outcome lacks stable transition facts: %#v", outcome)
		}
	}
	for kind, seen := range want {
		if !seen {
			t.Errorf("missing %s transition", kind)
		}
	}
}

func TestDeploymentOutcomeQueueDrainsExactlyOnce(t *testing.T) {
	app := &App{deployments: []core.AttentionDeploymentOutcome{{Key: "one"}, {Key: "two"}}}
	if got := app.takeDeploymentOutcomes(); len(got) != 2 {
		t.Fatalf("first drain returned %d outcomes", len(got))
	}
	if got := app.takeDeploymentOutcomes(); len(got) != 0 {
		t.Fatalf("second drain replayed %d outcomes", len(got))
	}
}
