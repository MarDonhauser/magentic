//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris && !windows

package core

import (
	"context"
)

// Platforms without advisory file locks use the same atomic-directory
// protocol as Windows. The directory is only coordination; Registry data is
// still committed by fsync + atomic replacement.
func withRegistryFileLock(ctx context.Context, statePath string, fn func() error) error {
	return withPortableDirectoryLock(ctx, statePath+".lockdir", fn)
}
