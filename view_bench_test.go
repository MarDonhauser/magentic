package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"magentic/core"
)

// benchmarkModel builds a tree of the size a busy day produces, with one full
// terminal preview per Session.
func benchmarkModel(projects, sessionsPerProject int) model {
	state := &State{}
	observed := map[tuiSessionKey]core.SessionObservation{}
	content := strings.Repeat(strings.Repeat("x", 110)+"\n", 200)
	for p := 0; p < projects; p++ {
		name := fmt.Sprintf("proj%d", p)
		state.Projects = append(state.Projects, Project{
			ID: core.ProjectID(name), Name: name, Path: "/tmp/" + name,
		})
		for s := 0; s < sessionsPerProject; s++ {
			session := Agent{
				ID:          core.SessionID(fmt.Sprintf("%s-%d", name, s)),
				Name:        fmt.Sprintf("%s-sess%d", name, s),
				Project:     name,
				Dir:         "/tmp/" + name,
				CreatedAt:   time.Now().Add(-time.Hour),
				RuntimeName: fmt.Sprintf("mgt-%s-%d", name, s),
			}
			state.Agents = append(state.Agents, session)
			observed[sessionKey(session)] = core.SessionObservation{
				SessionID: session.ID, Availability: core.ObservationAvailable,
				Presence: core.SessionPresencePresent, Status: StatusRunning,
				ContentKnown: true, Content: content,
				Activity: time.Now(), ActivityKnown: true,
			}
		}
	}
	m := model{
		state: state, collapsed: map[string]bool{},
		attention: core.NewAttentionPlanner(core.AttentionPlannerConfig{}),
		width:     180, height: 50,
	}
	m.poll.observed = observed
	return m
}

func BenchmarkView(b *testing.B) {
	m := benchmarkModel(6, 5)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

func BenchmarkRows(b *testing.B) {
	m := benchmarkModel(6, 5)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.rows()
	}
}
