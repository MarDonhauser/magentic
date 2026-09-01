package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

const registrySchemaVersion = 2

var ErrRegistryConflict = errors.New("Registry wurde gleichzeitig geändert")

type Registry struct {
	path string
}

func OpenRegistry(path string) *Registry {
	if path == "" {
		path = StatePath()
	}
	return &Registry{path: path}
}

type RegistrySnapshot struct {
	state State
}

func (s RegistrySnapshot) Revision() uint64 { return s.state.Revision }

func (s RegistrySnapshot) State() State {
	return cloneState(&s.state)
}

type registryChangeKind uint8

const (
	registryRegisterProject registryChangeKind = iota + 1
	registryRemoveProject
	registryReorderProjects
	registrySetMainBranch
	registryRegisterSession
	registryRemoveSession
	registryMarkSeen
	registryMarkLater
	registryReopenSession
	registryMarkDeploy
	registryRenameSession
	registryAddDiscovered
	registryEnqueueMessage
	registryMarkMessageAttempt
	registryDequeueMessage
	registryResetMessageAttempt
	registryRecordAgentRun
	registrySetVendor
)

// RegistryChange is intentionally constructed through semantic helpers. Its
// representation is private so callers cannot submit arbitrary record patches.
type RegistryChange struct {
	kind        registryChangeKind
	project     Project
	session     Session
	projectID   ProjectID
	sessionID   SessionID
	projectName string
	sessionName string
	newName     string
	newRuntime  string
	mainBranch  string
	order       []ProjectID
	sessions    []Session
	baseCommit  string
	baseDirty   []string
	message     QueuedMessage
	messageID   string
	agentRun    AgentRunRef
	vendor      AgentVendor
	at          time.Time
}

func RegisterProject(project Project) RegistryChange {
	return RegistryChange{kind: registryRegisterProject, project: project}
}

func removeProjectChange(projectID ProjectID, name string) RegistryChange {
	return RegistryChange{kind: registryRemoveProject, projectID: projectID, projectName: name}
}

func ReorderProjects(order []ProjectID) RegistryChange {
	return RegistryChange{kind: registryReorderProjects, order: append([]ProjectID(nil), order...)}
}

func SetProjectMainBranch(projectID ProjectID, name, branch string) RegistryChange {
	return RegistryChange{kind: registrySetMainBranch, projectID: projectID, projectName: name, mainBranch: strings.TrimSpace(branch)}
}

func RegisterSession(session Session) RegistryChange {
	return RegistryChange{kind: registryRegisterSession, session: session}
}

func RemoveSession(sessionID SessionID, name string) RegistryChange {
	return RegistryChange{kind: registryRemoveSession, sessionID: sessionID, sessionName: name}
}

func MarkSessionSeen(sessionID SessionID, name string, at time.Time) RegistryChange {
	return RegistryChange{kind: registryMarkSeen, sessionID: sessionID, sessionName: name, at: at}
}

func MarkSessionLater(sessionID SessionID, name string, at time.Time) RegistryChange {
	return RegistryChange{kind: registryMarkLater, sessionID: sessionID, sessionName: name, at: at}
}

// ReopenRegisteredSessionWithBaseline atomically records a newly-observed
// repository baseline and clears the durable Later intent. Existing baseline
// truth is validated rather than overwritten, so retries cannot clobber a
// concurrent Registry change.
func ReopenRegisteredSessionWithBaseline(sessionID SessionID, name, baseCommit string, baseDirty []string) RegistryChange {
	return RegistryChange{
		kind: registryReopenSession, sessionID: sessionID, sessionName: name,
		baseCommit: strings.TrimSpace(baseCommit), baseDirty: append([]string(nil), baseDirty...),
	}
}

func MarkSessionDeploy(sessionID SessionID, name string, at time.Time) RegistryChange {
	return RegistryChange{kind: registryMarkDeploy, sessionID: sessionID, sessionName: name, at: at}
}

// RenameRegisteredSessionRuntime records the runtime rename already applied by
// an external Adapter. RuntimeName remains the sole address for later tmux
// operations, including Sessions whose runtime never used the default name.
func RenameRegisteredSessionRuntime(sessionID SessionID, oldName, newName, newRuntime string) RegistryChange {
	return RegistryChange{
		kind: registryRenameSession, sessionID: sessionID, sessionName: oldName,
		newName: newName, newRuntime: newRuntime,
	}
}

// RecordAgentRun stores a vendor-qualified run reference that was discovered
// from the vendor's own history. An existing reference for that vendor is
// never overwritten: run identity is durable once known.
func RecordAgentRun(sessionID SessionID, name string, run AgentRunRef) RegistryChange {
	return RegistryChange{kind: registryRecordAgentRun, sessionID: sessionID, sessionName: name, agentRun: run}
}

// SetSessionVendor records which coding-agent vendor starts this Session from
// now on. AgentRuns of other vendors stay untouched, so a Session can carry a
// run reference per vendor and be switched back without losing history.
func SetSessionVendor(sessionID SessionID, name string, vendor AgentVendor) RegistryChange {
	return RegistryChange{kind: registrySetVendor, sessionID: sessionID, sessionName: name, vendor: vendor}
}

// EnqueueSessionMessage appends a message to the Session's durable Outbox. A
// message with the same Kind and Text is treated as a repeated request (double
// click) and is not queued twice.
func EnqueueSessionMessage(sessionID SessionID, name string, message QueuedMessage) RegistryChange {
	return RegistryChange{kind: registryEnqueueMessage, sessionID: sessionID, sessionName: name, message: message}
}

// MarkQueuedMessageAttempt records that delivery of a queued message was
// started, so a crash mid-send leaves the outcome visibly unknown.
func MarkQueuedMessageAttempt(sessionID SessionID, name, messageID string, at time.Time) RegistryChange {
	return RegistryChange{kind: registryMarkMessageAttempt, sessionID: sessionID, sessionName: name, messageID: messageID, at: at}
}

// DequeueSessionMessage removes a queued message, either after delivery or
// because the user discarded it.
func DequeueSessionMessage(sessionID SessionID, name, messageID string) RegistryChange {
	return RegistryChange{kind: registryDequeueMessage, sessionID: sessionID, sessionName: name, messageID: messageID}
}

// ResetQueuedMessageAttempt clears a recorded attempt so the message becomes
// deliverable again.
func ResetQueuedMessageAttempt(sessionID SessionID, name, messageID string) RegistryChange {
	return RegistryChange{kind: registryResetMessageAttempt, sessionID: sessionID, sessionName: name, messageID: messageID}
}

func addDiscoveredSessionsChange(sessions []Session) RegistryChange {
	return RegistryChange{kind: registryAddDiscovered, sessions: append([]Session(nil), sessions...)}
}

type RegistryChangeResult struct {
	Revision  uint64
	Applied   bool
	ProjectID ProjectID
	SessionID SessionID
	Snapshot  RegistrySnapshot
}

func (r *Registry) Snapshot(ctx context.Context) (RegistrySnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var snapshot RegistrySnapshot
	err := withRegistryFileLock(ctx, r.path, func() error {
		state, raw, exists, rescued, err := readRegistryFile(r.path)
		if err != nil {
			return err
		}
		changed := normalizeRegistryState(&state)
		if exists && (changed || rescued) {
			if err := backupLegacyRegistry(r.path, raw); err != nil {
				return err
			}
			state.Revision++
			if err := writeRegistryFile(r.path, &state); err != nil {
				return err
			}
		}
		snapshot = RegistrySnapshot{state: cloneState(&state)}
		return nil
	})
	return snapshot, err
}

func (r *Registry) Change(ctx context.Context, change RegistryChange) (RegistryChangeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var result RegistryChangeResult
	err := r.commit(ctx, func(state *State) (bool, error) {
		applied, projectID, sessionID, err := applyRegistryChange(state, change)
		result.Applied = applied
		result.ProjectID = projectID
		result.SessionID = sessionID
		return applied, err
	}, &result)
	return result, err
}

func (r *Registry) commit(ctx context.Context, apply func(*State) (bool, error), result *RegistryChangeResult) error {
	return withRegistryFileLock(ctx, r.path, func() error {
		state, raw, exists, rescued, err := readRegistryFile(r.path)
		if err != nil {
			return err
		}
		migrated := normalizeRegistryState(&state)
		changed, err := apply(&state)
		if err != nil {
			return err
		}
		if err := validateRegistryState(&state); err != nil {
			return err
		}
		if migrated || rescued || changed || !exists {
			if exists && (migrated || rescued) {
				if err := backupLegacyRegistry(r.path, raw); err != nil {
					return err
				}
			}
			state.Revision++
			if err := writeRegistryFile(r.path, &state); err != nil {
				return err
			}
		}
		if result != nil {
			result.Revision = state.Revision
			result.Snapshot = RegistrySnapshot{state: cloneState(&state)}
		}
		return nil
	})
}

func applyRegistryChange(state *State, change RegistryChange) (bool, ProjectID, SessionID, error) {
	switch change.kind {
	case registryRegisterProject:
		project := change.project
		if project.ID == "" {
			project.ID = ProjectID(NewUUID())
		}
		if state.ProjectByName(project.Name) != nil {
			return false, "", "", fmt.Errorf("Projekt %q existiert schon", project.Name)
		}
		state.Projects = append(state.Projects, project)
		return true, project.ID, "", nil
	case registryRemoveProject:
		idx := projectIndex(state, change.projectID, change.projectName)
		if idx < 0 {
			return false, "", "", fmt.Errorf("Projekt %q nicht gefunden", change.projectName)
		}
		for _, session := range state.Agents {
			if session.ProjectID == state.Projects[idx].ID || session.Project == state.Projects[idx].Name {
				return false, "", "", fmt.Errorf("Projekt %q hat noch Sessions", state.Projects[idx].Name)
			}
		}
		id := state.Projects[idx].ID
		state.Projects = append(state.Projects[:idx], state.Projects[idx+1:]...)
		return true, id, "", nil
	case registryReorderProjects:
		before := projectOrder(state.Projects)
		known := make(map[ProjectID]bool, len(state.Projects))
		for _, project := range state.Projects {
			known[project.ID] = true
		}
		rank := make(map[ProjectID]int, len(change.order))
		for i, id := range change.order {
			if id == "" || rank[id] != 0 {
				return false, "", "", fmt.Errorf("ungültige ProjectID in Sortierung: %q", id)
			}
			if !known[id] {
				return false, "", "", fmt.Errorf("unbekannte ProjectID in Sortierung: %s", id)
			}
			rank[id] = i + 1
		}
		sort.SliceStable(state.Projects, func(i, j int) bool {
			ri, iok := rank[state.Projects[i].ID]
			rj, jok := rank[state.Projects[j].ID]
			if !iok {
				ri = len(rank) + 1
			}
			if !jok {
				rj = len(rank) + 1
			}
			return ri < rj
		})
		return !reflect.DeepEqual(before, projectOrder(state.Projects)), "", "", nil
	case registrySetMainBranch:
		idx := projectIndex(state, change.projectID, change.projectName)
		if idx < 0 {
			return false, "", "", fmt.Errorf("Projekt %q nicht gefunden", change.projectName)
		}
		if state.Projects[idx].MainBranch == change.mainBranch {
			return false, state.Projects[idx].ID, "", nil
		}
		state.Projects[idx].MainBranch = change.mainBranch
		return true, state.Projects[idx].ID, "", nil
	case registryRegisterSession:
		session := change.session
		normalizeSession(&session)
		associateSessionProject(state, &session)
		if state.AgentByName(session.Name) != nil {
			return false, "", "", fmt.Errorf("Session %q existiert schon", session.Name)
		}
		state.Agents = append(state.Agents, session)
		return true, "", session.ID, nil
	case registryRemoveSession:
		idx := sessionIndex(state, change.sessionID, change.sessionName)
		if idx < 0 {
			return false, "", "", nil
		}
		id := state.Agents[idx].ID
		state.Agents = append(state.Agents[:idx], state.Agents[idx+1:]...)
		return true, "", id, nil
	case registryMarkSeen, registryMarkLater, registryReopenSession, registryMarkDeploy, registryRenameSession, registryRecordAgentRun, registrySetVendor:
		idx := sessionIndex(state, change.sessionID, change.sessionName)
		if idx < 0 {
			return false, "", "", fmt.Errorf("Session %q nicht gefunden", change.sessionName)
		}
		session := &state.Agents[idx]
		at := change.at
		if at.IsZero() {
			at = time.Now()
		}
		switch change.kind {
		case registryMarkSeen:
			if at.Sub(session.SeenAt) < 5*time.Second {
				return false, "", session.ID, nil
			}
			session.SeenAt = at
		case registryMarkLater:
			session.LaterAt = at
		case registryReopenSession:
			changed := false
			if change.baseCommit != "" {
				switch {
				case session.BaseCommit == "":
					session.BaseCommit = change.baseCommit
					session.BaseDirty = append([]string(nil), change.baseDirty...)
					changed = true
				case session.BaseCommit != change.baseCommit || !equalStringSlice(session.BaseDirty, change.baseDirty):
					return false, "", "", fmt.Errorf("%w: Session %q hat bereits eine andere Repository-Baseline", ErrRegistryConflict, session.Name)
				}
			}
			if !session.LaterAt.IsZero() {
				session.LaterAt = time.Time{}
				changed = true
			}
			return changed, "", session.ID, nil
		case registryMarkDeploy:
			session.DeployAt = at
		case registryRecordAgentRun:
			if change.agentRun.Vendor == "" || change.agentRun.ExternalID == "" {
				return false, "", "", fmt.Errorf("unvollständige AgentRunRef für Session %q", session.Name)
			}
			if _, exists := session.AgentRun(change.agentRun.Vendor); exists {
				return false, "", session.ID, nil
			}
			session.AgentRuns = append(session.AgentRuns, change.agentRun)
		case registrySetVendor:
			if session.IsTerm() {
				return false, "", "", fmt.Errorf("Session %q ist ein Terminal und hat keinen Agent-Vendor", session.Name)
			}
			if _, known := providerForVendor(change.vendor); !known {
				return false, "", "", fmt.Errorf("unbekannter Agent-Vendor %q", change.vendor)
			}
			if session.Vendor == change.vendor {
				return false, "", session.ID, nil
			}
			session.Vendor = change.vendor
		case registryRenameSession:
			if change.newName == "" {
				return false, "", "", fmt.Errorf("leerer Session-Name")
			}
			if other := state.AgentByName(change.newName); other != nil && other.ID != session.ID {
				return false, "", "", fmt.Errorf("Session %q existiert schon", change.newName)
			}
			oldDefaultRuntime := SessionName(session.Name)
			session.Name = change.newName
			if change.newRuntime != "" {
				session.RuntimeName = change.newRuntime
			} else if session.RuntimeName == "" || session.RuntimeName == oldDefaultRuntime {
				session.RuntimeName = SessionName(change.newName)
			}
		}
		return true, "", session.ID, nil
	case registryEnqueueMessage, registryMarkMessageAttempt, registryDequeueMessage, registryResetMessageAttempt:
		idx := sessionIndex(state, change.sessionID, change.sessionName)
		if idx < 0 {
			return false, "", "", fmt.Errorf("Session %q nicht gefunden", change.sessionName)
		}
		session := &state.Agents[idx]
		if change.kind == registryEnqueueMessage {
			message := change.message
			if message.ID == "" {
				return false, "", "", fmt.Errorf("Nachricht ohne ID")
			}
			for _, queued := range session.Outbox {
				if queued.Kind == message.Kind && queued.Text == message.Text {
					return false, "", session.ID, nil
				}
			}
			session.Outbox = append(session.Outbox, message)
			return true, "", session.ID, nil
		}
		msgIdx := queuedMessageIndex(session.Outbox, change.messageID)
		if msgIdx < 0 {
			// The message is already gone (delivered or discarded elsewhere).
			return false, "", session.ID, nil
		}
		switch change.kind {
		case registryMarkMessageAttempt:
			at := change.at
			if at.IsZero() {
				at = time.Now()
			}
			if session.Outbox[msgIdx].AttemptedAt.Equal(at) {
				return false, "", session.ID, nil
			}
			session.Outbox[msgIdx].AttemptedAt = at
		case registryDequeueMessage:
			session.Outbox = append(session.Outbox[:msgIdx], session.Outbox[msgIdx+1:]...)
			if len(session.Outbox) == 0 {
				session.Outbox = nil
			}
		case registryResetMessageAttempt:
			if session.Outbox[msgIdx].AttemptedAt.IsZero() {
				return false, "", session.ID, nil
			}
			session.Outbox[msgIdx].AttemptedAt = time.Time{}
		}
		return true, "", session.ID, nil
	case registryAddDiscovered:
		changed := false
		var last SessionID
		for _, session := range change.sessions {
			normalizeSession(&session)
			if existing := state.AgentByName(session.Name); existing != nil {
				if existing.RuntimeName == session.RuntimeName {
					continue
				}
				return false, "", "", fmt.Errorf(
					"%w: Session name %q belongs to RuntimeName %q, not %q",
					ErrRegistryConflict, session.Name, existing.RuntimeName, session.RuntimeName,
				)
			}
			for _, existing := range state.Agents {
				if existing.RuntimeName == session.RuntimeName {
					return false, "", "", fmt.Errorf(
						"%w: RuntimeName %q belongs to Session %q",
						ErrRegistryConflict, session.RuntimeName, existing.Name,
					)
				}
			}
			associateSessionProject(state, &session)
			state.Agents = append(state.Agents, session)
			last = session.ID
			changed = true
		}
		return changed, "", last, nil
	default:
		return false, "", "", fmt.Errorf("unbekannte Registry-Änderung")
	}
}

func equalStringSlice(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func normalizeRegistryState(state *State) bool {
	changed := false
	if state.Schema != registrySchemaVersion {
		state.Schema = registrySchemaVersion
		changed = true
	}
	for i := range state.Projects {
		if state.Projects[i].ID == "" {
			state.Projects[i].ID = ProjectID(NewUUID())
			changed = true
		}
	}
	for i := range state.Agents {
		before := state.Agents[i]
		normalizeSession(&state.Agents[i])
		associateSessionProject(state, &state.Agents[i])
		if !reflect.DeepEqual(before, state.Agents[i]) {
			changed = true
		}
	}
	return changed
}

func associateSessionProject(state *State, session *Session) {
	if session.ProjectID != "" {
		if project := state.ProjectByID(session.ProjectID); project != nil {
			session.Project = project.Name
		}
		return
	}
	if project := state.ProjectByName(session.Project); project != nil {
		session.ProjectID = project.ID
	}
}

func normalizeSession(session *Session) {
	if session.ID == "" {
		session.ID = SessionID(NewUUID())
	}
	if session.RuntimeName == "" {
		session.RuntimeName = SessionName(session.Name)
	}
	if session.SessionKind == "" {
		if session.Kind == KindTerm || session.Kind == KindDock {
			session.SessionKind = SessionKindTerminal
		} else {
			session.SessionKind = SessionKindCodingAgent
		}
	}
	if session.Presentation == "" {
		if session.Kind == KindDock {
			session.Presentation = SessionPresentationDock
		} else {
			session.Presentation = SessionPresentationListed
		}
	}
	if session.Purpose == "" {
		switch session.Kind {
		case "cleanup":
			session.Purpose = SessionPurposeCleanup
		case "merge":
			session.Purpose = SessionPurposeMerge
		case "deploy":
			session.Purpose = SessionPurposeDeploy
		default:
			session.Purpose = SessionPurposeWork
		}
	}
	if !session.IsTerm() && session.Vendor == "" {
		session.Vendor = AgentVendorClaude
	}
	if session.SessionID != "" {
		hasLegacy := false
		for _, run := range session.AgentRuns {
			if run.Vendor == AgentVendorClaude && run.ExternalID == session.SessionID {
				hasLegacy = true
				break
			}
		}
		if !hasLegacy {
			session.AgentRuns = append(session.AgentRuns, AgentRunRef{Vendor: AgentVendorClaude, ExternalID: session.SessionID})
		}
	}
}

func validateRegistryState(state *State) error {
	projectIDs := map[ProjectID]bool{}
	projectNames := map[string]bool{}
	for _, project := range state.Projects {
		if project.ID == "" || project.Name == "" || project.Path == "" {
			return fmt.Errorf("Registry enthält ein unvollständiges Projekt")
		}
		if projectIDs[project.ID] || projectNames[project.Name] {
			return fmt.Errorf("Registry enthält ein doppeltes Projekt: %s", project.Name)
		}
		projectIDs[project.ID] = true
		projectNames[project.Name] = true
	}
	sessionIDs := map[SessionID]bool{}
	sessionNames := map[string]bool{}
	runtimeNames := map[string]bool{}
	runs := map[string]bool{}
	for _, session := range state.Agents {
		if session.ID == "" || session.Name == "" || session.RuntimeName == "" {
			return fmt.Errorf("Registry enthält eine unvollständige Session")
		}
		if sessionIDs[session.ID] || sessionNames[session.Name] || runtimeNames[session.RuntimeName] {
			return fmt.Errorf("Registry enthält eine doppelte Session: %s", session.Name)
		}
		sessionIDs[session.ID] = true
		sessionNames[session.Name] = true
		runtimeNames[session.RuntimeName] = true
		if !session.IsTerm() && session.Vendor != "" {
			if _, known := providerForVendor(session.Vendor); !known {
				return fmt.Errorf("Session %q hat einen unbekannten Agent-Vendor %q", session.Name, session.Vendor)
			}
		}
		if session.ProjectID != "" {
			project := state.ProjectByID(session.ProjectID)
			if project == nil {
				return fmt.Errorf("Session %q verweist auf ein unbekanntes Projekt", session.Name)
			}
			if session.Project != project.Name {
				return fmt.Errorf("Session %q enthält eine widersprüchliche Projektzuordnung", session.Name)
			}
		}
		for _, run := range session.AgentRuns {
			if run.Vendor == "" || run.ExternalID == "" {
				return fmt.Errorf("Session %q enthält eine unvollständige AgentRunRef", session.Name)
			}
			key := string(run.Vendor) + "\x00" + run.ExternalID
			if runs[key] {
				return fmt.Errorf("AgentRunRef %q ist mehreren Sessions zugeordnet", run.ExternalID)
			}
			runs[key] = true
		}
	}
	return nil
}

func readRegistryFile(path string) (State, []byte, bool, bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return State{}, nil, false, false, nil
	}
	if err != nil {
		return State{}, nil, false, false, err
	}
	var state State
	if err := json.Unmarshal(data, &state); err == nil {
		return state, data, true, false, nil
	}
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&state); err != nil {
		return State{}, data, true, false, fmt.Errorf("state.json ist beschädigt: %w", err)
	}
	return state, data, true, true, nil
}

func writeRegistryFile(path string, state *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".state-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	remove = false
	if dir, err := os.Open(filepath.Dir(path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func backupLegacyRegistry(path string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	backup := path + ".pre-registry-v2.bak"
	if _, err := os.Stat(backup); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(backup, data, 0o600)
}

func cloneState(state *State) State {
	if state == nil {
		return State{}
	}
	clone := *state
	clone.Projects = append([]Project(nil), state.Projects...)
	clone.Agents = append([]Session(nil), state.Agents...)
	for i := range clone.Agents {
		clone.Agents[i].BaseDirty = append([]string(nil), clone.Agents[i].BaseDirty...)
		clone.Agents[i].AgentRuns = append([]AgentRunRef(nil), clone.Agents[i].AgentRuns...)
		clone.Agents[i].Outbox = append([]QueuedMessage(nil), clone.Agents[i].Outbox...)
	}
	return clone
}

func projectIndex(state *State, id ProjectID, name string) int {
	for i := range state.Projects {
		if (id != "" && state.Projects[i].ID == id) || (id == "" && state.Projects[i].Name == name) {
			return i
		}
	}
	return -1
}

func sessionIndex(state *State, id SessionID, name string) int {
	for i := range state.Agents {
		if (id != "" && state.Agents[i].ID == id) || (id == "" && state.Agents[i].Name == name) {
			return i
		}
	}
	return -1
}

func queuedMessageIndex(outbox []QueuedMessage, id string) int {
	for i := range outbox {
		if id != "" && outbox[i].ID == id {
			return i
		}
	}
	return -1
}

func projectOrder(projects []Project) []ProjectID {
	order := make([]ProjectID, len(projects))
	for i, project := range projects {
		order[i] = project.ID
	}
	return order
}
