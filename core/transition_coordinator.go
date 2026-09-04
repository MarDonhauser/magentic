package core

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"sort"
)

// transitionScope ist eine Ebene der prozessübergreifenden Koordination. Die
// Reihenfolge der Konstanten IST die Reihenfolge, in der Sperren genommen
// werden müssen: Project vor Worktree vor Session vor Runtime. Sie stand
// bisher in vier getrennten Koordinatoren in keinem einzigen — jeder kannte
// nur seine eigene Ebene.
type transitionScope int

const (
	transitionScopeProject transitionScope = iota
	transitionScopeWorktree
	transitionScopeSession
	transitionScopeRuntime
)

// lockDir ist der Verzeichnisname der Ebene. Die Namen sind unverändert: sie
// liegen auf der Platte und werden von Prozessen geteilt, die diese Fassung
// noch nicht kennen.
func (s transitionScope) lockDir() string {
	switch s {
	case transitionScopeProject:
		return ".project-transition-locks"
	case transitionScopeWorktree:
		return ".worktree-transition-locks"
	case transitionScopeSession:
		return ".lifecycle-session-locks"
	default:
		return ".runtime-transition-locks"
	}
}

func (s transitionScope) String() string {
	switch s {
	case transitionScopeProject:
		return "Project"
	case transitionScopeWorktree:
		return "Worktree"
	case transitionScopeSession:
		return "Session"
	default:
		return "Runtime"
	}
}

type transitionScopeKey struct{}

// heldTransitionScope liest die zuletzt genommene Ebene aus dem Kontext.
func heldTransitionScope(ctx context.Context) (transitionScope, bool) {
	if ctx == nil {
		return 0, false
	}
	scope, held := ctx.Value(transitionScopeKey{}).(transitionScope)
	return scope, held
}

// transitionCoordinator serialisiert Übergänge prozessübergreifend. Ein
// Koordinator statt vier: die Ebenen unterscheiden sich nur in ihrem
// Verzeichnis und ihrem Schlüssel, nicht in der Implementation.
type transitionCoordinator struct {
	root          string
	beforeAcquire func(transitionScope, string)
	// rootOverrides ist ein interner Seam für Tests: er verschiebt die Wurzel
	// einer einzelnen Ebene, damit ein Test die Verschränkung einer tieferen
	// Ebene isoliert prüfen kann. In Produktion ist er leer.
	rootOverrides map[transitionScope]string
}

func (c transitionCoordinator) rootFor(scope transitionScope) string {
	if root, overridden := c.rootOverrides[scope]; overridden {
		return root
	}
	return c.root
}

func newTransitionCoordinator(statePath string) transitionCoordinator {
	return transitionCoordinator{root: filepath.Dir(statePath)}
}

// with nimmt die Sperren einer Ebene und ruft fn. Mehrere Schlüssel werden
// sortiert und entdoppelt, damit zwei gleichzeitige Übergänge über denselben
// Schlüsselsatz einander nicht verklemmen können. Ein Aufruf, der die
// Reihenfolge verletzt, scheitert ausdrücklich, statt eine Verklemmung erst im
// Betrieb zu zeigen.
func (c transitionCoordinator) with(ctx context.Context, scope transitionScope, keys []string, fn func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if held, ok := heldTransitionScope(ctx); ok && scope <= held {
		return fmt.Errorf("Transition-Reihenfolge verletzt: %s nach %s", scope, held)
	}

	unique := make([]string, 0, len(keys))
	for _, key := range keys {
		if key != "" {
			unique = append(unique, key)
		}
	}
	if len(unique) == 0 {
		return fn(ctx)
	}
	sort.Strings(unique)
	deduped := unique[:1]
	for _, key := range unique[1:] {
		if deduped[len(deduped)-1] != key {
			deduped = append(deduped, key)
		}
	}

	// Der innere Kontext trägt die genommene Ebene weiter. Nur deshalb kann
	// eine verschachtelte Anforderung überhaupt sehen, was schon gehalten wird.
	inner := context.WithValue(ctx, transitionScopeKey{}, scope)
	var acquire func(int) error
	acquire = func(index int) error {
		if index == len(deduped) {
			return fn(inner)
		}
		if c.beforeAcquire != nil {
			c.beforeAcquire(scope, deduped[index])
		}
		digest := sha256.Sum256([]byte(deduped[index]))
		lockPath := filepath.Join(c.rootFor(scope), scope.lockDir(), fmt.Sprintf("%x", digest[:]))
		return withRegistryFileLock(inner, lockPath, func() error { return acquire(index + 1) })
	}
	return acquire(0)
}
