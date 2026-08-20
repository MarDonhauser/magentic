package core

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
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
			if writeErr := writePortableDirectoryLockOwner(owner, nonce); writeErr != nil {
				// This process created the unpublished lock directory. Removing the
				// exact directory also clears a temporary owner file left by a short
				// write; no successor can own it yet.
				_ = os.RemoveAll(lockDir)
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

func writePortableDirectoryLockOwner(owner, nonce string) error {
	temporary := filepath.Join(filepath.Dir(owner), ".owner-"+nonce+".tmp")
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	data := []byte(nonce + "\n")
	written, writeErr := file.Write(data)
	if writeErr == nil && written != len(data) {
		writeErr = io.ErrShortWrite
	}
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if closeErr := file.Close(); writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = os.Rename(temporary, owner)
	}
	if writeErr != nil {
		_ = os.Remove(temporary)
	}
	return writeErr
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
	nonce, valid := parsePortableDirectoryLockOwner(data)
	info, err := os.Stat(owner)
	if err != nil || time.Since(info.ModTime()) <= staleAfter {
		return "", false
	}
	if !valid {
		return "", true
	}
	return nonce, true
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
		nonce, valid := parsePortableDirectoryLockOwner(data)
		info, statErr := os.Stat(owner)
		if statErr != nil || time.Since(info.ModTime()) <= staleAfter {
			return errors.New("lock owner heartbeat resumed")
		}
		if valid {
			if expectedNonce == "" || nonce != expectedNonce || !portableLockOwnedBy(owner, nonce) {
				return errors.New("lock owner changed")
			}
		} else if expectedNonce != "" {
			return errors.New("lock owner changed")
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
	actual, valid := parsePortableDirectoryLockOwner(data)
	return err == nil && valid && actual == nonce
}

func parsePortableDirectoryLockOwner(data []byte) (string, bool) {
	if len(data) != 37 || data[36] != '\n' {
		return "", false
	}
	nonce := string(data[:36])
	if nonce[8] != '-' || nonce[13] != '-' || nonce[18] != '-' || nonce[23] != '-' {
		return "", false
	}
	hexNonce := strings.ReplaceAll(nonce, "-", "")
	if len(hexNonce) != 32 {
		return "", false
	}
	if _, err := hex.DecodeString(hexNonce); err != nil {
		return "", false
	}
	return nonce, true
}
