package core

import (
	"os"
	"path/filepath"
	"time"
)

type ProjectID string
type SessionID string

type AgentVendor string

const (
	AgentVendorClaude  AgentVendor = "claude"
	AgentVendorCodex   AgentVendor = "codex"
	AgentVendorGemini  AgentVendor = "gemini"
	AgentVendorCopilot AgentVendor = "copilot"
)

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
	ID           SessionID           `json:"id,omitempty"`
	Name         string              `json:"name"`
	ProjectID    ProjectID           `json:"project_id,omitempty"`
	Project      string              `json:"project"`
	Dir          string              `json:"dir"`
	Worktree     bool                `json:"worktree"`
	Kind         string              `json:"kind,omitempty"` // legacy transport field
	SessionKind  SessionKind         `json:"session_kind,omitempty"`
	Presentation SessionPresentation `json:"presentation,omitempty"`
	Purpose      SessionPurpose      `json:"purpose,omitempty"`
	RuntimeName  string              `json:"runtime_name,omitempty"`
	CreatedAt    time.Time           `json:"created_at"`
	BaseCommit   string              `json:"base_commit,omitempty"`
	BaseDirty    []string            `json:"base_dirty,omitempty"`
	SessionID    string              `json:"session_id,omitempty"` // legacy Claude run identifier
	AgentRuns    []AgentRunRef       `json:"agent_runs,omitempty"`
	DeployAt     time.Time           `json:"deploy_at,omitzero"`
	LaterAt      time.Time           `json:"later_at,omitzero"`
	SeenAt       time.Time           `json:"seen_at,omitzero"`
}

// Agent remains as a source-compatible name while callers migrate to Session.
type Agent = Session

type State struct {
	Schema   int       `json:"schema,omitempty"`
	Revision uint64    `json:"revision,omitempty"`
	Projects []Project `json:"projects"`
	Agents   []Session `json:"agents"`

	registryPath string
	baseline     *State
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
	return snapshot.MutableState(), nil
}

// Save is the compatibility Adapter for callers that still hold a mutable
// State. New code should express one semantic mutation with Registry.Change.
func (s *State) Save() error {
	return saveMutableState(s)
}

func (a Session) IsTerm() bool {
	return a.SessionKind == SessionKindTerminal || a.Kind == KindTerm || a.Kind == KindDock
}

func (a Session) IsDock() bool {
	return a.Presentation == SessionPresentationDock || a.Kind == KindDock
}

func (a Session) TmuxName() string {
	if a.RuntimeName != "" {
		return a.RuntimeName
	}
	return SessionName(a.Name)
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

func (s *State) AgentByName(name string) *Agent {
	for i := range s.Agents {
		if s.Agents[i].Name == name {
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

func (s *State) AddAgent(a Agent) {
	s.Agents = append(s.Agents, a)
}

func (s *State) RemoveAgent(name string) {
	out := s.Agents[:0]
	for _, a := range s.Agents {
		if a.Name != name {
			out = append(out, a)
		}
	}
	s.Agents = out
}

func (s *State) MarkDeploy(name string) {
	for i := range s.Agents {
		if s.Agents[i].Name == name {
			s.Agents[i].DeployAt = time.Now()
		}
	}
}

// MarkSeen liefert nur dann true, wenn sich der Wert lohnt zu speichern —
// sonst schreibt jeder Ansichtswechsel den State neu.
func (s *State) MarkSeen(name string) bool {
	for i := range s.Agents {
		if s.Agents[i].Name != name {
			continue
		}
		now := time.Now()
		if now.Sub(s.Agents[i].SeenAt) < 5*time.Second {
			return false
		}
		s.Agents[i].SeenAt = now
		return true
	}
	return false
}

func (s *State) MarkLater(name string) {
	for i := range s.Agents {
		if s.Agents[i].Name == name {
			s.Agents[i].LaterAt = time.Now()
		}
	}
}

func (s *State) RenameAgent(oldName, newName string) {
	for i := range s.Agents {
		if s.Agents[i].Name == oldName {
			s.Agents[i].Name = newName
		}
	}
}

func (s *State) HasAgent(name string) bool {
	for _, a := range s.Agents {
		if a.Name == name {
			return true
		}
	}
	return false
}
