package core

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
)

// OmpConversationStorePath is where an omp Session's durable Conversation
// lives: one JSON-encoded Item per line, appended as the agent host observes
// them. It is keyed by the Session's own run reference (its Session ID —
// see ompProvider.NewRunID), never by omp's own sessionId, and it is
// Magentic's own file: omp's session records are never read (see
// omp-runtime/conversation-normalization).
func OmpConversationStorePath(runID string) string {
	return filepath.Join(filepath.Dir(StatePath()), "omp-conversations", runID+".ndjson")
}

// AppendOmpConversationItems durably appends items to runID's Conversation
// record, each Item as one JSON line. A runID with no items to append does
// nothing. The append happens whether or not any interface is presenting
// the Session — durability does not depend on being watched.
func AppendOmpConversationItems(runID string, items []Item) error {
	if runID == "" || len(items) == 0 {
		return nil
	}
	path := OmpConversationStorePath(runID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	var buffer bytes.Buffer
	for _, item := range items {
		encoded, err := json.Marshal(item)
		if err != nil {
			continue
		}
		buffer.Write(encoded)
		buffer.WriteByte('\n')
	}
	_, err = file.Write(buffer.Bytes())
	return err
}

// ompDurableScan reads the durable file AppendOmpConversationItems writes:
// each line is already one normalized Item, so reading it back needs no
// re-normalization, only a JSON decode per complete line. A trailing partial
// line — a write caught mid-flush — is left unconsumed and read again once
// finished, the same guarantee 4.9 gives a half-formed streamed message.
type ompDurableScan struct{}

func (ompDurableScan) Normalize(_ ConversationSource, data []byte) ([]Item, int) {
	var items []Item
	consumed := 0
	for {
		idx := bytes.IndexByte(data[consumed:], '\n')
		if idx < 0 {
			break
		}
		line := bytes.TrimSpace(data[consumed : consumed+idx])
		consumed += idx + 1
		if len(line) == 0 {
			continue
		}
		var item Item
		if err := json.Unmarshal(line, &item); err != nil {
			// A corrupt line cannot have been produced by
			// AppendOmpConversationItems itself; skip it rather than failing
			// every Item after it.
			continue
		}
		items = append(items, item)
	}
	return items, consumed
}

// OmpConversationCost sums the known per-turn costs across items, attributed
// to whichever provider served each (Item.ModelProvider), never assuming a
// single vendor's price list. complete is false whenever any cost-bearing
// item — one whose Model is set, meaning it was served by some provider —
// had no determinable cost; such an item contributes nothing to total,
// which is therefore an undercount, never a total presented as whole.
func OmpConversationCost(items []Item) (total float64, complete bool) {
	complete = true
	for _, item := range items {
		if item.Model == "" && item.ModelProvider == "" {
			// Not a model-served activity at all (a tool call, a permission
			// decision, …): it carries no cost to account for.
			continue
		}
		if !item.CostKnown {
			complete = false
			continue
		}
		total += item.Cost
	}
	return total, complete
}
