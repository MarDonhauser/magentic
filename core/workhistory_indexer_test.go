package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkHistoryIndexerParsesNewestFirstAndReportsProgress(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	for i, name := range []string{"alt.jsonl", "neu.jsonl"} {
		path := filepath.Join(home, ".claude", "projects", "-work-demo", name)
		writeHistoryTestFile(t, path, `{"type":"user","uuid":"u-`+name+`","timestamp":"2026-08-30T10:0`+string(rune('0'+i))+`:00Z","cwd":"/work/demo","sessionId":"conv-`+name+`","message":{"role":"user","content":"Prompt `+name+`"}}`+"\n")
	}
	// Die neuere Datei muss zuerst indexiert werden.
	touchHistoryTestFile(t, filepath.Join(home, ".claude", "projects", "-work-demo", "alt.jsonl"), time.Now().Add(-48*time.Hour))
	touchHistoryTestFile(t, filepath.Join(home, ".claude", "projects", "-work-demo", "neu.jsonl"), time.Now())

	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := history.Events(context.Background(), HistoryAssociations{}, HistoryEventQuery{Limit: 100, IncludeUnknownTime: true})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 {
		t.Fatalf("events = %d, want 2", page.Total)
	}
	if page.Meta.Progress.Active {
		t.Fatalf("progress still active after a synchronous run: %#v", page.Meta.Progress)
	}
	if page.Meta.Progress.CompletedFiles != 2 {
		t.Fatalf("completed = %d, want 2", page.Meta.Progress.CompletedFiles)
	}
}

func TestWorkHistoryIndexerReusesUnchangedSources(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	path := filepath.Join(home, ".claude", "projects", "-work-demo", "a.jsonl")
	writeHistoryTestFile(t, path, `{"type":"user","uuid":"u-1","timestamp":"2026-08-30T10:00:00Z","cwd":"/work/demo","sessionId":"conv-1","message":{"role":"user","content":"Prompt"}}`+"\n")

	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	meta, err := history.snapshotMeta(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if coverage := historyTestCoverage(t, meta, HistoryProviderClaude); coverage.ParsedFiles != 1 {
		t.Fatalf("first run parsed = %d, want 1", coverage.ParsedFiles)
	}
	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	meta, err = history.snapshotMeta(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	coverage := historyTestCoverage(t, meta, HistoryProviderClaude)
	if coverage.ParsedFiles != 0 || coverage.ReusedFiles != 1 || coverage.IndexedFiles != 1 {
		t.Fatalf("second run coverage = %#v, want reuse", coverage)
	}
}

func TestWorkHistoryIndexerRemovesVanishedSources(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	path := filepath.Join(home, ".claude", "projects", "-work-demo", "a.jsonl")
	writeHistoryTestFile(t, path, `{"type":"user","uuid":"u-1","timestamp":"2026-08-30T10:00:00Z","cwd":"/work/demo","sessionId":"conv-1","message":{"role":"user","content":"Prompt"}}`+"\n")
	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	removeHistoryTestFile(t, path)
	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := history.Events(context.Background(), HistoryAssociations{}, HistoryEventQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Fatalf("events after deletion = %d, want 0", page.Total)
	}
}

func TestWorkHistoryIndexerKeepsAggregatesBeyondRetention(t *testing.T) {
	history, home, _, _ := openTestWorkHistory(t)
	history.config.Retention = 24 * time.Hour
	path := filepath.Join(home, ".claude", "projects", "-work-demo", "alt.jsonl")
	writeHistoryTestFile(t, path, `{"type":"user","uuid":"u-1","timestamp":"2026-01-05T10:00:00Z","cwd":"/work/demo","sessionId":"conv-1","message":{"role":"user","content":"sehr alt"}}`+"\n")

	if err := history.indexOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	page, err := history.Events(context.Background(), HistoryAssociations{}, HistoryEventQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 {
		t.Fatalf("events outside retention = %d, want 0", page.Total)
	}
	var prompts int
	if err := history.store.db.QueryRow(`SELECT sum(prompts) FROM activity`).Scan(&prompts); err != nil {
		t.Fatal(err)
	}
	if prompts != 1 {
		t.Fatalf("aggregated prompts = %d, want 1 — Aggregate müssen den Verfall überleben", prompts)
	}
}
