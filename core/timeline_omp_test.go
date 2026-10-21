package core

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// readOmpFrameFile reads a testdata/timeline/*.ndjson file of plain omp
// frames, one JSON object per line (unlike core/testdata/omp/*.ndjson, which
// wraps each frame in {"dir":..., "frame": ...}).
func readOmpFrameFile(t *testing.T, name string) []OmpFrame {
	t.Helper()
	path := filepath.Join("testdata", "timeline", name)
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var frames []OmpFrame
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		frame, err := DecodeOmpFrame(line)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		frames = append(frames, frame)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return frames
}

// 4.1: an omp ConversationRef resolves through the same registry every other
// vendor's does — providerForVendor(...).Normalizer() — and no second
// registry path exists for it.
func TestOmpConversationRefResolvesThroughProviderRegistry(t *testing.T) {
	ref := ConversationRef{Vendor: AgentVendorOmp, RunID: "run-1"}
	normalizer, reading, ok := normalizerForRef(ref)
	if !ok {
		t.Fatalf("normalizerForRef(omp) nicht aufgelöst: %+v", reading)
	}
	if normalizer.Vendor() != AgentVendorOmp {
		t.Fatalf("normalizer.Vendor() = %q, want %q", normalizer.Vendor(), AgentVendorOmp)
	}
	provider, ok := providerForVendor(AgentVendorOmp)
	if !ok {
		t.Fatal("kein Provider für omp registriert")
	}
	fromProvider, supported := provider.Normalizer()
	if !supported {
		t.Fatal("ompProvider erklärt keinen Normalizer")
	}
	if fromProvider.Vendor() != AgentVendorOmp {
		t.Fatalf("provider.Normalizer().Vendor() = %q, want %q", fromProvider.Vendor(), AgentVendorOmp)
	}
}

// 4.6: Locate finds nothing for a run nobody has persisted anything for yet
// (an available empty Conversation, not a missing record — see
// ConversationReader.Read), and finds the durable file once
// AppendOmpConversationItems has written to it.
func TestOmpConversationNormalizerLocatesTheDurableFileOnceOneExists(t *testing.T) {
	testAgentHostEnv(t)
	var normalizer ConversationNormalizer = ompConversationNormalizer{}
	ref := ConversationRef{Vendor: AgentVendorOmp, RunID: "run-" + NewUUID()[:8]}
	if _, ok := normalizer.Locate(ref, nil); ok {
		t.Error("Locate fand eine Quelle, bevor irgendetwas persistiert wurde")
	}
	if err := AppendOmpConversationItems(ref.RunID, []Item{{ID: "x", Kind: ItemKindAgentMessage}}); err != nil {
		t.Fatal(err)
	}
	sources, ok := normalizer.Locate(ref, nil)
	if !ok || len(sources) != 1 || sources[0].Path != OmpConversationStorePath(ref.RunID) {
		t.Fatalf("sources = %+v, ok = %v, want the durable file this run's own Items were appended to", sources, ok)
	}
	if scan := normalizer.NewScan(); scan == nil {
		t.Error("NewScan liefert keinen Scan")
	}
}

// 4.2: a recorded omp event stream normalizes into the existing Item kinds.
// The fixture is the "in" frames of core/testdata/omp/approval-deny.ndjson,
// real content recorded against omp/18.2.8 (task 2.11's spike).
func TestGoldenOmpConversation(t *testing.T) {
	frames := readOmpFrameFile(t, "omp-run.ndjson")
	scan := NewOmpFrameScan()
	conversation := Conversation{Ref: ConversationRef{Vendor: AgentVendorOmp, RunID: "omp-run"}}
	for _, frame := range frames {
		conversation.Apply(scan.Normalize(frame)...)
	}

	encoded, err := json.MarshalIndent(conversation.Items, "", "  ")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	encoded = append(encoded, '\n')
	golden := filepath.Join("testdata", "timeline", "omp-run.golden.json")
	if *updateGolden {
		if err := os.WriteFile(golden, encoded, 0o644); err != nil {
			t.Fatalf("Golden schreiben: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("Golden lesen: %v (mit -update neu schreiben)", err)
	}
	if string(encoded) != string(want) {
		t.Errorf("die normalisierte Item-Folge weicht vom Golden ab:\n%s", encoded)
	}

	kinds := map[ItemKind]int{}
	failed := 0
	for _, item := range conversation.Items {
		kinds[item.Kind]++
		if item.Failed {
			failed++
		}
	}
	for _, kind := range []ItemKind{ItemKindDeveloperPrompt, ItemKindReasoning, ItemKindFileChange, ItemKindAgentMessage} {
		if kinds[kind] == 0 {
			t.Errorf("das Fixture deckt %q nicht ab", kind)
		}
	}
	if failed == 0 {
		t.Error("das Fixture enthält keinen abgelehnten Werkzeugaufruf")
	}
}

// 4.3: normalizing a delta stream and re-reading it yields identical Item
// identities — no duplication, no mutation of identity.
func TestOmpFrameScanReReadingYieldsIdenticalIdentities(t *testing.T) {
	frames := readOmpFrameFile(t, "omp-run.ndjson")

	first := NewOmpFrameScan()
	firstConversation := Conversation{Ref: ConversationRef{Vendor: AgentVendorOmp, RunID: "omp-run"}}
	for _, frame := range frames {
		firstConversation.Apply(first.Normalize(frame)...)
	}

	second := NewOmpFrameScan()
	secondConversation := Conversation{Ref: ConversationRef{Vendor: AgentVendorOmp, RunID: "omp-run"}}
	for _, frame := range frames {
		secondConversation.Apply(second.Normalize(frame)...)
	}

	if len(firstConversation.Items) != len(secondConversation.Items) {
		t.Fatalf("%d Items bei der ersten Normalisierung, %d bei der zweiten",
			len(firstConversation.Items), len(secondConversation.Items))
	}
	for i, item := range firstConversation.Items {
		if item.ID != secondConversation.Items[i].ID {
			t.Errorf("Item %d: ID = %q, zweite Normalisierung = %q", i, item.ID, secondConversation.Items[i].ID)
		}
	}

	// Feeding the same frames into the already-populated first Conversation a
	// second time must not grow it: Apply is idempotent per identity.
	for _, frame := range frames {
		firstConversation.Apply(first.Normalize(frame)...)
	}
	if len(firstConversation.Items) != len(secondConversation.Items) {
		t.Errorf("erneutes Anwenden derselben Frames hat die Conversation vergrößert: %d statt %d Items",
			len(firstConversation.Items), len(secondConversation.Items))
	}
}

// 4.4: the serving model and provider are carried per Item, not on the
// Conversation or ConversationRef, and a model change mid-run shows up on
// the Items recorded after it.
func TestOmpFrameScanCarriesModelPerItemAcrossAModelChange(t *testing.T) {
	scan := NewOmpFrameScan()
	frame := func(t *testing.T, line string) OmpFrame {
		t.Helper()
		decoded, err := DecodeOmpFrame([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	}

	first := scan.Normalize(frame(t, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Erste Antwort."}],"provider":"anthropic","model":"claude-opus-4","timestamp":1000,"responseId":"resp-1"}}`))
	if len(first) != 1 {
		t.Fatalf("erste Antwort: %d Items, want 1", len(first))
	}
	if first[0].ModelProvider != "anthropic" || first[0].Model != "claude-opus-4" {
		t.Errorf("erstes Item trägt Provider/Model %q/%q, want anthropic/claude-opus-4", first[0].ModelProvider, first[0].Model)
	}

	second := scan.Normalize(frame(t, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Zweite Antwort nach Modellwechsel."}],"provider":"ollama","model":"qwen3.5:9b","timestamp":2000,"responseId":"resp-2"}}`))
	if len(second) != 1 {
		t.Fatalf("zweite Antwort: %d Items, want 1", len(second))
	}
	if second[0].ModelProvider != "ollama" || second[0].Model != "qwen3.5:9b" {
		t.Errorf("zweites Item trägt Provider/Model %q/%q, want ollama/qwen3.5:9b", second[0].ModelProvider, second[0].Model)
	}

	conversation := Conversation{Ref: ConversationRef{Vendor: AgentVendorOmp, RunID: "run-1"}}
	conversation.Apply(first...)
	conversation.Apply(second...)
	if conversation.Ref.Vendor != AgentVendorOmp || conversation.Ref.RunID != "run-1" {
		t.Errorf("die Conversation trägt eine zweite Identität statt einer Referenz: %+v", conversation.Ref)
	}
}

// 4.9: an in-progress assistant message — message_start and message_update
// without message_end — produces no Item. It is normalized once message_end
// arrives and completes it.
func TestOmpFrameScanDefersAnInProgressMessage(t *testing.T) {
	scan := NewOmpFrameScan()
	decode := func(t *testing.T, line string) OmpFrame {
		t.Helper()
		frame, err := DecodeOmpFrame([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		return frame
	}

	start := decode(t, `{"type":"message_start","message":{"role":"assistant","content":[{"type":"thinking","thinking":"Ein"}],"provider":"ollama","model":"qwen3.5:9b","timestamp":1000,"responseId":"resp-1"}}`)
	if items := scan.Normalize(start); len(items) != 0 {
		t.Fatalf("message_start erzeugt %d Items, want 0", len(items))
	}
	update := decode(t, `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Teilantwort"},"message":{"role":"assistant","content":[{"type":"text","text":"Teilantwort"}],"provider":"ollama","model":"qwen3.5:9b","timestamp":1000,"responseId":"resp-1"}}`)
	if items := scan.Normalize(update); len(items) != 0 {
		t.Fatalf("message_update erzeugt %d Items, want 0", len(items))
	}
	// The sequence is truncated here — no message_end arrives in this
	// reading — so nothing has been produced for this message at all.

	end := decode(t, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Vollständige Antwort."}],"provider":"ollama","model":"qwen3.5:9b","timestamp":1000,"responseId":"resp-1"}}`)
	items := scan.Normalize(end)
	if len(items) != 1 {
		t.Fatalf("message_end erzeugt %d Items, want 1", len(items))
	}
	if items[0].Kind != ItemKindAgentMessage || items[0].Title != "Vollständige Antwort." {
		t.Errorf("Item = %+v, want die vollständige Antwort", items[0])
	}
}

// 4.5: subagent activity is attributed to its parent task when the protocol
// stream names one, and marked delegated with an unknown parent when it
// does not. The fixture is synthetic — omp sent no subagent_* frame during
// the 2.11 spike — built from omp's own source (see testdata/timeline/README.md).
func TestGoldenOmpSubagentConversation(t *testing.T) {
	frames := readOmpFrameFile(t, "omp-subagent.ndjson")
	scan := NewOmpFrameScan()
	conversation := Conversation{Ref: ConversationRef{Vendor: AgentVendorOmp, RunID: "omp-subagent"}}
	for _, frame := range frames {
		conversation.Apply(scan.Normalize(frame)...)
	}

	encoded, err := json.MarshalIndent(conversation.Items, "", "  ")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	encoded = append(encoded, '\n')
	golden := filepath.Join("testdata", "timeline", "omp-subagent.golden.json")
	if *updateGolden {
		if err := os.WriteFile(golden, encoded, 0o644); err != nil {
			t.Fatalf("Golden schreiben: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("Golden lesen: %v (mit -update neu schreiben)", err)
	}
	if string(encoded) != string(want) {
		t.Errorf("die normalisierte Item-Folge weicht vom Golden ab:\n%s", encoded)
	}

	var withParent, withoutParent, primary int
	for _, item := range conversation.Items {
		if !item.Delegated {
			primary++
			continue
		}
		if item.ParentTaskID != "" {
			withParent++
		} else {
			withoutParent++
		}
	}
	if withParent == 0 {
		t.Error("das Fixture deckt keine delegierte Aktivität mit bekanntem Elternteil ab")
	}
	if withoutParent == 0 {
		t.Error("das Fixture deckt keine delegierte Aktivität mit unbekanntem Elternteil ab")
	}
	// The delegated-task Item the "task" tool call itself produced is not
	// delegated work — it is what spawned it — and stays primary.
	if primary == 0 {
		t.Error("das Fixture deckt den delegierenden Werkzeugaufruf selbst nicht ab")
	}
}

// 4.7: turns served by different providers are priced separately and summed
// correctly, each attributed to its own provider — never one price list
// applied to all of them.
func TestOmpConversationCostSumsAcrossProviders(t *testing.T) {
	scan := NewOmpFrameScan()
	frame := func(t *testing.T, line string) OmpFrame {
		t.Helper()
		decoded, err := DecodeOmpFrame([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	var items []Item
	items = append(items, scan.Normalize(frame(t, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Antwort eins."}],"provider":"anthropic","model":"claude-opus-4","timestamp":1000,"responseId":"resp-1","usage":{"cost":{"total":0.42}}}}`))...)
	items = append(items, scan.Normalize(frame(t, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Antwort zwei."}],"provider":"openai","model":"gpt-5.2","timestamp":2000,"responseId":"resp-2","usage":{"cost":{"total":0.13}}}}`))...)

	total, complete := OmpConversationCost(items)
	if !complete {
		t.Fatal("want a complete total: every turn reported a determinable cost")
	}
	if want := 0.55; total < want-0.0001 || total > want+0.0001 {
		t.Fatalf("total = %v, want %v", total, want)
	}
}

// 4.7: a turn served by a provider whose cost omp does not report — the
// usage object present, its cost sub-object absent — is excluded from the
// total, which is then reported incomplete rather than treated as $0.
func TestOmpConversationCostIsIncompleteForAnUnpricedProvider(t *testing.T) {
	scan := NewOmpFrameScan()
	frame := func(t *testing.T, line string) OmpFrame {
		t.Helper()
		decoded, err := DecodeOmpFrame([]byte(line))
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	var items []Item
	items = append(items, scan.Normalize(frame(t, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Bezahlte Antwort."}],"provider":"anthropic","model":"claude-opus-4","timestamp":1000,"responseId":"resp-1","usage":{"cost":{"total":1.0}}}}`))...)
	items = append(items, scan.Normalize(frame(t, `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Unbepreiste Antwort."}],"provider":"some-new-provider","model":"unlisted-model","timestamp":2000,"responseId":"resp-2","usage":{"input":10,"output":20}}}`))...)

	total, complete := OmpConversationCost(items)
	if complete {
		t.Fatal("want an incomplete total: one turn's cost could not be determined")
	}
	if total != 1.0 {
		t.Fatalf("total = %v, want only the determinable turn's 1.0 (never guessing the unpriced one as 0)", total)
	}

	var unpriced Item
	for _, item := range items {
		if item.ModelProvider == "some-new-provider" {
			unpriced = item
		}
	}
	if unpriced.CostKnown {
		t.Fatal("an item with no reported cost.total must not read as a known $0")
	}
}

// A message with no usage object at all — a local slash-command style reply,
// say — carries no cost claim either way.
func TestOmpConversationCostLeavesUnreportedUsageUnknown(t *testing.T) {
	scan := NewOmpFrameScan()
	decoded, err := DecodeOmpFrame([]byte(`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Ohne Nutzungsangabe."}],"provider":"anthropic","model":"claude-opus-4","timestamp":1000,"responseId":"resp-1"}}`))
	if err != nil {
		t.Fatal(err)
	}
	items := scan.Normalize(decoded)
	if len(items) != 1 || items[0].CostKnown {
		t.Fatalf("items = %+v, want one item with no known cost", items)
	}
}
