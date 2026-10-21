package core

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestVerifyOmpApprovalGateLive runs the behavioral verification against the
// installed omp under the magentic profile. It is opt-in because it starts a
// real model turn: set MAGENTIC_OMP_LIVE=1, and give the magentic profile a
// default model first, or omp picks whichever provider it finds.
func TestVerifyOmpApprovalGateLive(t *testing.T) {
	if os.Getenv("MAGENTIC_OMP_LIVE") != "1" {
		t.Skip("MAGENTIC_OMP_LIVE=1 startet einen echten omp-Lauf")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	result, err := VerifyOmpApprovalGate(ctx, newOmpExecProcess(""), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("omp %s: %s — %s", result.OmpVersion, result.Outcome, result.Reason)
	if result.Outcome == OmpVerifyOutcomeFailed {
		t.Fatalf("approval gate failed against the installed omp: %s", result.Reason)
	}
}
