//go:build windows || (!aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris)

package core

import (
	"context"
)

// Platforms without flock use an atomic local lock directory. Index writes
// are atomic replacements, and a lock left by a crashed process is recoverable.
func withWorkHistoryFileLock(ctx context.Context, path string, fn func() error) error {
	return withPortableDirectoryLock(ctx, path+".dir", fn)
}
