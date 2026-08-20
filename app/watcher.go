package main

import (
	"context"
	"time"

	"magentic/core"
)

func (a *App) SetActiveTerm(sessionID string) {
	a.mu.Lock()
	a.activeTerm = sessionID
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
	observe := core.Observe
	if a.observeSessions != nil {
		observe = a.observeSessions
	}
	snapshot := observe(context.Background(), sessions)
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
				state := changed.Snapshot.State()
				st = &state
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
		activeID := core.SessionID(a.getActiveTerm())
		labels := make(map[core.SessionID]string, len(st.Agents))
		for _, session := range st.Agents {
			labels[session.ID] = session.Name
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
			Events:        a.takeAttentionEvents(),
			Quiet:         quiet,
			Now:           time.Now(),
		})
		executeAttentionPlan(plan)
	}
}

func (a *App) enqueueAttentionEvent(event core.AttentionEvent) {
	a.attentionEventMu.Lock()
	defer a.attentionEventMu.Unlock()
	if event.Key != "" {
		for _, pending := range a.attentionEvents {
			if pending.Kind == event.Kind && pending.Key == event.Key {
				return
			}
		}
	}
	a.attentionEvents = append(a.attentionEvents, event)
}

func (a *App) takeAttentionEvents() []core.AttentionEvent {
	a.attentionEventMu.Lock()
	defer a.attentionEventMu.Unlock()
	events := append([]core.AttentionEvent(nil), a.attentionEvents...)
	a.attentionEvents = nil
	return events
}

func (a *App) executeAttentionEvents(events ...core.AttentionEvent) {
	plan := a.attentionPlanner().Plan(core.AttentionInput{
		Observation: core.ObservationSnapshot{Availability: core.ObservationUnavailable},
		Events:      append([]core.AttentionEvent(nil), events...),
		Now:         time.Now(),
	})
	executeAttentionPlan(plan)
}

func breakFinishedAttentionEvent(at time.Time) core.AttentionEvent {
	episode := at.UTC().Truncate(time.Minute).Format("20060102T1504")
	return core.AttentionEvent{Key: "break-finished:" + episode, Kind: core.AttentionEventBreakFinished}
}

type attentionPlanExecutor struct {
	badge   func(string)
	notify  func(string, string, string)
	request func(bool)
	cancel  func()
	front   func()
}

var platformAttentionExecutor = attentionPlanExecutor{
	badge: setDockBadge, notify: core.NotifyDesktop,
	request: requestAttention, cancel: cancelAttention, front: bringToFront,
}

func executeAttentionPlan(plan core.AttentionPlan) {
	platformAttentionExecutor.execute(plan)
}

func (executor attentionPlanExecutor) execute(plan core.AttentionPlan) {
	if plan.DockBadge.Update {
		if executor.badge != nil {
			executor.badge(plan.DockBadge.Label)
		}
	}
	for _, notification := range plan.Notifications {
		if executor.notify != nil {
			executor.notify(notification.Title, notification.Message, notification.Sound)
		}
	}
	switch plan.NativeAttention {
	case core.NativeAttentionCancel:
		if executor.cancel != nil {
			executor.cancel()
		}
	case core.NativeAttentionInformational:
		if executor.request != nil {
			executor.request(false)
		}
	case core.NativeAttentionCritical:
		if executor.request != nil {
			executor.request(true)
		}
	}
	if plan.BringToFront && executor.front != nil {
		executor.front()
	}
}
