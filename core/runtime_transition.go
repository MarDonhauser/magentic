package core

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sort"
)

// runtimeTransitionCoordinator serializes every Lifecycle transition that can
// claim, release, or rename an external RuntimeName. The identity is used
// exactly as stored; hashing must never hide normalization at this Seam.
//
// Callers acquire Project, Worktree, and Session locks before RuntimeName
// locks. Multi-name transitions (rename) sort their runtime identities so two
// concurrent renames cannot deadlock each other.
type runtimeTransitionCoordinator struct {
	root          string
	beforeAcquire func(string)
}

func newRuntimeTransitionCoordinator(registryPath string) runtimeTransitionCoordinator {
	return runtimeTransitionCoordinator{root: filepath.Dir(registryPath)}
}

func (c runtimeTransitionCoordinator) with(ctx context.Context, runtimeNames []string, fn func() error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	names := append([]string(nil), runtimeNames...)
	sort.Strings(names)
	unique := names[:0]
	for _, name := range names {
		if len(unique) == 0 || unique[len(unique)-1] != name {
			unique = append(unique, name)
		}
	}
	var acquire func(int) error
	acquire = func(index int) error {
		if index == len(unique) {
			return fn()
		}
		if c.beforeAcquire != nil {
			c.beforeAcquire(unique[index])
		}
		digest := sha256.Sum256([]byte(unique[index]))
		lockPath := filepath.Join(c.root, ".runtime-transition-locks", fmt.Sprintf("%x", digest[:]))
		return withRegistryFileLock(ctx, lockPath, func() error { return acquire(index + 1) })
	}
	return acquire(0)
}
