package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// HookReportState is the semantic vocabulary a vendor may report about itself.
// It is deliberately the same vocabulary the manifests use, plus the refresh
// report that only keeps the freshness window open.
type HookReportState string

const (
	HookStateWorking HookReportState = "working"
	HookStateBlocked HookReportState = "blocked"
	HookStateDone    HookReportState = "done"
	HookStateIdle    HookReportState = "idle"
	// HookStateRefresh carries no new state. A long tool call would otherwise
	// let a correct "working" decay into snapshot inference.
	HookStateRefresh HookReportState = "refresh"
)

const (
	// hookReportFreshness is long enough to cover an agent that works quietly
	// between tool calls and short enough that an agent which died without a
	// final report decays into snapshot inference while the developer looks at
	// one screenful. It is a constant, not a setting.
	hookReportFreshness = 60 * time.Second
	// hookReportFileCap bounds the event file. A larger file is rotated away
	// rather than parsed: it is telemetry, not a ledger.
	hookReportFileCap = 1 << 20
)

// HookReport is one vendor's statement about its own lifecycle. It addresses a
// Session through identities Magentic already owns (ADR 0001), never by
// display name.
type HookReport struct {
	State       HookReportState `json:"state"`
	At          time.Time       `json:"at"`
	Vendor      AgentVendor     `json:"vendor"`
	SessionID   SessionID       `json:"sessionId,omitempty"`
	RuntimeName string          `json:"runtimeName,omitempty"`
	RunRef      string          `json:"runRef,omitempty"`
	Detail      string          `json:"detail,omitempty"`
	// UID attributes the report to the owning user. The channel is local-only;
	// a report that cannot be attributed to the owner is rejected.
	UID int `json:"uid"`
}

func (r HookReport) status() (AgentStatus, bool) {
	switch r.State {
	case HookStateWorking:
		return StatusRunning, true
	case HookStateBlocked:
		return StatusBlocked, true
	case HookStateDone:
		return StatusDone, true
	case HookStateIdle:
		return StatusIdle, true
	}
	return StatusUnknown, false
}

func (r HookReport) validate(owner int) error {
	if _, known := r.status(); !known && r.State != HookStateRefresh {
		return fmt.Errorf("Meldung nennt den unbekannten Zustand %q", r.State)
	}
	if r.At.IsZero() {
		return fmt.Errorf("Meldung nennt keinen Zeitpunkt")
	}
	if strings.TrimSpace(string(r.Vendor)) == "" {
		return fmt.Errorf("Meldung nennt keinen Vendor")
	}
	if r.SessionID == "" && (r.RuntimeName == "" || r.RunRef == "") {
		return fmt.Errorf("Meldung nennt weder SessionID noch RuntimeName mit AgentRunRef")
	}
	if r.UID != owner {
		return fmt.Errorf("Meldung gehört nicht dem angemeldeten Benutzer")
	}
	return nil
}

type hookRecord struct {
	status     AgentStatus
	detail     string
	reportedAt time.Time
	receivedAt time.Time
}

// HookReportStore keeps the latest report per Session together with the instant
// it was received, which is what the freshness window is measured against.
type HookReportStore struct {
	mu      sync.Mutex
	records map[SessionID]hookRecord
	now     func() time.Time
	path    string
	owner   int
}

// defaultHookReports is the store Observation reads. The desktop app applies
// reports into the same store as soon as the event file changes.
var defaultHookReports = NewHookReportStore()

func NewHookReportStore() *HookReportStore {
	return &HookReportStore{records: map[SessionID]hookRecord{}, now: time.Now, owner: os.Getuid()}
}

// DefaultHookReports is the process-wide report store. The desktop app hands it
// the reports it sees before the next observation cycle.
func DefaultHookReports() *HookReportStore { return defaultHookReports }

func (s *HookReportStore) clock() time.Time {
	if s.now == nil {
		return time.Now()
	}
	return s.now()
}

// Apply records one report against the Session it addresses. A report that
// cannot be correlated, that is older than the recorded one, or that fails
// validation leaves every Session's status untouched.
func (s *HookReportStore) Apply(report HookReport, sessions []Session) error {
	if err := report.validate(s.owner); err != nil {
		return err
	}
	id, ok := correlateHookReport(report, sessions)
	if !ok {
		return fmt.Errorf("Meldung gehört zu keiner registrierten Session")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = map[SessionID]hookRecord{}
	}
	existing, known := s.records[id]
	if known && !report.At.After(existing.reportedAt) {
		return fmt.Errorf("Meldung ist älter als die bereits aufgezeichnete")
	}
	received := s.clock()
	if report.State == HookStateRefresh {
		if !known {
			return fmt.Errorf("Auffrischung ohne vorherige Meldung")
		}
		existing.reportedAt = report.At
		existing.receivedAt = received
		s.records[id] = existing
		return nil
	}
	status, _ := report.status()
	s.records[id] = hookRecord{
		status: status, detail: report.Detail,
		reportedAt: report.At, receivedAt: received,
	}
	return nil
}

// fresh returns the Session's report while it is still authoritative.
func (s *HookReportStore) fresh(id SessionID, now time.Time) (hookRecord, bool) {
	if s == nil || id == "" {
		return hookRecord{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	record, known := s.records[id]
	if !known {
		return hookRecord{}, false
	}
	if now.Sub(record.receivedAt) >= hookReportFreshness {
		return hookRecord{}, false
	}
	return record, true
}

// forget drops every recorded report. Tests use it so one Session's report does
// not leak into another test's Observation.
func (s *HookReportStore) forget() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = map[SessionID]hookRecord{}
}

// correlateHookReport resolves a report to a Session under stable identities.
// A RuntimeName is only an address together with the vendor's AgentRunRef, so a
// report for a runtime that a different run has since taken over is discarded.
func correlateHookReport(report HookReport, sessions []Session) (SessionID, bool) {
	for _, session := range sessions {
		switch {
		case report.SessionID != "":
			if session.ID != report.SessionID {
				continue
			}
		case session.RuntimeName != report.RuntimeName:
			continue
		}
		if report.RunRef != "" {
			run, known := session.AgentRun(report.Vendor)
			if !known || run.ExternalID != report.RunRef {
				return "", false
			}
		} else if report.SessionID == "" {
			return "", false
		}
		return session.ID, true
	}
	return "", false
}

// --- the local event file ---------------------------------------------------

// HookReportPath is the append-only file the shipped hooks write one JSON line
// into. A file survives Magentic not running, which a socket does not: a hook
// must never fail because the UI is closed.
func HookReportPath() string {
	if path := os.Getenv("MAGENTIC_HOOK_REPORTS"); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(StatePath()), "hook-reports.jsonl")
}

// AppendHookReport writes one report as a single line. The file and its
// directory are owner-only: everything this channel can do, a local process
// running as the user can already do by typing into the pane.
func AppendHookReport(path string, report HookReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	line, err := json.Marshal(report)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	_, err = file.Write(append(line, '\n'))
	return err
}

// DrainHookReportFile folds every line of the event file into the store and
// truncates it, so no report is applied twice. An oversized file is rotated
// away instead of parsed.
func (s *HookReportStore) DrainHookReportFile(path string, sessions []Session) []error {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if info.Size() > hookReportFileCap {
		_ = os.Rename(path, path+".1")
		return []error{fmt.Errorf("Meldungsdatei war größer als %d Bytes und wurde rotiert", hookReportFileCap)}
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return []error{err}
	}
	defer file.Close()
	var problems []error
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var report HookReport
		if err := json.Unmarshal([]byte(line), &report); err != nil {
			problems = append(problems, err)
			continue
		}
		if err := s.Apply(report, sessions); err != nil {
			problems = append(problems, err)
		}
	}
	if err := file.Truncate(0); err != nil {
		problems = append(problems, err)
	}
	return problems
}
