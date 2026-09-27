package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// OmpBuildIdentity names one installed omp build well enough that a rebuilt
// binary carrying the same version string still counts as a different
// build: the version omp reports plus the resolved binary path, size and
// modification time.
type OmpBuildIdentity struct {
	Version string
	Path    string
	Size    int64
	ModTime time.Time
}

// ompVerifyCacheEntry is what OmpVerifyCachePath persists: the build
// identity a verification covered, and nothing about the run itself. Only
// OmpVerifyOutcomeVerified is ever written here — see SaveOmpVerifyCache.
type ompVerifyCacheEntry struct {
	Version string    `json:"version"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// OmpVerifyCachePath is where the cached verification result lives, next to
// Magentic's other state. MAGENTIC_OMP_VERIFY_CACHE overrides it, mirroring
// MAGENTIC_STATE for StatePath, so tests can inject a scratch path.
func OmpVerifyCachePath() string {
	if p := os.Getenv("MAGENTIC_OMP_VERIFY_CACHE"); p != "" {
		return p
	}
	return filepath.Join(filepath.Dir(StatePath()), "omp-verify-cache.json")
}

// ResolveOmpBuildIdentity locates the omp binary on PATH and reads its
// version, size and modification time. lookup and version default to
// exec.LookPath and running "<path> --version" when nil, so tests can inject
// both without touching PATH or spawning a real binary.
func ResolveOmpBuildIdentity(ctx context.Context, lookup func() (string, error), version func(ctx context.Context, path string) (string, error)) (OmpBuildIdentity, error) {
	if lookup == nil {
		lookup = func() (string, error) { return exec.LookPath("omp") }
	}
	if version == nil {
		version = func(ctx context.Context, path string) (string, error) {
			out, err := exec.CommandContext(ctx, path, "--version").Output()
			return string(out), err
		}
	}
	path, err := lookup()
	if err != nil {
		return OmpBuildIdentity{}, fmt.Errorf("omp wurde nicht gefunden: %w", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		return OmpBuildIdentity{}, fmt.Errorf("omp-Binary %s konnte nicht gelesen werden: %w", path, err)
	}
	raw, err := version(ctx, path)
	if err != nil {
		return OmpBuildIdentity{}, fmt.Errorf("omp --version schlug fehl: %w", err)
	}
	return OmpBuildIdentity{
		Version: trimOmpVersion(raw),
		Path:    path,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}, nil
}

func trimOmpVersion(raw string) string {
	for len(raw) > 0 && (raw[len(raw)-1] == '\n' || raw[len(raw)-1] == '\r' || raw[len(raw)-1] == ' ') {
		raw = raw[:len(raw)-1]
	}
	return raw
}

// LoadOmpVerifyCache reads the cached verification identity from path. A
// missing file is not an error: it reads as no cached verification.
func LoadOmpVerifyCache(path string) (*ompVerifyCacheEntry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var entry ompVerifyCacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

// SaveOmpVerifyCache persists identity as the build the last verification
// covered. Callers MUST only call this for an OmpVerifyOutcomeVerified
// result — a failed, inconclusive or not-attempted result is never cached,
// so it is repeated on the next start rather than assumed successful.
func SaveOmpVerifyCache(path string, identity OmpBuildIdentity) error {
	entry := ompVerifyCacheEntry{
		Version: identity.Version,
		Path:    identity.Path,
		Size:    identity.Size,
		ModTime: identity.ModTime,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// ompVerifyCacheMatches reports whether a cached entry covers identity, so a
// second start of the same build can skip verification, while a changed
// version string or a changed binary (same version, different size or
// modification time) forces it again.
func ompVerifyCacheMatches(entry *ompVerifyCacheEntry, identity OmpBuildIdentity) bool {
	if entry == nil {
		return false
	}
	return entry.Version == identity.Version &&
		entry.Path == identity.Path &&
		entry.Size == identity.Size &&
		entry.ModTime.Equal(identity.ModTime)
}

// EnsureInstalledOmpGateVerified verifies the approval gate of the omp on
// PATH, reusing a cached verification of the same build. An omp that cannot
// be located is reported as not attempted, never as verified.
func EnsureInstalledOmpGateVerified(ctx context.Context, roots []string) (OmpVerifyResult, error) {
	identity, err := ResolveOmpBuildIdentity(ctx, nil, nil)
	if err != nil {
		return OmpVerifyResult{Outcome: OmpVerifyOutcomeNotAttempted, Reason: err.Error()}, nil
	}
	return EnsureOmpApprovalGateVerified(ctx, newOmpExecProcess(identity.Path), roots, nil, identity, OmpVerifyCachePath())
}

// EnsureOmpApprovalGateVerified skips a fresh VerifyOmpApprovalGate run when
// cachePath already covers identity, and otherwise runs it and persists the
// result only when it verified. It never treats an unattempted, failed or
// inconclusive verification as reusable.
func EnsureOmpApprovalGateVerified(ctx context.Context, proc OmpVerifyProcess, roots []string, mkTempDir func() (string, error), identity OmpBuildIdentity, cachePath string) (OmpVerifyResult, error) {
	cached, err := LoadOmpVerifyCache(cachePath)
	if err != nil {
		return OmpVerifyResult{}, err
	}
	if ompVerifyCacheMatches(cached, identity) {
		return OmpVerifyResult{Outcome: OmpVerifyOutcomeVerified, OmpVersion: identity.Version, Reason: "bereits für diesen omp-Build verifiziert"}, nil
	}
	result, err := VerifyOmpApprovalGate(ctx, proc, roots, mkTempDir)
	if err != nil {
		return result, err
	}
	if result.Outcome == OmpVerifyOutcomeVerified {
		if saveErr := SaveOmpVerifyCache(cachePath, identity); saveErr != nil {
			return result, saveErr
		}
	}
	return result, nil
}

// OmpGateRefusal states why omp Sessions are refused for a verification that
// did not verify. A failed gate, an inconclusive run and a run that could not
// be attempted are named differently, so the developer is pointed at what
// actually blocks them rather than at the gate every time.
func OmpGateRefusal(result OmpVerifyResult) string {
	version := result.OmpVersion
	if version == "" {
		version = "unbekannte Version"
	}
	switch result.Outcome {
	case OmpVerifyOutcomeFailed:
		return fmt.Sprintf("omp-Sessions sind gesperrt: das Freigabe-Gate von omp (%s) ließ sich nicht verifizieren. %s", version, result.Reason)
	case OmpVerifyOutcomeInconclusive:
		return fmt.Sprintf("omp-Sessions sind noch gesperrt: die Prüfung des Freigabe-Gates von omp (%s) blieb ohne Ergebnis, weil das Modell kein Schreib- oder Ausführungs-Tool aufrief. Sie wird beim nächsten Start wiederholt.", version)
	case OmpVerifyOutcomeNotAttempted:
		return fmt.Sprintf("omp-Sessions sind gesperrt: die Prüfung des Freigabe-Gates konnte nicht laufen. %s", result.Reason)
	}
	return ""
}
