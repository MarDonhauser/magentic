package main

import (
	"context"
	"os"
	"testing"
	"time"

	"magentic/core"
)

func TestRealPollCost(t *testing.T) {
	if os.Getenv("POLLCOST") == "" {
		t.Skip("nur mit POLLCOST=1")
	}
	st, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("Projekte=%d Agents=%d", len(st.Projects), len(st.Agents))

	ctx := context.Background()
	start := time.Now()
	observation := core.Observe(ctx, st.Agents)
	t.Logf("Observe(%d sessions): %v (availability=%s)", len(st.Agents), time.Since(start), observation.Availability)

	start = time.Now()
	survey, surveyErr := core.NewRepositories().Survey(ctx, st.Projects)
	t.Logf("Repositories.Survey(%d projects): %v (results=%d, err=%v)", len(st.Projects), time.Since(start), len(survey.Projects), surveyErr)

	start = time.Now()
	res := pollCmd(*st, nil)()
	_ = res
	t.Logf("GESAMTER pollCmd: %v", time.Since(start))
}
