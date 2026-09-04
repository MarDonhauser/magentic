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

	portableLockOwnerPrefix = "owner-"
)

// errPortableLockTaken meldet, dass der kanonische Pfad bereits von einer
// fremden Generation belegt ist. Nur dieser Fall darf erneut versucht werden.
var errPortableLockTaken = errors.New("portable lock is taken")

// portableLockStage benennt die Punkte, an denen eine Generation nach ihrer
// Validierung aufräumt. Ein Test klemmt sich hier ein, weil sich das Fenster
// zwischen Prüfung und Aufräumen sonst nicht deterministisch treffen lässt.
type portableLockStage string

const (
	portableLockStagePublish portableLockStage = "publish"
	portableLockStageRelease portableLockStage = "release"
)

type portableDirectoryLockConfig struct {
	staleAfter time.Duration
	heartbeat  time.Duration
	retry      time.Duration

	// Test-Seams, in Produktion nil: publishOwner ersetzt die Owner-Publikation,
	// beforeCleanup hält zwischen Validierung und Aufräumen an.
	publishOwner  func(owner, nonce string) error
	beforeCleanup func(stage portableLockStage)
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

// portableLockClaim ist die Generation, die dieser Prozess am kanonischen Pfad
// hält. Der Nonce steckt im Namen der Owner-Datei, deshalb adressieren Freigabe
// und Heartbeat genau diese Generation und nie die eines Nachfolgers, der
// denselben Pfad inzwischen neu belegt hat.
type portableLockClaim struct {
	dir        string
	owner      string
	nonce      string
	generation string
}

func newPortableLockClaim(dir, nonce string) portableLockClaim {
	generation := portableLockOwnerPrefix + nonce
	return portableLockClaim{
		dir:        dir,
		owner:      filepath.Join(dir, generation),
		nonce:      nonce,
		generation: generation,
	}
}

func (c portableLockClaim) held() bool {
	return portableLockOwnedBy(c.owner, c.nonce)
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
	for {
		claim, err := publishPortableLockClaim(lockDir, config)
		if err == nil {
			stop := make(chan struct{})
			done := make(chan struct{})
			go heartbeatPortableLockClaim(claim, config.heartbeat, stop, done)
			defer func() {
				close(stop)
				<-done
				releasePortableLockClaim(claim, config)
			}()
			return fn()
		}
		if !errors.Is(err, errPortableLockTaken) {
			return err
		}
		if sighting, inspectErr := inspectPortableLock(lockDir); inspectErr == nil && sighting.stale(config.staleAfter) {
			if recoverPortableLock(sighting) == nil {
				continue
			}
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

// publishPortableLockClaim baut die vollständige Generation neben dem
// kanonischen Pfad und schiebt sie mit einem Rename hinein. Das Lock-Verzeichnis
// ist dadurch nie ohne Owner sichtbar, und ein Fehlschlag räumt ausschließlich
// den eigenen, nicht wiederverwendbaren Staging-Pfad ab.
func publishPortableLockClaim(lockDir string, config portableDirectoryLockConfig) (portableLockClaim, error) {
	// Ein sichtbar belegter Pfad wird ohne Staging beantwortet, damit ein
	// wartender Kandidat nicht im Retry-Takt schreibt und synct. Die
	// Ausschlussentscheidung fällt weiterhin allein das Rename.
	if _, err := os.Lstat(lockDir); err == nil {
		return portableLockClaim{}, errPortableLockTaken
	}
	nonce := NewUUID()
	claim := newPortableLockClaim(lockDir, nonce)
	staging := lockDir + ".staging-" + nonce
	if err := os.Mkdir(staging, 0o700); err != nil {
		return portableLockClaim{}, fmt.Errorf("create lock directory: %w", err)
	}
	write := config.publishOwner
	if write == nil {
		write = writePortableDirectoryLockOwner
	}
	if err := write(filepath.Join(staging, claim.generation), nonce); err != nil {
		if config.beforeCleanup != nil {
			config.beforeCleanup(portableLockStagePublish)
		}
		_ = os.RemoveAll(staging)
		return portableLockClaim{}, fmt.Errorf("write lock owner: %w", err)
	}
	if err := os.Rename(staging, lockDir); err != nil {
		_ = os.RemoveAll(staging)
		// Ein belegtes Ziel ist der Normalfall der Konkurrenz und kein Fehler;
		// alles andere bricht ab, statt endlos zu wiederholen.
		if _, statErr := os.Lstat(lockDir); statErr == nil {
			return portableLockClaim{}, errPortableLockTaken
		}
		return portableLockClaim{}, fmt.Errorf("create lock directory: %w", err)
	}
	return claim, nil
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

func heartbeatPortableLockClaim(claim portableLockClaim, interval time.Duration, stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case now := <-ticker.C:
			if !claim.held() {
				return
			}
			_ = os.Chtimes(claim.owner, now, now)
		}
	}
}

// releasePortableLockClaim gibt ausschließlich die eigene Generation frei. Die
// Owner-Datei trägt den Nonce im Namen, und das Verzeichnis wird nur im leeren
// Zustand entfernt: Ein Nachfolger, der den Pfad bereits neu belegt hat, hält
// eine eigene Owner-Datei und übersteht damit beide Schritte unverändert.
func releasePortableLockClaim(claim portableLockClaim, config portableDirectoryLockConfig) {
	if !claim.held() {
		return
	}
	if config.beforeCleanup != nil {
		config.beforeCleanup(portableLockStageRelease)
	}
	_ = os.Remove(claim.owner)
	_ = os.Remove(claim.dir)
}

// portableLockEntry ist ein beobachteter Eintrag samt der Änderungszeit, unter
// der er gesehen wurde.
type portableLockEntry struct {
	path    string
	modTime time.Time
}

// portableLockSighting ist die Momentaufnahme einer fremden Generation. Die
// Wiederherstellung arbeitet nur auf ihr, damit sie später nichts entfernt, was
// erst nach der Beobachtung entstanden ist.
type portableLockSighting struct {
	dir     string
	entries []portableLockEntry
	latest  time.Time
}

func (s portableLockSighting) stale(after time.Duration) bool {
	return time.Since(s.latest) > after
}

func inspectPortableLock(lockDir string) (portableLockSighting, error) {
	listing, err := os.ReadDir(lockDir)
	if err != nil {
		return portableLockSighting{}, err
	}
	sighting := portableLockSighting{dir: lockDir}
	for _, item := range listing {
		path := filepath.Join(lockDir, item.Name())
		info, statErr := os.Lstat(path)
		if statErr != nil {
			// Der Blick ist bereits überholt; eine spätere Runde sieht mehr.
			return portableLockSighting{}, statErr
		}
		sighting.entries = append(sighting.entries, portableLockEntry{path: path, modTime: info.ModTime()})
		if info.ModTime().After(sighting.latest) {
			sighting.latest = info.ModTime()
		}
	}
	if len(sighting.entries) == 0 {
		// Ein leeres Verzeichnis stammt aus einem Absturz vor oder nach der
		// Publikation; seine eigene Änderungszeit ist der einzige Anhaltspunkt.
		info, statErr := os.Stat(lockDir)
		if statErr != nil {
			return portableLockSighting{}, statErr
		}
		sighting.latest = info.ModTime()
	}
	return sighting, nil
}

// recoverPortableLock entfernt genau die beobachtete Generation. Jeder Eintrag
// wird nur gelöscht, solange seine Änderungszeit die beobachtete ist – ein
// wieder aufgenommener Heartbeat bricht damit exakt ab –, und das Verzeichnis
// selbst nur im leeren Zustand. Ein Nachfolger überlebt deshalb auch mehrere
// verspätete Wiederhersteller: Seine Owner-Datei trägt einen anderen Namen und
// hält das Verzeichnis gefüllt.
func recoverPortableLock(sighting portableLockSighting) error {
	for _, entry := range sighting.entries {
		info, err := os.Lstat(entry.path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if !info.ModTime().Equal(entry.modTime) {
			return errors.New("lock owner heartbeat resumed")
		}
		if err := os.RemoveAll(entry.path); err != nil {
			return err
		}
	}
	if len(sighting.entries) == 0 {
		info, err := os.Stat(sighting.dir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if !info.ModTime().Equal(sighting.latest) {
			return errors.New("ownerless lock changed")
		}
	}
	if err := os.Remove(sighting.dir); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
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
