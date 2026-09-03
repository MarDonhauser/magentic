package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"magentic/core"
)

func resumableUIModel(t *testing.T, vendor core.AgentVendor, runs []core.AgentRunRef) (model, Agent) {
	t.Helper()
	dir := t.TempDir()
	session := Agent{
		ID: "session-1", Name: "hera", RuntimeName: "mgt-hera",
		ProjectID: "project-1", Project: "project", Dir: dir,
		SessionKind: core.SessionKindCodingAgent, Vendor: vendor, AgentRuns: runs,
		CreatedAt:  time.Now().Add(-3 * time.Hour),
		LastStatus: core.StatusBlocked, LastStatusAt: time.Now().Add(-2 * time.Hour),
	}
	observed := core.SessionObservation{
		SessionID: session.ID, Availability: core.ObservationAvailable,
		Presence: core.SessionPresenceAbsent, Status: core.StatusDead,
	}
	m := model{
		state: &State{
			Projects: []Project{{ID: "project-1", Name: "project", Path: dir}},
			Agents:   []Agent{session},
		},
		collapsed: map[string]bool{}, cursor: 1,
		width: 180, height: 50,
	}
	m.poll.observed = map[tuiSessionKey]core.SessionObservation{sessionKey(session): observed}
	m.poll.resumable = map[tuiSessionKey]core.SessionResumability{
		sessionKey(session): core.ResumabilityForSession(session, observed),
	}
	return m, session
}

func pressKey(m model, key string) (model, tea.Cmd) {
	var runes []rune
	if len(key) == 1 {
		runes = []rune(key)
	}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: runes})
	return next.(model), cmd
}

// Eine fortsetzbare Session steht mit eigenem Icon und Label in der Liste —
// weder laufend noch tot — und behauptet nirgends, der Prozess hätte überlebt.
func TestResumableAgentLine(t *testing.T) {
	m, session := resumableUIModel(t, core.AgentVendorGemini, nil)
	line := m.agentLine(session, 100)
	if !strings.Contains(line, core.ResumableStatusLabel) || !strings.Contains(line, core.ResumableStatusIcon) {
		t.Fatalf("agent line = %q, want the resumable reading", line)
	}
	for _, forbidden := range []string{"tot", "✗", "läuft", "wiederhergestellt", "am Leben"} {
		if strings.Contains(line, forbidden) {
			t.Fatalf("agent line = %q, want no %q", line, forbidden)
		}
	}
	if !strings.Contains(line, "2h") {
		t.Fatalf("agent line = %q, want the last-seen age", line)
	}
}

func TestResumeKeyStartsFreshVendorFresh(t *testing.T) {
	previousResume, previousFresh := resumeSessionByID, resumeFreshSessionByID
	t.Cleanup(func() { resumeSessionByID, resumeFreshSessionByID = previousResume, previousFresh })
	var resumed, freshed core.SessionID
	resumeSessionByID = func(id core.SessionID) error {
		resumed = id
		return errors.New("must not resume a fresh-only vendor")
	}
	resumeFreshSessionByID = func(id core.SessionID) error {
		freshed = id
		return nil
	}
	m, session := resumableUIModel(t, core.AgentVendorGemini, nil)
	next, cmd := pressKey(m, "R")
	if resumed != "" || freshed != session.ID {
		t.Fatalf("resume=%q fresh=%q, want only a fresh start", resumed, freshed)
	}
	if next.flashIsErr || !strings.Contains(next.flash, "frisch") || cmd == nil {
		t.Fatalf("fresh feedback = flash %q error=%v command=%#v", next.flash, next.flashIsErr, cmd)
	}
}

func TestResumeKeyResumesStoredConversation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "projects", "run-1.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previousResume, previousFresh := resumeSessionByID, resumeFreshSessionByID
	t.Cleanup(func() { resumeSessionByID, resumeFreshSessionByID = previousResume, previousFresh })
	var resumed, freshed core.SessionID
	resumeSessionByID = func(id core.SessionID) error {
		resumed = id
		return nil
	}
	resumeFreshSessionByID = func(id core.SessionID) error {
		freshed = id
		return errors.New("must not fresh-start a held conversation")
	}
	m, session := resumableUIModel(t, core.AgentVendorClaude,
		[]core.AgentRunRef{{Vendor: core.AgentVendorClaude, ExternalID: "run-1"}})
	next, cmd := pressKey(m, "R")
	if resumed != session.ID || freshed != "" {
		t.Fatalf("resume=%q fresh=%q, want only a resume", resumed, freshed)
	}
	if next.flashIsErr || !strings.Contains(next.flash, "fortgesetzt") || cmd == nil {
		t.Fatalf("resume feedback = flash %q error=%v command=%#v", next.flash, next.flashIsErr, cmd)
	}
}

func TestResumeKeyRefusesLiveSession(t *testing.T) {
	previousResume, previousFresh := resumeSessionByID, resumeFreshSessionByID
	t.Cleanup(func() { resumeSessionByID, resumeFreshSessionByID = previousResume, previousFresh })
	called := false
	resumeSessionByID = func(core.SessionID) error { called = true; return nil }
	resumeFreshSessionByID = func(core.SessionID) error { called = true; return nil }
	m, session := resumableUIModel(t, core.AgentVendorGemini, nil)
	running := core.SessionObservation{
		SessionID: session.ID, Availability: core.ObservationAvailable,
		Presence: core.SessionPresencePresent, Status: core.StatusRunning,
	}
	m.poll.observed[sessionKey(session)] = running
	m.poll.resumable[sessionKey(session)] = core.ResumabilityForSession(session, running)
	next, _ := pressKey(m, "R")
	if called {
		t.Fatal("R on a live Session started something")
	}
	if !next.flashIsErr || !strings.Contains(next.flash, "nicht fortsetzbar") {
		t.Fatalf("live feedback = flash %q error=%v", next.flash, next.flashIsErr)
	}
}

func TestDiscardConfirmDropsResumableRecord(t *testing.T) {
	previousDiscard, previousLoad := discardSessionByID, LoadState
	t.Cleanup(func() { discardSessionByID, LoadState = previousDiscard, previousLoad })
	var discarded core.SessionID
	var discardedObs core.SessionObservation
	discardSessionByID = func(id core.SessionID, observed core.SessionObservation) error {
		discarded, discardedObs = id, observed
		return nil
	}
	m, session := resumableUIModel(t, core.AgentVendorGemini, nil)
	emptied := *m.state
	emptied.Agents = nil
	LoadState = func() (*State, error) { return &emptied, nil }
	m.confirmKill = true
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("y")})
	got := next.(model)
	if discarded != session.ID || discardedObs.Presence != core.SessionPresenceAbsent {
		t.Fatalf("discard = %q %+v, want the absent Session", discarded, discardedObs)
	}
	if got.flashIsErr || !strings.Contains(got.flash, "verworfen") || cmd == nil {
		t.Fatalf("discard feedback = flash %q error=%v command=%#v", got.flash, got.flashIsErr, cmd)
	}
	if len(got.state.Agents) != 0 {
		t.Fatalf("discarded Session still in state: %+v", got.state.Agents)
	}
}
