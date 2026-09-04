package core

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"
)

// claudeRecordFile lays out a Claude record where the provider looks for it
// and returns the Session that resolves to it.
func claudeRecordFile(t *testing.T, runID string, lines ...string) (Session, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, ".claude", "projects", "-Users-dev-navi")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(project, runID+".jsonl")
	if err := os.WriteFile(path, claudeRecords(lines...), 0o644); err != nil {
		t.Fatal(err)
	}
	session := Session{
		ID: SessionID("s-" + runID), Name: "navi", RuntimeName: "magentic-navi",
		SessionKind: SessionKindCodingAgent, Vendor: AgentVendorClaude,
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: runID}},
	}
	return session, path
}

func appendRecords(t *testing.T, path string, lines ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write(claudeRecords(lines...)); err != nil {
		t.Fatal(err)
	}
}

func prompt(uuid, text string) string {
	return `{"type":"user","uuid":"` + uuid + `","message":{"role":"user","content":"` + text + `"}}`
}

func TestAGrownRecordContinuesFromItsOffset(t *testing.T) {
	session, path := claudeRecordFile(t, "run-1", prompt("u1", "erste Frage"))
	reader := NewConversationReader()
	reader.Watch(session.ID)

	first := reader.Pass([]Session{session})
	if len(first) != 1 || len(first[0].Items) != 1 {
		t.Fatalf("erste Lesung = %+v, want ein Item", first)
	}

	appendRecords(t, path, prompt("u2", "zweite Frage"))
	second := reader.Pass([]Session{session})
	if len(second) != 1 {
		t.Fatalf("%d Updates, want 1", len(second))
	}
	if second[0].Replaced {
		t.Error("ein gewachsenes Record darf keine vollständige Neulesung auslösen")
	}
	if len(second[0].Items) != 1 || second[0].Items[0].ID != "u2#0" {
		t.Fatalf("die zweite Lesung liefert %+v, want nur das angehängte Item", second[0].Items)
	}

	reading := reader.Read(session)
	if reading.Availability != ConversationAvailable || len(reading.Conversation.Items) != 2 {
		t.Fatalf("die Conversation hält %+v, want beide Items", reading.Conversation)
	}
}

func TestNoAppendedRecordsPublishNothing(t *testing.T) {
	session, _ := claudeRecordFile(t, "run-1", prompt("u1", "erste Frage"))
	reader := NewConversationReader()
	reader.Watch(session.ID)
	reader.Pass([]Session{session})

	if updates := reader.Pass([]Session{session}); len(updates) != 0 {
		t.Fatalf("ein Durchlauf ohne neue Records veröffentlicht %+v, want nichts", updates)
	}
}

func TestAShortenedRecordIsReadInFullAndReplacesWhatWasHeld(t *testing.T) {
	session, path := claudeRecordFile(t, "run-1",
		prompt("u1", "erste Frage"), prompt("u2", "zweite Frage"))
	reader := NewConversationReader()
	reader.Watch(session.ID)
	if updates := reader.Pass([]Session{session}); len(updates[0].Items) != 2 {
		t.Fatalf("erste Lesung = %+v, want zwei Items", updates)
	}

	if err := os.WriteFile(path, claudeRecords(prompt("u3", "ganz neu")), 0o644); err != nil {
		t.Fatal(err)
	}
	updates := reader.Pass([]Session{session})
	if len(updates) != 1 || !updates[0].Replaced {
		t.Fatalf("Updates = %+v, want eine vollständige Neulesung", updates)
	}

	reading := reader.Read(session)
	items := reading.Conversation.Items
	if len(items) != 1 || items[0].ID != "u3#0" {
		t.Fatalf("die Conversation hält %+v, want nur das Item der neuen Lesung", items)
	}
}

func TestASameLengthRecordWithAChangedPrefixIsReadInFull(t *testing.T) {
	session, path := claudeRecordFile(t, "run-1", prompt("u1", "erste Frage"))
	reader := NewConversationReader()
	reader.Watch(session.ID)
	reader.Pass([]Session{session})

	// Same length, different content: only the prefix fingerprint can tell.
	if err := os.WriteFile(path, claudeRecords(prompt("u1", "andere Frage")), 0o644); err != nil {
		t.Fatal(err)
	}
	updates := reader.Pass([]Session{session})
	if len(updates) != 1 || !updates[0].Replaced {
		t.Fatalf("Updates = %+v, want eine vollständige Neulesung", updates)
	}
	if got := updates[0].Items[0].Title; got != "andere Frage" {
		t.Fatalf("Titel = %q, want den Inhalt des neu geschriebenen Records", got)
	}
}

func TestOnlyWatchedSessionsAreRead(t *testing.T) {
	watched, _ := claudeRecordFile(t, "run-1", prompt("u1", "erste Frage"))
	home := os.Getenv("HOME")
	project := filepath.Join(home, ".claude", "projects", "-Users-dev-navi")
	for _, runID := range []string{"run-2", "run-3"} {
		if err := os.WriteFile(filepath.Join(project, runID+".jsonl"),
			claudeRecords(prompt("x1", "andere Session")), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	unwatched := func(runID string) Session {
		return Session{
			ID: SessionID("s-" + runID), Name: runID, RuntimeName: "magentic-" + runID,
			SessionKind: SessionKindCodingAgent, Vendor: AgentVendorClaude,
			AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: runID}},
		}
	}

	reader := NewConversationReader()
	reads := 0
	underlying := reader.readRange
	reader.readRange = func(path string, offset int64) (conversationRange, error) {
		reads++
		return underlying(path, offset)
	}
	reader.Watch(watched.ID)
	reader.Pass([]Session{watched, unwatched("run-2"), unwatched("run-3")})

	if reads != 1 {
		t.Fatalf("%d Records gelesen, want 1 — nur die betrachtete Session", reads)
	}
}

func TestReleasingAWatchDropsTheHeldConversation(t *testing.T) {
	session, _ := claudeRecordFile(t, "run-1", prompt("u1", "erste Frage"))
	reader := NewConversationReader()
	reader.Watch(session.ID)
	reader.Pass([]Session{session})

	reader.Watch()
	if reader.Watching(session.ID) {
		t.Fatal("nach Watch() ohne Session darf nichts mehr betrachtet werden")
	}
	if len(reader.held) != 0 {
		t.Fatalf("der Reader hält weiter %d Conversations", len(reader.held))
	}
}

func TestAPassStartsNoGoroutineAndNoTicker(t *testing.T) {
	session, path := claudeRecordFile(t, "run-1", prompt("u1", "erste Frage"))
	reader := NewConversationReader()
	reader.Watch(session.ID)
	reader.Pass([]Session{session})

	before := runtime.NumGoroutine()
	appendRecords(t, path, prompt("u2", "zweite Frage"))
	reader.Pass([]Session{session})
	reader.Pass([]Session{session})
	if after := runtime.NumGoroutine(); after > before {
		t.Fatalf("%d Goroutinen nach den Durchläufen, vorher %d — der Reader startet keine eigene Schleife", after, before)
	}
}

func TestReadingLeavesTheVendorRecordUntouched(t *testing.T) {
	session, path := claudeRecordFile(t, "run-1",
		prompt("u1", "erste Frage"), prompt("u2", "zweite Frage"))
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// Without tmux on PATH a runtime command could not succeed, so a passing
	// read proves the reader issues none.
	t.Setenv("PATH", t.TempDir())
	time.Sleep(10 * time.Millisecond)

	reader := NewConversationReader()
	reader.Watch(session.ID)
	if updates := reader.Pass([]Session{session}); len(updates) != 1 {
		t.Fatalf("Updates = %+v, want eine Lesung", updates)
	}
	if reading := reader.Read(session); reading.Availability != ConversationAvailable {
		t.Fatalf("Lesung = %+v, want verfügbar", reading)
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() != before.Size() {
		t.Errorf("Größe = %d, want %d", after.Size(), before.Size())
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Errorf("Änderungszeit = %v, want %v", after.ModTime(), before.ModTime())
	}
	if after.Mode() != before.Mode() {
		t.Errorf("Rechte = %v, want %v", after.Mode(), before.Mode())
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(content) {
		t.Error("der Inhalt des Records hat sich verändert")
	}
}

func TestAHalfWrittenTrailingRecordIsNormalizedOnALaterPass(t *testing.T) {
	session, path := claudeRecordFile(t, "run-1", prompt("u1", "erste Frage"))
	reader := NewConversationReader()
	reader.Watch(session.ID)
	reader.Pass([]Session{session})

	partial := `{"type":"user","uuid":"u2","message":{"role":"user","con`
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(partial); err != nil {
		t.Fatal(err)
	}
	file.Close()

	if updates := reader.Pass([]Session{session}); len(updates) != 0 {
		t.Fatalf("ein halb geschriebener Record ergibt %+v, want nichts", updates)
	}

	file, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`tent":"zweite Frage"}}` + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()

	updates := reader.Pass([]Session{session})
	if len(updates) != 1 || len(updates[0].Items) != 1 || updates[0].Items[0].ID != "u2#0" {
		t.Fatalf("Updates = %+v, want genau das nachgetragene Item", updates)
	}
	if reading := reader.Read(session); len(reading.Conversation.Items) != 2 {
		t.Fatalf("die Conversation hält %+v, want zwei Items", reading.Conversation.Items)
	}
}

func TestAMissingRecordIsNotAnEmptyConversation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	session := Session{
		ID: "s1", Name: "navi", SessionKind: SessionKindCodingAgent, Vendor: AgentVendorClaude,
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorClaude, ExternalID: "run-fehlt"}},
	}
	reading := NewConversationReader().Read(session)
	if reading.Availability != ConversationRecordNotFound {
		t.Fatalf("Lesung = %q, want %q", reading.Availability, ConversationRecordNotFound)
	}
	if reading.Conversation != nil {
		t.Error("eine fehlende Aufzeichnung darf keine leere Conversation liefern")
	}
	if reading.Reason == "" {
		t.Error("eine fehlende Aufzeichnung muss ihren Grund nennen")
	}
}

func TestAVendorWithoutANormalizerAnswersExplicitly(t *testing.T) {
	session := Session{
		ID: "s1", Name: "navi", SessionKind: SessionKindCodingAgent, Vendor: AgentVendorCodex,
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorCodex, ExternalID: "run-1"}},
	}
	reading := NewConversationReader().Read(session)
	if reading.Availability != ConversationNoNormalizer {
		t.Fatalf("Lesung = %q, want %q", reading.Availability, ConversationNoNormalizer)
	}
	if reading.Conversation != nil {
		t.Error("ein Vendor ohne Normalizer darf keine leere Conversation liefern")
	}
	if reading.Ref.Vendor != AgentVendorCodex {
		t.Errorf("die Lesung nennt den Vendor nicht: %+v", reading)
	}
}

func TestARunThatHasNotStartedReadsAsEmptyAndAvailable(t *testing.T) {
	session, _ := claudeRecordFile(t, "run-1")
	reader := NewConversationReader()
	reader.Watch(session.ID)
	reading := reader.Read(session)
	if reading.Availability != ConversationAvailable {
		t.Fatalf("Lesung = %q, want %q", reading.Availability, ConversationAvailable)
	}
	if reading.Conversation == nil || len(reading.Conversation.Items) != 0 {
		t.Fatalf("Conversation = %+v, want leer und verfügbar", reading.Conversation)
	}
}

func TestARepeatedPassDoesNotSearchTheVendorStorageAgain(t *testing.T) {
	session, path := claudeRecordFile(t, "run-1", prompt("u1", "erste Frage"))
	reader := NewConversationReader()
	searches := 0
	underlying := reader.locate
	reader.locate = func(normalizer ConversationNormalizer, ref ConversationRef, known []ConversationSource) ([]ConversationSource, bool) {
		if len(known) == 0 {
			searches++
		}
		return underlying(normalizer, ref, known)
	}
	reader.Watch(session.ID)

	reader.Pass([]Session{session})
	appendRecords(t, path, prompt("u2", "zweite Frage"))
	reader.Pass([]Session{session})
	reader.Pass([]Session{session})

	if searches != 1 {
		t.Fatalf("%d Suchläufe über die Ablage des Vendors, want 1 — spätere Durchläufe bestätigen nur", searches)
	}
}

func TestApplyingALongConversationStaysLinear(t *testing.T) {
	conversation := Conversation{}
	items := make([]Item, 20000)
	for i := range items {
		items[i] = Item{ID: "id-" + strconv.Itoa(i), Kind: ItemKindAgentMessage, Title: "x"}
	}
	conversation.Apply(items...)
	if len(conversation.Items) != len(items) {
		t.Fatalf("Conversation hält %d Items, want %d", len(conversation.Items), len(items))
	}
	// Erneutes Anwenden derselben Items ersetzt an Ort und Stelle statt anzuhängen.
	conversation.Apply(items...)
	if len(conversation.Items) != len(items) {
		t.Fatalf("erneutes Anwenden wächst auf %d Items", len(conversation.Items))
	}
}
