package main

import (
	"context"
	"time"

	"magentic/core"
)

func (a *App) SetActiveTerm(name string) {
	a.mu.Lock()
	a.activeTerm = name
	a.mu.Unlock()
}

func (a *App) getActiveTerm() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.activeTerm
}

func cloneObservation(snapshot core.ObservationSnapshot) core.ObservationSnapshot {
	copyOfSnapshot := snapshot
	copyOfSnapshot.Sessions = append([]core.SessionObservation(nil), snapshot.Sessions...)
	copyOfSnapshot.Problems = append([]core.ObservationProblem(nil), snapshot.Problems...)
	return copyOfSnapshot
}

func (a *App) storeObservation(snapshot core.ObservationSnapshot) {
	a.observationMu.Lock()
	a.observation = cloneObservation(snapshot)
	a.observationAt = time.Now()
	a.observationMu.Unlock()
}

func (a *App) observationFor(sessions []core.Session, fresh bool) core.ObservationSnapshot {
	if !fresh {
		a.observationMu.Lock()
		cached, cachedAt := cloneObservation(a.observation), a.observationAt
		a.observationMu.Unlock()
		if time.Since(cachedAt) <= 5*time.Second && observationCovers(cached, sessions) {
			return cached
		}
	}
	snapshot := core.Observe(context.Background(), sessions)
	a.storeObservation(snapshot)
	return snapshot
}

func observationCovers(snapshot core.ObservationSnapshot, sessions []core.Session) bool {
	if len(snapshot.Sessions) != len(sessions) {
		return false
	}
	observed := make(map[core.SessionID]bool, len(snapshot.Sessions))
	for _, session := range snapshot.Sessions {
		observed[session.SessionID] = true
	}
	for _, session := range sessions {
		if session.ID == "" || !observed[session.ID] {
			return false
		}
	}
	return true
}

func (a *App) watchLoop() {
	var lastErrLog time.Time
	for {
		time.Sleep(4 * time.Second)
		st, err := core.LoadState()
		if err != nil {
			if time.Since(lastErrLog) > time.Minute {
				core.Logf("watchLoop: state laden fehlgeschlagen: %v", err)
				lastErrLog = time.Now()
			}
			continue
		}
		if discovered := core.DiscoverNew(st); len(discovered) > 0 {
			changed, changeErr := core.OpenRegistry(core.StatePath()).Change(context.Background(), core.AddDiscoveredSessions(discovered))
			if changeErr != nil {
				if time.Since(lastErrLog) > time.Minute {
					core.Logf("watchLoop: Session-Discovery fehlgeschlagen: %v", changeErr)
					lastErrLog = time.Now()
				}
			} else {
				st = changed.Snapshot.MutableState()
			}
		}
		snapshot := core.Observe(context.Background(), st.Agents)
		a.storeObservation(snapshot)
		if len(st.Agents) > 0 && snapshot.Availability == core.ObservationUnavailable && time.Since(lastErrLog) > time.Minute {
			problem := "tmux unavailable"
			if len(snapshot.Problems) > 0 {
				problem = snapshot.Problems[0].Operation + ": " + snapshot.Problems[0].Message
			}
			core.Logf("watchLoop: Observation unavailable for %d Sessions (%s)", len(st.Agents), problem)
			lastErrLog = time.Now()
		}
		activeName := a.getActiveTerm()
		labels := make(map[core.SessionID]string, len(st.Agents))
		var activeID core.SessionID
		for _, session := range st.Agents {
			labels[session.ID] = session.Name
			if session.Name == activeName {
				activeID = session.ID
			}
		}
		quiet := core.AttentionQuietNone
		if micInUse() {
			quiet = core.AttentionQuietMeeting
		}
		plan := a.attentionPlanner().Plan(core.AttentionInput{
			Observation:   snapshot,
			ActiveSession: activeID,
			SessionLabels: labels,
			Break:         core.BreakStatusFromObservation(st, snapshot),
			Deployments:   a.takeDeploymentOutcomes(),
			Quiet:         quiet,
			Now:           time.Now(),
		})
		executeAttentionPlan(plan)
	}
}

func executeAttentionPlan(plan core.AttentionPlan) {
	if plan.DockBadge.Update {
		setDockBadge(attentionDockBadgeLabel(plan.DockBadge))
	}
	for _, notification := range plan.Notifications {
		core.NotifyDesktop(notification.Title, notification.Message, notification.Sound)
	}
	switch plan.NativeAttention {
	case core.NativeAttentionCancel:
		cancelAttention()
	case core.NativeAttentionInformational:
		requestAttention(false)
	case core.NativeAttentionCritical:
		requestAttention(true)
	}
	if plan.BringToFront {
		bringToFront()
	}
}

func attentionDockBadgeLabel(badge core.AttentionDockBadge) string {
	if badge.Label != "" && !badge.Complete {
		return badge.Label + "+"
	}
	return badge.Label
}
