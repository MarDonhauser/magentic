package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestAdoptLegacyIndexDerivesAggregatesAndRemovesFile(t *testing.T) {
	history, _, indexDir, _ := openTestWorkHistory(t)
	legacy := filepath.Join(indexDir, "index.json")
	writeHistoryTestFile(t, legacy, `{"version":1,"revision":3,"files":{"claude:alt":{
		"provider":"claude","adapterVersion":1,"digest":"d","size":10,"modTime":12345,
		"records":[
			{"id":"alt-1","sourceId":"claude:alt","provider":"claude","conversationId":"conv-alt",
			 "timestamp":"2026-01-05T10:00:00Z","role":"user","kind":"prompt","lineage":"primary","text":"sehr alt","cwd":"/work/demo"}
		]}}}`)

	if err := history.adoptLegacyIndex(context.Background()); err != nil {
		t.Fatal(err)
	}
	var prompts int
	if err := history.store.db.QueryRow(`SELECT sum(prompts) FROM activity WHERE agg_key = 'conv-alt'`).Scan(&prompts); err != nil {
		t.Fatal(err)
	}
	if prompts != 1 {
		t.Fatalf("adopted prompts = %d, want 1", prompts)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy index still present: %v", err)
	}
	var events int
	if err := history.store.db.QueryRow(`SELECT count(*) FROM events`).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 0 {
		t.Fatalf("events adopted = %d, want 0 — nur Aggregate werden übernommen", events)
	}
}

func TestAdoptLegacyIndexRenamesBrokenFile(t *testing.T) {
	history, _, indexDir, _ := openTestWorkHistory(t)
	legacy := filepath.Join(indexDir, "index.json")
	writeHistoryTestFile(t, legacy, `{ kaputt`)
	if err := history.adoptLegacyIndex(context.Background()); err != nil {
		t.Fatalf("adoption must not fail the run: %v", err)
	}
	if _, err := os.Stat(legacy + ".rejected"); err != nil {
		t.Fatalf("rejected file missing: %v", err)
	}
}
