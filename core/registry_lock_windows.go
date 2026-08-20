//go:build windows

package core

import (
	"context"
	"os"
	"path/filepath"
	"time"
)

// Windows uses an atomic lock directory. A stale lock older than a minute is
// recoverable; Registry commits themselves remain atomic replacements.
func withRegistryFileLock(ctx context.Context, statePath string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		return err
	}
	lockPath := statePath + ".lockdir"
	for {
		err := os.Mkdir(lockPath, 0o700)
		if err == nil {
			defer os.Remove(lockPath)
			return fn()
		}
		if info, statErr := os.Stat(lockPath); statErr == nil && time.Since(info.ModTime()) > time.Minute {
			_ = os.Remove(lockPath)
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
