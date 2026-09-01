package core

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultAgentKindTail is the tail a manifest evaluates when it declares no
	// length of its own. It is the tail the Claude detection has always used.
	defaultAgentKindTail = 25
	// agentKindBudget bounds one Session's evaluation. User manifests may carry
	// expressions Magentic never reviewed; an expensive one must cost that
	// Session's status for one cycle, not the cycle.
	agentKindBudget = 5 * time.Millisecond
)

// agentKindEvaluation is what one manifest could prove about one snapshot.
// Matched false means nothing matched, which stays unknown rather than idle.
type agentKindEvaluation struct {
	Status    AgentStatus
	Detail    string
	Matched   bool
	Abandoned bool
}

// evaluateAgentKind applies the manifest in the order the format fixes —
// working, blocked, done, idle — and takes the first match within a state.
func evaluateAgentKind(kind *agentKind, content string) agentKindEvaluation {
	if kind == nil {
		return agentKindEvaluation{}
	}
	tail := normalizeObservedContent(LastLines(content, kind.tail))
	folded := strings.ToLower(tail)
	deadline := time.Now().Add(agentKindBudget)

	for _, state := range []struct {
		status   AgentStatus
		patterns []agentKindPattern
	}{
		{StatusRunning, kind.working},
		{StatusBlocked, kind.blocked},
		{StatusDone, kind.done},
		{StatusIdle, kind.idle},
	} {
		matched, abandoned := matchAgentKindPatterns(state.patterns, tail, folded, deadline)
		if abandoned {
			return agentKindEvaluation{Abandoned: true}
		}
		if !matched {
			continue
		}
		return agentKindEvaluation{
			Status:  state.status,
			Detail:  agentKindDetail(kind, state.status, tail, folded),
			Matched: true,
		}
	}
	return agentKindEvaluation{}
}

func matchAgentKindPatterns(patterns []agentKindPattern, tail, folded string, deadline time.Time) (bool, bool) {
	for _, pattern := range patterns {
		if time.Now().After(deadline) {
			return false, true
		}
		if matchAgentKindPattern(pattern, tail, folded) {
			return true, false
		}
	}
	return false, false
}

func matchAgentKindPattern(pattern agentKindPattern, tail, folded string) bool {
	if pattern.regex != nil {
		return pattern.regex.MatchString(tail)
	}
	return pattern.literal != "" && strings.Contains(folded, pattern.literal)
}

// agentKindDetail extracts the short qualifier a state may carry. Detail never
// changes the resolved status: an unrecognized dialog stays blocked without one.
func agentKindDetail(kind *agentKind, status AgentStatus, tail, folded string) string {
	switch status {
	case StatusBlocked:
		for _, detail := range kind.blockedDetails {
			for _, pattern := range detail.patterns {
				if matchAgentKindPattern(pattern, tail, folded) {
					return detail.label
				}
			}
		}
	case StatusRunning:
		for _, detail := range kind.workingDetails {
			if count := detail.count(tail); count > 0 {
				return detail.render(count)
			}
		}
	}
	return ""
}

func (d agentKindWorkingDetail) count(tail string) int {
	if d.occurrences != nil {
		if n := len(d.occurrences.FindAllString(tail, -1)); n > 0 {
			return n
		}
	}
	if d.capture != nil {
		matches := d.capture.FindAllStringSubmatch(tail, -1)
		if len(matches) > 0 {
			count, _ := strconv.Atoi(matches[len(matches)-1][1])
			return count
		}
	}
	return 0
}

func (d agentKindWorkingDetail) render(count int) string {
	if count == 1 {
		return fmt.Sprintf(d.singular, count)
	}
	return fmt.Sprintf(d.plural, count)
}

// agentKindComposerReady reports whether the agent's own input line is visible.
// A kind that declares no such patterns is never ready: Magentic would
// otherwise type a queued prompt into a dialog it cannot read.
func agentKindComposerReady(kind *agentKind, content string) bool {
	if kind == nil || len(kind.composer) == 0 {
		return false
	}
	tail := normalizeObservedContent(LastLines(content, kind.tail))
	folded := strings.ToLower(tail)
	matched, _ := matchAgentKindPatterns(kind.composer, tail, folded, time.Now().Add(agentKindBudget))
	return matched
}
