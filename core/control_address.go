package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// controlError is a resolved refusal: the stable outcome code, the German
// message a person reads, and — for an ambiguous address — the candidates the
// caller has to choose between.
type controlError struct {
	Outcome    ControlOutcome
	Message    string
	Candidates []ControlSessionView
}

func (e *controlError) response(id string) ControlResponse {
	response := controlFailure(id, e.Outcome, e.Message)
	if len(e.Candidates) > 0 {
		response.Result = &ControlResult{Candidates: e.Candidates}
	}
	return response
}

func controlRefusal(outcome ControlOutcome, format string, args ...any) *controlError {
	return &controlError{Outcome: outcome, Message: fmt.Sprintf(format, args...)}
}

// resolveControlProject turns a caller's Project reference — a ProjectID or a
// Project name — into exactly one registered Project.
func resolveControlProject(state State, reference string) (Project, *controlError) {
	reference = strings.TrimSpace(reference)
	if reference == "" {
		return Project{}, controlRefusal(ControlNoTarget, "Es wurde kein Projekt genannt.")
	}
	if project := state.ProjectByID(ProjectID(reference)); project != nil {
		return *project, nil
	}
	if project := state.ProjectByName(reference); project != nil {
		return *project, nil
	}
	return Project{}, controlRefusal(ControlNotFound, "Projekt %q ist nicht registriert.", reference)
}

// resolveControlSession resolves an address to exactly one registered Session
// before any action is taken. A name is a label and never lookup authority
// beyond this step (ADR 0001): an ambiguous bare name is refused rather than
// resolved by guessing, and a Project qualifier narrows but never widens.
func resolveControlSession(state State, args ControlArgs) (Session, *controlError) {
	address := strings.TrimSpace(args.Session)
	if address == "" {
		return Session{}, controlRefusal(ControlNoTarget,
			"Es wurde keine Session adressiert — die eigene Session wird nicht eingesetzt.")
	}
	var scope *Project
	if strings.TrimSpace(args.Project) != "" {
		project, failure := resolveControlProject(state, args.Project)
		if failure != nil {
			return Session{}, failure
		}
		scope = &project
	}
	if session := state.SessionByID(SessionID(address)); session != nil {
		if scope != nil && !controlSessionInProject(*session, *scope) {
			return Session{}, controlRefusal(ControlNotFound,
				"Session %s gehört nicht zu Projekt %q.", address, scope.Name)
		}
		return *session, nil
	}

	var matches []Session
	for _, session := range state.Agents {
		if session.Name != address {
			continue
		}
		if scope != nil && !controlSessionInProject(session, *scope) {
			continue
		}
		matches = append(matches, session)
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		if scope != nil {
			return Session{}, controlRefusal(ControlNotFound,
				"In Projekt %q gibt es keine Session %q.", scope.Name, address)
		}
		return Session{}, controlRefusal(ControlNotFound, "Session %q ist nicht registriert.", address)
	}
	failure := controlRefusal(ControlAmbiguous,
		"Der Name %q wird in mehreren Projekten geführt — bitte mit Projekt oder SessionID adressieren.", address)
	failure.Candidates = controlSessionViews(state, matches)
	return Session{}, failure
}

func controlSessionInProject(session Session, project Project) bool {
	if session.ProjectID != "" && project.ID != "" {
		return session.ProjectID == project.ID
	}
	return session.Project != "" && session.Project == project.Name
}

// controlSessionView projects a registered Session without any Observation. The
// status stays empty until an Observation fills it in.
func controlSessionView(session Session) ControlSessionView {
	kind := session.SessionKind
	if kind == "" {
		if session.IsTerm() {
			kind = SessionKindTerminal
		} else {
			kind = SessionKindCodingAgent
		}
	}
	return ControlSessionView{
		SessionID: session.ID, Name: session.Name,
		ProjectID: session.ProjectID, Project: session.Project,
		RuntimeName: session.RuntimeName, Dir: session.Dir, Worktree: session.Worktree,
		Kind: kind, Vendor: session.SessionVendor(),
	}
}

func controlSessionViews(state State, sessions []Session) []ControlSessionView {
	views := make([]ControlSessionView, 0, len(sessions))
	for _, session := range sessions {
		view := controlSessionView(session)
		if view.Project == "" && view.ProjectID != "" {
			if project := state.ProjectByID(view.ProjectID); project != nil {
				view.Project = project.Name
			}
		}
		views = append(views, view)
	}
	sort.Slice(views, func(i, j int) bool { return views[i].SessionID < views[j].SessionID })
	return views
}

// controlRepositories is the Repositories seam the control surface uses. It is
// narrowed to the two calls that resolve a Worktree freshly.
type controlRepositories interface {
	ResolveWorktree(context.Context, Project, WorktreeRef) (RepositoryWorktreeTarget, error)
	Survey(context.Context, []Project) (RepositoriesSurvey, error)
}

// controlWorktreeScope is the resolved place of work for a start request.
type controlWorktreeScope struct {
	// Directory is empty when Lifecycle is asked to provision a fresh managed
	// Worktree; Create then carries that intent.
	Directory string
	Reference WorktreeRef
	Create    bool
}

// resolveControlWorktree turns a start request's Worktree scope into a place of
// work. A Project-qualified handle is resolved by Repositories immediately
// before use; a caller-supplied directory is never taken on trust but checked
// against the Project's fresh checkout topology.
func resolveControlWorktree(
	ctx context.Context, repositories controlRepositories, project Project, args ControlArgs,
) (controlWorktreeScope, *controlError) {
	switch {
	case args.NewWorktree:
		return controlWorktreeScope{Create: true}, nil
	case strings.TrimSpace(args.Worktree) != "":
		target, err := repositories.ResolveWorktree(ctx, project, WorktreeRef(strings.TrimSpace(args.Worktree)))
		if err != nil {
			return controlWorktreeScope{}, controlRefusal(ControlContainment,
				"Worktree %q gehört nicht zu Projekt %q: %v", args.Worktree, project.Name, err)
		}
		return controlWorktreeScope{Directory: target.Worktree.Path, Reference: target.Worktree.Reference}, nil
	case strings.TrimSpace(args.Directory) != "":
		return controlWorktreeForDirectory(ctx, repositories, project, args.Directory)
	}
	return controlWorktreeScope{Directory: project.Path}, nil
}

// controlWorktreeForDirectory admits a directory only when the Project's fresh
// Survey lists it as one of that Project's checkouts.
func controlWorktreeForDirectory(
	ctx context.Context, repositories controlRepositories, project Project, directory string,
) (controlWorktreeScope, *controlError) {
	survey, err := repositories.Survey(ctx, []Project{project})
	if err != nil {
		return controlWorktreeScope{}, controlRefusal(ControlUnavailable,
			"Die Checkout-Topologie von %q ist nicht lesbar: %v", project.Name, err)
	}
	if len(survey.Projects) != 1 || !survey.Projects[0].Worktrees.Known() {
		return controlWorktreeScope{}, controlRefusal(ControlUnavailable,
			"Die Checkout-Topologie von %q ist nicht lesbar.", project.Name)
	}
	for _, worktree := range survey.Projects[0].Worktrees.Value {
		if sameRepositoryPath(worktree.Path, directory) {
			return controlWorktreeScope{Directory: worktree.Path, Reference: worktree.Reference}, nil
		}
	}
	return controlWorktreeScope{}, controlRefusal(ControlContainment,
		"%s liegt nicht in einem Worktree von Projekt %q.", directory, project.Name)
}
