package main

import (
	"strings"
	"testing"
	"testing/fstest"

	"magentic/core"
)

func TestNotchDocumentInlinesItsEntryAssets(t *testing.T) {
	assets := fstest.MapFS{
		"frontend/dist/notch.html":       &fstest.MapFile{Data: []byte(`<!doctype html><link rel="stylesheet" href="/assets/notch.css"><script type="module" src="/assets/notch.js"></script>`)},
		"frontend/dist/assets/notch.css": &fstest.MapFile{Data: []byte(`body{background:transparent}`)},
		"frontend/dist/assets/notch.js":  &fstest.MapFile{Data: []byte(`window.ready=true`)},
	}
	document, err := notchDocumentFromAssets(assets)
	if err != nil {
		t.Fatalf("notchDocumentFromAssets() error = %v", err)
	}
	for _, want := range []string{"<style>body{background:transparent}</style>", `<script type="module">window.ready=true</script>`} {
		if !strings.Contains(document, want) {
			t.Fatalf("document fehlt %q: %s", want, document)
		}
	}
	if strings.Contains(document, "/assets/") {
		t.Fatalf("document enthält weiterhin externe Assets: %s", document)
	}
}

func TestNotchEventMapsKnownPermissionQuestionAndReview(t *testing.T) {
	permissionIntent := core.AttentionNotificationIntent{
		Kind: core.AttentionIntentNeedsInput, Title: "magentic · atlas",
		SessionID: "session-1", DedupeKey: "needs:1",
	}
	permission, ok := notchEventForAttention(permissionIntent, core.ObservationSnapshot{Sessions: []core.SessionObservation{{
		SessionID: "session-1", Attention: core.AttentionNeedsInput, Detail: "Shell-Freigabe",
	}}})
	if !ok || permission.Kind != "permission" || len(permission.Options) != 3 {
		t.Fatalf("permission mapping = %#v, %v", permission, ok)
	}
	if permission.Options[0].ID != "deny" || permission.Options[2].ID != "allow" {
		t.Fatalf("permission options = %#v", permission.Options)
	}

	question, ok := notchEventForAttention(permissionIntent, core.ObservationSnapshot{Sessions: []core.SessionObservation{{
		SessionID: "session-1", Attention: core.AttentionNeedsInput,
	}}})
	if !ok || question.Kind != "question" || len(question.Options) != 2 {
		t.Fatalf("question mapping = %#v, %v", question, ok)
	}

	review, ok := notchEventForAttention(core.AttentionNotificationIntent{
		Kind: core.AttentionIntentSessionComplete, Title: "magentic · atlas", Message: "fertig",
		SessionID: "session-1", DedupeKey: "review:1",
	}, core.ObservationSnapshot{})
	if !ok || review.Kind != "review" || review.Options[1].ID != "open" {
		t.Fatalf("review mapping = %#v, %v", review, ok)
	}
}

func TestValidateNotchEventRejectsUnknownKindsAndDuplicateOptions(t *testing.T) {
	base := NotchEvent{ID: "one", Kind: "question", Title: "Frage", Options: []NotchOption{{ID: "open", Label: "Öffnen"}}}
	if err := validateNotchEvent(base); err != nil {
		t.Fatalf("valid event rejected: %v", err)
	}
	base.Kind = "toast"
	if err := validateNotchEvent(base); err == nil {
		t.Fatal("unknown kind accepted")
	}
	base.Kind = "question"
	base.Options = append(base.Options, base.Options[0])
	if err := validateNotchEvent(base); err == nil {
		t.Fatal("duplicate option accepted")
	}
}
