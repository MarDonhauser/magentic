package core

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
)

// worktreeTransitionCoordinator serializes every transition that can make a
// managed Worktree newly occupied with the transition that can destroy it.
// The lock is process-coordinated and keyed by canonical Worktree identity,
// not by a SessionID: different Sessions in the same Worktree must contend.
type worktreeTransitionCoordinator struct {
	root string
}

func newWorktreeTransitionCoordinator(registryPath string) worktreeTransitionCoordinator {
	return worktreeTransitionCoordinator{root: filepath.Dir(registryPath)}
}

func (c worktreeTransitionCoordinator) with(ctx context.Context, project Project, target string, fn func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	canonical := canonicalWorktreeTransitionPath(target)
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
