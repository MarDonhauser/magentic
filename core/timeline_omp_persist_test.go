package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ompPersistedSession returns a Session set up the way Provision now sets
// one up: its own ID doubling as the omp run reference, so
// ConversationRefForSession resolves it.
func ompPersistedSession(id SessionID, dir string) Session {
	return Session{ID: id, Name: "orbit", RuntimeName: "mgt-orbit", Dir: dir,
		Runtime: RuntimeOmp, Vendor: AgentVendorOmp, SessionKind: SessionKindCodingAgent,
		AgentRuns: []AgentRunRef{{Vendor: AgentVendorOmp, ExternalID: string(id)}}}
}

// startPersistingOmpHost starts a real scripted host under sessionID and
// drives one prompt/response exchange that normalizes into a developer
// prompt and an agent message, so a test has real durably-appended Items to
// read back.
func startPersistingOmpHost(t *testing.T, sessionID SessionID) *AgentHost {
	t.Helper()
	testAgentHostEnv(t)
	previous := ompBinary
	ompBinary = "sh"
	t.Cleanup(func() { ompBinary = previous })
	previousGetState := ompSendInitialGetState
	ompSendInitialGetState = false
	t.Cleanup(func() { ompSendInitialGetState = previousGetState })

	host, err := StartAgentHost(sessionID, NewAgentHostToken())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { host.Close() })
	script := `echo '` + ompTestReady + `'
IFS= read -r line
echo '{"type":"turn_start"}'
echo '{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"hallo"}]}}'
echo '{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"hallo"}],"timestamp":1000}}'
echo '{"type":"message_start","message":{"role":"assistant","provider":"ollama","model":"qwen3.5:9b"}}'
echo '{"type":"message_end","message":{"role":"assistant","provider":"ollama","model":"qwen3.5:9b","responseId":"resp-1","content":[{"type":"text","text":"Hallo zurück"}]}}'
echo '{"type":"turn_end"}'
echo '{"type":"agent_end","isTerminal":true}'
sleep 30`
	if _, err := host.StartOmpProcess([]string{"-c", script, "sh"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := host.Deliver("msg-1", "hallo"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "das Turn-Ende", func() bool {
		state := host.HostState()
		return state.TurnKnown && !state.Turn.Running
	})
	return host
}

// 4.6/4.8: Items are persisted durably as the host observes them, whether or
// not any interface is presenting the Session — and readable through the
// normal ConversationReader path, exactly like any vendor's own record.
func TestOmpConversationPersistsWithoutBeingWatchedAndReadsBackWatched(t *testing.T) {
	sessionID := SessionID("s" + NewUUID()[:8])
	startPersistingOmpHost(t, sessionID)

	// Persisted before anything ever watches this Session: the durable file
	// itself holds the Items already, independent of presentation.
	path := OmpConversationStorePath(string(sessionID))
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("Konversationsdatei nicht lesbar: %v", err)
	}
	if !strings.Contains(string(data), "hallo") || !strings.Contains(string(data), "Hallo zur") {
		t.Fatalf("persistierte Items = %q, want den Prompt und die Antwort", data)
	}

	session := ompPersistedSession(sessionID, t.TempDir())
	reader := NewConversationReader()
	// Read() without ever calling Watch: publication (a Pass update) is
	// gated by presentation, but Read() itself still ends up reading the
	// file directly on demand.
	reading := reader.Read(session)
	if reading.Availability != ConversationAvailable || reading.Conversation == nil || len(reading.Conversation.Items) == 0 {
		t.Fatalf("reading = %+v, want the persisted Items available on demand", reading)
	}
	foundPrompt, foundReply := false, false
	for _, item := range reading.Conversation.Items {
		if item.Kind == ItemKindDeveloperPrompt {
			foundPrompt = true
		}
		if item.Kind == ItemKindAgentMessage && item.Model == "qwen3.5:9b" && item.ModelProvider == "ollama" {
			foundReply = true
		}
	}
	if !foundPrompt || !foundReply {
		t.Fatalf("items = %+v, want a developer prompt and a model-attributed agent message", reading.Conversation.Items)
	}
}

// 4.6: a Conversation survives a fresh ConversationReader — standing in for
// the daemon restarting, since a new reader starts with no held state — and
// remains identical to what was read before.
func TestOmpConversationSurvivesAFreshReader(t *testing.T) {
	sessionID := SessionID("s" + NewUUID()[:8])
	startPersistingOmpHost(t, sessionID)
	session := ompPersistedSession(sessionID, t.TempDir())

	before := NewConversationReader()
	before.Watch(sessionID)
	before.Pass([]Session{session})
	firstReading := before.Read(session)
	if firstReading.Availability != ConversationAvailable || firstReading.Conversation == nil || len(firstReading.Conversation.Items) == 0 {
		t.Fatalf("erste Lesung = %+v, want verfügbare Items", firstReading)
	}

	// A fresh reader holds nothing yet — the daemon-restart case.
	after := NewConversationReader()
	after.Watch(sessionID)
	after.Pass([]Session{session})
	secondReading := after.Read(session)
	if secondReading.Availability != ConversationAvailable || secondReading.Conversation == nil || len(secondReading.Conversation.Items) != len(firstReading.Conversation.Items) {
		t.Fatalf("nach Neustart = %+v, want dieselben Items wie vorher: %+v", secondReading, firstReading)
	}
	for i := range firstReading.Conversation.Items {
		if firstReading.Conversation.Items[i].ID != secondReading.Conversation.Items[i].ID {
			t.Fatalf("Item-Identitäten verschieden nach Neustart: %+v vs %+v",
				firstReading.Conversation.Items[i], secondReading.Conversation.Items[i])
		}
	}
}

// 4.6/spec "omp's own session files are never read": persistence writes and
// reads exclusively under Magentic's own state directory, never under any
// path naming omp's own agent directory.
func TestOmpConversationStoreNeverTouchesOmpsOwnDirectory(t *testing.T) {
	sessionID := SessionID("s" + NewUUID()[:8])
	home := t.TempDir()
	t.Setenv("HOME", home)

	startPersistingOmpHost(t, sessionID)

	path := OmpConversationStorePath(string(sessionID))
	stateDir := filepath.Dir(StatePath())
	if !strings.HasPrefix(path, stateDir) {
		t.Fatalf("Konversationsdatei %q liegt nicht unter Magentics eigenem State-Verzeichnis %q", path, stateDir)
	}
	if strings.Contains(path, ".omp") {
		t.Fatalf("Konversationsdatei %q verweist auf omps eigenes Verzeichnis", path)
	}

	// Nothing this pipeline did was capable of writing under $HOME/.omp:
	// the scripted stand-in never touches disk on its own, so an empty tree
	// here is exactly what a persistence path that stayed inside Magentic's
	// own state directory would leave behind.
	ompDir := filepath.Join(home, ".omp")
	if entries, err := os.ReadDir(ompDir); err == nil && len(entries) > 0 {
		t.Fatalf("etwas schrieb unter %q: %v", ompDir, entries)
	}
}
