package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Project struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	MainBranch string `json:"main_branch,omitempty"`
}

type Agent struct {
	Name       string    `json:"name"`
	Project    string    `json:"project"`
	Dir        string    `json:"dir"`
	Worktree   bool      `json:"worktree"`
	CreatedAt  time.Time `json:"created_at"`
	BaseCommit string    `json:"base_commit,omitempty"`
	BaseDirty  []string  `json:"base_dirty,omitempty"`
}

type Todo struct {
	Text      string    `json:"text"`
	Project   string    `json:"project,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

type State struct {
	Projects []Project `json:"projects"`
	Agents   []Agent   `json:"agents"`
	Todos    []Todo    `json:"todos,omitempty"`
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
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *State) Save() error {
	p := StatePath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
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
