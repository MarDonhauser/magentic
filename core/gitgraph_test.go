package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGitGraphJSONKeepsWorktreePathsPrivate(t *testing.T) {
	graph := GitGraph{
		Branches: []GraphBranch{{Name: "topic", Worktree: "/private/topic", WorktreeRef: "wt_topic", WorktreeLocation: "project-agents/topic"}},
		Commits:  []GraphCommit{{Refs: []GraphRef{{Name: "topic", Worktree: "/private/topic", WorktreeRef: "wt_topic", WorktreeLocation: "project-agents/topic"}}}},
	}
	data, err := json.Marshal(graph)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "/private/topic") || !strings.Contains(string(data), `"worktreeRef":"wt_topic"`) {
		t.Fatalf("GitGraph Worktree projection is not opaque: %s", data)
	}
}

func laneOf(commits []GraphCommit, hash string) int {
	for _, c := range commits {
		if c.Hash == hash {
			return c.Lane
		}
	}
	return -1
}

func TestAssignLanesLinear(t *testing.T) {
	commits := []GraphCommit{
		{Hash: "c", Parents: []string{"b"}},
		{Hash: "b", Parents: []string{"a"}},
		{Hash: "a"},
	}
	assignLanes(commits)
	for _, c := range commits {
		if c.Lane != 0 {
			t.Fatalf("%s: Lane %d, erwartet 0", c.Hash, c.Lane)
		}
	}
}

func TestAssignLanesBranchAndMerge(t *testing.T) {
	// m ist ein Merge von main (d) und feature (f); f zweigt von b ab.
	commits := []GraphCommit{
		{Hash: "m", Parents: []string{"d", "f"}},
		{Hash: "d", Parents: []string{"c"}},
		{Hash: "f", Parents: []string{"b"}},
		{Hash: "c", Parents: []string{"b"}},
		{Hash: "b", Parents: []string{"a"}},
		{Hash: "a"},
	}
	assignLanes(commits)

	if got := laneOf(commits, "m"); got != 0 {
		t.Fatalf("Merge-Commit auf Lane %d, erwartet 0", got)
	}
	if got := laneOf(commits, "d"); got != 0 {
		t.Fatalf("erster Parent auf Lane %d, erwartet 0", got)
	}
	if got := laneOf(commits, "f"); got == 0 {
		t.Fatal("zweiter Parent muss eine eigene Lane bekommen")
	}
	if got := laneOf(commits, "b"); got != 0 {
		t.Fatalf("gemeinsamer Vorfahr auf Lane %d, erwartet 0 — die Lanes müssen wieder zusammenlaufen", got)
	}
	if got := laneOf(commits, "a"); got != 0 {
		t.Fatalf("Wurzel auf Lane %d, erwartet 0", got)
	}
}

func TestAssignLanesReusesFreedLane(t *testing.T) {
	// Nach dem Zusammenlaufen bei b darf ein späterer Abzweig die frei
	// gewordene Lane wiederverwenden statt immer weiter nach rechts zu wandern.
	commits := []GraphCommit{
		{Hash: "m", Parents: []string{"d", "f"}},
		{Hash: "d", Parents: []string{"b"}},
		{Hash: "f", Parents: []string{"b"}},
		{Hash: "b", Parents: []string{"a"}},
		{Hash: "x", Parents: []string{"a"}},
		{Hash: "a"},
	}
	assignLanes(commits)

	if got := laneOf(commits, "x"); got > 1 {
		t.Fatalf("neuer Abzweig auf Lane %d — freie Lane wurde nicht wiederverwendet", got)
	}
}

func TestAssignLanesUnknownParent(t *testing.T) {
	commits := []GraphCommit{
		{Hash: "b", Parents: []string{"abgeschnitten"}},
	}
	assignLanes(commits)
	if commits[0].Lane != 0 {
		t.Fatalf("Lane %d, erwartet 0", commits[0].Lane)
	}
}

func TestParseRefs(t *testing.T) {
	refs := parseRefs("HEAD -> main, origin/main, tag: v1.2.0, agent/foo")
	if len(refs) != 4 {
		t.Fatalf("%d Refs, erwartet 4: %+v", len(refs), refs)
	}
	if refs[0].Name != "main" || refs[0].Kind != "branch" || !refs[0].Current {
		t.Fatalf("HEAD -> main falsch geparst: %+v", refs[0])
	}
	if refs[1].Kind != "remote" {
		t.Fatalf("origin/main als %q geparst, erwartet remote", refs[1].Kind)
	}
	if refs[2].Kind != "tag" || refs[2].Name != "v1.2.0" {
		t.Fatalf("Tag falsch geparst: %+v", refs[2])
	}
	if refs[3].Kind != "branch" || refs[3].Current {
		t.Fatalf("agent/foo falsch geparst: %+v", refs[3])
	}
}

func TestParseRefsEmpty(t *testing.T) {
	if refs := parseRefs("  "); refs != nil {
		t.Fatalf("erwartet nil, bekam %+v", refs)
	}
}

func TestParseGraphCommits(t *testing.T) {
	out := "aaa\x1fa\x1fbbb ccc\x1fMerge branch 'x'\x1fMartin\x1f1754200000\x1fHEAD -> main\x1e" +
		"bbb\x1fb\x1f\x1fInitial\x1fMartin\x1f1754100000\x1f\x1e"
	commits := parseGraphCommits(out, map[string]string{"main": "/tmp/wt"}, map[string][]string{"/tmp/wt": {"hera"}})
	if len(commits) != 2 {
		t.Fatalf("%d Commits, erwartet 2", len(commits))
	}
	if !commits[0].Merge || len(commits[0].Parents) != 2 {
		t.Fatalf("erster Commit muss ein Merge mit 2 Parents sein: %+v", commits[0])
	}
	if len(commits[0].Agents) != 1 || commits[0].Agents[0] != "hera" {
		t.Fatalf("Agent des Worktrees fehlt: %+v", commits[0].Agents)
	}
	if commits[0].Refs[0].Worktree != "/tmp/wt" {
		t.Fatalf("Worktree nicht am Ref: %+v", commits[0].Refs[0])
	}
	if commits[1].Merge || len(commits[1].Parents) != 0 {
		t.Fatalf("Wurzel-Commit falsch geparst: %+v", commits[1])
	}
}
