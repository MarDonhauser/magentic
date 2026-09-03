package core

import "testing"

func recordedPaneObservation(t *testing.T, tool, fixture string, status AgentStatus) SessionObservation {
	t.Helper()
	content := paneFixture(t, fixture)
	return SessionObservation{
		SessionID: "target", Availability: ObservationAvailable, Presence: SessionPresencePresent,
		ContentKnown: true, Tool: tool, Status: status, Content: content,
	}
}

// Codex und Copilot sind als Handoff-Ziel zugelassen, seit ihre Bildschirme
// aufgenommen sind: ihre Eingabebereitschaft lässt sich belegen.
func TestHandoffAcceptsRecordedVendorsAsTarget(t *testing.T) {
	tests := []struct {
		tool    string
		fixture string
	}{
		{tool: AgentToolCodex, fixture: "codex-idle.txt"},
		{tool: AgentToolCopilot, fixture: "copilot-idle.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			session := Session{ID: "target", Name: "ziel", RuntimeName: "mgt-ziel", SessionKind: SessionKindCodingAgent}
			observed := recordedPaneObservation(t, tt.tool, tt.fixture, StatusIdle)
			if err := validateHandoffEnqueueTarget(session, observed); err != nil {
				t.Fatalf("Einreihen abgelehnt: %v", err)
			}
			if err := validateHandoffDeliveryReady("ziel", promptTargetObservationFromSession(observed)); err != nil {
				t.Fatalf("Zustellung abgelehnt: %v", err)
			}
			tool, waitForReady, err := validateHandoffTarget(session, observed)
			if err != nil {
				t.Fatalf("Zielwerkzeug: %v", err)
			}
			if tool != tt.tool || waitForReady {
				t.Fatalf("Zielwerkzeug = %q, waitForReady = %v", tool, waitForReady)
			}
		})
	}
}

// Ein Anbieter ohne aufgenommene Bildschirme bleibt als Ziel gesperrt.
func TestHandoffRefusesUnrecordedVendorAsTarget(t *testing.T) {
	session := Session{ID: "target", Name: "ziel", RuntimeName: "mgt-ziel", SessionKind: SessionKindCodingAgent}
	tests := []struct {
		tool    string
		content string
	}{
		{tool: AgentToolGemini, content: "› Type your message"},
		{tool: AgentToolAntigravity, content: "> Type your message"},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			observed := SessionObservation{
				SessionID: "target", Availability: ObservationAvailable, Presence: SessionPresencePresent,
				ContentKnown: true, Tool: tt.tool, Status: StatusIdle, Content: tt.content,
			}
			if err := validateHandoffEnqueueTarget(session, observed); err == nil {
				t.Fatalf("%s darf noch kein Handoff-Ziel sein", tt.tool)
			}
			if err := validateHandoffDeliveryReady("ziel", promptTargetObservationFromSession(observed)); err == nil {
				t.Fatalf("%s darf keine Handoff-Zustellung annehmen", tt.tool)
			}
		})
	}
}

// Ein wartender Ziel-Dialog bleibt für jeden Anbieter eine Absage.
func TestHandoffRefusesBlockedRecordedTarget(t *testing.T) {
	observed := recordedPaneObservation(t, AgentToolCodex, "codex-blocked-trust.txt", StatusBlocked)
	if err := validateHandoffDeliveryReady("ziel", promptTargetObservationFromSession(observed)); err == nil {
		t.Fatal("ein offener Dialog darf keinen Handoff annehmen")
	}
}
