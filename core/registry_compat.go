package core

import (
	"context"
	"fmt"
	"reflect"
	"sync"
)

// mutableStateSaveMu serializes the complete read/commit/copy-back sequence of
// the legacy mutable State Adapter. Unlike Registry.Change, State.Save may be
// called concurrently with the same State pointer (or States that share slice
// storage), so the Registry's file lock alone cannot protect the in-memory
// value while it is cloned and replaced.
var mutableStateSaveMu sync.Mutex

func saveMutableState(state *State) error {
	if state == nil {
		return fmt.Errorf("nil Registry state")
	}
	mutableStateSaveMu.Lock()
	defer mutableStateSaveMu.Unlock()

	path := state.registryPath
	if path == "" {
		path = StatePath()
	}
	desired := cloneState(state)
	normalizeRegistryState(&desired)
	var baseline *State
	if state.baseline != nil {
		copy := cloneState(state.baseline)
		baseline = &copy
	}

	registry := OpenRegistry(path)
	var result RegistryChangeResult
	err := registry.commit(context.Background(), func(latest *State) (bool, error) {
		if baseline == nil {
			if reflect.DeepEqual(registryData(latest), registryData(&desired)) {
				return false, nil
			}
			revision := latest.Revision
			*latest = cloneState(&desired)
			latest.Revision = revision
			return true, nil
		}
		return applyMutableStateDelta(baseline, &desired, latest)
	}, &result)
	if err != nil {
		return err
	}
	committed := result.Snapshot.MutableState()
	*state = *committed
	return nil
}

type comparableRegistryData struct {
	Projects []Project
	Sessions []Session
}

func registryData(state *State) comparableRegistryData {
	return comparableRegistryData{Projects: state.Projects, Sessions: state.Agents}
}

func applyMutableStateDelta(base, desired, latest *State) (bool, error) {
	changed := false
	for _, before := range base.Projects {
		after, exists := projectByID(desired.Projects, before.ID)
		idx := projectIndex(latest, before.ID, before.Name)
		if !exists {
			if idx < 0 {
				continue
			}
			if !reflect.DeepEqual(latest.Projects[idx], before) {
				return false, fmt.Errorf("%w: Projekt %q", ErrRegistryConflict, before.Name)
			}
			latest.Projects = append(latest.Projects[:idx], latest.Projects[idx+1:]...)
			changed = true
			continue
		}
		if reflect.DeepEqual(before, after) {
			continue
		}
		if idx < 0 {
			return false, fmt.Errorf("%w: Projekt %q wurde entfernt", ErrRegistryConflict, before.Name)
		}
		merged := latest.Projects[idx]
		if err := mergeChangedFields(before, after, &merged, "ID"); err != nil {
			return false, fmt.Errorf("%w: Projekt %q: %v", ErrRegistryConflict, before.Name, err)
		}
		latest.Projects[idx] = merged
		changed = true
	}
	for _, project := range desired.Projects {
		if _, exists := projectByID(base.Projects, project.ID); exists {
			continue
		}
		if projectIndex(latest, project.ID, project.Name) >= 0 || latest.ProjectByName(project.Name) != nil {
			return false, fmt.Errorf("%w: Projekt %q wurde gleichzeitig angelegt", ErrRegistryConflict, project.Name)
		}
		latest.Projects = append(latest.Projects, project)
		changed = true
	}

	for _, before := range base.Agents {
		after, exists := sessionByID(desired.Agents, before.ID)
		idx := sessionIndex(latest, before.ID, before.Name)
		if !exists {
			if idx < 0 {
				continue
			}
			if !reflect.DeepEqual(latest.Agents[idx], before) {
				return false, fmt.Errorf("%w: Session %q", ErrRegistryConflict, before.Name)
			}
			latest.Agents = append(latest.Agents[:idx], latest.Agents[idx+1:]...)
			changed = true
			continue
		}
		if reflect.DeepEqual(before, after) {
			continue
		}
		if idx < 0 {
			return false, fmt.Errorf("%w: Session %q wurde entfernt", ErrRegistryConflict, before.Name)
		}
		merged := latest.Agents[idx]
		if err := mergeChangedFields(before, after, &merged, "ID"); err != nil {
			return false, fmt.Errorf("%w: Session %q: %v", ErrRegistryConflict, before.Name, err)
		}
		latest.Agents[idx] = merged
		changed = true
	}
	for _, session := range desired.Agents {
		if _, exists := sessionByID(base.Agents, session.ID); exists {
			continue
		}
		if sessionIndex(latest, session.ID, session.Name) >= 0 || latest.AgentByName(session.Name) != nil {
			return false, fmt.Errorf("%w: Session %q wurde gleichzeitig angelegt", ErrRegistryConflict, session.Name)
		}
		latest.Agents = append(latest.Agents, session)
		changed = true
	}

	if !reflect.DeepEqual(projectOrder(base.Projects), projectOrder(desired.Projects)) {
		latest.Projects = reorderProjects(latest.Projects, desired.Projects)
		changed = true
	}
	return changed, nil
}

func mergeChangedFields(base, desired any, latest any, immutable ...string) error {
	baseValue := reflect.ValueOf(base)
	desiredValue := reflect.ValueOf(desired)
	latestValue := reflect.ValueOf(latest)
	if latestValue.Kind() != reflect.Pointer {
		return fmt.Errorf("latest muss ein Pointer sein")
	}
	latestValue = latestValue.Elem()
	blocked := map[string]bool{}
	for _, name := range immutable {
		blocked[name] = true
	}
	typ := baseValue.Type()
	for i := 0; i < baseValue.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() || reflect.DeepEqual(baseValue.Field(i).Interface(), desiredValue.Field(i).Interface()) {
			continue
		}
		if blocked[field.Name] {
			return fmt.Errorf("%s ist unveränderlich", field.Name)
		}
		current := latestValue.Field(i).Interface()
		before := baseValue.Field(i).Interface()
		after := desiredValue.Field(i).Interface()
		if !reflect.DeepEqual(current, before) && !reflect.DeepEqual(current, after) {
			return fmt.Errorf("Feld %s wurde ebenfalls geändert", field.Name)
		}
		latestValue.Field(i).Set(desiredValue.Field(i))
	}
	return nil
}

func projectByID(projects []Project, id ProjectID) (Project, bool) {
	for _, project := range projects {
		if project.ID == id {
			return project, true
		}
	}
	return Project{}, false
}

func sessionByID(sessions []Session, id SessionID) (Session, bool) {
	for _, session := range sessions {
		if session.ID == id {
			return session, true
		}
	}
	return Session{}, false
}

func reorderProjects(latest, desired []Project) []Project {
	byID := make(map[ProjectID]Project, len(latest))
	for _, project := range latest {
		byID[project.ID] = project
	}
	ordered := make([]Project, 0, len(latest))
	used := map[ProjectID]bool{}
	for _, project := range desired {
		if current, ok := byID[project.ID]; ok {
			ordered = append(ordered, current)
			used[project.ID] = true
		}
	}
	for _, project := range latest {
		if !used[project.ID] {
			ordered = append(ordered, project)
		}
	}
	return ordered
}
