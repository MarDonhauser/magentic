package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type ProjectID string
type SessionID string

type AgentVendor string

const (
	AgentVendorClaude      AgentVendor = "claude"
	AgentVendorCodex       AgentVendor = "codex"
	AgentVendorGemini      AgentVendor = "gemini"
	AgentVendorCopilot     AgentVendor = "copilot"
	AgentVendorAntigravity AgentVendor = "antigravity"
)

// AgentRuntime names what owns a Session's coding-agent process: the tmux
// runtime, driving it by keystrokes in a pane, or the managed runtime, where
// the daemon owns the process directly and speaks its protocol. It is chosen
// when a Session is created and does not change for that Session's life.
type AgentRuntime string

const (
	RuntimeTmux    AgentRuntime = "tmux"
	RuntimeManaged AgentRuntime = "managed"
)

// Runtime action identities offered for a live Session, derived from its
// AgentRuntime. These govern what a running Session offers, distinct from
// SessionActionsFor's absent-runtime readings.
const (
	RuntimeActionAttach           = "attach"
	RuntimeActionInterrupt        = "interrupt"
	RuntimeActionAnswerPermission = "answer-permission"
)

// RuntimeActionsFor lists the actions a Session's AgentRuntime offers while
// it is live. Attach is offered only for tmux, because a managed Session has
// no pane to attach to. Interrupting a turn and answering a permission
// decision are offered only for managed, because a tmux Session answers both
// in its own pane. An action absent from this list MUST NOT be offered for
// that runtime; it does not fail at execution time as the way of
// communicating that.
func RuntimeActionsFor(runtime AgentRuntime) []string {
	if runtime == RuntimeManaged {
		return []string{RuntimeActionInterrupt, RuntimeActionAnswerPermission}
	}
	return []string{RuntimeActionAttach}
}

// RuntimeOffersAction reports whether the given AgentRuntime offers action.
func RuntimeOffersAction(runtime AgentRuntime, action string) bool {
	for _, a := range RuntimeActionsFor(runtime) {
		if a == action {
			return true
		}
	}
	return false
}

type AgentRunRef struct {
	Vendor     AgentVendor `json:"vendor"`
	ExternalID string      `json:"external_id"`
}

type SessionKind string

const (
	SessionKindCodingAgent SessionKind = "coding-agent"
	SessionKindTerminal    SessionKind = "terminal"
)

type SessionPresentation string

const (
	SessionPresentationListed SessionPresentation = "listed"
	SessionPresentationDock   SessionPresentation = "dock"
)

type SessionPurpose string

const (
	SessionPurposeWork    SessionPurpose = "work"
	SessionPurposeCleanup SessionPurpose = "cleanup"
	SessionPurposeMerge   SessionPurpose = "merge"
	SessionPurposeDeploy  SessionPurpose = "deploy"
)

// QueuedMessageKind distinguishes what a durably queued message carries, so
// delivery can apply the rules that belong to that kind.
type QueuedMessageKind string

const (
	QueuedMessageKindMessage    QueuedMessageKind = "message"
	QueuedMessageKindSkill      QueuedMessageKind = "skill"
	QueuedMessageKindHandoff    QueuedMessageKind = "handoff"
	QueuedMessageKindAutomation QueuedMessageKind = "automation"
	// QueuedMessageKindReview carries a rendered diff Review back into the
	// Session that produced the changes. Delivery reuses the literal
	// bracketed-paste path every other kind uses.
	QueuedMessageKindReview QueuedMessageKind = "review"
)

// QueuedMessage is one message waiting in a Session's Outbox. AttemptedAt is
// set right before a send so a crash cannot silently duplicate a delivery.
type QueuedMessage struct {
	ID          string            `json:"id"`
	Kind        QueuedMessageKind `json:"kind"`
	Text        string            `json:"text"`
	EnqueuedAt  time.Time         `json:"enqueued_at"`
	AttemptedAt time.Time         `json:"attempted_at,omitzero"`
}

// ReviewComment anchors one developer remark to a single diff line or a
// contiguous line range within one file. OldStart/OldEnd address removed
// lines, NewStart/NewEnd address added and context lines; Quoted carries the
// anchored code as captured, and Mode records which comparison it was made
// in, so a Review mixing both modes renders unambiguously.
type ReviewComment struct {
	ID        string             `json:"id"`
	Path      string             `json:"path"`
	OldStart  int                `json:"old_start,omitempty"`
	OldEnd    int                `json:"old_end,omitempty"`
	NewStart  int                `json:"new_start,omitempty"`
	NewEnd    int                `json:"new_end,omitempty"`
	Quoted    string             `json:"quoted,omitempty"`
	Text      string             `json:"text"`
	Mode      DiffComparisonMode `json:"mode"`
	CreatedAt time.Time          `json:"created_at"`
}

// SessionReview is one Review belonging to a Session. A zero SentAt means the
// Review is still open; a sent Review carries its send time and is read-only
// history.
type SessionReview struct {
	ID       string          `json:"id"`
	Comments []ReviewComment `json:"comments,omitempty"`
	SentAt   time.Time       `json:"sent_at,omitzero"`
}

// MaxSentReviewsPerSession bounds the retained sent Reviews per Session; the
// oldest is dropped beyond the bound.
const MaxSentReviewsPerSession = 10

// SessionAutomation is the one recurring instruction attached to a coding
// Session. NextRunAt is persisted so restarts preserve the cadence instead of
// starting a fresh timer from application launch.
type SessionAutomation struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Instructions string    `json:"instructions"`
	EveryMinutes int       `json:"every_minutes"`
	NextRunAt    time.Time `json:"next_run_at"`
	LastRunAt    time.Time `json:"last_run_at,omitzero"`
	Enabled      bool      `json:"enabled"`
}

type Project struct {
	ID         ProjectID `json:"id,omitempty"`
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	MainBranch string    `json:"main_branch,omitempty"`
}

const KindTerm = "term"

// Terminals aus dem Dock unten in der App. Sie verhalten sich wie
// Terminal-Sessions, tauchen aber bewusst nicht in der Sitzungsliste auf —
// sie gehören zum Dock und werden dort auch wieder geschlossen.
const KindDock = "dock"

type Session struct {
	ID               SessionID           `json:"id,omitempty"`
	Name             string              `json:"name"`
	ProjectID        ProjectID           `json:"project_id,omitempty"`
	Project          string              `json:"project"`
	Dir              string              `json:"dir"`
	Worktree         bool                `json:"worktree"`
	Kind             string              `json:"kind,omitempty"` // legacy transport field
	SessionKind      SessionKind         `json:"session_kind,omitempty"`
	Presentation     SessionPresentation `json:"presentation,omitempty"`
	Purpose          SessionPurpose      `json:"purpose,omitempty"`
	SpecificationRef SpecificationRef    `json:"specification_ref,omitempty"`
	RuntimeName      string              `json:"runtime_name,omitempty"`
	CreatedAt        time.Time           `json:"created_at"`
	BaseCommit       string              `json:"base_commit,omitempty"`
	BaseDirty        []string            `json:"base_dirty,omitempty"`
	SessionID        string              `json:"session_id,omitempty"` // legacy Claude run identifier
	Vendor           AgentVendor         `json:"vendor,omitempty"`
	Runtime          AgentRuntime        `json:"runtime,omitempty"`
	AgentRuns        []AgentRunRef       `json:"agent_runs,omitempty"`
	DeployAt         time.Time           `json:"deploy_at,omitzero"`
	LaterAt          time.Time           `json:"later_at,omitzero"`
	SeenAt           time.Time           `json:"seen_at,omitzero"`
	// LastStatus is the last status an Observation pass reported while the
	// Session's runtime still existed, with the time it was observed. It
	// answers "what was it doing, and when" after a reboot, before the first
	// Observation pass of the fresh process runs. It is persisted as a stable
	// string label (see AgentStatus.PersistedLabel), never as the iota
	// ordinal, so inserting a status constant later cannot rewrite history.
	// The zero value reads as never observed, never as a status.
	LastStatus   AgentStatus        `json:"-"`
	LastStatusAt time.Time          `json:"last_status_at,omitzero"`
	Service      bool               `json:"service,omitempty"`
	Outbox       []QueuedMessage    `json:"outbox,omitempty"`
	Automation   *SessionAutomation `json:"automation,omitempty"`
	// Review is the Session's one open Review. A nil Review reads as an empty
	// open Review, which keeps every state.json written before Reviews existed
	// valid without a migration step. SentReviews carries the retained sent
	// history, oldest first.
	Review      *SessionReview  `json:"review,omitempty"`
	SentReviews []SessionReview `json:"sent_reviews,omitempty"`
}

// Agent remains as a source-compatible name while callers migrate to Session.
type Agent = Session

// MarshalJSON persists LastStatus under its stable string label. Every other
// field keeps the shape it always had, so older records read unchanged.
func (s Session) MarshalJSON() ([]byte, error) {
	type sessionJSON Session
	return json.Marshal(struct {
		sessionJSON
		LastStatus string `json:"last_status,omitempty"`
	}{
		sessionJSON: sessionJSON(s),
		LastStatus:  s.LastStatus.PersistedLabel(),
	})
}

// UnmarshalJSON reads LastStatus back from its stable string label. An absent
// or unrecognized label reads as StatusUnknown, which is also what every
// state.json written before this change yields.
func (s *Session) UnmarshalJSON(data []byte) error {
	type sessionJSON Session
	var decoded struct {
		sessionJSON
		LastStatus string `json:"last_status"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*s = Session(decoded.sessionJSON)
	s.LastStatus = AgentStatusFromPersistedLabel(decoded.LastStatus)
	return nil
}

// SidebarSlotKind names what a SidebarSlot places. Dividers are the only
// slots the user creates outright; project and session slots merely record a
// placement that differs from the default one.
type SidebarSlotKind string

const (
	SidebarSlotDivider SidebarSlotKind = "divider"
	SidebarSlotProject SidebarSlotKind = "project"
	SidebarSlotSession SidebarSlotKind = "session"
)

type DividerID string

// SidebarSlot is one placed entry of the session list. The slice order is the
// order on screen; Parent carries the nesting. A divider always sits at the top
// level, a project sits at the top level or inside a divider, and a session
// sits at the top level, inside a divider, or inside its own project.
//
// Anything without a slot keeps the default placement: projects trail the top
// level, sessions trail their project. A freshly started session therefore
// needs no slot to show up where it always did.
type SidebarSlot struct {
	Kind       SidebarSlotKind `json:"kind"`
	Ref        string          `json:"ref"`
	Name       string          `json:"name,omitempty"`
	Collapsed  bool            `json:"collapsed,omitempty"`
	ParentKind SidebarSlotKind `json:"parent_kind,omitempty"`
	Parent     string          `json:"parent,omitempty"`
}

// TopLevel reports whether the slot hangs directly under the session list.
func (s SidebarSlot) TopLevel() bool { return s.ParentKind == "" }

type State struct {
	Schema   int           `json:"schema,omitempty"`
	Revision uint64        `json:"revision,omitempty"`
	Projects []Project     `json:"projects"`
	Agents   []Session     `json:"agents"`
	Sidebar  []SidebarSlot `json:"sidebar,omitempty"`
}

// SidebarSlotFor finds the slot placing one entry, or nil when the entry keeps
// its default placement.
func (s *State) SidebarSlotFor(kind SidebarSlotKind, ref string) *SidebarSlot {
	for i := range s.Sidebar {
		if s.Sidebar[i].Kind == kind && s.Sidebar[i].Ref == ref {
			return &s.Sidebar[i]
		}
	}
	return nil
}

func StatePath() string {
	if p := os.Getenv("MAGENTIC_STATE"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "magentic", "state.json")
}

func LoadState() (*State, error) {
	snapshot, err := OpenRegistry(StatePath()).Snapshot(nil)
	if err != nil {
		return nil, err
	}
	state := snapshot.State()
	return &state, nil
}

func (a Session) IsTerm() bool {
	return a.SessionKind == SessionKindTerminal || a.Kind == KindTerm || a.Kind == KindDock
}

func (a Session) IsDock() bool {
	return a.Presentation == SessionPresentationDock || a.Kind == KindDock
}

func (a Session) TmuxName() string {
	// RuntimeName is an opaque durable identity. Legacy reconstruction belongs
	// exclusively to Registry migration; process-facing callers must never
	// derive an address from the mutable display name.
	return a.RuntimeName
}

func (a Session) AgentRun(vendor AgentVendor) (AgentRunRef, bool) {
	for _, run := range a.AgentRuns {
		if run.Vendor == vendor && run.ExternalID != "" {
			return run, true
		}
	}
	if vendor == AgentVendorClaude && a.SessionID != "" {
		return AgentRunRef{Vendor: AgentVendorClaude, ExternalID: a.SessionID}, true
	}
	return AgentRunRef{}, false
}

// SessionVendor is the durable vendor that starts this Session. An empty
// stored value means Claude, which keeps every pre-multi-provider state
// valid. Terminal Sessions host no coding agent and have no vendor.
func (a Session) SessionVendor() AgentVendor {
	if a.IsTerm() {
		return ""
	}
	if a.Vendor != "" {
		return a.Vendor
	}
	return AgentVendorClaude
}

// SessionRuntime is the durable AgentRuntime that owns this Session's
// process. An empty stored value means tmux, so every Session record written
// before the managed runtime existed keeps working unchanged.
func (a Session) SessionRuntime() AgentRuntime {
	if a.Runtime == "" {
		return RuntimeTmux
	}
	return a.Runtime
}

func (s *State) AgentByName(name string) *Agent {
	for i := range s.Agents {
		if s.Agents[i].Name == name {
			return &s.Agents[i]
		}
	}
	return nil
}

func (s *State) SessionByID(id SessionID) *Session {
	if s == nil || id == "" {
		return nil
	}
	for i := range s.Agents {
		if s.Agents[i].ID == id {
			return &s.Agents[i]
		}
	}
	return nil
}

func (s *State) ProjectByName(name string) *Project {
	for i := range s.Projects {
		if s.Projects[i].Name == name {
			return &s.Projects[i]
		}
	}
	return nil
}

func (s *State) ProjectByID(id ProjectID) *Project {
	for i := range s.Projects {
		if s.Projects[i].ID == id {
			return &s.Projects[i]
		}
	}
	return nil
}

func (s *State) AgentsFor(project string) []Agent {
	var out []Agent
	for _, a := range s.Agents {
		if project == "" || a.Project == project {
			out = append(out, a)
		}
	}
	return out
}

func (s *State) HasAgent(name string) bool {
	for _, a := range s.Agents {
		if a.Name == name {
			return true
		}
	}
	return false
}
