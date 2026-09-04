package core

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type historyProviderAdapter interface {
	Provider() HistoryProvider
	Version() int
	Roots() []string
	Accept(path string) bool
	Fingerprint(files workHistoryFS, path string, data []byte) (string, error)
	Parse(context.Context, workHistoryFS, string, []byte) ([]historyRecord, []HistoryProblem, error)
}

func builtinHistoryAdapters(config WorkHistoryConfig) []historyProviderAdapter {
	return []historyProviderAdapter{
		claudeHistoryAdapter{root: filepath.Join(config.HomeDir, ".claude", "projects")},
		codexHistoryAdapter{roots: []string{
			filepath.Join(config.CodexHome, "sessions"),
			filepath.Join(config.CodexHome, "archived_sessions"),
		}},
		geminiHistoryAdapter{root: filepath.Join(config.HomeDir, ".gemini", "tmp")},
		copilotHistoryAdapter{root: filepath.Join(config.HomeDir, ".copilot", "session-state")},
		antigravityHistoryAdapter{
			root:        filepath.Join(config.HomeDir, ".gemini", "antigravity-cli", "brain"),
			historyPath: filepath.Join(config.HomeDir, ".gemini", "antigravity-cli", "history.jsonl"),
		},
	}
}

type historyDiscovery struct {
	files    []string
	coverage HistoryProviderCoverage
}

func discoverHistoryFiles(ctx context.Context, files workHistoryFS, adapter historyProviderAdapter) historyDiscovery {
	coverage := HistoryProviderCoverage{Provider: adapter.Provider(), State: HistorySourceAbsent}
	seen := map[string]bool{}
	presentRoots, failedRoots := 0, 0
	for _, root := range adapter.Roots() {
		if err := ctx.Err(); err != nil {
			coverage.Problems = append(coverage.Problems, HistoryProblem{Provider: adapter.Provider(), Kind: "cancelled", Message: err.Error()})
			failedRoots++
			continue
		}
		entryInfo, entryErr := files.Lstat(root)
		if entryErr == nil && entryInfo.Mode()&fs.ModeSymlink != 0 {
			failedRoots++
			coverage.Problems = append(coverage.Problems, HistoryProblem{
				Provider: adapter.Provider(), Kind: "source-symlink", Message: filepath.Base(root),
			})
			continue
		}
		if entryErr != nil && !os.IsNotExist(entryErr) {
			failedRoots++
			coverage.Problems = append(coverage.Problems, HistoryProblem{Provider: adapter.Provider(), Kind: "source-unavailable", Message: entryErr.Error()})
			continue
		}
		info, err := files.Stat(root)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			failedRoots++
			coverage.Problems = append(coverage.Problems, HistoryProblem{Provider: adapter.Provider(), Kind: "source-unavailable", Message: err.Error()})
			continue
		}
		if !info.IsDir() {
			failedRoots++
			coverage.Problems = append(coverage.Problems, HistoryProblem{Provider: adapter.Provider(), Kind: "source-not-directory", Message: filepath.Base(root)})
			continue
		}
		presentRoots++
		walkErr := files.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if walkErr != nil {
				coverage.Problems = append(coverage.Problems, HistoryProblem{Provider: adapter.Provider(), Kind: "source-entry-unavailable", Message: walkErr.Error()})
				if entry != nil && entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				coverage.Problems = append(coverage.Problems, HistoryProblem{
					Provider: adapter.Provider(), Kind: "source-symlink-entry", Message: filepath.Base(path),
				})
				return nil
			}
			if entry.IsDir() || !adapter.Accept(path) {
				return nil
			}
			clean, err := filepath.Abs(path)
			if err == nil {
				path = filepath.Clean(clean)
			}
			if !seen[path] {
				seen[path] = true
				coverage.Files++
			}
			return nil
		})
		if walkErr != nil {
			failedRoots++
			coverage.Problems = append(coverage.Problems, HistoryProblem{Provider: adapter.Provider(), Kind: "source-walk-failed", Message: walkErr.Error()})
		}
	}
	result := make([]string, 0, len(seen))
	for path := range seen {
		result = append(result, path)
	}
	sort.Strings(result)
	switch {
	case presentRoots == 0 && failedRoots == 0:
		coverage.State = HistorySourceAbsent
	case presentRoots == 0:
		coverage.State = HistorySourceUnavailable
	case failedRoots > 0 || len(coverage.Problems) > 0:
		coverage.State = HistorySourcePartial
	default:
		coverage.State = HistorySourceAvailable
	}
	return historyDiscovery{files: result, coverage: coverage}
}

func basicHistoryFingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func historyJSONLines(data []byte, visit func([]byte, int)) error {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 1<<20), 32<<20)
	line := 0
	for scanner.Scan() {
		line++
		if trimmed := bytes.TrimSpace(scanner.Bytes()); len(trimmed) > 0 {
			visit(trimmed, line)
		}
	}
	return scanner.Err()
}

func malformedHistoryProblem(count int) []HistoryProblem {
	if count == 0 {
		return nil
	}
	return []HistoryProblem{{Kind: "malformed-records", Message: fmt.Sprintf("%d malformed record(s) ignored", count)}}
}

var (
	historyImageWrapper = regexp.MustCompile(`(?m)^</?image(?:\s[^>]*)?>\s*$`)
	historyImagePrefix  = regexp.MustCompile(`^(?:\[(?:Image|File) #[^]]+\]\s*)+`)
	historyReminder     = regexp.MustCompile(`(?s)<system_reminder>.*?</system_reminder>`)
	historyURLPattern   = regexp.MustCompile(`https?://[^\s<>"'\x60)\]}]+`)
)

func cleanHistoryText(text string) string {
	text = historyReminder.ReplaceAllString(text, "")
	text = historyImageWrapper.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	text = historyImagePrefix.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func extractHistoryLinks(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, match := range historyURLPattern.FindAllString(text, -1) {
		match = strings.TrimRight(match, ".,;:!?*…")
		withoutScheme := strings.TrimPrefix(strings.TrimPrefix(match, "https://"), "http://")
		if len(withoutScheme) < 3 || seen[match] {
			continue
		}
		seen[match] = true
		out = append(out, match)
	}
	return out
}

func historyInjectedText(text string) bool {
	text = strings.TrimSpace(text)
	for _, prefix := range []string{
		"# AGENTS.md instructions",
		"<environment_context>",
		"<permissions instructions>",
		"<collaboration_mode>",
		"<skills_instructions>",
		"<apps_instructions>",
		"<plugins_instructions>",
		"<multi_agent_mode>",
		"<command-name>",
		"<command-message>",
		"<local-command-stdout>",
		"<local-command-stderr>",
	} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return strings.HasPrefix(text, "[Request interrupted") || strings.HasPrefix(text, "Caveat:")
}

func historyExtractText(raw json.RawMessage) string {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return ""
	}
	if raw[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			return text
		}
		return ""
	}
	var blocks []struct {
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var parts []string
		for _, block := range blocks {
			if block.Text != "" && (block.Type == "text" || block.Type == "input_text" || block.Type == "output_text" || block.Type == "") {
				parts = append(parts, block.Text)
			} else if len(block.Content) > 0 {
				if text := historyExtractText(block.Content); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) == nil {
		for _, key := range []string{"text", "content", "message"} {
			if value := historyExtractText(object[key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func historyUsageFromRaw(raw json.RawMessage) (historyUsageRecord, bool) {
	if len(raw) == 0 {
		return historyUsageRecord{}, false
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return historyUsageRecord{}, true
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return historyUsageRecord{}, true
	}
	return historyUsageFromValue(value)
}

func historyUsageFromValue(value any) (historyUsageRecord, bool) {
	input, inputOK, inputInvalid := historyFindNumber(value, "input_tokens", "inputTokens", "promptTokenCount", "prompt_tokens")
	output, outputOK, outputInvalid := historyFindNumber(value, "output_tokens", "outputTokens", "candidatesTokenCount", "completion_tokens")
	cacheRead, cacheReadOK, cacheReadInvalid := historyFindNumber(value, "cache_read_input_tokens", "cacheReadTokens", "cached_input_tokens", "cachedContentTokenCount")
	cacheWrite, cacheWriteOK, cacheWriteInvalid := historyFindNumber(value, "cache_creation_input_tokens", "cacheWriteTokens")
	return historyUsageRecord{
		Input: input, InputKnown: inputOK,
		Output: output, OutputKnown: outputOK,
		CacheRead: cacheRead, CacheReadKnown: cacheReadOK,
		CacheWrite: cacheWrite, CacheWriteKnown: cacheWriteOK,
	}, inputInvalid || outputInvalid || cacheReadInvalid || cacheWriteInvalid
}

func historyFindNumber(value any, keys ...string) (int64, bool, bool) {
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	var values []int64
	invalid := false
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for key, child := range typed {
				if wanted[key] {
					if number, ok := historyNumber(child); ok {
						values = append(values, number)
					} else {
						invalid = true
					}
				}
			}
			for _, child := range typed {
				walk(child)
			}
		case []any:
			for _, child := range typed {
				walk(child)
			}
		}
	}
	walk(value)
	if invalid {
		return 0, false, true
	}
	if len(values) == 0 {
		return 0, false, false
	}
	valueAt := values[0]
	for _, candidate := range values[1:] {
		if candidate != valueAt {
			return 0, false, true
		}
	}
	return valueAt, true, false
}

func historyNumber(value any) (int64, bool) {
	switch typed := value.(type) {
	case json.Number:
		n, err := typed.Int64()
		if err == nil && n >= 0 {
			return n, true
		}
	case float64:
		if typed >= 0 && typed < math.Exp2(63) && !math.IsInf(typed, 0) && !math.IsNaN(typed) && math.Trunc(typed) == typed {
			return int64(typed), true
		}
	case int64:
		if typed >= 0 {
			return typed, true
		}
	case int:
		if typed >= 0 {
			return int64(typed), true
		}
	}
	return 0, false
}

func historyRawAt(raw json.RawMessage, path ...string) json.RawMessage {
	current := raw
	for _, key := range path {
		var object map[string]json.RawMessage
		if json.Unmarshal(current, &object) != nil {
			return nil
		}
		current = object[key]
		if len(current) == 0 {
			return nil
		}
	}
	return current
}

func historyStringAt(raw json.RawMessage, path ...string) string {
	value := historyRawAt(raw, path...)
	var text string
	_ = json.Unmarshal(value, &text)
	return text
}

func historyBoolishParent(raw json.RawMessage) bool {
	value := strings.TrimSpace(string(raw))
	return value != "" && value != "null" && value != "0" && value != `""` && value != `"0"`
}

// Claude Code

type claudeHistoryAdapter struct{ root string }

func (a claudeHistoryAdapter) Provider() HistoryProvider { return HistoryProviderClaude }
func (a claudeHistoryAdapter) Version() int              { return 1 }
func (a claudeHistoryAdapter) Roots() []string           { return []string{a.root} }
func (a claudeHistoryAdapter) Accept(path string) bool   { return strings.HasSuffix(path, ".jsonl") }
func (a claudeHistoryAdapter) Fingerprint(_ workHistoryFS, _ string, data []byte) (string, error) {
	return basicHistoryFingerprint(data), nil
}

func (a claudeHistoryAdapter) Parse(ctx context.Context, _ workHistoryFS, path string, data []byte) ([]historyRecord, []HistoryProblem, error) {
	conversation := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	alias := claudeProjectAlias(path, a.root)
	malformed := 0
	var records []historyRecord
	err := historyJSONLines(data, func(line []byte, _ int) {
		if ctx.Err() != nil {
			return
		}
		entry, ok := decodeClaudeRecord(line)
		if !ok {
			malformed++
			return
		}
		if entry.SessionID != "" {
			conversation = entry.SessionID
		}
		lineage := HistoryLineagePrimary
		if entry.IsSidechain || strings.Contains(path, string(filepath.Separator)+"subagents"+string(filepath.Separator)) {
			lineage = HistoryLineageDelegated
		}
		switch entry.Type {
		case "user":
			if entry.IsMeta || len(entry.ToolUseResult) > 0 {
				return
			}
			blocks, plain := claudeTextBlocks(entry.Content)
			text := plain
			if blocks != nil {
				text = claudeJoinText(blocks)
			}
			text = cleanHistoryText(text)
			if text == "" || historyInjectedText(text) {
				return
			}
			records = append(records, historyRecord{
				ConversationID: conversation, Timestamp: entry.Timestamp,
				Role: HistoryRoleDeveloper, Kind: HistoryEventPrompt, Lineage: lineage,
				Text: text, CWD: entry.CWD, ProjectAlias: alias, NativeID: entry.UUID,
			})
		case "assistant":
			blocks, plain := claudeTextBlocks(entry.Content)
			text := plain
			if blocks != nil {
				text = claudeJoinText(blocks)
			}
			text = cleanHistoryText(text)
			usage, usageInvalid := historyUsageFromRaw(entry.Usage)
			if usageInvalid {
				malformed++
			}
			if text == "" && !usage.anyKnown() {
				return
			}
			kind := HistoryEventOutput
			if text == "" {
				kind = HistoryEventUsage
			}
			records = append(records, historyRecord{
				ConversationID: conversation, Timestamp: entry.Timestamp,
				Role: HistoryRoleAssistant, Kind: kind, Lineage: lineage,
				Text: text, Model: entry.Model, Usage: usage,
				CWD: entry.CWD, ProjectAlias: alias, NativeID: entry.UUID,
			})
		}
	})
	if err != nil {
		return nil, malformedHistoryProblem(malformed), err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return records, malformedHistoryProblem(malformed), nil
}

func claudeProjectAlias(path, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) > 1 {
		return parts[0]
	}
	return ""
}

// Codex

type codexHistoryAdapter struct{ roots []string }

func (a codexHistoryAdapter) Provider() HistoryProvider { return HistoryProviderCodex }
func (a codexHistoryAdapter) Version() int              { return 1 }
func (a codexHistoryAdapter) Roots() []string           { return append([]string(nil), a.roots...) }
func (a codexHistoryAdapter) Accept(path string) bool   { return strings.HasSuffix(path, ".jsonl") }
func (a codexHistoryAdapter) Fingerprint(_ workHistoryFS, _ string, data []byte) (string, error) {
	return basicHistoryFingerprint(data), nil
}

type codexHistoryLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

func (a codexHistoryAdapter) Parse(ctx context.Context, _ workHistoryFS, path string, data []byte) ([]historyRecord, []HistoryProblem, error) {
	conversation := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	var cwd, model string
	malformed := 0
	var records []historyRecord
	err := historyJSONLines(data, func(line []byte, _ int) {
		if ctx.Err() != nil {
			return
		}
		var entry codexHistoryLine
		if json.Unmarshal(line, &entry) != nil {
			malformed++
			return
		}
		payloadType := historyStringAt(entry.Payload, "type")
		if entry.Type == "session_meta" {
			if value := historyStringAt(entry.Payload, "cwd"); value != "" {
				cwd = value
			}
			conversation = firstHistoryString(
				historyStringAt(entry.Payload, "session_id"),
				historyStringAt(entry.Payload, "id"), conversation,
			)
			if value := historyStringAt(entry.Payload, "model"); value != "" {
				model = value
			}
			return
		}
		if payloadType == "turn_context" {
			if value := historyStringAt(entry.Payload, "cwd"); value != "" {
				cwd = value
			}
			if value := historyStringAt(entry.Payload, "model"); value != "" {
				model = value
			}
			return
		}
		if value := historyStringAt(entry.Payload, "model"); value != "" {
			model = value
		}
		switch {
		case entry.Type == "event_msg" && payloadType == "user_message":
			text := cleanHistoryText(historyStringAt(entry.Payload, "message"))
			if text != "" && !historyInjectedText(text) {
				records = append(records, historyRecord{ConversationID: conversation, Timestamp: entry.Timestamp, Role: HistoryRoleDeveloper, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary, Text: text, CWD: cwd})
			}
		case entry.Type == "event_msg" && payloadType == "agent_message":
			text := cleanHistoryText(historyStringAt(entry.Payload, "message"))
			if text != "" {
				records = append(records, historyRecord{ConversationID: conversation, Timestamp: entry.Timestamp, Role: HistoryRoleAssistant, Kind: HistoryEventOutput, Lineage: HistoryLineagePrimary, Text: text, Model: model, CWD: cwd})
			}
		case entry.Type == "response_item" && payloadType == "message":
			role := historyStringAt(entry.Payload, "role")
			content := codexHistoryContent(historyRawAt(entry.Payload, "content"))
			if content == "" {
				return
			}
			if role == "user" {
				records = append(records, historyRecord{ConversationID: conversation, Timestamp: entry.Timestamp, Role: HistoryRoleDeveloper, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary, Text: content, CWD: cwd})
			} else if role == "assistant" {
				records = append(records, historyRecord{ConversationID: conversation, Timestamp: entry.Timestamp, Role: HistoryRoleAssistant, Kind: HistoryEventOutput, Lineage: HistoryLineagePrimary, Text: content, Model: model, CWD: cwd})
			}
		case entry.Type == "event_msg" && payloadType == "token_count":
			usageRaw := historyRawAt(entry.Payload, "info", "last_token_usage")
			usage, usageInvalid := historyUsageFromRaw(usageRaw)
			if usageInvalid {
				malformed++
			}
			if usage.anyKnown() {
				records = append(records, historyRecord{ConversationID: conversation, Timestamp: entry.Timestamp, Role: HistoryRoleAssistant, Kind: HistoryEventUsage, Lineage: HistoryLineagePrimary, Model: model, Usage: usage, CWD: cwd})
			}
		}
	})
	if err != nil {
		return nil, malformedHistoryProblem(malformed), err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return records, malformedHistoryProblem(malformed), nil
}

func codexHistoryContent(raw json.RawMessage) string {
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return cleanHistoryText(historyExtractText(raw))
	}
	var parts []string
	for _, block := range blocks {
		if block.Type != "input_text" && block.Type != "output_text" && block.Type != "text" {
			continue
		}
		text := cleanHistoryText(block.Text)
		if text != "" && !historyInjectedText(text) {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// Gemini CLI

type geminiHistoryAdapter struct{ root string }

func (a geminiHistoryAdapter) Provider() HistoryProvider { return HistoryProviderGemini }
func (a geminiHistoryAdapter) Version() int              { return 1 }
func (a geminiHistoryAdapter) Roots() []string           { return []string{a.root} }
func (a geminiHistoryAdapter) Accept(path string) bool {
	base := filepath.Base(path)
	return base == "logs.json" || strings.HasPrefix(base, "session-") && (strings.HasSuffix(base, ".json") || strings.HasSuffix(base, ".jsonl"))
}
func (a geminiHistoryAdapter) Fingerprint(_ workHistoryFS, _ string, data []byte) (string, error) {
	return basicHistoryFingerprint(data), nil
}

type geminiHistoryMessage struct {
	ID            string          `json:"id"`
	SessionID     string          `json:"sessionId"`
	Type          string          `json:"type"`
	Role          string          `json:"role"`
	Timestamp     string          `json:"timestamp"`
	Content       json.RawMessage `json:"content"`
	Message       json.RawMessage `json:"message"`
	Model         string          `json:"model"`
	Usage         json.RawMessage `json:"usage"`
	UsageMetadata json.RawMessage `json:"usageMetadata"`
}

func (a geminiHistoryAdapter) Parse(ctx context.Context, _ workHistoryFS, path string, data []byte) ([]historyRecord, []HistoryProblem, error) {
	conversation := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	alias := geminiHistoryProjectAlias(path, a.root)
	var messages []geminiHistoryMessage
	malformed := 0
	if strings.HasSuffix(path, ".jsonl") {
		err := historyJSONLines(data, func(line []byte, _ int) {
			if ctx.Err() != nil {
				return
			}
			var message geminiHistoryMessage
			if json.Unmarshal(line, &message) != nil {
				malformed++
				return
			}
			if message.SessionID != "" {
				conversation = message.SessionID
			}
			if message.Type != "" || message.Role != "" {
				messages = append(messages, message)
			}
		})
		if err != nil {
			return nil, malformedHistoryProblem(malformed), err
		}
	} else {
		var list []geminiHistoryMessage
		if json.Unmarshal(data, &list) == nil {
			messages = list
		} else {
			var document struct {
				SessionID string                 `json:"sessionId"`
				Messages  []geminiHistoryMessage `json:"messages"`
			}
			if err := json.Unmarshal(data, &document); err != nil {
				return nil, nil, err
			}
			if document.SessionID != "" {
				conversation = document.SessionID
			}
			messages = document.Messages
		}
	}
	var records []historyRecord
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if message.SessionID != "" {
			conversation = message.SessionID
		}
		role := firstHistoryString(message.Type, message.Role)
		text := cleanHistoryText(historyExtractText(message.Content))
		if text == "" {
			text = cleanHistoryText(historyExtractText(message.Message))
		}
		usage, usageInvalid := historyUsageFromRaw(message.UsageMetadata)
		if !usage.anyKnown() && !usageInvalid {
			usage, usageInvalid = historyUsageFromRaw(message.Usage)
		}
		if usageInvalid {
			malformed++
		}
		switch role {
		case "user":
			if text != "" && !historyInjectedText(text) {
				records = append(records, historyRecord{ConversationID: conversation, Timestamp: message.Timestamp, Role: HistoryRoleDeveloper, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary, Text: text, ProjectAlias: alias, NativeID: message.ID})
			}
		case "gemini", "model", "assistant":
			if text == "" && !usage.anyKnown() {
				continue
			}
			kind := HistoryEventOutput
			if text == "" {
				kind = HistoryEventUsage
			}
			records = append(records, historyRecord{ConversationID: conversation, Timestamp: message.Timestamp, Role: HistoryRoleAssistant, Kind: kind, Lineage: HistoryLineagePrimary, Text: text, Model: message.Model, Usage: usage, ProjectAlias: alias, NativeID: message.ID})
		}
	}
	return records, malformedHistoryProblem(malformed), nil
}

func geminiHistoryProjectAlias(path, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) > 1 {
		return parts[0]
	}
	return ""
}

// GitHub Copilot

type copilotHistoryAdapter struct{ root string }

func (a copilotHistoryAdapter) Provider() HistoryProvider { return HistoryProviderCopilot }
func (a copilotHistoryAdapter) Version() int              { return 1 }
func (a copilotHistoryAdapter) Roots() []string           { return []string{a.root} }
func (a copilotHistoryAdapter) Accept(path string) bool   { return filepath.Base(path) == "events.jsonl" }
func (a copilotHistoryAdapter) Fingerprint(files workHistoryFS, path string, data []byte) (string, error) {
	sidecar, err := files.ReadFile(filepath.Join(filepath.Dir(path), "workspace.yaml"))
	if err != nil && !os.IsNotExist(err) {
		return "", err
	}
	sum := sha256.New()
	_, _ = sum.Write(data)
	_, _ = sum.Write([]byte("\x00workspace\x00"))
	_, _ = sum.Write(sidecar)
	return hex.EncodeToString(sum.Sum(nil)), nil
}

type copilotHistoryLine struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Data      struct {
		Content           string          `json:"content"`
		Transformed       string          `json:"transformedContent"`
		ParentAgentTaskID json.RawMessage `json:"parentAgentTaskId"`
		Model             string          `json:"model"`
		Usage             json.RawMessage `json:"usage"`
	} `json:"data"`
}

func (a copilotHistoryAdapter) Parse(ctx context.Context, files workHistoryFS, path string, data []byte) ([]historyRecord, []HistoryProblem, error) {
	conversation := filepath.Base(filepath.Dir(path))
	cwd := readHistoryYAMLValue(files, filepath.Join(filepath.Dir(path), "workspace.yaml"), "cwd")
	malformed := 0
	var records []historyRecord
	err := historyJSONLines(data, func(line []byte, _ int) {
		if ctx.Err() != nil {
			return
		}
		var entry copilotHistoryLine
		if json.Unmarshal(line, &entry) != nil {
			malformed++
			return
		}
		lineage := HistoryLineagePrimary
		if historyBoolishParent(entry.Data.ParentAgentTaskID) {
			lineage = HistoryLineageDelegated
		}
		text := entry.Data.Content
		if text == "" {
			text = entry.Data.Transformed
		}
		text = cleanHistoryText(text)
		switch entry.Type {
		case "user.message":
			if text != "" && !historyInjectedText(text) {
				records = append(records, historyRecord{ConversationID: conversation, Timestamp: entry.Timestamp, Role: HistoryRoleDeveloper, Kind: HistoryEventPrompt, Lineage: lineage, Text: text, CWD: cwd, NativeID: entry.ID})
			}
		case "assistant.message", "assistant.response":
			usage, usageInvalid := historyUsageFromRaw(entry.Data.Usage)
			if usageInvalid {
				malformed++
			}
			if text == "" && !usage.anyKnown() {
				return
			}
			kind := HistoryEventOutput
			if text == "" {
				kind = HistoryEventUsage
			}
			records = append(records, historyRecord{ConversationID: conversation, Timestamp: entry.Timestamp, Role: HistoryRoleAssistant, Kind: kind, Lineage: lineage, Text: text, Model: entry.Data.Model, Usage: usage, CWD: cwd, NativeID: entry.ID})
		}
	})
	if err != nil {
		return nil, malformedHistoryProblem(malformed), err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return records, malformedHistoryProblem(malformed), nil
}

func readHistoryYAMLValue(files workHistoryFS, path, key string) string {
	data, err := files.ReadFile(path)
	if err != nil {
		return ""
	}
	prefix := key + ":"
	for _, rawLine := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, prefix))
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
		return value
	}
	return ""
}

func firstHistoryString(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// Antigravity CLI (agy, Google)

type antigravityHistoryAdapter struct {
	root        string
	historyPath string
}

func (a antigravityHistoryAdapter) Provider() HistoryProvider { return HistoryProviderAntigravity }
func (a antigravityHistoryAdapter) Version() int              { return 1 }
func (a antigravityHistoryAdapter) Roots() []string           { return []string{a.root} }

// Only transcript.jsonl is indexed. transcript_full.jsonl carries the same
// steps with differently quoted tool arguments and would double every event.
func (a antigravityHistoryAdapter) Accept(path string) bool {
	return filepath.Base(path) == "transcript.jsonl"
}

func (a antigravityHistoryAdapter) Fingerprint(files workHistoryFS, path string, data []byte) (string, error) {
	workspace, err := antigravitySidecarWorkspace(files, a.historyPath, antigravityConversationID(path, a.root))
	if err != nil {
		return "", err
	}
	sum := sha256.New()
	_, _ = sum.Write(data)
	_, _ = sum.Write([]byte("\x00workspace\x00"))
	_, _ = sum.Write([]byte(workspace))
	return hex.EncodeToString(sum.Sum(nil)), nil
}

type antigravityToolCall struct {
	Name string                     `json:"name"`
	Args map[string]json.RawMessage `json:"args"`
}

type antigravityHistoryLine struct {
	StepIndex int                   `json:"step_index"`
	Source    string                `json:"source"`
	Type      string                `json:"type"`
	CreatedAt string                `json:"created_at"`
	Content   string                `json:"content"`
	ToolCalls []antigravityToolCall `json:"tool_calls"`
}

func (a antigravityHistoryAdapter) Parse(ctx context.Context, files workHistoryFS, path string, data []byte) ([]historyRecord, []HistoryProblem, error) {
	conversation := antigravityConversationID(path, a.root)
	var entries []antigravityHistoryLine
	malformed := 0
	err := historyJSONLines(data, func(line []byte, _ int) {
		if ctx.Err() != nil {
			return
		}
		var entry antigravityHistoryLine
		if json.Unmarshal(line, &entry) != nil {
			malformed++
			return
		}
		entries = append(entries, entry)
	})
	if err != nil {
		return nil, malformedHistoryProblem(malformed), err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	cwd, _ := antigravitySidecarWorkspace(files, a.historyPath, conversation)
	if cwd == "" {
		for _, entry := range entries {
			if cwd = antigravityTranscriptCWD(entry.ToolCalls); cwd != "" {
				break
			}
		}
	}
	var records []historyRecord
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		switch {
		case entry.Source == "USER_EXPLICIT" && entry.Type == "USER_INPUT":
			text := cleanHistoryText(antigravityPromptText(entry.Content))
			if text == "" || historyInjectedText(text) {
				continue
			}
			records = append(records, historyRecord{
				ConversationID: conversation, Timestamp: entry.CreatedAt,
				Role: HistoryRoleDeveloper, Kind: HistoryEventPrompt, Lineage: HistoryLineagePrimary,
				Text: text, CWD: cwd, NativeID: "step-" + strconv.Itoa(entry.StepIndex),
			})
		case entry.Source == "MODEL" && entry.Type == "PLANNER_RESPONSE":
			// Other MODEL types (VIEW_FILE, LIST_DIRECTORY, RUN_COMMAND and
			// friends) carry tool results, not assistant prose, and stay out
			// of the index just like tool-only Claude messages do.
			text := cleanHistoryText(entry.Content)
			if text == "" {
				continue
			}
			records = append(records, historyRecord{
				ConversationID: conversation, Timestamp: entry.CreatedAt,
				Role: HistoryRoleAssistant, Kind: HistoryEventOutput, Lineage: HistoryLineagePrimary,
				Text: text, CWD: cwd, NativeID: "step-" + strconv.Itoa(entry.StepIndex),
			})
		}
	}
	return records, malformedHistoryProblem(malformed), nil
}

func antigravityConversationID(path, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) > 1 {
		return parts[0]
	}
	return ""
}

var (
	antigravityUserRequest    = regexp.MustCompile(`(?s)<USER_REQUEST>(.*?)</USER_REQUEST>`)
	antigravityMetadataBlocks = regexp.MustCompile(`(?s)<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>|<USER_SETTINGS_CHANGE>.*?</USER_SETTINGS_CHANGE>`)
)

// antigravityPromptText keeps the user's own request and drops the envelope
// agy wraps it in: local time metadata and model-selection change notes that
// would otherwise pollute every prompt.
func antigravityPromptText(content string) string {
	if match := antigravityUserRequest.FindStringSubmatch(content); match != nil {
		return strings.TrimSpace(match[1])
	}
	return strings.TrimSpace(antigravityMetadataBlocks.ReplaceAllString(content, ""))
}

// antigravitySidecarWorkspace resolves the conversation's working directory
// from the history.jsonl log next to the brain directory. A missing log means
// sessions that never reported a workspace, not an error; any other read
// failure must surface so a half-read mapping is never trusted silently.
func antigravitySidecarWorkspace(files workHistoryFS, historyPath, conversation string) (string, error) {
	if strings.TrimSpace(conversation) == "" || strings.TrimSpace(historyPath) == "" {
		return "", nil
	}
	data, err := files.ReadFile(historyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	workspace := ""
	scanErr := historyJSONLines(data, func(line []byte, _ int) {
		if workspace != "" {
			return
		}
		var entry struct {
			ConversationID string `json:"conversationId"`
			Workspace      string `json:"workspace"`
		}
		if json.Unmarshal(line, &entry) != nil {
			return
		}
		if entry.ConversationID == conversation && strings.TrimSpace(entry.Workspace) != "" {
			workspace = strings.TrimSpace(entry.Workspace)
		}
	})
	if scanErr != nil {
		return "", scanErr
	}
	return workspace, nil
}

// antigravityTranscriptCWD falls back to the paths the transcript itself
// names when the sidecar never logged the conversation. run_command carries
// its own Cwd; the first listing or search path names the workspace root the
// session started in.
func antigravityTranscriptCWD(calls []antigravityToolCall) string {
	var dir, search string
	for _, call := range calls {
		switch call.Name {
		case "run_command":
			if value := antigravityArgString(call.Args["Cwd"]); value != "" {
				return value
			}
		case "list_dir":
			if dir == "" {
				dir = antigravityArgString(call.Args["DirectoryPath"])
			}
		case "grep_search":
			if search == "" {
				search = antigravityArgString(call.Args["SearchPath"])
			}
		}
	}
	return firstHistoryString(dir, search)
}

// antigravityArgString reads one tool argument. transcript.jsonl quotes path
// arguments one layer too many ("/work/demo" instead of /work/demo), so a
// single surrounding double-quote pair is removed.
func antigravityArgString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return ""
	}
	text = strings.TrimSpace(text)
	if len(text) >= 2 && strings.HasPrefix(text, `"`) && strings.HasSuffix(text, `"`) {
		if unquoted, err := strconv.Unquote(text); err == nil {
			text = unquoted
		} else {
			text = strings.Trim(text, `"`)
		}
	}
	return strings.TrimSpace(text)
}
