package core

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// worktreeTransitionCoordinator serializes every transition that can make a
// managed Worktree newly occupied with the transition that can destroy it.
// The lock is process-coordinated and keyed by canonical Worktree identity,
// not by a SessionID: different Sessions in the same Worktree must contend.
type worktreeTransitionCoordinator struct {
	root          string
	beforeAcquire func(Project, string)
}

type projectTransitionCoordinator struct {
	root          string
	beforeAcquire func(ProjectID)
}

func newWorktreeTransitionCoordinator(registryPath string) worktreeTransitionCoordinator {
	return worktreeTransitionCoordinator{root: filepath.Dir(registryPath)}
}

func newProjectTransitionCoordinator(registryPath string) projectTransitionCoordinator {
	return projectTransitionCoordinator{root: filepath.Dir(registryPath)}
}

func (c projectTransitionCoordinator) with(ctx context.Context, projectID ProjectID, fn func() error) error {
	if projectID == "" {
		return fn()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if c.beforeAcquire != nil {
		c.beforeAcquire(projectID)
	}
	digest := sha256.Sum256([]byte(projectID))
	lockPath := filepath.Join(c.root, ".project-transition-locks", fmt.Sprintf("%x", digest[:]))
	return withRegistryFileLock(ctx, lockPath, fn)
}

func (c worktreeTransitionCoordinator) with(ctx context.Context, project Project, target string, fn func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	canonical := canonicalWorktreeTransitionPath(target)
	if c.beforeAcquire != nil {
		c.beforeAcquire(project, canonical)
	}
	identity := strings.TrimSpace(string(project.ID))
	if identity == "" {
		identity = canonicalWorktreeTransitionPath(project.Path)
	}
	digest := sha256.Sum256([]byte(identity + "\x00" + canonical))
	lockPath := filepath.Join(c.root, ".worktree-transition-locks", fmt.Sprintf("%x", digest[:]))
	return withRegistryFileLock(ctx, lockPath, fn)
}

// canonicalWorktreeTransitionPath resolves the longest existing ancestor, then
// reattaches missing path elements. Provisioning therefore derives the same
// identity before a Worktree exists that removal derives after Git created it,
// including when an ancestor is reached through a symlink.
func canonicalWorktreeTransitionPath(path string) string {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return filepath.Clean(path)
	}
	probe := absolute
	var missing []string
	for {
		if resolved, resolveErr := filepath.EvalSymlinks(probe); resolveErr == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return absolute
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent
	}
}

func managedWorktreeTarget(project Project, name string) (string, bool) {
	if strings.TrimSpace(project.Path) == "" || !validManagedWorktreeName(name) {
		return "", false
	}
	root := filepath.Join(filepath.Dir(project.Path), filepath.Base(project.Path)+"-agents")
	return canonicalWorktreeTransitionPath(filepath.Join(root, name)), true
}

// managedWorktreeForDirectory maps a Session directory (which may be nested
// below the checkout root) to the one managed Worktree identity it occupies.
func managedWorktreeForDirectory(project Project, directory string) (string, bool) {
	if strings.TrimSpace(project.Path) == "" || strings.TrimSpace(directory) == "" {
		return "", false
	}
	root := canonicalWorktreeTransitionPath(filepath.Join(
		filepath.Dir(project.Path), filepath.Base(project.Path)+"-agents",
	))
	directory = canonicalWorktreeTransitionPath(directory)
	relative, err := filepath.Rel(root, directory)
	if err != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) == 0 || parts[0] == "" || parts[0] == "." || parts[0] == ".." {
		return "", false
	}
	return filepath.Join(root, parts[0]), true
}

func sessionBelongsToWorktree(session Session, target string) bool {
	if strings.TrimSpace(session.Dir) == "" || strings.TrimSpace(target) == "" {
		return false
	}
	directory := canonicalWorktreeTransitionPath(session.Dir)
	target = canonicalWorktreeTransitionPath(target)
	relative, err := filepath.Rel(target, directory)
	return err == nil && !filepath.IsAbs(relative) && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

type discoveredWorktreeTransition struct {
	project Project
	target  string
	key     string
}

// AdoptDiscoveredSessions is the only production adoption entry point. It
// holds Project locks first and canonical Worktree locks second, in stable
// order, through the semantic Registry commit. Removal therefore sees either
// the complete adopted Session or no adoption at all.
func (r *Registry) AdoptDiscoveredSessions(ctx context.Context, sessions []Session) (RegistryChangeResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(sessions) == 0 {
		snapshot, err := r.Snapshot(ctx)
		return RegistryChangeResult{Revision: snapshot.Revision(), Snapshot: snapshot}, err
	}
	snapshot, err := r.Snapshot(ctx)
	if err != nil {
		return RegistryChangeResult{}, err
	}
	state := snapshot.State()
	projectIDs, worktrees, err := discoveredTransitionLocks(state, sessions)
	if err != nil {
		return RegistryChangeResult{}, err
	}
	projects := newProjectTransitionCoordinator(r.path)
	worktreeCoordinator := newWorktreeTransitionCoordinator(r.path)
	var result RegistryChangeResult
	commit := func() error {
		freshSnapshot, snapshotErr := r.Snapshot(ctx)
		if snapshotErr != nil {
			return snapshotErr
		}
		freshState := freshSnapshot.State()
		freshProjectIDs, freshWorktrees, lockErr := discoveredTransitionLocks(freshState, sessions)
		if lockErr != nil {
			return lockErr
		}
		if !equalProjectIDSlice(projectIDs, freshProjectIDs) || !equalDiscoveredWorktreeTransitions(worktrees, freshWorktrees) {
			return errors.New("Project or Worktree identity changed during Session adoption")
		}
		for _, session := range sessions {
			info, statErr := os.Stat(session.Dir)
			if statErr != nil || !info.IsDir() {
				if statErr != nil {
					return fmt.Errorf("adopt Session %q directory: %w", session.Name, statErr)
				}
				return fmt.Errorf("adopt Session %q: directory is not a directory", session.Name)
			}
		}
		result, err = r.Change(ctx, addDiscoveredSessionsChange(sessions))
		return err
	}
	commitWorktrees := func(index int) func() error {
		var acquire func(int) error
		acquire = func(i int) error {
			if i == len(worktrees) {
				return commit()
			}
			entry := worktrees[i]
			return worktreeCoordinator.with(ctx, entry.project, entry.target, func() error { return acquire(i + 1) })
		}
		return func() error { return acquire(index) }
	}
	var acquireProjects func(int) error
	acquireProjects = func(index int) error {
		if index == len(projectIDs) {
			return commitWorktrees(0)()
		}
		return projects.with(ctx, projectIDs[index], func() error { return acquireProjects(index + 1) })
	}
	if err := acquireProjects(0); err != nil {
		return RegistryChangeResult{}, err
	}
	return result, nil
}

func discoveredTransitionLocks(state State, sessions []Session) ([]ProjectID, []discoveredWorktreeTransition, error) {
	projectSet := make(map[ProjectID]bool)
	worktreeByKey := make(map[string]discoveredWorktreeTransition)
	for _, session := range sessions {
		if session.ProjectID == "" {
			continue
		}
		project := state.ProjectByID(session.ProjectID)
		if project == nil {
			return nil, nil, fmt.Errorf("ProjectID %q not found", session.ProjectID)
		}
		projectSet[project.ID] = true
		if target, managed := managedWorktreeForDirectory(*project, session.Dir); managed {
			key := string(project.ID) + "\x00" + canonicalWorktreeTransitionPath(target)
			worktreeByKey[key] = discoveredWorktreeTransition{project: *project, target: target, key: key}
		}
	}
	projectIDs := make([]ProjectID, 0, len(projectSet))
	for id := range projectSet {
		projectIDs = append(projectIDs, id)
	}
	sort.Slice(projectIDs, func(i, j int) bool { return projectIDs[i] < projectIDs[j] })
	worktrees := make([]discoveredWorktreeTransition, 0, len(worktreeByKey))
	for _, entry := range worktreeByKey {
		worktrees = append(worktrees, entry)
	}
	sort.Slice(worktrees, func(i, j int) bool { return worktrees[i].key < worktrees[j].key })
	return projectIDs, worktrees, nil
}

func equalProjectIDSlice(left, right []ProjectID) bool {
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

func equalDiscoveredWorktreeTransitions(left, right []discoveredWorktreeTransition) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].key != right[i].key {
			return false
		}
	}
	return true
}
