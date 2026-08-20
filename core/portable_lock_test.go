package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPortableDirectoryLockHeartbeatProtectsLongActiveOwner(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "state.lockdir")
	config := portableDirectoryLockConfig{
		staleAfter: 60 * time.Millisecond,
		heartbeat:  10 * time.Millisecond,
		retry:      2 * time.Millisecond,
	}
	acquired := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- withPortableDirectoryLockConfig(context.Background(), lockDir, config, func() error {
			close(acquired)
			<-release
			return nil
		})
	}()
	<-acquired

	ctx, cancel := context.WithTimeout(context.Background(), 140*time.Millisecond)
	defer cancel()
	err := withPortableDirectoryLockConfig(ctx, lockDir, config, func() error {
		return errors.New("live lock was stolen")
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("long active lock was not protected: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("owner failed: %v", err)
	}
}

func TestPortableDirectoryLockRecoversAbandonedOwner(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "history.lockdir")
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	owner := filepath.Join(lockDir, "owner")
	if err := os.WriteFile(owner, []byte("abandoned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(owner, old, old); err != nil {
		t.Fatal(err)
	}
	called := false
	err := withPortableDirectoryLockConfig(context.Background(), lockDir, portableDirectoryLockConfig{
		staleAfter: 20 * time.Millisecond,
		heartbeat:  5 * time.Millisecond,
		retry:      time.Millisecond,
	}, func() error {
		called = true
		return nil
	})
	if err != nil || !called {
		t.Fatalf("abandoned lock was not recovered: called=%v err=%v", called, err)
	}
}

func TestPortableDirectoryLockRecoversCrashBeforeOwnerWrite(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "ownerless.lockdir")
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(lockDir, old, old); err != nil {
		t.Fatal(err)
	}
	called := false
	err := withPortableDirectoryLockConfig(context.Background(), lockDir, portableDirectoryLockConfig{
		staleAfter: 20 * time.Millisecond, heartbeat: 5 * time.Millisecond, retry: time.Millisecond,
	}, func() error {
		called = true
		return nil
	})
	if err != nil || !called {
		t.Fatalf("ownerless abandoned lock was not recovered: called=%v err=%v", called, err)
	}
}
