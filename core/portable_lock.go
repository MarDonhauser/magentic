package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	portableLockStaleAfter = time.Minute
	portableLockHeartbeat  = 10 * time.Second
	portableLockRetry      = 10 * time.Millisecond
)

type portableDirectoryLockConfig struct {
	staleAfter time.Duration
	heartbeat  time.Duration
	retry      time.Duration
}

func (c portableDirectoryLockConfig) normalized() portableDirectoryLockConfig {
	if c.staleAfter <= 0 {
		c.staleAfter = portableLockStaleAfter
	}
	if c.heartbeat <= 0 {
		c.heartbeat = portableLockHeartbeat
	}
	if c.heartbeat >= c.staleAfter {
		c.heartbeat = c.staleAfter / 3
		if c.heartbeat <= 0 {
			c.heartbeat = time.Millisecond
		}
	}
	if c.retry <= 0 {
		c.retry = portableLockRetry
	}
	return c
}

// withPortableDirectoryLock is the fallback for platforms where this package
// has no native advisory-lock Adapter. The owner nonce prevents an old holder
// from deleting a successor's lock; the heartbeat distinguishes a long live
// operation from an abandoned directory.
func withPortableDirectoryLock(ctx context.Context, lockDir string, fn func() error) error {
	return withPortableDirectoryLockConfig(ctx, lockDir, portableDirectoryLockConfig{}, fn)
}

func withPortableDirectoryLockConfig(ctx context.Context, lockDir string, config portableDirectoryLockConfig, fn func() error) error {
	config = config.normalized()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := os.MkdirAll(filepath.Dir(lockDir), 0o700); err != nil {
		return err
	}
	owner := filepath.Join(lockDir, "owner")
	nonce := NewUUID()
	for {
		err := os.Mkdir(lockDir, 0o700)
		if err == nil {
			if writeErr := os.WriteFile(owner, []byte(nonce+"\n"), 0o600); writeErr != nil {
				_ = os.Remove(lockDir)
				return fmt.Errorf("write lock owner: %w", writeErr)
			}
			stop := make(chan struct{})
			done := make(chan struct{})
			go heartbeatPortableDirectoryLock(owner, nonce, config.heartbeat, stop, done)
			defer func() {
				close(stop)
				<-done
				releasePortableDirectoryLock(lockDir, owner, nonce)
			}()
			return fn()
		}
		if !os.IsExist(err) {
			return fmt.Errorf("create lock directory: %w", err)
		}
		if nonce, stale := portableDirectoryLockOwnerIfStale(lockDir, owner, config.staleAfter); stale {
			_ = recoverPortableDirectoryLock(lockDir, owner, nonce, config.staleAfter)
			continue
		}
		timer := time.NewTimer(config.retry)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func heartbeatPortableDirectoryLock(owner, nonce string, interval time.Duration, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			if !portableLockOwnedBy(owner, nonce) {
				return
			}
			_ = os.Chtimes(owner, now, now)
		}
	}
}

func portableDirectoryLockOwnerIfStale(lockDir, owner string, staleAfter time.Duration) (string, bool) {
	data, err := os.ReadFile(owner)
	if err != nil {
		if !os.IsNotExist(err) {
			return "", false
		}
		info, statErr := os.Stat(lockDir)
		return "", statErr == nil && time.Since(info.ModTime()) > staleAfter
	}
	nonce := strings.TrimSpace(string(data))
	if nonce == "" {
		return "", false
	}
	info, err := os.Stat(owner)
	return nonce, err == nil && time.Since(info.ModTime()) > staleAfter
}

func recoverPortableDirectoryLock(lockDir, owner, expectedNonce string, staleAfter time.Duration) error {
	data, err := os.ReadFile(owner)
	if err != nil {
		if !os.IsNotExist(err) || expectedNonce != "" {
			return err
		}
		info, statErr := os.Stat(lockDir)
		if statErr != nil || time.Since(info.ModTime()) <= staleAfter {
			return errors.New("ownerless lock is not stale")
		}
	} else {
		nonce := strings.TrimSpace(string(data))
		if nonce == "" || nonce != expectedNonce || !portableLockOwnedBy(owner, nonce) {
			return errors.New("lock owner changed")
		}
		info, statErr := os.Stat(owner)
		if statErr != nil || time.Since(info.ModTime()) <= staleAfter {
			return errors.New("lock owner heartbeat resumed")
		}
	}
	tombstone := lockDir + ".abandoned-" + NewUUID()
	if err := os.Rename(lockDir, tombstone); err != nil {
		return err
	}
	return os.RemoveAll(tombstone)
}

func releasePortableDirectoryLock(lockDir, owner, nonce string) {
	if !portableLockOwnedBy(owner, nonce) {
		return
	}
	_ = os.Remove(owner)
	_ = os.Remove(lockDir)
}

func portableLockOwnedBy(owner, nonce string) bool {
	data, err := os.ReadFile(owner)
	return err == nil && strings.TrimSpace(string(data)) == nonce
}
