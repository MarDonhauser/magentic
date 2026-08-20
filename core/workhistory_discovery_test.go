package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestHistoryDiscoveryDoesNotPresentSymlinkedRootAsKnownEmpty(t *testing.T) {
	realRoot := t.TempDir()
	link := filepath.Join(t.TempDir(), "projects")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	discovery := discoverHistoryFiles(context.Background(), osWorkHistoryFS{}, claudeHistoryAdapter{root: link})
	if discovery.coverage.State == HistorySourceAvailable || len(discovery.coverage.Problems) == 0 {
		t.Fatalf("symlinked root became known-empty coverage: %#v", discovery.coverage)
	}
}

func TestHistoryDiscoveryMarksSkippedNestedSymlinkPartial(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "conversation.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "linked-project")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	discovery := discoverHistoryFiles(context.Background(), osWorkHistoryFS{}, claudeHistoryAdapter{root: root})
	if discovery.coverage.State != HistorySourcePartial || discovery.coverage.Files != 0 || len(discovery.coverage.Problems) == 0 {
		t.Fatalf("skipped nested symlink coverage = %#v", discovery.coverage)
	}
}
