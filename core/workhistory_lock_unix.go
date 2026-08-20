//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

func withWorkHistoryFileLock(ctx context.Context, path string, fn func() error) error {
	lock, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open work history lock: %w", err)
	}
	defer lock.Close()
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect work history lock: %w", err)
	}
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("lock work history index: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck
	return fn()
}
