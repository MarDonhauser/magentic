package main

import (
	"context"
	"strings"
	"testing"

	"magentic/core"
)

func reviewBindingApp(t *testing.T, statePath string) *App {
	t.Helper()
	return &App{observeSessions: func(_ context.Context, sessions []core.Session) core.ObservationSnapshot {
		return core.ObservationSnapshot{
			Availability: core.ObservationAvailable,
			Sessions: []core.SessionObservation{{
				SessionID: sessions[0].ID, Availability: core.ObservationAvailable,
				Presence: core.SessionPresencePresent, Status: core.StatusRunning,
				Tool: core.AgentToolClaude, Content: "arbeitet", ContentKnown: true,
			}},
		}
	}}
}

func writeReviewBindingState(t *testing.T, statePath string) {
	t.Helper()
	t.Setenv("MAGENTIC_STATE", statePath)
	registry := core.OpenRegistry(statePath)
	if _, err := registry.Change(context.Background(), core.RegisterSession(core.Session{
		ID: core.SessionID("session-1"), Name: "hera", RuntimeName: "mgt-hera",
		Dir: "/workspace/project", SessionKind: core.SessionKindCodingAgent,
	})); err != nil {
		t.Fatal(err)
	}
}

func TestStructuredDiffBindingRejectsUnresolvableWorktree(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	t.Setenv("MAGENTIC_STATE", statePath)
	writeAppState(t, statePath, core.State{
		Projects: []core.Project{{ID: core.ProjectID("project-id"), Name: "Missing", Path: t.TempDir() + "/missing-repository"}},
	})
	app := &App{}
	if _, err := app.StructuredDiff("project-id", "wt_untrusted", "working_tree"); err == nil {
		t.Fatal("unresolvable Worktree must yield an error, not an empty diff")
	}
	if _, err := app.StructuredDiff("unknown-project", "wt_untrusted", "working_tree"); err == nil {
		t.Fatal("unknown ProjectID must yield an error")
	}
	if _, err := app.StructuredDiff("project-id", "wt_untrusted", "side-by-side"); err == nil {
		t.Fatal("unknown comparison mode must yield an error")
	}
}

func TestReviewCommentBindingsRejectBlankAndUnknownSession(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	writeReviewBindingState(t, statePath)
	app := reviewBindingApp(t, statePath)

	if _, err := app.AddReviewComment("unknown-session", "app/a.go", 0, 0, 1, 1, "x", "text", "working_tree"); err == nil ||
		!strings.Contains(err.Error(), "SessionID") {
		t.Fatalf("unknown SessionID = %v", err)
	}
	if _, err := app.AddReviewComment("session-1", "app/a.go", 0, 0, 1, 1, "x", "   ", "working_tree"); err == nil ||
		!strings.Contains(err.Error(), "leer") {
		t.Fatalf("blank comment = %v", err)
	}
	if err := app.EditReviewComment("session-1", "missing-comment", "neu"); err == nil {
		t.Fatal("editing a missing comment must fail")
	}
	if err := app.DeleteReviewComment("unknown-session", "c1"); err == nil {
		t.Fatal("deleting for an unknown session must fail")
	}
	if err := app.DiscardSentReview("session-1", "missing-review"); err == nil {
		t.Fatal("discarding a missing sent Review must fail")
	}
	if err := app.SendReview("session-1"); err == nil || !strings.Contains(err.Error(), "keine Kommentare") {
		t.Fatalf("empty send = %v, want a refusal", err)
	}
}

func TestReviewBindingsAddPreviewSendAndDiscard(t *testing.T) {
	statePath := t.TempDir() + "/state.json"
	writeReviewBindingState(t, statePath)
	app := reviewBindingApp(t, statePath)

	stored, err := app.AddReviewComment("session-1", "app/a.go", 0, 0, 5, 7, "x := 1", "Bitte prüfen.", "working_tree")
	if err != nil {
		t.Fatal(err)
	}
	if stored.ID == "" || stored.Path != "app/a.go" || stored.Text != "Bitte prüfen." {
		t.Fatalf("stored comment = %+v", stored)
	}
	if err := app.EditReviewComment("session-1", stored.ID, "Neu formuliert."); err != nil {
		t.Fatal(err)
	}
	open, err := app.OpenReview("session-1")
	if err != nil || open == nil || len(open.Comments) != 1 || open.Comments[0].Text != "Neu formuliert." {
		t.Fatalf("open Review = %+v, %v", open, err)
	}
	preview, err := app.ReviewPreview("session-1")
	if err != nil || !strings.Contains(preview, "app/a.go") || !strings.Contains(preview, "Neu formuliert.") {
		t.Fatalf("preview = %q, %v", preview, err)
	}
	if err := app.SendReview("session-1"); err != nil {
		t.Fatal(err)
	}
	open, err = app.OpenReview("session-1")
	if err != nil || open == nil || len(open.Comments) != 0 {
		t.Fatalf("open Review after send = %+v, %v", open, err)
	}
	sent, err := app.SentReviews("session-1")
	if err != nil || len(sent) != 1 || len(sent[0].Comments) != 1 || sent[0].SentAt.IsZero() {
		t.Fatalf("sent Reviews = %+v, %v", sent, err)
	}
	if err := app.DiscardSentReview("session-1", sent[0].ID); err != nil {
		t.Fatal(err)
	}
	sent, err = app.SentReviews("session-1")
	if err != nil || len(sent) != 0 {
		t.Fatalf("sent Reviews after discard = %+v, %v", sent, err)
	}
}
