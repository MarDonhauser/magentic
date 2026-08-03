package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Project struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	MainBranch string `json:"main_branch,omitempty"`
}

const KindTerm = "term"

// Terminals aus dem Dock unten in der App. Sie verhalten sich wie
// Terminal-Sessions, tauchen aber bewusst nicht in der Sitzungsliste auf —
// sie gehören zum Dock und werden dort auch wieder geschlossen.
const KindDock = "dock"

type Agent struct {
	Name       string    `json:"name"`
	Project    string    `json:"project"`
	Dir        string    `json:"dir"`
	Worktree   bool      `json:"worktree"`
	Kind       string    `json:"kind,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	BaseCommit string    `json:"base_commit,omitempty"`
	BaseDirty  []string  `json:"base_dirty,omitempty"`
	SessionID  string    `json:"session_id,omitempty"`
	DeployAt   time.Time `json:"deploy_at,omitzero"`
	LaterAt    time.Time `json:"later_at,omitzero"`
	SeenAt     time.Time `json:"seen_at,omitzero"`
}

type State struct {
	Projects []Project `json:"projects"`
	Agents   []Agent   `json:"agents"`
}

func StatePath() string {
	if p := os.Getenv("MAGENTIC_STATE"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "magentic", "state.json")
}

func LoadState() (*State, error) {
	s := &State{}
	data, err := os.ReadFile(StatePath())
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, s); err == nil {
		return s, nil
	}
	// Wurde die Datei von zwei Prozessen überlappend geschrieben, steht am
	// Anfang noch ein vollständiges Objekt — das ist der letzte heile Stand.
	rescued := &State{}
	if derr := json.NewDecoder(bytes.NewReader(data)).Decode(rescued); derr != nil {
		return nil, fmt.Errorf("state.json ist beschädigt: %w", derr)
	}
	rescued.Save()
	return rescued, nil
}

var saveMu sync.Mutex

func (s *State) Save() error {
	saveMu.Lock()
	defer saveMu.Unlock()

	p := StatePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	// Eindeutiger Name pro Prozess: TUI und App schreiben sonst in dieselbe
	// Zwischendatei und verschränken ihre Bytes zu ungültigem JSON.
	tmp := fmt.Sprintf("%s.tmp.%d", p, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

func (a Agent) IsTerm() bool {
	return a.Kind == KindTerm || a.Kind == KindDock
}

func (a Agent) IsDock() bool {
	return a.Kind == KindDock
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
