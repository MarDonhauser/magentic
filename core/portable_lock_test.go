package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestPortableDirectoryLockRecoversMalformedOwnerWrite(t *testing.T) {
	for _, contents := range []string{"", "short-write"} {
		contents := contents
		t.Run(fmt.Sprintf("bytes_%d", len(contents)), func(t *testing.T) {
			lockDir := filepath.Join(t.TempDir(), "malformed-owner.lockdir")
			if err := os.Mkdir(lockDir, 0o700); err != nil {
				t.Fatal(err)
			}
			owner := filepath.Join(lockDir, "owner")
			if err := os.WriteFile(owner, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			old := time.Now().Add(-time.Hour)
			if err := os.Chtimes(owner, old, old); err != nil {
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
				t.Fatalf("malformed abandoned lock was not recovered: called=%v err=%v", called, err)
			}
		})
	}
}

// portableLockTestConfig ist die schnelle Alterung, unter der die
// Generationswechsel dieser Datei überhaupt beobachtbar sind.
func portableLockTestConfig() portableDirectoryLockConfig {
	return portableDirectoryLockConfig{
		staleAfter: 40 * time.Millisecond,
		heartbeat:  5 * time.Millisecond,
		retry:      time.Millisecond,
	}
}

func portableLockSoleOwner(t *testing.T, lockDir string) string {
	t.Helper()
	entries, err := os.ReadDir(lockDir)
	if err != nil {
		t.Fatalf("lock directory is gone: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("lock directory does not hold exactly one owner: %#v", entries)
	}
	name := entries[0].Name()
	nonce, ok := strings.CutPrefix(name, portableLockOwnerPrefix)
	if !ok || !portableLockOwnedBy(filepath.Join(lockDir, name), nonce) {
		t.Fatalf("lock directory holds no valid owner: %q", name)
	}
	return name
}

func TestPortableDirectoryLockFailedPublicationSparesSuccessor(t *testing.T) {
	// Der aufgegebene Lock zwingt den ersten Kandidaten erst durch die
	// Wiederherstellung und danach in eine fehlschlagende Publikation.
	lockDir := filepath.Join(t.TempDir(), "publication.lockdir")
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	abandoned := filepath.Join(lockDir, "owner")
	if err := os.WriteFile(abandoned, []byte("abandoned\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(abandoned, old, old); err != nil {
		t.Fatal(err)
	}

	paused := make(chan struct{})
	resume := make(chan struct{})
	failing := portableLockTestConfig()
	failing.publishOwner = func(owner, nonce string) error { return errors.New("owner publication failed") }
	failing.beforeCleanup = func(stage portableLockStage) {
		if stage != portableLockStagePublish {
			return
		}
		close(paused)
		<-resume
	}
	first := make(chan error, 1)
	go func() {
		first <- withPortableDirectoryLockConfig(context.Background(), lockDir, failing, func() error {
			return errors.New("failed publication acquired the lock")
		})
	}()
	<-paused

	acquired := make(chan struct{})
	release := make(chan struct{})
	second := make(chan error, 1)
	go func() {
		second <- withPortableDirectoryLockConfig(context.Background(), lockDir, portableLockTestConfig(), func() error {
			close(acquired)
			<-release
			return nil
		})
	}()
	<-acquired
	owner := portableLockSoleOwner(t, lockDir)

	close(resume)
	if err := <-first; err == nil {
		t.Fatal("failed publication reported success")
	}
	if resumed := portableLockSoleOwner(t, lockDir); resumed != owner {
		t.Fatalf("cleanup of the failed publication replaced the successor: %q -> %q", owner, resumed)
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatalf("successor failed: %v", err)
	}
	if _, err := os.Stat(lockDir); !os.IsNotExist(err) {
		t.Fatalf("successor did not release its lock: %v", err)
	}
}

func TestPortableDirectoryLockReleaseSparesSuccessor(t *testing.T) {
	lockDir := filepath.Join(t.TempDir(), "release.lockdir")
	paused := make(chan struct{})
	resume := make(chan struct{})
	releasing := portableLockTestConfig()
	releasing.beforeCleanup = func(stage portableLockStage) {
		if stage != portableLockStageRelease {
			return
		}
		close(paused)
		<-resume
	}
	first := make(chan error, 1)
	go func() {
		first <- withPortableDirectoryLockConfig(context.Background(), lockDir, releasing, func() error { return nil })
	}()
	<-paused

	// Der angehaltene Freigeber altert; der Nachfolger stellt seine Generation
	// wieder her und belegt denselben Pfad neu.
	acquired := make(chan struct{})
	release := make(chan struct{})
	second := make(chan error, 1)
	go func() {
		second <- withPortableDirectoryLockConfig(context.Background(), lockDir, portableLockTestConfig(), func() error {
			close(acquired)
			<-release
			return nil
		})
	}()
	<-acquired
	owner := portableLockSoleOwner(t, lockDir)

	close(resume)
	if err := <-first; err != nil {
		t.Fatalf("paused release failed: %v", err)
	}
	if resumed := portableLockSoleOwner(t, lockDir); resumed != owner {
		t.Fatalf("late release removed the successor: %q -> %q", owner, resumed)
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatalf("successor failed: %v", err)
	}
}

func TestPortableDirectoryLockLateRecoverersSpareSuccessor(t *testing.T) {
	parent := t.TempDir()
	lockDir := filepath.Join(parent, "recovery.lockdir")
	if err := os.Mkdir(lockDir, 0o700); err != nil {
		t.Fatal(err)
	}
	nonce := NewUUID()
	stale := filepath.Join(lockDir, portableLockOwnerPrefix+nonce)
	if err := writePortableDirectoryLockOwner(stale, nonce); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	config := portableLockTestConfig()
	sightings := make([]portableLockSighting, 2)
	for index := range sightings {
		sighting, err := inspectPortableLock(lockDir)
		if err != nil {
			t.Fatal(err)
		}
		if !sighting.stale(config.staleAfter) {
			t.Fatal("abandoned generation was not observed as stale")
		}
		sightings[index] = sighting
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
	owner := portableLockSoleOwner(t, lockDir)

	for _, sighting := range sightings {
		_ = recoverPortableLock(sighting)
	}
	if resumed := portableLockSoleOwner(t, lockDir); resumed != owner {
		t.Fatalf("late recovery removed the successor: %q -> %q", owner, resumed)
	}
	siblings, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	if len(siblings) != 1 || siblings[0].Name() != filepath.Base(lockDir) {
		t.Fatalf("late recovery renamed the successor away: %#v", siblings)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("successor failed: %v", err)
	}
}

func TestPortableDirectoryLockPublishesOwnerAtomically(t *testing.T) {
	lockDir := t.TempDir()
	owner := filepath.Join(lockDir, "owner")
	nonce := NewUUID()
	if err := writePortableDirectoryLockOwner(owner, nonce); err != nil {
		t.Fatalf("writePortableDirectoryLockOwner() error = %v", err)
	}
	if !portableLockOwnedBy(owner, nonce) {
		t.Fatal("published owner is not valid")
	}
	entries, err := os.ReadDir(lockDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "owner" {
		t.Fatalf("owner publication left temporary files: %#v", entries)
	}
}
