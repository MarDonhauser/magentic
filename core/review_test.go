package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

var errReviewDeliveryTest = errors.New("Prompt an tmux senden: kaputt")

func newReviewRegistrySession(t *testing.T) (*Registry, Session) {
	t.Helper()
	statePath := useTempState(t)
	registry := OpenRegistry(statePath)
	session := Session{
		ID: SessionID("session-review"), Name: "review-hera", RuntimeName: "mgt-review-hera",
		Dir: "/workspace/project", SessionKind: SessionKindCodingAgent,
	}
	if _, err := registry.Change(context.Background(), RegisterSession(session)); err != nil {
		t.Fatal(err)
	}
	return registry, session
}

func reviewCommentFixture(id, path, text string, newStart, newEnd int, mode DiffComparisonMode) ReviewComment {
	return ReviewComment{
		ID: id, Path: path, NewStart: newStart, NewEnd: newEnd,
		Quoted: "zitierter Code", Text: text, Mode: mode, CreatedAt: time.Now(),
	}
}

func reviewStateOf(t *testing.T, registry *Registry, session Session) Session {
	t.Helper()
	snapshot, err := registry.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	state := snapshot.State()
	found := state.SessionByID(session.ID)
	if found == nil {
		t.Fatal("Session fehlt in der Registry")
	}
	return *found
}

func TestSessionReviewJSONRoundTripKeepsAbsentAbsent(t *testing.T) {
	legacy := []byte(`{"id":"s1","name":"hera","project":"req.pilot","dir":"/tmp/req.pilot","created_at":"2026-01-01T00:00:00Z"}`)
	var session Session
	if err := json.Unmarshal(legacy, &session); err != nil {
		t.Fatal(err)
	}
	if session.Review != nil || len(session.SentReviews) != 0 {
		t.Fatalf("legacy state must read as no Review: %+v", session.Review)
	}
	data, err := json.Marshal(Session{ID: "s1", Name: "hera"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "review") {
		t.Fatalf("absent Review must stay absent in JSON: %s", data)
	}

	withReview := Session{
		ID: "s1", Name: "hera",
		Review: &SessionReview{ID: "r1", Comments: []ReviewComment{
			reviewCommentFixture("c1", "app/foo.go", "bitte umbenennen", 12, 12, DiffComparisonWorkingTree),
		}},
	}
	roundTripped, err := json.Marshal(withReview)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Session
	if err := json.Unmarshal(roundTripped, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Review == nil || len(decoded.Review.Comments) != 1 || decoded.Review.Comments[0].Text != "bitte umbenennen" {
		t.Fatalf("Review round trip = %+v", decoded.Review)
	}
	if decoded.Review.Comments[0].Mode != DiffComparisonWorkingTree {
		t.Fatalf("comment mode = %q", decoded.Review.Comments[0].Mode)
	}
}

func TestAddReviewCommentKeepsOneOpenReviewInFileLineOrder(t *testing.T) {
	registry, session := newReviewRegistrySession(t)
	ctx := context.Background()
	second := reviewCommentFixture("c2", "app/b.go", "zweiter", 3, 3, DiffComparisonBranch)
	first := reviewCommentFixture("c1", "app/a.go", "erster", 30, 32, DiffComparisonWorkingTree)
	if _, err := registry.Change(ctx, AddReviewComment(session.ID, session.Name, second)); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Change(ctx, AddReviewComment(session.ID, session.Name, first)); err != nil {
		t.Fatal(err)
	}
	got := reviewStateOf(t, registry, session)
	if got.Review == nil || len(got.Review.Comments) != 2 {
		t.Fatalf("open Review = %+v", got.Review)
	}
	if got.Review.Comments[0].ID != "c1" || got.Review.Comments[1].ID != "c2" {
		t.Fatalf("comment order = %+v, want file-then-line", got.Review.Comments)
	}
	if got.Review.Comments[1].Mode != DiffComparisonBranch {
		t.Fatalf("branch comment lost its mode: %+v", got.Review.Comments[1])
	}
}

func TestEditAndDeleteReviewCommentKeepAnchors(t *testing.T) {
	registry, session := newReviewRegistrySession(t)
	ctx := context.Background()
	comment := reviewCommentFixture("c1", "app/a.go", "alt", 10, 12, DiffComparisonWorkingTree)
	if _, err := registry.Change(ctx, AddReviewComment(session.ID, session.Name, comment)); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Change(ctx, EditReviewComment(session.ID, session.Name, "c1", "neu formuliert")); err != nil {
		t.Fatal(err)
	}
	got := reviewStateOf(t, registry, session)
	edited := got.Review.Comments[0]
	if edited.Text != "neu formuliert" || edited.Path != "app/a.go" || edited.NewStart != 10 || edited.NewEnd != 12 || edited.Quoted != "zitierter Code" {
		t.Fatalf("edited comment = %+v", edited)
	}
	if _, err := registry.Change(ctx, DeleteReviewComment(session.ID, session.Name, "c1")); err != nil {
		t.Fatal(err)
	}
	got = reviewStateOf(t, registry, session)
	if len(got.Review.Comments) != 0 {
		t.Fatalf("deleted comment survived: %+v", got.Review.Comments)
	}
}

func TestBlankReviewCommentIsRejected(t *testing.T) {
	registry, session := newReviewRegistrySession(t)
	ctx := context.Background()
	blank := reviewCommentFixture("c1", "app/a.go", "   \n ", 10, 10, DiffComparisonWorkingTree)
	if _, err := registry.Change(ctx, AddReviewComment(session.ID, session.Name, blank)); err == nil ||
		!strings.Contains(err.Error(), "leer") {
		t.Fatalf("blank add = %v, want a rejection", err)
	}
	got := reviewStateOf(t, registry, session)
	if got.Review != nil && len(got.Review.Comments) != 0 {
		t.Fatalf("rejected comment changed the Review: %+v", got.Review)
	}

	valid := reviewCommentFixture("c1", "app/a.go", "gehaltvoll", 10, 10, DiffComparisonWorkingTree)
	if _, err := registry.Change(ctx, AddReviewComment(session.ID, session.Name, valid)); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Change(ctx, EditReviewComment(session.ID, session.Name, "c1", "  ")); err == nil ||
		!strings.Contains(err.Error(), "leer") {
		t.Fatalf("blank edit = %v, want a rejection", err)
	}
	got = reviewStateOf(t, registry, session)
	if got.Review.Comments[0].Text != "gehaltvoll" {
		t.Fatalf("rejected edit changed the comment: %+v", got.Review.Comments[0])
	}
}

func TestMarkReviewSentStartsFreshReviewAndBoundsHistory(t *testing.T) {
	registry, session := newReviewRegistrySession(t)
	ctx := context.Background()
	if _, err := registry.Change(ctx, AddReviewComment(session.ID, session.Name,
		reviewCommentFixture("c1", "app/a.go", "erster", 1, 1, DiffComparisonWorkingTree))); err != nil {
		t.Fatal(err)
	}
	openID := reviewStateOf(t, registry, session).Review.ID
	sentAt := time.Now()
	if _, err := registry.Change(ctx, MarkReviewSent(session.ID, session.Name, sentAt)); err != nil {
		t.Fatal(err)
	}
	got := reviewStateOf(t, registry, session)
	if len(got.SentReviews) != 1 || got.SentReviews[0].ID != openID || !got.SentReviews[0].SentAt.Equal(sentAt) {
		t.Fatalf("sent history = %+v", got.SentReviews)
	}
	if len(got.SentReviews[0].Comments) != 1 || got.SentReviews[0].Comments[0].Text != "erster" {
		t.Fatalf("sent comments = %+v", got.SentReviews[0].Comments)
	}
	if got.Review == nil || len(got.Review.Comments) != 0 || got.Review.ID == openID {
		t.Fatalf("fresh open Review = %+v", got.Review)
	}

	for i := 0; i < MaxSentReviewsPerSession+2; i++ {
		if _, err := registry.Change(ctx, AddReviewComment(session.ID, session.Name,
			reviewCommentFixture("", "app/a.go", "laufend", i+1, i+1, DiffComparisonWorkingTree))); err != nil {
			t.Fatal(err)
		}
		if _, err := registry.Change(ctx, MarkReviewSent(session.ID, session.Name, time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	got = reviewStateOf(t, registry, session)
	if len(got.SentReviews) != MaxSentReviewsPerSession {
		t.Fatalf("sent history = %d, want bound %d", len(got.SentReviews), MaxSentReviewsPerSession)
	}
	for _, sent := range got.SentReviews {
		if sent.ID == openID {
			t.Fatal("bound kept the oldest sent Review instead of dropping it")
		}
	}
}

func TestDiscardSentReviewLeavesOpenReviewUntouched(t *testing.T) {
	registry, session := newReviewRegistrySession(t)
	ctx := context.Background()
	if _, err := registry.Change(ctx, AddReviewComment(session.ID, session.Name,
		reviewCommentFixture("c1", "app/a.go", "gesendet", 1, 1, DiffComparisonWorkingTree))); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Change(ctx, MarkReviewSent(session.ID, session.Name, time.Now())); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Change(ctx, AddReviewComment(session.ID, session.Name,
		reviewCommentFixture("c2", "app/b.go", "offen", 2, 2, DiffComparisonWorkingTree))); err != nil {
		t.Fatal(err)
	}
	sentID := reviewStateOf(t, registry, session).SentReviews[0].ID
	if _, err := registry.Change(ctx, DiscardSentReview(session.ID, session.Name, sentID)); err != nil {
		t.Fatal(err)
	}
	got := reviewStateOf(t, registry, session)
	if len(got.SentReviews) != 0 {
		t.Fatalf("sent history = %+v", got.SentReviews)
	}
	if len(got.Review.Comments) != 1 || got.Review.Comments[0].ID != "c2" {
		t.Fatalf("open Review = %+v", got.Review)
	}
}

func TestRenderReviewPromptGolden(t *testing.T) {
	review := SessionReview{ID: "r1", Comments: []ReviewComment{
		{
			ID: "c1", Path: "app/b.go", OldStart: 7, OldEnd: 7,
			Quoted: "- return 1", Text: "Dieser Zweig fehlt im Branch-Vergleich.",
			Mode:      DiffComparisonBranch,
			CreatedAt: time.Now(),
		},
		{
			ID: "c2", Path: "app/a.go", NewStart: 30, NewEnd: 32,
			Quoted: "x := 1\ny := 2", Text: "Bitte zusammenfassen.",
			Mode:      DiffComparisonWorkingTree,
			CreatedAt: time.Now(),
		},
	}}
	got := RenderReviewPrompt(review, "hera")
	want := "Code-Review für die Session \"hera\" mit 2 Kommentaren. Gehe jeden Kommentar der Reihe nach durch und setze ihn um.\n" +
		"\nKommentar 1 von 2 — Datei \"app/a.go\" (Zeilen 30–32, Arbeitsverzeichnis gegen HEAD)\n" +
		"```\nx := 1\ny := 2\n```\nBitte zusammenfassen.\n" +
		"\nKommentar 2 von 2 — Datei \"app/b.go\" (Zeile 7 (entfernte Zeile), Branch gegen Basis-Branch)\n" +
		"```\n- return 1\n```\nDieser Zweig fehlt im Branch-Vergleich.\n"
	if got != want {
		t.Fatalf("RenderReviewPrompt:\n got %q\nwant %q", got, want)
	}
}

func TestSendSessionReviewQueuesSingleReviewMessage(t *testing.T) {
	registry, session, recorder := newOutboxDispatchSession(t)
	ctx := context.Background()
	comment := reviewCommentFixture("c1", "app/a.go", "bitte prüfen", 5, 5, DiffComparisonWorkingTree)
	if _, err := registry.Change(ctx, AddReviewComment(session.ID, session.Name, comment)); err != nil {
		t.Fatal(err)
	}
	busy := func(_ context.Context, sessions []Session) ObservationSnapshot {
		return outboxSnapshot(outboxObservation(sessions[0].ID, StatusRunning, "arbeitet"))
	}
	if err := SendSessionReview(session.ID, busy); err != nil {
		t.Fatalf("SendSessionReview: %v", err)
	}
	if sends := recorder.delivered(); len(sends) != 0 {
		t.Fatalf("busy Session received a send: %+v", sends)
	}
	queued := outboxOf(t, registry, session.Name)
	if len(queued) != 1 || queued[0].Kind != QueuedMessageKindReview {
		t.Fatalf("queue = %+v, want one review message", queued)
	}
	got := reviewStateOf(t, registry, session)
	if len(got.SentReviews) != 1 || len(got.SentReviews[0].Comments) != 1 {
		t.Fatalf("sent history = %+v", got.SentReviews)
	}
	if queued[0].Text != RenderReviewPrompt(got.SentReviews[0], session.Name) {
		t.Fatalf("queued prompt is not the rendered Review:\n%s", queued[0].Text)
	}
	if !strings.Contains(queued[0].Text, "app/a.go") || !strings.Contains(queued[0].Text, "bitte prüfen") {
		t.Fatalf("queued prompt lost path or text:\n%s", queued[0].Text)
	}
	if got.Review == nil || len(got.Review.Comments) != 0 {
		t.Fatalf("open Review is not fresh: %+v", got.Review)
	}
}

func TestSendSessionReviewRefusesEmptyReview(t *testing.T) {
	registry, session, recorder := newOutboxDispatchSession(t)
	observe := func(_ context.Context, sessions []Session) ObservationSnapshot {
		return outboxSnapshot(outboxReadyObservation(sessions[0].ID))
	}
	if err := SendSessionReview(session.ID, observe); err == nil ||
		!strings.Contains(err.Error(), "keine Kommentare") {
		t.Fatalf("empty send = %v, want a refusal", err)
	}
	if got := outboxOf(t, registry, session.Name); len(got) != 0 {
		t.Fatalf("refused Review was queued: %+v", got)
	}
	if sends := recorder.delivered(); len(sends) != 0 {
		t.Fatalf("refused Review was delivered: %+v", sends)
	}
}

func TestSendSessionReviewKeepsCommentsWhenDeliveryFails(t *testing.T) {
	registry, session, recorder := newOutboxDispatchSession(t)
	ctx := context.Background()
	if _, err := registry.Change(ctx, AddReviewComment(session.ID, session.Name,
		reviewCommentFixture("c1", "app/a.go", "trotz Fehler erhalten", 5, 5, DiffComparisonWorkingTree))); err != nil {
		t.Fatal(err)
	}
	recorder.fail(errReviewDeliveryTest)
	observe := func(_ context.Context, sessions []Session) ObservationSnapshot {
		return outboxSnapshot(outboxReadyObservation(sessions[0].ID))
	}
	// Die Queue hat den Prompt angenommen, also gilt das Review als gesendet;
	// die Zustellung selbst bleibt sichtbar in der Outbox zurück.
	if err := SendSessionReview(session.ID, observe); err != nil {
		t.Fatalf("SendSessionReview: %v", err)
	}
	queued := outboxOf(t, registry, session.Name)
	if len(queued) != 1 || queued[0].Kind != QueuedMessageKindReview ||
		!strings.Contains(queued[0].Text, "trotz Fehler erhalten") {
		t.Fatalf("failed delivery lost the prompt: %+v", queued)
	}
	got := reviewStateOf(t, registry, session)
	if len(got.SentReviews) != 1 || len(got.SentReviews[0].Comments) != 1 ||
		got.SentReviews[0].Comments[0].Text != "trotz Fehler erhalten" {
		t.Fatalf("sent history = %+v", got.SentReviews)
	}
}

func TestSendSessionReviewLeavesOpenReviewWhenQueueRejects(t *testing.T) {
	statePath := useTempState(t)
	registry := OpenRegistry(statePath)
	terminal := Session{
		ID: SessionID("session-term-review"), Name: "term-review", RuntimeName: "mgt-term-review",
		Dir: "/workspace/project", SessionKind: SessionKindTerminal,
	}
	if _, err := registry.Change(context.Background(), RegisterSession(terminal)); err != nil {
		t.Fatal(err)
	}
	recorder := &outboxSendRecorder{}
	recorder.install(t)
	if _, err := registry.Change(context.Background(), AddReviewComment(terminal.ID, terminal.Name,
		reviewCommentFixture("c1", "app/a.go", "bleibt offen", 5, 5, DiffComparisonWorkingTree))); err != nil {
		t.Fatal(err)
	}
	observe := func(_ context.Context, sessions []Session) ObservationSnapshot {
		return outboxSnapshot(outboxReadyObservation(sessions[0].ID))
	}
	if err := SendSessionReview(terminal.ID, observe); err == nil {
		t.Fatal("terminal send must fail")
	}
	got := reviewStateOf(t, registry, terminal)
	if got.Review == nil || len(got.Review.Comments) != 1 || got.Review.Comments[0].Text != "bleibt offen" {
		t.Fatalf("rejected Review did not stay open: %+v", got.Review)
	}
	if len(got.SentReviews) != 0 {
		t.Fatalf("rejected Review was recorded as sent: %+v", got.SentReviews)
	}
}

func TestRenameSessionKeepsOpenReviewAttached(t *testing.T) {
	registry, session := newReviewRegistrySession(t)
	ctx := context.Background()
	if _, err := registry.Change(ctx, AddReviewComment(session.ID, session.Name,
		reviewCommentFixture("c1", "app/a.go", "bleibt", 1, 1, DiffComparisonWorkingTree))); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Change(ctx, RenameRegisteredSessionRuntime(session.ID, session.Name, "hera-neu", "mgt-hera-neu")); err != nil {
		t.Fatal(err)
	}
	got := reviewStateOf(t, registry, session)
	if got.Name != "hera-neu" {
		t.Fatalf("session was not renamed: %+v", got.Name)
	}
	if got.Review == nil || len(got.Review.Comments) != 1 || got.Review.Comments[0].Text != "bleibt" {
		t.Fatalf("open Review after rename = %+v", got.Review)
	}
}

// TestReviewCommentsForReadingRendersAnchorsWithoutStoringThem hält fest, dass
// der Ankertext beim Lesen entsteht und nicht gespeichert wird: die Regel lebt
// allein in ReviewLineRef, und keine zweite Kopie kann daneben altern.
func TestReviewCommentsForReadingRendersAnchorsWithoutStoringThem(t *testing.T) {
	stored := []ReviewComment{
		{ID: "a", Path: "core/state.go", NewStart: 12, NewEnd: 12},
		{ID: "b", Path: "core/state.go", NewStart: 30, NewEnd: 34},
		{ID: "c", Path: "core/review.go", OldStart: 7, OldEnd: 7},
		{ID: "d", Path: "core/review.go", OldStart: 40, OldEnd: 42},
	}
	want := []string{
		"Zeile 12",
		"Zeilen 30–34",
		"Zeile 7 (entfernte Zeile)",
		"Zeilen 40–42 (entfernte Zeilen)",
	}

	read := ReviewCommentsForReading(stored)
	if len(read) != len(want) {
		t.Fatalf("ReviewCommentsForReading: %d Kommentare, erwartet %d", len(read), len(want))
	}
	for i := range read {
		if read[i].LineRef != want[i] {
			t.Errorf("Kommentar %s: LineRef %q, erwartet %q", read[i].ID, read[i].LineRef, want[i])
		}
		if read[i].LineRef != ReviewLineRef(stored[i]) {
			t.Errorf("Kommentar %s: gelesener Anker weicht von ReviewLineRef ab", read[i].ID)
		}
	}
	for i := range stored {
		if stored[i].LineRef != "" {
			t.Errorf("Kommentar %s: Eingabe wurde verändert", stored[i].ID)
		}
	}
}

// TestAddReviewCommentDropsRenderedAnchor hält fest, dass ein gelesener
// Kommentar seinen Ankertext nicht zurück auf die Platte trägt.
func TestAddReviewCommentDropsRenderedAnchor(t *testing.T) {
	change := AddReviewComment(SessionID("s1"), "hera", ReviewComment{
		ID: "a", Path: "core/state.go", NewStart: 12, NewEnd: 12, LineRef: "Zeile 12",
	})
	if change.reviewComment.LineRef != "" {
		t.Fatalf("LineRef %q wurde zum Speichern durchgereicht", change.reviewComment.LineRef)
	}
}
