package main

import (
	"os"
	"testing"
	"time"
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

	start := time.Now()
	CollectStatuses(st.Agents)
	t.Logf("CollectStatuses(%d agents): %v", len(st.Agents), time.Since(start))

	dirs := map[string]bool{}
	for _, a := range st.Agents {
		dirs[a.Dir] = true
	}
	for _, p := range st.Projects {
		dirs[p.Path] = true
	}
	for d := range dirs {
		s := time.Now()
		gi := CollectGitInfo(d)
		el := time.Since(s)
		t.Logf("CollectGitInfo %-60s %8v (repo=%v)", d, el, gi.IsRepo)
	}

	start = time.Now()
	res := pollCmd(*st, nil)()
	_ = res
	t.Logf("GESAMTER pollCmd: %v", time.Since(start))
}
