package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// stubResumeProvider is a controllable AgentProvider for resumability tests:
// its RunExists answers from a map, so a test can flip the vendor's memory of
// a conversation between classification and execution.
type stubResumeProvider struct {
	vendor   AgentVendor
	behavior ResumeBehavior
	exists   map[string]bool
}

func (s stubResumeProvider) Vendor() AgentVendor { return s.vendor }
func (s stubResumeProvider) Tool() string        { return string(s.vendor) }
func (s stubResumeProvider) Binary() string      { return string(s.vendor) }
func (s stubResumeProvider) Matches(string) bool { return false }
func (s stubResumeProvider) StartCommand(Session, *AgentRunRef, string) (string, error) {
	return "stub resume", nil
}
func (s stubResumeProvider) NewRunID() string { return "stub-run" }
func (s stubResumeProvider) RunExists(id string) bool {
	return s.exists[id]
}
func (s stubResumeProvider) Normalizer() (ConversationNormalizer, bool) { return nil, false }
func (s stubResumeProvider) ResumeBehavior() ResumeBehavior             { return s.behavior }
func (s stubResumeProvider) Runtimes() []AgentRuntime                   { return []AgentRuntime{RuntimeTmux} }

func resumeTestSession(dir string) Session {
	return Session{
		ID: "session-1", Name: "hera", RuntimeName: "mgt-hera",
		Project: "project", Dir: dir, SessionKind: SessionKindCodingAgent,
		Vendor:    AgentVendorClaude,
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-1"}},
		CreatedAt: time.Date(2026, 9, 2, 20, 0, 0, 0, time.UTC),
	}
}

func resumeAbsentObservation(id SessionID) SessionObservation {
	return SessionObservation{
		SessionID: id, Availability: ObservationAvailable,
		Presence: SessionPresenceAbsent, Status: StatusDead,
		StatusSource: StatusSourcePresence, Occupancy: OccupancyVacant,
	}
}

func actionIDs(actions []SessionAction) []string {
	ids := make([]string, 0, len(actions))
	for _, action := range actions {
		ids = append(ids, action.ID)
	}
	return ids
}

// When the vendor no longer holds the recorded conversation, the offered
// action — not just the command line — changes from resume to fresh start.
func TestResumabilityDowngradeChangesOfferedAction(t *testing.T) {
	session := resumeTestSession(t.TempDir())
	absent := resumeAbsentObservation(session.ID)
	provider := stubResumeProvider{
		vendor: AgentVendorClaude, behavior: ResumeByRunRef,
		exists: map[string]bool{"run-1": true},
	}

	res := ClassifyResumability(session, absent, provider, nil)
	actions := SessionActionsFor(session, absent, res)
	if !res.Resumable || res.FreshOnly {
		t.Fatalf("stored conversation must offer resume: %+v", res)
	}
	if got := actionIDs(actions); len(got) != 2 || got[0] != SessionActionResume || got[1] != SessionActionDiscard {
		t.Fatalf("actions = %v, want [resume discard]", got)
	}
	if actions[0].Label != "Fortsetzen" {
		t.Fatalf("resume label = %q, want Fortsetzen", actions[0].Label)
	}

	provider.exists["run-1"] = false
	res = ClassifyResumability(session, absent, provider, nil)
	actions = SessionActionsFor(session, absent, res)
	if !res.Resumable || !res.FreshOnly || res.FreshReason == "" {
		t.Fatalf("forgotten conversation must offer fresh start: %+v", res)
	}
	if got := actionIDs(actions); len(got) != 2 || got[0] != SessionActionResumeFresh || got[1] != SessionActionDiscard {
		t.Fatalf("actions = %v, want [resume-fresh discard]", got)
	}
	if !strings.Contains(actions[0].Label, "Frisch") {
		t.Fatalf("fresh label = %q, want fresh-start wording", actions[0].Label)
	}
	for _, action := range actions {
		if action.ID == SessionActionResume {
			t.Fatalf("forgotten conversation still offers resume: %+v", actions)
		}
	}
}

func TestResumeLastSeenRendersStatusAndTime(t *testing.T) {
	session := resumeTestSession(t.TempDir())
	if got := ResumeLastSeen(session); got != "" {
		t.Fatalf("never-observed Session renders %q, want empty", got)
	}
	at := time.Now().Add(-2 * time.Hour).UTC()
	session.LastStatus = StatusBlocked
	session.LastStatusAt = at
	got := ResumeLastSeen(session)
	if !strings.Contains(got, StatusBlocked.Label()) || !strings.Contains(got, "vor ") {
		t.Fatalf("last seen = %q, want status and relative time", got)
	}
}

// Kein Resume läuft je automatisch: Der Startup-Pass nach einem Reboot liest
// nur, klassifiziert jede Session und bietet Fortsetzen an — er erstellt keine
// Runtime und schreibt keine Transition.
func TestResumeIsNeverAutomaticAtStartup(t *testing.T) {
	lifecycle, runtime, registry, _ := lifecycleHarness(t)
	projDir := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	project := Project{ID: ProjectID("project-1"), Name: "project", Path: projDir, MainBranch: "main"}
	if _, err := registry.Change(context.Background(), RegisterProject(project)); err != nil {
		t.Fatal(err)
	}
	claudeSession := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-hera", Name: "hera", RuntimeName: "mgt-hera",
		ProjectID: project.ID, Project: project.Name, Dir: projDir,
		SessionKind: SessionKindCodingAgent, Vendor: AgentVendorClaude,
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-1"}},
	}, false)
	geminiSession := registerLifecycleSession(t, registry, runtime, Session{
		ID: "session-geo", Name: "geo", RuntimeName: "mgt-geo",
		ProjectID: project.ID, Project: project.Name, Dir: projDir,
		SessionKind: SessionKindCodingAgent, Vendor: AgentVendorGemini,
	}, false)
	// So sieht ein Observation-Pass nach einem Reboot aus: Der tmux-Server ist
	// weg, jede Runtime fehlt beobachtbar.
	for _, session := range []Session{claudeSession, geminiSession} {
		observed := SessionObservation{
			SessionID: session.ID, Availability: ObservationAvailable,
			Presence: SessionPresenceAbsent, Status: StatusDead,
			StatusSource: StatusSourcePresence, Occupancy: OccupancyVacant,
		}
		res := ResumabilityForSession(session, observed)
		_ = SessionActionsFor(session, observed, res)
	}
	if runtime.startCalls != 0 || len(runtime.existsCalls) != 0 {
		t.Fatalf("startup pass touched the runtime: start=%d exists=%q", runtime.startCalls, runtime.existsCalls)
	}
	ledger, err := lifecycle.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Records) != 0 {
		t.Fatalf("startup pass persisted intents: %+v", ledger.Records)
	}
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	if len(state.Agents) != 2 {
		t.Fatalf("startup pass changed the Registry: %+v", state.Agents)
	}
	for _, session := range []Session{claudeSession, geminiSession} {
		if got := state.SessionByID(session.ID); got == nil || got.RuntimeName != session.RuntimeName {
			t.Fatalf("startup pass rewrote a record: %+v", got)
		}
	}
}

func discardTestRegistry(t *testing.T) (Session, string) {
	t.Helper()
	statePath := filepath.Join(t.TempDir(), "state.json")
	t.Setenv("MAGENTIC_STATE", statePath)
	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "arbeit.txt"), []byte("ungespeicherte Arbeit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workdir, ".git-worktree-marker"), []byte("worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vendorStorage := filepath.Join(t.TempDir(), "run-1.jsonl")
	if err := os.WriteFile(vendorStorage, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	registry := OpenRegistry(statePath)
	session := Session{
		ID: "session-1", Name: "hera", RuntimeName: "mgt-hera",
		Project: "project", Dir: workdir, SessionKind: SessionKindCodingAgent,
		Vendor:    AgentVendorClaude,
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-1"}},
		CreatedAt: time.Now().Add(-time.Hour).UTC(),
	}
	if _, err := registry.Change(context.Background(), RegisterSession(session)); err != nil {
		t.Fatal(err)
	}
	return session, vendorStorage
}

// Verwerfen löscht nur den Registry-Eintrag: Arbeitsverzeichnis, Worktree und
// gespeicherte Anbieter-Konversation überleben.
func TestDiscardSessionByIDRemovesOnlyTheRecord(t *testing.T) {
	session, vendorStorage := discardTestRegistry(t)
	absent := resumeAbsentObservation(session.ID)
	if err := DiscardSessionByID(session.ID, absent); err != nil {
		t.Fatal(err)
	}
	stored, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if stored.SessionByID(session.ID) != nil {
		t.Fatalf("discarded Session still listed: %+v", stored.Agents)
	}
	if info, err := os.Stat(session.Dir); err != nil || !info.IsDir() {
		t.Fatalf("working directory did not survive discard: %v", err)
	}
	for _, path := range []string{
		filepath.Join(session.Dir, "arbeit.txt"),
		filepath.Join(session.Dir, ".git-worktree-marker"),
		vendorStorage,
	} {
		content, err := os.ReadFile(path)
		if err != nil || len(content) == 0 {
			t.Fatalf("discard touched %s: %v", path, err)
		}
	}
}

// Verwerfen wird einer laufenden Session und einer unbekannten
// Verfügbarkeit verweigert; die Entfernung läuft weiter über die bestehende
// Kill-Aktion.
func TestDiscardSessionByIDRefusesLiveAndUnknown(t *testing.T) {
	session, _ := discardTestRegistry(t)
	running := SessionObservation{
		SessionID: session.ID, Availability: ObservationAvailable,
		Presence: SessionPresencePresent, Status: StatusRunning,
	}
	if err := DiscardSessionByID(session.ID, running); err == nil || !strings.Contains(err.Error(), "Runtime") {
		t.Fatalf("discard of running Session = %v", err)
	}
	unknown := SessionObservation{
		SessionID: session.ID, Availability: ObservationUnavailable,
		Presence: SessionPresenceUnknown, Status: StatusUnknown,
	}
	if err := DiscardSessionByID(session.ID, unknown); err == nil || !strings.Contains(err.Error(), "nicht verlässlich") {
		t.Fatalf("discard of unknown Session = %v", err)
	}
	foreign := resumeAbsentObservation("other-session")
	if err := DiscardSessionByID(session.ID, foreign); err == nil || !strings.Contains(err.Error(), "gehört nicht zu") {
		t.Fatalf("discard with foreign observation = %v", err)
	}
	stored, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if stored.SessionByID(session.ID) == nil {
		t.Fatal("refused discard removed the record")
	}
}

// Fortsetzbare Sessions rendern mit eigenem Status in ihrer normalen
// Projektgruppe: weder laufend noch tot, mit letztem Stand und Zeit. Ein
// Satz ohne Verzeichnis bleibt tot, mit genanntem Grund.
func TestOverviewRendersResumableInProjectGroup(t *testing.T) {
	projDir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude", "projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "projects", "run-1.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	project := Project{ID: "project-1", Name: "project", Path: projDir, MainBranch: "main"}
	lastSeen := time.Now().Add(-2 * time.Hour).UTC()
	sessions := []Session{
		{
			ID: "session-hera", Name: "hera", RuntimeName: "mgt-hera",
			ProjectID: project.ID, Project: project.Name, Dir: projDir,
			SessionKind: SessionKindCodingAgent, Vendor: AgentVendorClaude,
			AgentRuns:  []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-1"}},
			CreatedAt:  lastSeen.Add(-time.Hour),
			LastStatus: StatusBlocked, LastStatusAt: lastSeen,
		},
		{
			ID: "session-geo", Name: "geo", RuntimeName: "mgt-geo",
			ProjectID: project.ID, Project: project.Name, Dir: projDir,
			SessionKind: SessionKindCodingAgent, Vendor: AgentVendorGemini,
			CreatedAt: lastSeen.Add(-time.Hour),
		},
		{
			ID: "session-oid", Name: "oid", RuntimeName: "mgt-oid",
			ProjectID: project.ID, Project: project.Name, Dir: filepath.Join(projDir, "weg"),
			SessionKind: SessionKindCodingAgent, Vendor: AgentVendorClaude,
			AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-9"}},
			CreatedAt: lastSeen.Add(-time.Hour),
		},
	}
	state := &State{Projects: []Project{project}, Agents: sessions}
	var readings []SessionObservation
	for _, session := range sessions {
		readings = append(readings, SessionObservation{
			SessionID: session.ID, Availability: ObservationAvailable,
			Presence: SessionPresenceAbsent, Status: StatusDead,
			StatusSource: StatusSourcePresence, Occupancy: OccupancyVacant,
		})
	}
	overview := BuildOverviewFromObservation(state, ObservationSnapshot{
		ObservedAt: time.Now(), Availability: ObservationAvailable, Sessions: readings,
	})
	if overview.Counts["resumable"] != 2 || overview.Counts["dead"] != 1 {
		t.Fatalf("counts = %v, want resumable 2 and dead 1", overview.Counts)
	}
	if len(overview.Projects) != 1 {
		t.Fatalf("projects = %d, want the one Project group", len(overview.Projects))
	}
	agents := overview.Projects[0].Worktrees[0].Agents
	if len(agents) != 3 {
		t.Fatalf("Project group holds %d agents, want all three Sessions", len(agents))
	}
	byName := map[string]OvAgent{}
	for _, agent := range agents {
		byName[agent.Name] = agent
	}
	hera := byName["hera"]
	if hera.Status != "resumable" || hera.Label != ResumableStatusLabel || !hera.Resumable || hera.ResumeFresh {
		t.Fatalf("hera = %+v, want true resume rendering", hera)
	}
	if !strings.Contains(hera.LastSeen, StatusBlocked.Label()) || !strings.Contains(hera.LastSeen, "vor ") {
		t.Fatalf("hera LastSeen = %q, want status and relative time", hera.LastSeen)
	}
	if hera.Detail != hera.LastSeen {
		t.Fatalf("hera Detail = %q, want the last-seen reading", hera.Detail)
	}
	geo := byName["geo"]
	if !geo.Resumable || !geo.ResumeFresh || geo.Status != "resumable" {
		t.Fatalf("geo = %+v, want fresh-start rendering", geo)
	}
	oid := byName["oid"]
	if oid.Resumable || oid.Status != "dead" {
		t.Fatalf("oid = %+v, want dead, never resumable", oid)
	}
	if !strings.Contains(oid.ResumeReason, "Arbeitsverzeichnis") || !strings.Contains(oid.Detail, "Arbeitsverzeichnis") {
		t.Fatalf("oid reason = %q, want the stated dead reason", oid.ResumeReason)
	}
	for _, agent := range agents {
		if agent.Status == "running" || agent.Status == "idle" {
			t.Fatalf("%s presented as %s, want neither running nor idle", agent.Name, agent.Status)
		}
	}
}

// Eine fortsetzbare Session behält ihren Platz in der Projektgruppe, statt in
// einen eigenen Wiederherstellungs-Bereich zu wandern.
func TestSidebarKeepsResumableSessionsInProjectGroup(t *testing.T) {
	project := Project{ID: "p1", Name: "project", Path: "/workspace/project", MainBranch: "main"}
	session := Session{
		ID: "s1", Name: "hera", RuntimeName: "mgt-hera",
		ProjectID: project.ID, Project: project.Name, Dir: "/workspace/project",
		SessionKind: SessionKindCodingAgent, Vendor: AgentVendorClaude,
		AgentRuns:  []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-1"}},
		CreatedAt:  time.Now().Add(-time.Hour).UTC(),
		LastStatus: StatusBlocked, LastStatusAt: time.Now().Add(-2 * time.Hour).UTC(),
	}
	layout := SidebarLayout(&State{Projects: []Project{project}, Agents: []Session{session}})
	if shape := layoutShape(layout); len(shape) != 1 || shape[0] != "project:p1(session:s1)" {
		t.Fatalf("sidebar shape = %v, want the Session inside its Project", shape)
	}
}

// Keine sichtbare Zeichenkette einer fortsetzbaren Session darf behaupten,
// der Prozess hätte überlebt: Die Konversation wird fortgesetzt, nicht der
// Prozess. Deshalb läuft jede feste Kopie — Lesungs-Label, Aktions-Label und
// -Details, Gründe, Projektionsfelder — durch die wörtliche Suche nach den
// verbotenen Behauptungen. Ein laufender letzter Status ("läuft · vor 2h")
// ist keine davon: Er trägt seine Vergangenheits-Verankerung explizit bei
// sich, was der zweite Teil des Tests festnagelt.
func TestResumableCopyClaimsNoSurvivingProcess(t *testing.T) {
	forbidden := []string{"läuft", "wiederhergestellt", "am Leben"}
	dir := t.TempDir()
	knownAt := time.Now().Add(-90 * time.Minute).UTC()
	absent := SessionObservation{
		SessionID: "session-1", Availability: ObservationAvailable,
		Presence: SessionPresenceAbsent, Status: StatusDead,
	}
	providers := []AgentProvider{
		stubResumeProvider{vendor: AgentVendorClaude, behavior: ResumeByRunRef, exists: map[string]bool{"run-1": true}},
		stubResumeProvider{vendor: AgentVendorClaude, behavior: ResumeByRunRef, exists: map[string]bool{}},
		stubResumeProvider{vendor: AgentVendorGemini, behavior: ResumeFreshOnly},
		stubResumeProvider{vendor: AgentVendorClaude, behavior: ResumeUnsupported},
	}
	var literal []string
	literal = append(literal, ResumableStatusLabel, ResumableStatusIcon)
	mkSession := func(last AgentStatus) Session {
		return Session{
			ID: "session-1", Name: "hera", RuntimeName: "mgt-hera",
			Project: "project", Dir: dir, SessionKind: SessionKindCodingAgent,
			Vendor:    AgentVendorClaude,
			AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-1"}},
			CreatedAt: knownAt.Add(-time.Hour), LastStatus: last, LastStatusAt: knownAt,
		}
	}
	for _, provider := range providers {
		for _, last := range []AgentStatus{StatusBlocked, StatusIdle, StatusDone, StatusUnknown} {
			session := mkSession(last)
			res := ClassifyResumability(session, absent, provider, nil)
			literal = append(literal, res.Reason, res.FreshReason)
			for _, action := range SessionActionsFor(session, absent, res) {
				literal = append(literal, action.Label, action.Detail)
			}
			agent := toOvAgent(session, absent, "", res)
			literal = append(literal, agent.Status, agent.Label, agent.Detail, agent.LastSeen, agent.ResumeReason)
		}
	}
	deadCases := []struct {
		name    string
		session Session
	}{
		{"Verzeichnis fehlt", mkSession(StatusBlocked)},
		{"keine Run-Referenz", func() Session { s := mkSession(StatusBlocked); s.AgentRuns = nil; return s }()},
		{"Terminal", Session{ID: "session-1", Name: "term-hera", RuntimeName: "mgt-term-hera", Dir: dir, Kind: KindTerm}},
		{"verwaltete Runtime", func() Session { s := mkSession(StatusBlocked); s.Runtime = RuntimeManaged; return s }()},
	}
	for _, tc := range deadCases {
		var provider AgentProvider
		if !tc.session.IsTerm() {
			provider = providers[0]
		}
		res := ClassifyResumability(tc.session, absent, provider, nil)
		literal = append(literal, res.Reason, res.FreshReason)
		for _, action := range SessionActionsFor(tc.session, absent, res) {
			literal = append(literal, action.Label, action.Detail)
		}
		agent := toOvAgent(tc.session, absent, "", res)
		literal = append(literal, agent.Status, agent.Label, agent.Detail, agent.LastSeen, agent.ResumeReason)
	}
	for _, s := range literal {
		for _, f := range forbidden {
			if strings.Contains(s, f) {
				t.Errorf("resumable copy %q claims %q", s, f)
			}
		}
	}
	// Ein laufender letzter Status bleibt sagbar, weil er seine
	// Vergangenheits-Verankerung ("vor …"/"jetzt") immer dabei hat.
	for _, last := range []AgentStatus{StatusRunning, StatusShell, StatusAgents} {
		got := ResumeLastSeen(mkSession(last))
		if !strings.Contains(got, "vor ") && !strings.Contains(got, "jetzt") {
			t.Errorf("ResumeLastSeen(%v) = %q without past anchoring", last, got)
		}
	}
}
