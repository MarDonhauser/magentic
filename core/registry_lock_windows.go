//go:build windows

package core

import (
	"context"
)

// Windows uses the owner/heartbeat portable lock Adapter. Registry commits
// themselves remain atomic replacements.
func withRegistryFileLock(ctx context.Context, statePath string, fn func() error) error {
	return withPortableDirectoryLock(ctx, statePath+".lockdir", fn)
}
