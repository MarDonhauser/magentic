package remote

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"magentic/core"
)

// CoreBackend bedient Host-Aufrufe über dieselben core-Pfade wie die lokale
// Desktop-App: derselbe Registry-Zugriff, dieselbe Lifecycle-Koordination
// (ADR 0002), dieselben Domänen-Semantiken. Das Netz ist Transport, kein
// zweiter Lifecycle. Was hier nicht verdrahtet ist (App-lokale
// Kompositionen aus Verlaufslesern), meldet ErrorMethod statt zu raten.
type CoreBackend struct {
	observe func(context.Context, []core.Session) core.ObservationSnapshot
	planner *core.AttentionPlanner
	ledger  *identityLedger
	termSrc TermSource
	events  *eventLog
}

// TermSource liefert Terminal-Bytes für Streams; die Produktion hängt den
// tmux-Poll an, Tests einen Stub.
type TermSource interface {
	// Snapshot liefert den aktuellen Pane-Inhalt einer Session.
	Snapshot(sessionID string) ([]byte, error)
	// Write schreibt Eingabe-Bytes in die Session.
	Write(sessionID string, data []byte) error
	// Resize meldet eine Größenänderung.
	Resize(sessionID string, cols, rows int) error
}

// NewCoreBackend hängt die echte lokale Implementierung ein.
func NewCoreBackend() *CoreBackend {
	backend := &CoreBackend{
		observe: core.Observe,
		planner: core.NewAttentionPlanner(core.AttentionPlannerConfig{}),
		ledger:  newIdentityLedger(),
		events:  newEventLog(),
	}
	backend.termSrc = &tmuxTermSource{backend: backend}
	backend.events.termOf = func(sessionID string) TermSource {
		if backend.termSrc == nil {
			return nil
		}
		return backend.termSrc
	}
	return backend
}

func (b *CoreBackend) freshObserve(ctx context.Context, sessions []core.Session) core.ObservationSnapshot {
	observe := b.observe
	if observe == nil {
		observe = core.Observe
	}
	snapshot := observe(ctx, sessions)
	snapshot.Transport = core.ObservationTransportRemote
	return snapshot
}

// HandleCall führt einen policy-erlaubten Aufruf aus.
func (b *CoreBackend) HandleCall(ctx context.Context, method string, params json.RawMessage, identity string) (any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch method {
	case "Overview":
		var args struct {
			Fresh bool `json:"fresh"`
		}
		decodeParams(params, &args)
		return b.overview(ctx)
	case "Inbox":
		return b.inbox(ctx)
	case "Board":
		var args struct {
			ProjectID string `json:"projectID"`
		}
		decodeParams(params, &args)
		return b.board(args.ProjectID, false, 0)
	case "BoardArchive":
		var args struct {
			ProjectID string `json:"projectID"`
			Limit     int    `json:"limit"`
		}
		decodeParams(params, &args)
		return b.board(args.ProjectID, true, args.Limit)
	case "GitGraph":
		var args struct {
			ProjectID string `json:"projectID"`
			Limit     int    `json:"limit"`
		}
		decodeParams(params, &args)
		return b.gitGraph(args.ProjectID, args.Limit)
	case "Stats":
		var args struct {
			Days int `json:"days"`
		}
		decodeParams(params, &args)
		return b.stats(args.Days)
	case "SessionAutomation":
		var args struct {
			SessionID string `json:"sessionID"`
		}
		decodeParams(params, &args)
		return b.sessionAutomation(args.SessionID)
	case "CompleteCommands":
		var args struct {
			SessionID string `json:"sessionID"`
			Query     string `json:"query"`
		}
		decodeParams(params, &args)
		return b.completeCommands(args.SessionID, args.Query)
	case "PromptLinePattern":
		var args struct {
			SessionID string `json:"sessionID"`
		}
		decodeParams(params, &args)
		return b.promptLinePattern(args.SessionID)
	case "AgentVendors":
		return core.AgentVendorCatalog(), nil
	case "StartBoardItem":
		var args struct {
			ProjectID string `json:"projectID"`
			Token     string `json:"token"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return b.startBoardItem(ctx, args.ProjectID, args.Token)
		})
	case "StructuredDiff":
		var args struct {
			ProjectID string `json:"projectID"`
			Reference string `json:"reference"`
			Mode      string `json:"mode"`
		}
		decodeParams(params, &args)
		return b.structuredDiff(ctx, args.ProjectID, args.Reference, args.Mode)
	case "WorktreeDiff":
		var args struct {
			ProjectID string `json:"projectID"`
			Reference string `json:"reference"`
		}
		decodeParams(params, &args)
		return b.worktreeDiff(ctx, args.ProjectID, args.Reference)
	case "NewSession", "NewSessionWithVendor", "NewTermSession", "NewTermSessionFor", "NewDockSession":
		var args struct {
			ProjectID string `json:"projectID"`
			Worktree  bool   `json:"worktree"`
			Name      string `json:"name"`
			Vendor    string `json:"vendor"`
			SessionID string `json:"sessionID"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return b.createSession(ctx, method, args.ProjectID, args.Worktree, args.Name, args.Vendor, args.SessionID)
		})
	case "SendMessage":
		var args struct {
			SessionID string `json:"sessionID"`
			Text      string `json:"text"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return nil, b.sendMessage(args.SessionID, args.Text)
		})
	case "SendSkill":
		var args struct {
			SessionID string `json:"sessionID"`
			Cmd       string `json:"cmd"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return nil, b.sendSkill(args.SessionID, args.Cmd)
		})
	case "DiscardQueuedMessage", "RetryQueuedMessage":
		var args struct {
			SessionID string `json:"sessionID"`
			MessageID string `json:"messageID"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return nil, b.queuedMessage(method, args.SessionID, args.MessageID)
		})
	case "SaveSessionAutomation", "DeleteSessionAutomation":
		var args struct {
			SessionID    string `json:"sessionID"`
			AutomationID string `json:"automationID"`
			Name         string `json:"name"`
			Instructions string `json:"instructions"`
			EveryMinutes int    `json:"everyMinutes"`
			NextRunAt    string `json:"nextRunAt"`
			Enabled      bool   `json:"enabled"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return b.automation(method, args.SessionID, args.AutomationID, args.Name, args.Instructions, args.EveryMinutes, args.NextRunAt, args.Enabled)
		})
	case "SwitchSessionVendor":
		var args struct {
			SessionID      string `json:"sessionID"`
			Vendor         string `json:"vendor"`
			IncludeHistory bool   `json:"includeHistory"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return nil, b.switchVendor(ctx, args.SessionID, args.Vendor, args.IncludeHistory)
		})
	case "HandoffSession":
		var args struct {
			SourceID string `json:"sourceID"`
			TargetID string `json:"targetID"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return nil, b.handoff(ctx, args.SourceID, args.TargetID)
		})
	case "DoneAgent":
		var args struct {
			SessionID string `json:"sessionID"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return nil, core.DoneSession(core.SessionID(strings.TrimSpace(args.SessionID)))
		})
	case "Cleanup", "Merge", "Deploy":
		var args struct {
			ProjectID string `json:"projectID"`
			Reference string `json:"reference"`
			Source    string `json:"source"`
			Target    string `json:"target"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return b.startJob(ctx, method, args.ProjectID, args.Reference, args.Source, args.Target)
		})
	case "MarkSeen", "SetSessionService":
		var args struct {
			SessionID string `json:"sessionID"`
			Service   bool   `json:"service"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return nil, b.markSession(ctx, method, args.SessionID, args.Service)
		})
	case "SetMainBranch":
		var args struct {
			ProjectID string `json:"projectID"`
			Main      string `json:"main"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return nil, b.setMainBranch(ctx, args.ProjectID, args.Main)
		})
	case "LaterSession", "ReopenSession", "ResumeSession", "FreshStartSession", "DiscardSession":
		var args struct {
			SessionID string `json:"sessionID"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return nil, b.sessionState(ctx, method, args.SessionID)
		})
	case "KillSession":
		var args struct {
			SessionID      string `json:"sessionID"`
			LegacyDockName string `json:"legacyDockName"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return nil, b.killSession(ctx, args.SessionID, args.LegacyDockName)
		})
	case "RemoveWorktree":
		var args struct {
			ProjectID string `json:"projectID"`
			Reference string `json:"reference"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return nil, b.removeWorktree(ctx, args.ProjectID, args.Reference)
		})
	case "RemoveProject":
		var args struct {
			ProjectID string `json:"projectID"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return nil, core.OpenSessionLifecycle(core.SessionLifecycleConfig{}).RemoveProject(ctx, core.ProjectID(strings.TrimSpace(args.ProjectID)))
		})
	case "AddProject":
		var args struct {
			Path string `json:"path"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return b.addProject(ctx, args.Path)
		})
	case "AddReviewComment", "EditReviewComment", "DeleteReviewComment", "DiscardSentReview", "SendReview", "ReviewPreview", "OpenReview", "SentReviews":
		var args reviewArgs
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return b.review(ctx, method, args)
		})
	case "AddDivider", "RenameDivider", "RemoveDivider", "SetDividerCollapsed", "MoveSidebarItem":
		var args struct {
			DividerID  string            `json:"dividerID"`
			Name       string            `json:"name"`
			Collapsed  bool              `json:"collapsed"`
			Kind       string            `json:"kind"`
			Ref        string            `json:"ref"`
			ParentKind string            `json:"parentKind"`
			Parent     string            `json:"parent"`
			Order      []core.SidebarRef `json:"order"`
		}
		decodeParams(params, &args)
		return b.mutate(identity, method, func() (any, error) {
			return b.sidebar(ctx, method, args.DividerID, args.Name, args.Collapsed, args.Kind, args.Ref, args.ParentKind, args.Parent, args.Order)
		})
	case "OpenTerm", "WriteTerm", "ResizeTerm", "CloseTerm":
		var args struct {
			SessionID string `json:"sessionID"`
			DataB64   string `json:"dataB64"`
			Cols      int    `json:"cols"`
			Rows      int    `json:"rows"`
		}
		decodeParams(params, &args)
		return b.terminal(method, args.SessionID, args.DataB64, args.Cols, args.Rows)
	default:
		return nil, &WireError{Code: ErrorMethod, Message: "Methode " + method + " wird von diesem Host noch nicht über Remote bedient"}
	}
}

func decodeParams(params json.RawMessage, target any) {
	if len(params) == 0 {
		return
	}
	_ = json.Unmarshal(params, target)
}

// mutate führt aktionstragende Aufrufe über das Identitäts-Ledger (D7): Die
// client-generierte Identität macht eine bewusste Wiederholung idempotent;
// der Host wiederholt niemals von sich aus.
func (b *CoreBackend) mutate(identity, method string, fn func() (any, error)) (any, error) {
	return b.ledger.submit(identity, method, fn)
}

// identityLedger merkt sich je client-generierter Identität genau ein
// Ergebnis. Eine zweite Einreichung derselben Identität schreitet nichts
// erneut voran, sondern liefert das gemerkte Ergebnis — dieselbe Transition,
// keine zweite.
type identityLedger struct {
	mu   sync.Mutex
	done map[string]json.RawMessage
}

func newIdentityLedger() *identityLedger {
	return &identityLedger{done: map[string]json.RawMessage{}}
}

func (l *identityLedger) submit(identity, method string, fn func() (any, error)) (any, error) {
	if strings.TrimSpace(identity) == "" {
		return fn()
	}
	key := method + "\x00" + strings.TrimSpace(identity)
	l.mu.Lock()
	if raw, known := l.done[key]; known {
		l.mu.Unlock()
		var result any
		if err := json.Unmarshal(raw, &result); err != nil {
			return nil, err
		}
		return result, nil
	}
	l.mu.Unlock()
	result, err := fn()
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.done[key] = encoded
	l.mu.Unlock()
	return result, nil
}

func (b *CoreBackend) loadState() (*core.State, error) {
	return core.LoadState()
}

func (b *CoreBackend) loadSession(sessionID string) (*core.State, core.Session, error) {
	id := core.SessionID(strings.TrimSpace(sessionID))
	if id == "" {
		return nil, core.Session{}, fmt.Errorf("SessionID fehlt")
	}
	st, err := b.loadState()
	if err != nil {
		return nil, core.Session{}, err
	}
	session := st.SessionByID(id)
	if session == nil {
		return nil, core.Session{}, fmt.Errorf("unbekannte SessionID: %s", id)
	}
	return st, *session, nil
}

func (b *CoreBackend) loadProject(projectID string) (*core.State, core.Project, error) {
	id := core.ProjectID(strings.TrimSpace(projectID))
	if id == "" {
		return nil, core.Project{}, fmt.Errorf("ProjectID fehlt")
	}
	st, err := b.loadState()
	if err != nil {
		return nil, core.Project{}, err
	}
	project := st.ProjectByID(id)
	if project == nil {
		return nil, core.Project{}, fmt.Errorf("unbekannte ProjectID: %s", id)
	}
	return st, *project, nil
}

func (b *CoreBackend) resolveWorktree(ctx context.Context, projectID, reference string) (*core.State, core.RepositoryWorktreeTarget, error) {
	st, project, err := b.loadProject(projectID)
	if err != nil {
		return nil, core.RepositoryWorktreeTarget{}, err
	}
	if err := RejectClientPath(reference); err != nil {
		return nil, core.RepositoryWorktreeTarget{}, err
	}
	target, err := core.NewRepositories().ResolveWorktree(ctx, project, core.WorktreeRef(strings.TrimSpace(reference)))
	if err != nil {
		return nil, core.RepositoryWorktreeTarget{}, fmt.Errorf("Worktree konnte nicht frisch aufgelöst werden: %w", err)
	}
	return st, target, nil
}

func (b *CoreBackend) overview(ctx context.Context) (core.Overview, error) {
	st, err := b.loadState()
	if err != nil {
		return core.Overview{}, err
	}
	snapshot := b.freshObserve(ctx, st.Agents)
	survey, surveyErr := core.NewRepositories().Survey(ctx, append([]core.Project(nil), st.Projects...))
	return core.BuildOverviewWithSurvey(st, snapshot, survey, surveyErr), nil
}

func (b *CoreBackend) inbox(ctx context.Context) (core.OvInbox, error) {
	st, err := b.loadState()
	if err != nil {
		return core.OvInbox{}, err
	}
	snapshot := b.freshObserve(ctx, st.Agents)
	plan := b.planner.Plan(core.AttentionInput{Observation: snapshot, Now: time.Now()})
	return core.BuildInbox(st, plan.Inbox), nil
}

func (b *CoreBackend) board(projectID string, archive bool, limit int) (core.Board, error) {
	st, project, err := b.loadProject(projectID)
	if err != nil {
		return core.Board{}, err
	}
	if archive {
		return core.BuildBoardWithQuery(st, project.ID, core.SpecificationQuery{
			IncludeArchived: true, ArchiveLimit: limit,
		}), nil
	}
	return core.BuildBoard(st, project.ID), nil
}

func (b *CoreBackend) gitGraph(projectID string, limit int) (core.GitGraph, error) {
	st, project, err := b.loadProject(projectID)
	if err != nil {
		return core.GitGraph{}, err
	}
	return core.BuildGitGraph(st, project.ID, limit), nil
}

func (b *CoreBackend) stats(days int) (core.Stats, error) {
	st, err := b.loadState()
	if err != nil {
		return core.Stats{}, err
	}
	return core.BuildStats(st, days), nil
}

func (b *CoreBackend) sessionAutomation(sessionID string) (*core.SessionAutomation, error) {
	_, session, err := b.loadSession(sessionID)
	if err != nil {
		return nil, err
	}
	if session.Automation == nil {
		return nil, nil
	}
	automation := *session.Automation
	return &automation, nil
}

const completionResultLimit = 50

func (b *CoreBackend) completeCommands(sessionID, query string) ([]core.SlashCommand, error) {
	_, session, err := b.loadSession(sessionID)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	matches := []core.SlashCommand{}
	for _, command := range core.SlashCommands(session.SessionVendor(), session.Dir) {
		if needle != "" && !strings.HasPrefix(strings.ToLower(command.Name), needle) {
			continue
		}
		matches = append(matches, command)
		if len(matches) >= completionResultLimit {
			break
		}
	}
	return matches, nil
}

func (b *CoreBackend) promptLinePattern(sessionID string) (string, error) {
	_, session, err := b.loadSession(sessionID)
	if err != nil {
		return "", err
	}
	return core.PromptLinePattern(session.SessionVendor()), nil
}

func (b *CoreBackend) startBoardItem(ctx context.Context, projectID, token string) (string, error) {
	_, project, err := b.loadProject(projectID)
	if err != nil {
		return "", err
	}
	intent, err := core.NewSpecifications().ResolveStart(ctx, project, core.SpecificationStartToken(strings.TrimSpace(token)))
	if err != nil {
		return "", fmt.Errorf("Specification kann nicht gestartet werden: %w", err)
	}
	st, err := b.loadState()
	if err != nil {
		return "", err
	}
	return core.StartSpecificationSession(st, intent)
}

func (b *CoreBackend) structuredDiff(ctx context.Context, projectID, reference, mode string) (core.StructuredDiff, error) {
	diffMode := core.DiffComparisonWorkingTree
	switch strings.TrimSpace(mode) {
	case "", string(core.DiffComparisonWorkingTree):
	case string(core.DiffComparisonBranch):
		diffMode = core.DiffComparisonBranch
	default:
		return core.StructuredDiff{}, fmt.Errorf("unbekannter Vergleichsmodus %q", mode)
	}
	_, target, err := b.resolveWorktree(ctx, projectID, reference)
	if err != nil {
		return core.StructuredDiff{}, err
	}
	fact := core.NewRepositories().StructuredDiff(ctx, target, diffMode)
	if !fact.Known() {
		message := "Strukturierter Diff ist derzeit nicht verfügbar"
		if fact.Problem != nil {
			operation := strings.TrimSpace(fact.Problem.Operation)
			detail := strings.TrimSpace(fact.Problem.Message)
			switch {
			case operation != "" && detail != "":
				message = operation + ": " + detail
			case detail != "":
				message = detail
			case operation != "":
				message = operation
			}
		}
		return core.StructuredDiff{}, fmt.Errorf("%s", message)
	}
	return fact.Value, nil
}

func (b *CoreBackend) worktreeDiff(ctx context.Context, projectID, reference string) (string, error) {
	_, target, err := b.resolveWorktree(ctx, projectID, reference)
	if err != nil {
		return "", err
	}
	fact := core.NewRepositories().WorktreeDiff(ctx, target.Worktree)
	if !fact.Known() {
		message := "Worktree-Diff ist derzeit nicht verfügbar"
		if fact.Problem != nil && strings.TrimSpace(fact.Problem.Message) != "" {
			message = fact.Problem.Message
		}
		return "", fmt.Errorf("%s", message)
	}
	out := fact.Value
	const cap = 400_000
	if len(out) > cap {
		out = out[:cap] + "\n… (gekürzt)"
	}
	return out, nil
}

func (b *CoreBackend) createSession(_ context.Context, method, projectID string, worktree bool, name, vendor, sessionID string) (any, error) {
	st, project, err := b.loadProject(projectID)
	if err != nil {
		return "", err
	}
	switch method {
	case "NewSession":
		return core.CreateAgentSession(st, project.ID, worktree, name)
	case "NewSessionWithVendor":
		return core.CreateAgentSessionWithVendor(st, project.ID, worktree, name, vendor)
	case "NewTermSession":
		return core.CreateTermSession(st, project.ID, worktree, name)
	case "NewTermSessionFor":
		_, session, err := b.loadSession(sessionID)
		if err != nil {
			return "", err
		}
		return core.CreateTermSessionForID(st, session.ID, "")
	case "NewDockSession":
		return b.createDockSession(st, project)
	}
	return "", &WireError{Code: ErrorMethod, Message: "unbekannte Anlage " + method}
}

func (b *CoreBackend) createDockSession(st *core.State, project core.Project) (map[string]string, error) {
	name, err := core.CreateDockSession(st, project.ID)
	if err != nil {
		return nil, err
	}
	fresh, err := b.loadState()
	if err != nil {
		return nil, err
	}
	session := fresh.AgentByName(name)
	if session == nil || !session.IsDock() || session.ID == "" {
		return nil, fmt.Errorf("Dock-Session %q wurde nicht stabil registriert", name)
	}
	return map[string]string{"id": string(session.ID), "name": session.Name}, nil
}

func (b *CoreBackend) sendMessage(sessionID, text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("Nachricht ist leer")
	}
	return core.SendQueuedMessageWithObserver(
		core.SessionID(strings.TrimSpace(sessionID)), core.QueuedMessageKindMessage, text, b.observe)
}

func (b *CoreBackend) sendSkill(sessionID, cmd string) error {
	if !strings.HasPrefix(cmd, "/") {
		return fmt.Errorf("nur Slash-Kommandos erlaubt")
	}
	return core.SendSkillByIDWithObserver(
		core.SessionID(strings.TrimSpace(sessionID)), cmd, b.observe)
}

func (b *CoreBackend) queuedMessage(method, sessionID, messageID string) error {
	id := core.SessionID(strings.TrimSpace(sessionID))
	switch method {
	case "DiscardQueuedMessage":
		return core.DiscardQueuedMessage(id, strings.TrimSpace(messageID))
	default:
		return core.RetryQueuedMessage(id, strings.TrimSpace(messageID))
	}
}

func (b *CoreBackend) automation(method, sessionID, automationID, name, instructions string, everyMinutes int, nextRunAt string, enabled bool) (any, error) {
	_, session, err := b.loadSession(sessionID)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if method == "DeleteSessionAutomation" {
		_, err = core.OpenRegistry(core.StatePath()).Change(ctx,
			core.DeleteSessionAutomation(session.ID, session.Name, strings.TrimSpace(automationID)))
		return nil, err
	}
	when, err := time.Parse(time.RFC3339, strings.TrimSpace(nextRunAt))
	if err != nil {
		return core.SessionAutomation{}, fmt.Errorf("Zeitpunkt ist ungültig: %w", err)
	}
	automation := core.SessionAutomation{
		ID: strings.TrimSpace(automationID), Name: name, Instructions: instructions,
		EveryMinutes: everyMinutes, NextRunAt: when, Enabled: enabled,
	}
	result, err := core.OpenRegistry(core.StatePath()).Change(ctx,
		core.SetSessionAutomation(session.ID, session.Name, automation))
	if err != nil {
		return core.SessionAutomation{}, err
	}
	snapshotState := result.Snapshot.State()
	saved := snapshotState.SessionByID(session.ID)
	if saved == nil || saved.Automation == nil {
		return core.SessionAutomation{}, fmt.Errorf("Automatisierung wurde nicht gespeichert")
	}
	return *saved.Automation, nil
}

func (b *CoreBackend) switchVendor(ctx context.Context, sessionID, vendor string, includeHistory bool) error {
	id := core.SessionID(strings.TrimSpace(sessionID))
	targetVendor := core.AgentVendor(strings.TrimSpace(vendor))
	handoffPrompt := ""
	if includeHistory {
		st, err := b.loadState()
		if err != nil {
			return err
		}
		snapshot := b.freshObserve(ctx, st.Agents)
		handoffPrompt, err = core.BuildVendorSwitchHandoffPrompt(st, snapshot, id, targetVendor)
		if err != nil {
			return fmt.Errorf("kompakte Verlaufsübergabe vorbereiten: %w", err)
		}
	}
	if err := core.SwitchSessionVendor(id, string(targetVendor)); err != nil {
		return err
	}
	if handoffPrompt == "" {
		return nil
	}
	if err := core.SendQueuedMessageWithObserver(id, core.QueuedMessageKindMessage, handoffPrompt, b.observe); err != nil {
		return fmt.Errorf("Agent gewechselt, aber Verlaufsübergabe konnte nicht vorgemerkt werden: %w", err)
	}
	return nil
}

func (b *CoreBackend) handoff(ctx context.Context, sourceID, targetID string) error {
	st, err := b.loadState()
	if err != nil {
		return err
	}
	snapshot := b.freshObserve(ctx, st.Agents)
	return core.HandoffSessionWithObserver(st, snapshot,
		core.SessionID(sourceID), core.SessionID(targetID), b.observe)
}

func (b *CoreBackend) startJob(ctx context.Context, method, projectID, reference, source, target string) (string, error) {
	st, project, err := b.loadProject(projectID)
	if err != nil {
		return "", err
	}
	switch method {
	case "Cleanup":
		_, resolved, err := b.resolveWorktree(ctx, projectID, reference)
		if err != nil {
			return "", err
		}
		if resolved.Worktree.Main {
			return "", fmt.Errorf("Cleanup ist nur für verwaltete Worktrees verfügbar")
		}
		if !resolved.MainBranch.Known() || strings.TrimSpace(resolved.MainBranch.Value) == "" {
			return "", fmt.Errorf("Hauptbranch ist derzeit nicht verlässlich bekannt")
		}
		return core.StartCleanup(st, project.ID, resolved.Worktree.Path, resolved.MainBranch.Value)
	case "Merge":
		return core.StartMerge(st, project.ID, project.Path, source, target)
	default:
		return core.StartDeploy(st, project.ID, project.Path)
	}
}

func (b *CoreBackend) markSession(ctx context.Context, method, sessionID string, service bool) error {
	_, session, err := b.loadSession(sessionID)
	if err != nil {
		return err
	}
	if method == "MarkSeen" {
		_, err = core.OpenRegistry(core.StatePath()).Change(ctx,
			core.MarkSessionSeen(session.ID, session.Name, time.Now()))
		return err
	}
	_, err = core.OpenRegistry(core.StatePath()).Change(ctx,
		core.SetSessionService(session.ID, session.Name, service))
	return err
}

func (b *CoreBackend) setMainBranch(ctx context.Context, projectID, main string) error {
	_, project, err := b.loadProject(projectID)
	if err != nil {
		return err
	}
	_, err = core.OpenRegistry(core.StatePath()).Change(ctx,
		core.SetProjectMainBranch(project.ID, project.Name, strings.TrimSpace(main)))
	return err
}

// sessionState bildet Later/Reopen/Resume/Fresh/Discard ab — mit derselben
// frischen Beobachtung und denselben Resumability-Gates wie lokal.
func (b *CoreBackend) sessionState(ctx context.Context, method, sessionID string) error {
	st, session, err := b.loadSession(sessionID)
	if err != nil {
		return err
	}
	switch method {
	case "LaterSession":
		return core.ParkSession(st, session.Name)
	case "ReopenSession":
		return core.ReopenLater(st, session.Name)
	}
	snapshot := b.freshObserve(ctx, []core.Session{session})
	observed := core.SessionObservation{Presence: core.SessionPresenceUnknown}
	for _, candidate := range snapshot.Sessions {
		if candidate.SessionID == session.ID {
			observed = candidate
		}
	}
	switch method {
	case "ResumeSession":
		res := core.ResumabilityForSession(session, observed)
		if !res.Resumable || res.FreshOnly {
			if res.Reason != "" {
				return fmt.Errorf("Session %q ist nicht fortsetzbar: %s", session.Name, res.Reason)
			}
			return fmt.Errorf("Session %q ist nur frisch startbar, nicht fortsetzbar", session.Name)
		}
		return core.ResumeSessionByID(session.ID)
	case "FreshStartSession":
		res := core.ResumabilityForSession(session, observed)
		if !res.Resumable || !res.FreshOnly {
			if res.Reason != "" {
				return fmt.Errorf("Session %q ist nicht startbar: %s", session.Name, res.Reason)
			}
			return fmt.Errorf("Session %q hält ihre Konversation noch vor — fortsetzen statt frisch starten", session.Name)
		}
		return core.ResumeFreshSessionByID(session.ID)
	default:
		return core.DiscardSessionByID(session.ID, observed)
	}
}

func (b *CoreBackend) killSession(ctx context.Context, sessionID, legacyDockName string) error {
	st, err := b.loadState()
	if err != nil {
		return err
	}
	var session *core.Session
	if strings.TrimSpace(sessionID) != "" {
		session = st.SessionByID(core.SessionID(strings.TrimSpace(sessionID)))
	} else if name := strings.TrimSpace(legacyDockName); name != "" {
		if candidate := st.AgentByName(name); candidate != nil && candidate.IsDock() {
			session = candidate
		}
	}
	if session == nil {
		return fmt.Errorf("unbekannte Session")
	}
	return core.RemoveRegisteredSession(st, session.Name)
}

func (b *CoreBackend) removeWorktree(ctx context.Context, projectID, reference string) error {
	st, project, err := b.loadProject(projectID)
	if err != nil {
		return err
	}
	if err := RejectClientPath(reference); err != nil {
		return err
	}
	target, resolveErr := core.NewRepositories().ResolveWorktree(ctx, project, core.WorktreeRef(strings.TrimSpace(reference)))
	if resolveErr != nil {
		return fmt.Errorf("Worktree konnte nicht frisch aufgelöst werden: %w", resolveErr)
	}
	return core.RemoveWorktree(st, &project, target.Worktree.Path)
}

func (b *CoreBackend) addProject(ctx context.Context, path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("kein Pfad angegeben")
	}
	if strings.HasPrefix(trimmed, "~") {
		home, _ := os.UserHomeDir()
		trimmed = filepath.Join(home, strings.TrimPrefix(trimmed, "~"))
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("Verzeichnis nicht gefunden: %s", abs)
	}
	name := filepath.Base(abs)
	if _, err := core.OpenRegistry(core.StatePath()).Change(ctx,
		core.RegisterProject(core.Project{Name: name, Path: abs})); err != nil {
		return "", err
	}
	return name, nil
}

type reviewArgs struct {
	SessionID string `json:"sessionID"`
	Path      string `json:"path"`
	OldStart  int    `json:"oldStart"`
	OldEnd    int    `json:"oldEnd"`
	NewStart  int    `json:"newStart"`
	NewEnd    int    `json:"newEnd"`
	Quoted    string `json:"quoted"`
	Text      string `json:"text"`
	Mode      string `json:"mode"`
	CommentID string `json:"commentID"`
	ReviewID  string `json:"reviewID"`
}

func (b *CoreBackend) review(ctx context.Context, method string, args reviewArgs) (any, error) {
	_, session, err := b.loadSession(args.SessionID)
	if err != nil {
		return nil, err
	}
	registry := func() *core.Registry { return core.OpenRegistry(core.StatePath()) }
	switch method {
	case "AddReviewComment":
		diffMode, err := parseReviewMode(args.Mode)
		if err != nil {
			return nil, err
		}
		comment := core.ReviewComment{
			ID: core.NewUUID(), Path: args.Path,
			OldStart: args.OldStart, OldEnd: args.OldEnd,
			NewStart: args.NewStart, NewEnd: args.NewEnd,
			Quoted: args.Quoted, Text: args.Text, Mode: diffMode, CreatedAt: time.Now(),
		}
		if _, err := registry().Change(ctx, core.AddReviewComment(session.ID, session.Name, comment)); err != nil {
			return nil, err
		}
		comment.Path = strings.TrimSpace(comment.Path)
		comment.Text = strings.TrimSpace(comment.Text)
		return comment, nil
	case "EditReviewComment":
		_, err = registry().Change(ctx, core.EditReviewComment(session.ID, session.Name, args.CommentID, args.Text))
		return nil, err
	case "DeleteReviewComment":
		_, err = registry().Change(ctx, core.DeleteReviewComment(session.ID, session.Name, args.CommentID))
		return nil, err
	case "DiscardSentReview":
		_, err = registry().Change(ctx, core.DiscardSentReview(session.ID, session.Name, args.ReviewID))
		return nil, err
	case "SendReview":
		return nil, core.SendSessionReview(session.ID, b.observe)
	case "ReviewPreview":
		if session.Review == nil || len(session.Review.Comments) == 0 {
			return "", fmt.Errorf("Review enthält keine Kommentare")
		}
		return core.RenderReviewPrompt(*session.Review, session.Name), nil
	case "OpenReview":
		if session.Review == nil {
			return nil, nil
		}
		review := *session.Review
		review.Comments = append([]core.ReviewComment(nil), session.Review.Comments...)
		return &review, nil
	default:
		return append([]core.SessionReview(nil), session.SentReviews...), nil
	}
}

func parseReviewMode(mode string) (core.DiffComparisonMode, error) {
	switch strings.TrimSpace(mode) {
	case "", string(core.DiffComparisonWorkingTree):
		return core.DiffComparisonWorkingTree, nil
	case string(core.DiffComparisonBranch):
		return core.DiffComparisonBranch, nil
	default:
		return "", fmt.Errorf("unbekannter Vergleichsmodus %q", mode)
	}
}

func (b *CoreBackend) sidebar(ctx context.Context, method, dividerID, name string, collapsed bool, kind, ref, parentKind, parent string, order []core.SidebarRef) (any, error) {
	registry := core.OpenRegistry(core.StatePath())
	switch method {
	case "AddDivider":
		name = strings.TrimSpace(name)
		if name == "" {
			return "", fmt.Errorf("Divider braucht einen Namen")
		}
		id := core.DividerID(core.NewUUID())
		if _, err := registry.Change(ctx, core.AddDivider(id, name)); err != nil {
			return "", err
		}
		return string(id), nil
	case "RenameDivider":
		_, err := registry.Change(ctx, core.RenameDivider(core.DividerID(dividerID), name))
		return nil, err
	case "RemoveDivider":
		_, err := registry.Change(ctx, core.RemoveDivider(core.DividerID(dividerID)))
		return nil, err
	case "SetDividerCollapsed":
		_, err := registry.Change(ctx, core.SetDividerCollapsed(core.DividerID(dividerID), collapsed))
		return nil, err
	default:
		_, err := registry.Change(ctx, core.MoveSidebarItem(
			core.SidebarSlotKind(kind), ref, core.SidebarSlotKind(parentKind), parent, order))
		return nil, err
	}
}

// terminal bedient Open/Write/Resize/Close über die Terminal-Quelle: Der
// Stream trägt die Bytes, diese Aufrufe tragen Anhang, Eingabe und Größe.
func (b *CoreBackend) terminal(method, sessionID, dataB64 string, cols, rows int) (any, error) {
	if b.termSrc == nil {
		return nil, &WireError{Code: ErrorMethod, Message: "keine Terminal-Quelle eingehängt"}
	}
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return nil, fmt.Errorf("SessionID fehlt")
	}
	switch method {
	case "OpenTerm":
		if err := b.termSrc.Resize(id, cols, rows); err != nil {
			return nil, err
		}
		return map[string]bool{"attached": true}, nil
	case "WriteTerm":
		data, err := base64.StdEncoding.DecodeString(dataB64)
		if err != nil || len(data) == 0 {
			return nil, fmt.Errorf("Eingabe unlesbar")
		}
		return nil, b.termSrc.Write(id, data)
	case "ResizeTerm":
		return nil, b.termSrc.Resize(id, cols, rows)
	default:
		return map[string]bool{"closed": true}, nil
	}
}

// Observe bedient Status-Events: derselbe Snapshot, den lokale Leser sehen,
// mit Transport-Herkunft markiert.
func (b *CoreBackend) Observe(ctx context.Context) core.ObservationSnapshot {
	st, err := b.loadState()
	if err != nil {
		return core.ObservationSnapshot{
			Availability: core.ObservationUnavailable,
			Transport:    core.ObservationTransportRemote,
			Problems: []core.ObservationProblem{
				{Operation: "load-state", Message: err.Error()},
			},
		}
	}
	return b.freshObserve(ctx, st.Agents)
}

// Subscribe öffnet einen Terminal-Stream; die Ringpuffer- und
// Reconnect-Semantik lebt in stream.go.
func (b *CoreBackend) Subscribe(sessionID string, fromSeq uint64) (StreamSubscription, error) {
	if b.termSrc != nil {
		source := b.termSrc
		b.events.ensureFeed(sessionID, source, func() ([]byte, error) {
			return source.Snapshot(sessionID)
		})
	}
	return b.events.subscribe(sessionID, fromSeq)
}
