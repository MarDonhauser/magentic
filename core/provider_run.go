package core

import (
	"context"
	"path/filepath"
)

// discoverAgentRun resolves the run identity of a Session whose vendor assigns
// its own. Exactness beats coverage: a directory match plus a start after the
// Session was created must name exactly one conversation, otherwise the
// Session deliberately stays without a run reference.
func discoverAgentRun(_ context.Context, session Session, events []HistoryEvent) (AgentRunRef, bool) {
	vendor := session.SessionVendor()
	provider, known := historyProviderFromAgentVendor(vendor)
	if !known || session.Dir == "" || session.CreatedAt.IsZero() {
		return AgentRunRef{}, false
	}
	dir := filepath.Clean(session.Dir)
	candidate := ""
	for _, event := range events {
		if event.Provider != provider || event.ConversationID.State != HistoryFactKnown || event.CWD.State != HistoryFactKnown {
			continue
		}
		if filepath.Clean(event.CWD.Value) != dir {
			continue
		}
		if event.OccurredAt.State != HistoryFactKnown || event.OccurredAt.Value.Before(session.CreatedAt) {
			continue
		}
		switch {
		case candidate == "":
			candidate = event.ConversationID.Value
		case candidate != event.ConversationID.Value:
			return AgentRunRef{}, false
		}
	}
	if candidate == "" {
		return AgentRunRef{}, false
	}
	return AgentRunRef{Vendor: vendor, ExternalID: candidate}, true
}

// resolveMissingAgentRun reads the vendor's history once and persists a found
// run reference. It returns the Session unchanged when nothing is resolvable;
// a missing reference is a normal outcome, not an error.
func resolveMissingAgentRun(ctx context.Context, session Session) (Session, error) {
	provider, err := resolveSessionProvider(session)
	if err != nil {
		return session, err
	}
	if provider.NewRunID() != "" {
		return session, nil
	}
	if _, exists := session.AgentRun(provider.Vendor()); exists {
		return session, nil
	}
	historyProvider, known := historyProviderFromAgentVendor(provider.Vendor())
	if !known {
		return session, nil
	}
	history, err := SharedWorkHistory()
	if err != nil {
		return session, nil
	}
	state, err := LoadState()
	if err != nil {
		return session, nil
	}
	page, err := history.Events(ctx, NewHistoryAssociations(*state), HistoryEventQuery{
		Providers: []HistoryProvider{historyProvider},
		Since:     session.CreatedAt,
	})
	if err != nil {
		return session, nil
	}
	run, found := discoverAgentRun(ctx, session, page.Events)
	if !found {
		return session, nil
	}
	result, err := OpenRegistry(StatePath()).Change(ctx, RecordAgentRun(session.ID, session.Name, run))
	if err != nil || !result.Applied {
		return session, nil
	}
	updated := result.Snapshot.State()
	if resolved := updated.SessionByID(session.ID); resolved != nil {
		return *resolved, nil
	}
	return session, nil
}
