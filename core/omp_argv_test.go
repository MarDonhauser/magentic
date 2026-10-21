package core

import (
	"reflect"
	"testing"
)

// assertAlwaysAskAndProfile checks the two invariants every OmpArgv call must
// satisfy, and that none of the forbidden flags ever appears.
func assertAlwaysAskAndProfile(t *testing.T, argv []string) {
	t.Helper()
	joined := argv
	hasPair := func(flag, value string) bool {
		for i := 0; i+1 < len(joined); i++ {
			if joined[i] == flag && joined[i+1] == value {
				return true
			}
		}
		return false
	}
	if !hasPair("--approval-mode", "always-ask") {
		t.Fatalf("argv %v missing --approval-mode always-ask", argv)
	}
	if !hasPair("--profile", OmpMagenticProfile) {
		t.Fatalf("argv %v missing --profile %s", argv, OmpMagenticProfile)
	}
	for _, forbidden := range []string{"--auto-approve", "yolo", "--no-session"} {
		for _, a := range argv {
			if a == forbidden {
				t.Fatalf("argv %v must never carry %q", argv, forbidden)
			}
		}
	}
}

func TestOmpArgvFreshSession(t *testing.T) {
	session := Session{Dir: "/home/dev/project"}
	argv, err := OmpArgv(session, nil, "new")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--mode", "rpc-ui",
		"--approval-mode", "always-ask",
		"--profile", OmpMagenticProfile,
		"--cwd", "/home/dev/project",
	}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	assertAlwaysAskAndProfile(t, argv)
}

func TestOmpArgvResumedSession(t *testing.T) {
	session := Session{Dir: "/home/dev/project"}
	run := &AgentRunRef{Vendor: AgentVendorOmp, ExternalID: "01a0ca7c-72de-73ea-ac9e-5bd35a7de617"}
	argv, err := OmpArgv(session, run, "resume")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--mode", "rpc-ui",
		"--approval-mode", "always-ask",
		"--profile", OmpMagenticProfile,
		"--cwd", "/home/dev/project",
		"--resume", run.ExternalID,
	}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	assertAlwaysAskAndProfile(t, argv)
}

func TestOmpArgvResumeWithoutRunRefIsRefused(t *testing.T) {
	session := Session{Dir: "/home/dev/project"}
	if _, err := OmpArgv(session, nil, "resume"); err == nil {
		t.Fatal("resuming without a run ref must be refused")
	}
	empty := &AgentRunRef{Vendor: AgentVendorOmp}
	if _, err := OmpArgv(session, empty, "resume"); err == nil {
		t.Fatal("resuming with an empty ExternalID must be refused")
	}
}

func TestOmpArgvWorktreeDir(t *testing.T) {
	session := Session{Dir: "/home/dev/project/.worktrees/feature-x", Worktree: true}
	argv, err := OmpArgv(session, nil, "new")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--mode", "rpc-ui",
		"--approval-mode", "always-ask",
		"--profile", OmpMagenticProfile,
		"--cwd", "/home/dev/project/.worktrees/feature-x",
	}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	assertAlwaysAskAndProfile(t, argv)
}

func TestOmpArgvUnknownModelOmitsFlag(t *testing.T) {
	session := Session{Dir: "/home/dev/project"}
	argv, err := OmpArgv(session, nil, "new")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range argv {
		if a == "--model" {
			t.Fatalf("argv %v must omit --model when the model is unknown", argv)
		}
	}
	assertAlwaysAskAndProfile(t, argv)
}

func TestOmpArgvKnownModel(t *testing.T) {
	session := Session{Dir: "/home/dev/project", Model: "qwen3.5:9b"}
	argv, err := OmpArgv(session, nil, "new")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--mode", "rpc-ui",
		"--approval-mode", "always-ask",
		"--profile", OmpMagenticProfile,
		"--cwd", "/home/dev/project",
		"--model", "qwen3.5:9b",
	}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
	assertAlwaysAskAndProfile(t, argv)
}
