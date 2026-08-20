//go:build windows || (!aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris)

package core

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Platforms without flock use an atomic local lock directory. Index writes
// are atomic replacements, and a lock left by a crashed process is recoverable.
func withWorkHistoryFileLock(ctx context.Context, path string, fn func() error) error {
	lockDir := path + ".dir"
	for {
		err := os.Mkdir(lockDir, 0o700)
		if err == nil {
			defer os.Remove(lockDir) //nolint:errcheck
			return fn()
		}
		if !os.IsExist(err) {
			return fmt.Errorf("lock work history index: %w", err)
		}
		if info, statErr := os.Stat(lockDir); statErr == nil && time.Since(info.ModTime()) > time.Minute {
			_ = os.Remove(lockDir)
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}
