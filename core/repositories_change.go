package core

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

type ManagedWorktreeChangeKind string

const (
	ManagedWorktreeCreate ManagedWorktreeChangeKind = "create"
	ManagedWorktreeRemove ManagedWorktreeChangeKind = "remove"
)

type ManagedWorktreeChange struct {
	Kind    ManagedWorktreeChangeKind `json:"kind"`
	Project Project                   `json:"project"`
	Name    string                    `json:"name,omitempty"`
	Path    string                    `json:"path,omitempty"`
}

func CreateManagedWorktreeChange(project Project, name string) ManagedWorktreeChange {
	return ManagedWorktreeChange{Kind: ManagedWorktreeCreate, Project: project, Name: name}
}

func RemoveManagedWorktreeChange(project Project, path string) ManagedWorktreeChange {
	return ManagedWorktreeChange{Kind: ManagedWorktreeRemove, Project: project, Path: path}
}

type ManagedWorktreeChangeResult struct {
	Kind           ManagedWorktreeChangeKind `json:"kind"`
	Project        string                    `json:"project"`
	Path           string                    `json:"path"`
	Branch         string                    `json:"branch,omitempty"`
	State          RepositoryKnowledge       `json:"state"`
	Changed        bool                      `json:"changed"`
	Problem        *RepositoryProblem        `json:"problem,omitempty"`
	MayHaveApplied bool                      `json:"mayHaveApplied"`
}

type ManagedWorktreeChangeErrorKind string

const (
	ManagedWorktreeInvalid       ManagedWorktreeChangeErrorKind = "invalid"
	ManagedWorktreeNotRepository ManagedWorktreeChangeErrorKind = "not_repository"
	ManagedWorktreeUnavailable   ManagedWorktreeChangeErrorKind = "unavailable"
	ManagedWorktreeConflict      ManagedWorktreeChangeErrorKind = "conflict"
	ManagedWorktreeMain          ManagedWorktreeChangeErrorKind = "main_worktree"
	ManagedWorktreeDirty         ManagedWorktreeChangeErrorKind = "dirty"
	ManagedWorktreeLocked        ManagedWorktreeChangeErrorKind = "locked"
	ManagedWorktreePostcondition ManagedWorktreeChangeErrorKind = "postcondition_unknown"
)

type ManagedWorktreeChangeError struct {
	Kind           ManagedWorktreeChangeErrorKind `json:"kind"`
	Operation      string                         `json:"operation"`
	Message        string                         `json:"message"`
	MayHaveApplied bool                           `json:"mayHaveApplied"`
}

func (e *ManagedWorktreeChangeError) Error() string {
	if e.Operation == "" {
		return e.Message
	}
	return e.Operation + ": " + e.Message
}

// Change is the sole mutation entry point for managed Worktrees. Preconditions
// and postconditions are always read afresh; Survey knowledge is never used.
func (r *Repositories) Change(ctx context.Context, change ManagedWorktreeChange) (ManagedWorktreeChangeResult, error) {
	r.changeMu.Lock()
	defer r.changeMu.Unlock()

	result := ManagedWorktreeChangeResult{Kind: change.Kind, Project: change.Project.Name, State: RepositoryUnknown}
	if strings.TrimSpace(change.Project.Path) == "" {
		return result, managedWorktreeError(ManagedWorktreeInvalid, "validate", "Project path is required", false)
	}
	switch change.Kind {
	case ManagedWorktreeCreate:
		return r.createManagedWorktree(ctx, change, result)
	case ManagedWorktreeRemove:
		return r.removeManagedWorktree(ctx, change, result)
	default:
		return result, managedWorktreeError(ManagedWorktreeInvalid, "validate", "unknown managed Worktree change", false)
	}
}

func (r *Repositories) createManagedWorktree(ctx context.Context, change ManagedWorktreeChange, result ManagedWorktreeChangeResult) (ManagedWorktreeChangeResult, error) {
	name := strings.TrimSpace(change.Name)
	if !validManagedWorktreeName(name) {
		return result, managedWorktreeError(ManagedWorktreeInvalid, "validate", fmt.Sprintf("invalid managed Worktree name %q", change.Name), false)
	}
	projectPath := filepath.Clean(change.Project.Path)
	target := filepath.Join(filepath.Dir(projectPath), filepath.Base(projectPath)+"-agents", name)
	branch := "agent/" + name
	result.Path, result.Branch = target, branch

	topology, err := r.loadTopology(ctx, projectPath)
	if err != nil {
		if ctxErr := repositoryContextError(ctx, err); ctxErr != nil {
			return result, ctxErr
		}
		return managedWorktreeFailureResult(result, "worktree_topology", err, false)
	}
	result.State = RepositoryKnown
	if existing, ok := topologyByPath(topology, target); ok {
		if existing.Prunable {
			return result, managedWorktreeError(ManagedWorktreeConflict, "create", "target has a stale Worktree registration", false)
		}
		if existing.Branch == branch {
			result.State = RepositoryKnown
			return result, nil
		}
		return result, managedWorktreeError(ManagedWorktreeConflict, "create", fmt.Sprintf("target path already belongs to branch %q", existing.Branch), false)
	}
	if existing, ok := topologyByBranch(topology, branch); ok {
		return result, managedWorktreeError(ManagedWorktreeConflict, "create", fmt.Sprintf("branch %q is already checked out at %s", branch, existing.Path), false)
	}

	_, firstErr := r.runner.Run(ctx, projectPath, "worktree", "add", "-b", branch, target)
	if firstErr == nil {
		return r.verifyManagedWorktreeCreated(ctx, result, projectPath, target, branch)
	}
	if ctxErr := repositoryContextError(ctx, firstErr); ctxErr != nil {
		result.State = RepositoryUnknown
		result.MayHaveApplied = true
		result.Problem = repositoryProblem("create_postcondition", ctxErr)
		return result, ctxErr
	}
	after, refreshErr := r.loadTopology(ctx, projectPath)
	if refreshErr != nil {
		return managedWorktreePostconditionFailure(result, "create_postcondition", refreshErr)
	}
	if existing, ok := topologyByPath(after, target); ok {
		if existing.Branch == branch {
			result.State, result.Changed = RepositoryKnown, true
			return result, nil
		}
		return result, managedWorktreeError(ManagedWorktreeConflict, "create", fmt.Sprintf("target path now belongs to branch %q", existing.Branch), false)
	}
	if existing, ok := topologyByBranch(after, branch); ok {
		return result, managedWorktreeError(ManagedWorktreeConflict, "create", fmt.Sprintf("branch %q is now checked out at %s", branch, existing.Path), false)
	}

	_, secondErr := r.runner.Run(ctx, projectPath, "worktree", "add", target, branch)
	if secondErr == nil {
		return r.verifyManagedWorktreeCreated(ctx, result, projectPath, target, branch)
	}
	if ctxErr := repositoryContextError(ctx, secondErr); ctxErr != nil {
		result.State = RepositoryUnknown
		result.MayHaveApplied = true
		result.Problem = repositoryProblem("create_postcondition", ctxErr)
		return result, ctxErr
	}
	after, refreshErr = r.loadTopology(ctx, projectPath)
	if refreshErr != nil {
		return managedWorktreePostconditionFailure(result, "create_postcondition", refreshErr)
	}
	if existing, ok := topologyByPath(after, target); ok && existing.Branch == branch {
		result.State, result.Changed = RepositoryKnown, true
		return result, nil
	}
	message := fmt.Sprintf("new branch failed: %v; existing branch failed: %v", firstErr, secondErr)
	result.State = RepositoryKnown
	result.Problem = repositoryProblem("create", errors.New(message))
	return result, managedWorktreeError(ManagedWorktreeUnavailable, "create", message, false)
}

func (r *Repositories) verifyManagedWorktreeCreated(ctx context.Context, result ManagedWorktreeChangeResult, projectPath, target, branch string) (ManagedWorktreeChangeResult, error) {
	topology, err := r.loadTopology(ctx, projectPath)
	if err != nil {
		return managedWorktreePostconditionFailure(result, "create_postcondition", err)
	}
	existing, ok := topologyByPath(topology, target)
	if !ok || existing.Branch != branch {
		message := "git reported success but the managed Worktree postcondition is absent"
		return managedWorktreePostconditionFailure(result, "create_postcondition", errors.New(message))
	}
	result.State, result.Changed = RepositoryKnown, true
	return result, nil
}

func (r *Repositories) removeManagedWorktree(ctx context.Context, change ManagedWorktreeChange, result ManagedWorktreeChangeResult) (ManagedWorktreeChangeResult, error) {
	projectPath := filepath.Clean(change.Project.Path)
	target := filepath.Clean(strings.TrimSpace(change.Path))
	result.Path = target
	if strings.TrimSpace(change.Path) == "" || target == "." {
		return result, managedWorktreeError(ManagedWorktreeInvalid, "validate", "managed Worktree path is required", false)
	}
	if sameRepositoryPath(projectPath, target) {
		return result, managedWorktreeError(ManagedWorktreeMain, "remove", "Project root Worktree cannot be removed", false)
	}
	if !isManagedWorktreePath(projectPath, target) {
		return result, managedWorktreeError(ManagedWorktreeInvalid, "remove", "path is outside the Project's managed Worktree directory", false)
	}

	topology, err := r.loadTopology(ctx, projectPath)
	if err != nil {
		if ctxErr := repositoryContextError(ctx, err); ctxErr != nil {
			return result, ctxErr
		}
		return managedWorktreeFailureResult(result, "worktree_topology", err, false)
	}
	result.State = RepositoryKnown
	existing, ok := topologyByPath(topology, target)
	if !ok {
		result.State = RepositoryKnown
		return result, nil
	}
	if !isManagedWorktreePath(projectPath, existing.Path) {
		return result, managedWorktreeError(ManagedWorktreeInvalid, "remove", "registered Worktree is outside the Project's managed Worktree directory", false)
	}
	result.Branch = existing.Branch
	if existing.Locked {
		return result, managedWorktreeError(ManagedWorktreeLocked, "remove", "managed Worktree is locked", false)
	}
	status, err := r.loadStatus(ctx, target)
	if err != nil {
		if ctxErr := repositoryContextError(ctx, err); ctxErr != nil {
			return result, ctxErr
		}
		return managedWorktreeFailureResult(result, "remove_precondition", err, false)
	}
	if !status.Changes.Clean() {
		return result, managedWorktreeError(ManagedWorktreeDirty, "remove_precondition", "managed Worktree has uncommitted changes", false)
	}

	_, removeErr := r.runner.Run(ctx, projectPath, "worktree", "remove", target)
	if removeErr != nil {
		if ctxErr := repositoryContextError(ctx, removeErr); ctxErr != nil {
			result.State = RepositoryUnknown
			result.MayHaveApplied = true
			result.Problem = repositoryProblem("remove_postcondition", ctxErr)
			return result, ctxErr
		}
		after, refreshErr := r.loadTopology(ctx, projectPath)
		if refreshErr != nil {
			return managedWorktreePostconditionFailure(result, "remove_postcondition", refreshErr)
		}
		if _, stillPresent := topologyByPath(after, target); !stillPresent {
			result.State, result.Changed = RepositoryKnown, true
			return result, nil
		}
		result.State = RepositoryKnown
		result.Problem = repositoryProblem("remove", removeErr)
		return result, managedWorktreeError(ManagedWorktreeUnavailable, "remove", removeErr.Error(), false)
	}

	after, err := r.loadTopology(ctx, projectPath)
	if err != nil {
		return managedWorktreePostconditionFailure(result, "remove_postcondition", err)
	}
	if _, stillPresent := topologyByPath(after, target); stillPresent {
		message := "git reported success but the managed Worktree is still registered"
		return managedWorktreePostconditionFailure(result, "remove_postcondition", errors.New(message))
	}
	result.State, result.Changed = RepositoryKnown, true
	return result, nil
}

func topologyByPath(topology []repositoriesTopologyWorktree, path string) (repositoriesTopologyWorktree, bool) {
	for _, wt := range topology {
		if sameRepositoryPath(wt.Path, path) {
			return wt, true
		}
	}
	return repositoriesTopologyWorktree{}, false
}

func topologyByBranch(topology []repositoriesTopologyWorktree, branch string) (repositoriesTopologyWorktree, bool) {
	for _, wt := range topology {
		if wt.Branch == branch {
			return wt, true
		}
	}
	return repositoriesTopologyWorktree{}, false
}

func validManagedWorktreeName(name string) bool {
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func isManagedWorktreePath(projectPath, target string) bool {
	root, rootErr := filepath.Abs(filepath.Join(filepath.Dir(projectPath), filepath.Base(projectPath)+"-agents"))
	path, pathErr := filepath.Abs(target)
	if rootErr != nil || pathErr != nil {
		return false
	}
	return filepath.Dir(filepath.Clean(path)) == filepath.Clean(root)
}

func managedWorktreeFailureResult(result ManagedWorktreeChangeResult, operation string, err error, mayApply bool) (ManagedWorktreeChangeResult, error) {
	result.State = RepositoryUnknown
	result.MayHaveApplied = mayApply
	result.Problem = repositoryProblem(operation, err)
	kind := ManagedWorktreeUnavailable
	if errors.Is(err, errRepositoriesNotRepository) {
		result.State = RepositoryNotRepository
		kind = ManagedWorktreeNotRepository
	}
	return result, managedWorktreeError(kind, operation, err.Error(), mayApply)
}

func managedWorktreePostconditionFailure(result ManagedWorktreeChangeResult, operation string, err error) (ManagedWorktreeChangeResult, error) {
	result.State = RepositoryUnknown
	result.MayHaveApplied = true
	result.Problem = repositoryProblem(operation, err)
	return result, managedWorktreeError(ManagedWorktreePostcondition, operation, err.Error(), true)
}

func managedWorktreeError(kind ManagedWorktreeChangeErrorKind, operation, message string, mayApply bool) error {
	return &ManagedWorktreeChangeError{Kind: kind, Operation: operation, Message: message, MayHaveApplied: mayApply}
}
