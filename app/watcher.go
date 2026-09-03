package main

import (
	"context"
	"encoding/json"
	"os"
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

func observationFingerprints(sessions []core.Session) map[core.SessionID]string {
	fingerprints := make(map[core.SessionID]string, len(sessions))
	for _, session := range sessions {
		if session.ID == "" {
			return nil
		}
		encoded, err := json.Marshal(session)
		if err != nil {
			return nil
		}
		fingerprints[session.ID] = string(encoded)
	}
	return fingerprints
}

func (a *App) storeObservation(snapshot core.ObservationSnapshot, sessions []core.Session) {
	a.observationMu.Lock()
	a.observation = cloneObservation(snapshot)
	a.observationAt = time.Now()
	a.observationInput = observationFingerprints(sessions)
	a.observationMu.Unlock()
	publishControlObservation(sessions, snapshot)
}

func (a *App) observationFor(sessions []core.Session, fresh bool) core.ObservationSnapshot {
	if !fresh {
		a.observationMu.Lock()
		cached, cachedAt, cachedInput := cloneObservation(a.observation), a.observationAt, a.observationInput
		a.observationMu.Unlock()
		if time.Since(cachedAt) <= 5*time.Second && observationCovers(cached, cachedInput, sessions) {
			return cached
		}
	}
	observe := core.Observe
	if a.observeSessions != nil {
		observe = a.observeSessions
	}
	snapshot := observe(context.Background(), sessions)
	a.storeObservation(snapshot, sessions)
	return snapshot
}

func observationCovers(snapshot core.ObservationSnapshot, cachedInput map[core.SessionID]string, sessions []core.Session) bool {
	if len(snapshot.Sessions) != len(sessions) {
		return false
	}
	requested := observationFingerprints(sessions)
	if len(cachedInput) != len(requested) || len(requested) != len(sessions) {
		return false
	}
	observed := make(map[core.SessionID]bool, len(snapshot.Sessions))
	for _, session := range snapshot.Sessions {
		observed[session.SessionID] = true
	}
	for _, session := range sessions {
		if session.ID == "" || !observed[session.ID] || cachedInput[session.ID] != requested[session.ID] {
			return false
		}
	}
	return true
}

// hookReportPollInterval keeps a vendor-reported transition well inside the
// sub-second budget the status contract states. The observation cycle itself
// stays at its own interval; this loop only refines the statuses it already
// holds.
const hookReportPollInterval = 200 * time.Millisecond

// hookReportLoop applies hook-reported transitions to the Observation the app
// already holds, so a blocked agent shows up without waiting for the next
// cycle.
func (a *App) hookReportLoop() {
	for {
		time.Sleep(hookReportPollInterval)
		a.applyHookReportsOnce()
	}
}

func (a *App) applyHookReportsOnce() bool {
	// Without pending hook reports there is nothing to refine. The file only
	// exists while hooks are installed and only has content right after an
	// agent reported, so in the common case this is one stat call instead of
	// a Registry load plus a snapshot clone five times a second.
	if info, err := os.Stat(core.HookReportPath()); err != nil || info.Size() == 0 {
		return false
	}
	st, err := core.LoadState()
	if err != nil {
		return false
	}
	a.observationMu.Lock()
	held := cloneObservation(a.observation)
	a.observationMu.Unlock()
	if len(held.Sessions) == 0 {
		return false
	}
	refined, changed := core.ApplyHookReports(held, st.Agents, time.Now())
	if !changed {
		return false
	}
	a.observationMu.Lock()
	a.observation = refined
	a.observationMu.Unlock()
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
		discovery := core.DiscoverNew(context.Background(), st)
		if discoveryErr := discovery.Err(); discoveryErr != nil {
			if time.Since(lastErrLog) > time.Minute {
				core.Logf("watchLoop: Session-Discovery unvollständig: %v", discoveryErr)
				lastErrLog = time.Now()
			}
		} else if len(discovery.Sessions) > 0 {
			changed, changeErr := core.OpenRegistry(core.StatePath()).AdoptDiscoveredSessions(context.Background(), discovery.Sessions)
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
		if queued, automationErr := core.RunDueAutomations(context.Background(), time.Now(), a.observeSessions); automationErr != nil {
			if time.Since(lastErrLog) > time.Minute {
				core.Logf("watchLoop: Automatisierungen konnten nicht eingeplant werden: %v", automationErr)
				lastErrLog = time.Now()
			}
		} else if queued > 0 {
			if current, loadErr := core.LoadState(); loadErr == nil {
				st = current
			}
		}
		snapshot := core.Observe(context.Background(), st.Agents)
		a.storeObservation(snapshot, st.Agents)
		core.DispatchOutbox(context.Background(), st, snapshot)
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
		plan = mutedAttentionPlan(plan)
		a.storeInbox(core.BuildInbox(st, plan.Inbox))
		a.syncNotch(plan, snapshot)
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

// mutedAttentionPlan schaltet nur das Aufdringliche stumm: Popups, Notch-
// Hinweise, Dock-Hüpfen und In-den-Vordergrund-Holen. Badge, Inbox und Cancel
// räumen weiter auf. Muss VOR syncNotch angewendet werden — das Notch liest
// dieselben Notifications.
func mutedAttentionPlan(plan core.AttentionPlan) core.AttentionPlan {
	if core.NotificationsEnabled() {
		return plan
	}
	plan.Notifications = nil
	plan.BringToFront = false
	if plan.NativeAttention == core.NativeAttentionInformational || plan.NativeAttention == core.NativeAttentionCritical {
		plan.NativeAttention = core.NativeAttentionUnchanged
	}
	return plan
}

func executeAttentionPlan(plan core.AttentionPlan) {
	platformAttentionExecutor.execute(mutedAttentionPlan(plan))
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
