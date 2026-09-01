package core

import (
	"context"
	"fmt"
	"strings"
	"time"
)

const maxAutomationIntervalMinutes = 365 * 24 * 60

// ValidateSessionAutomation keeps persisted schedules bounded and executable.
func ValidateSessionAutomation(automation SessionAutomation) error {
	if strings.TrimSpace(automation.ID) == "" {
		return fmt.Errorf("Automatisierung hat keine ID")
	}
	if strings.TrimSpace(automation.Name) == "" {
		return fmt.Errorf("Name der Automatisierung fehlt")
	}
	if strings.TrimSpace(automation.Instructions) == "" {
		return fmt.Errorf("Anweisungen der Automatisierung fehlen")
	}
	if automation.EveryMinutes < 1 || automation.EveryMinutes > maxAutomationIntervalMinutes {
		return fmt.Errorf("Intervall muss zwischen 1 Minute und 365 Tagen liegen")
	}
	if automation.NextRunAt.IsZero() {
		return fmt.Errorf("nächster Lauf fehlt")
	}
	return nil
}

// AutomationPrompt labels scheduled instructions without changing their
// meaning. The stable text also lets the Outbox coalesce a repeated occurrence
// while an earlier one is still waiting for the same Session.
func AutomationPrompt(automation SessionAutomation) string {
	return fmt.Sprintf("Automatisierung %q\n\n%s", strings.TrimSpace(automation.Name), strings.TrimSpace(automation.Instructions))
}

func nextAutomationRun(current time.Time, everyMinutes int, now time.Time) time.Time {
	interval := time.Duration(everyMinutes) * time.Minute
	if current.After(now) {
		return current
	}
	steps := now.Sub(current)/interval + 1
	return current.Add(steps * interval)
}

// RunDueAutomations atomically queues every due instruction and advances its
// schedule before attempting delivery. A crash can therefore leave a visible
// Outbox entry, but cannot silently enqueue the same occurrence twice.
func RunDueAutomations(ctx context.Context, now time.Time, observe func(context.Context, []Session) ObservationSnapshot) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if now.IsZero() {
		now = time.Now()
	}
	state, err := LoadState()
	if err != nil {
		return 0, err
	}
	type dueAutomation struct {
		sessionID    SessionID
		automationID string
	}
	var due []dueAutomation
	for _, session := range state.Agents {
		automation := session.Automation
		if session.IsTerm() || automation == nil || !automation.Enabled || automation.NextRunAt.After(now) {
			continue
		}
		due = append(due, dueAutomation{sessionID: session.ID, automationID: automation.ID})
	}

	registry := OpenRegistry(StatePath())
	queued := 0
	for _, candidate := range due {
		result, changeErr := registry.Change(ctx, QueueDueSessionAutomation(candidate.sessionID, candidate.automationID, now))
		if changeErr != nil {
			return queued, changeErr
		}
		if !result.Applied {
			continue
		}
		queued++
		kickOutboxForSession(ctx, candidate.sessionID, observe)
	}
	return queued, nil
}
