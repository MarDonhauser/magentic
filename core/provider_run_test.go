package core

import (
	"context"
	"testing"
	"time"
)

func TestDiscoverAgentRun(t *testing.T) {
	created := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	session := Session{
		Name: "navi", Dir: "/work/navi", SessionKind: SessionKindCodingAgent,
		Vendor: AgentVendorCodex, CreatedAt: created,
	}
	event := func(id, cwd string, at time.Time) HistoryEvent {
		return HistoryEvent{
			Provider:       HistoryProviderCodex,
			ConversationID: historyKnown(id),
			CWD:            historyKnown(cwd),
			OccurredAt:     historyKnown(at),
		}
	}
	tests := []struct {
		name   string
		events []HistoryEvent
		want   string
		found  bool
	}{
		{
			name:   "genau ein Lauf im richtigen Verzeichnis",
			events: []HistoryEvent{event("run-1", "/work/navi", created.Add(time.Minute))},
			want:   "run-1", found: true,
		},
		{
			name:   "fremdes Verzeichnis zählt nicht",
			events: []HistoryEvent{event("run-1", "/work/other", created.Add(time.Minute))},
			found:  false,
		},
		{
			name:   "Lauf vor der Session zählt nicht",
			events: []HistoryEvent{event("run-1", "/work/navi", created.Add(-time.Minute))},
			found:  false,
		},
		{
			name: "mehrdeutig bleibt ohne Ergebnis",
			events: []HistoryEvent{
				event("run-1", "/work/navi", created.Add(time.Minute)),
				event("run-2", "/work/navi", created.Add(2*time.Minute)),
			},
			found: false,
		},
		{
			name: "derselbe Lauf mehrfach ist eindeutig",
			events: []HistoryEvent{
				event("run-1", "/work/navi", created.Add(time.Minute)),
				event("run-1", "/work/navi", created.Add(2*time.Minute)),
			},
			want: "run-1", found: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run, ok := discoverAgentRun(context.Background(), session, tt.events)
			if ok != tt.found {
				t.Fatalf("gefunden = %v, want %v", ok, tt.found)
			}
			if ok && (run.ExternalID != tt.want || run.Vendor != AgentVendorCodex) {
				t.Fatalf("Run = %+v, want %q/%q", run, AgentVendorCodex, tt.want)
			}
		})
	}
}
