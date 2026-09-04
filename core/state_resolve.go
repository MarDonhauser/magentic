package core

import (
	"fmt"
	"strings"
)

// Auflösung von Identität zu Datensatz. Sie liegt hier, weil ADR 0001 sie
// festlegt: die ID ist die Autorität, der Name ist ein Etikett. Jede
// Oberfläche lädt ihren State selbst — das ist ihre Sache — aber keine
// entscheidet noch einmal neu, was eine gültige Auflösung ist.

// ResolveSession löst eine SessionID auf. Eine leere ID ist ein Fehler und
// niemals eine Einladung, über den Namen zu suchen.
func (s *State) ResolveSession(rawID string) (Session, error) {
	id := SessionID(strings.TrimSpace(rawID))
	if id == "" {
		return Session{}, fmt.Errorf("SessionID fehlt")
	}
	session := s.SessionByID(id)
	if session == nil {
		return Session{}, fmt.Errorf("unbekannte SessionID: %s", id)
	}
	return *session, nil
}

// ResolveProject löst eine ProjectID auf.
func (s *State) ResolveProject(rawID string) (Project, error) {
	id := ProjectID(strings.TrimSpace(rawID))
	if id == "" {
		return Project{}, fmt.Errorf("ProjectID fehlt")
	}
	project := s.ProjectByID(id)
	if project == nil {
		return Project{}, fmt.Errorf("unbekannte ProjectID: %s", id)
	}
	return *project, nil
}

// ResolveSessionTarget löst eine benannte Aktion ausschließlich über die
// SessionID auf. Der Namensrückfall besteht allein für persistierte
// Dock-Tabs aus der Zeit vor stabilen IDs; eine mitgelieferte, aber veraltete
// ID fällt niemals auf eine neue Session durch, die ihren Namen wiederverwendet.
func (s *State) ResolveSessionTarget(rawID, legacyDockName string) (Session, error) {
	if strings.TrimSpace(rawID) != "" {
		return s.ResolveSession(rawID)
	}
	name := strings.TrimSpace(legacyDockName)
	if name == "" {
		return Session{}, fmt.Errorf("SessionID fehlt")
	}
	session := s.AgentByName(name)
	if session == nil || !session.IsDock() {
		return Session{}, fmt.Errorf("unbekannter Legacy-Dock-Tab: %s", name)
	}
	return *session, nil
}
