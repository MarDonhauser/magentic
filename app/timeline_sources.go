package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"magentic/core"
)

const (
	timelineSourceClaude  = "Claude Code"
	timelineSourceCodex   = "Codex"
	timelineSourceGemini  = "Gemini CLI"
	timelineSourceCopilot = "GitHub Copilot"
)

type timelinePathProject struct {
	path string
	name string
}

type timelineContext struct {
	agentBySession map[string]string
	projects       []timelinePathProject
	projectAliases map[string]string
}

func newTimelineContext(st *core.State) timelineContext {
	ctx := timelineContext{
		agentBySession: map[string]string{},
		projectAliases: map[string]string{},
	}
	if st == nil {
		return ctx
	}
	addProject := func(path, name string) {
		if path == "" || name == "" {
			return
		}
		clean := filepath.Clean(path)
		ctx.projects = append(ctx.projects, timelinePathProject{path: clean, name: name})
		for _, alias := range []string{filepath.Base(clean), sha256Path(clean)} {
			if _, exists := ctx.projectAliases[alias]; !exists {
				ctx.projectAliases[alias] = name
			}
		}
	}
	for _, p := range st.Projects {
		addProject(p.Path, p.Name)
	}
	for _, ag := range st.Agents {
		if ag.SessionID != "" {
			ctx.agentBySession[ag.SessionID] = ag.Name
		}
		addProject(ag.Dir, ag.Project)
	}
	sort.Slice(ctx.projects, func(i, j int) bool { return len(ctx.projects[i].path) > len(ctx.projects[j].path) })
	return ctx
}

func sha256Path(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])
}

func (c timelineContext) projectForPath(path string) string {
	if path == "" {
		return ""
	}
	clean := filepath.Clean(path)
	for _, p := range c.projects {
		if clean == p.path || strings.HasPrefix(clean, p.path+string(filepath.Separator)) {
			return p.name
		}
	}
	return filepath.Base(clean)
}

func (c timelineContext) projectForAlias(alias string) string {
	if name := c.projectAliases[alias]; name != "" {
		return name
	}
	if len(alias) == 64 {
		if _, err := hex.DecodeString(alias); err == nil {
			return ""
		}
	}
	return alias
}

func timelineEntry(source, agent, project, timestamp, text string, cutoff time.Time) (TimelineEntry, bool) {
	text = cleanTimelinePrompt(text)
	if text == "" {
		return TimelineEntry{}, false
	}
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil || t.Before(cutoff) {
		return TimelineEntry{}, false
	}
	lt := t.Local()
	if project == "" {
		project = "ohne Projekt"
	}
	return TimelineEntry{
		Agent:   agent,
		Project: project,
		Source:  source,
		Day:     tlWeekdays[lt.Weekday()] + " " + lt.Format("02.01."),
		Time:    lt.Format("15:04"),
		TimeRaw: t.UTC().Format(time.RFC3339Nano),
		Text:    capStr(text, 500),
	}, true
}

var (
	imageWrapperLine = regexp.MustCompile(`(?m)^</?image(?:\s[^>]*)?>\s*$`)
	imageRefPrefix   = regexp.MustCompile(`^(?:\[(?:Image|File) #[^]]+\]\s*)+`)
	systemReminder   = regexp.MustCompile(`(?s)<system_reminder>.*?</system_reminder>`)
)

func cleanTimelinePrompt(text string) string {
	text = systemReminder.ReplaceAllString(text, "")
	text = imageWrapperLine.ReplaceAllString(text, "")
	text = strings.TrimSpace(text)
	text = imageRefPrefix.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}

func codexInjectedText(text string) bool {
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
	} {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

func recentTimelineFiles(root string, cutoff time.Time, accept func(string) bool) []string {
	var files []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !accept(path) {
			return nil
		}
		info, err := d.Info()
		if err == nil && !info.ModTime().Before(cutoff) {
			files = append(files, path)
		}
		return nil
	})
	return files
}

func collectClaudeTimeline(root, home string, ctx timelineContext, cutoff time.Time) []TimelineEntry {
	var out []TimelineEntry
	dirs, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		project := decodeProjectDir(d.Name(), home)
		files, _ := filepath.Glob(filepath.Join(root, d.Name(), "*.jsonl"))
		for _, path := range files {
			info, err := os.Stat(path)
			if err != nil || info.ModTime().Before(cutoff) {
				continue
			}
			sid := strings.TrimSuffix(filepath.Base(path), ".jsonl")
			out = append(out, scanUserPrompts(path, project, ctx.agentBySession[sid], cutoff)...)
		}
	}
	return out
}

type codexTimelineLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Payload   struct {
		Type      string `json:"type"`
		Role      string `json:"role"`
		Message   string `json:"message"`
		Cwd       string `json:"cwd"`
		ID        string `json:"id"`
		SessionID string `json:"session_id"`
		Content   []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"payload"`
}

type rawTimelinePrompt struct {
	timestamp string
	text      string
}

func collectCodexTimeline(codexHome string, ctx timelineContext, cutoff time.Time) []TimelineEntry {
	var out []TimelineEntry
	for _, root := range []string{filepath.Join(codexHome, "sessions"), filepath.Join(codexHome, "archived_sessions")} {
		for _, path := range recentTimelineFiles(root, cutoff, func(path string) bool { return strings.HasSuffix(path, ".jsonl") }) {
			out = append(out, scanCodexPrompts(path, ctx, cutoff)...)
		}
	}
	return out
}

func scanCodexPrompts(path string, ctx timelineContext, cutoff time.Time) []TimelineEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	var cwd, sessionID string
	var prompts []rawTimelinePrompt
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 32<<20)
	for sc.Scan() {
		var line codexTimelineLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.Type == "session_meta" {
			cwd = line.Payload.Cwd
			sessionID = line.Payload.SessionID
			if sessionID == "" {
				sessionID = line.Payload.ID
			}
			continue
		}
		text := ""
		switch {
		case line.Type == "event_msg" && line.Payload.Type == "user_message":
			text = line.Payload.Message
		case line.Type == "response_item" && line.Payload.Type == "message" && line.Payload.Role == "user":
			var parts []string
			for _, block := range line.Payload.Content {
				if (block.Type == "input_text" || block.Type == "text") && block.Text != "" && !codexInjectedText(block.Text) {
					parts = append(parts, block.Text)
				}
			}
			text = strings.Join(parts, "\n")
		}
		if text != "" {
			prompts = append(prompts, rawTimelinePrompt{timestamp: line.Timestamp, text: text})
		}
	}

	project := ctx.projectForPath(cwd)
	agent := ctx.agentBySession[sessionID]
	seen := map[string]bool{}
	var out []TimelineEntry
	for _, prompt := range prompts {
		e, ok := timelineEntry(timelineSourceCodex, agent, project, prompt.timestamp, prompt.text, cutoff)
		if !ok {
			continue
		}
		key := e.TimeRaw + "|" + e.Text
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	return out
}

type geminiTimelineMessage struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Content   string `json:"content"`
	Message   string `json:"message"`
}

func collectGeminiTimeline(root string, ctx timelineContext, cutoff time.Time) []TimelineEntry {
	files := recentTimelineFiles(root, cutoff, func(path string) bool {
		base := filepath.Base(path)
		return base == "logs.json" || (strings.HasPrefix(base, "session-") && (strings.HasSuffix(base, ".json") || strings.HasSuffix(base, ".jsonl")))
	})
	var out []TimelineEntry
	for _, path := range files {
		out = append(out, scanGeminiPrompts(path, root, ctx, cutoff)...)
	}
	return out
}

func scanGeminiPrompts(path, root string, ctx timelineContext, cutoff time.Time) []TimelineEntry {
	projectKey := geminiProjectKey(path, root)
	project := ctx.projectForAlias(projectKey)
	var sessionID string
	var messages []geminiTimelineMessage

	if filepath.Base(path) == "logs.json" {
		data, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(data, &messages) != nil {
			return nil
		}
	} else if strings.HasSuffix(path, ".jsonl") {
		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 0, 1<<20), 32<<20)
		for sc.Scan() {
			var header struct {
				SessionID string `json:"sessionId"`
			}
			var msg geminiTimelineMessage
			if json.Unmarshal(sc.Bytes(), &header) == nil && header.SessionID != "" {
				sessionID = header.SessionID
			}
			if json.Unmarshal(sc.Bytes(), &msg) == nil && msg.Type == "user" {
				messages = append(messages, msg)
			}
		}
	} else {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		var doc struct {
			SessionID string                  `json:"sessionId"`
			Messages  []geminiTimelineMessage `json:"messages"`
		}
		if json.Unmarshal(data, &doc) != nil {
			return nil
		}
		sessionID, messages = doc.SessionID, doc.Messages
	}

	agent := ctx.agentBySession[sessionID]
	var out []TimelineEntry
	for _, msg := range messages {
		if msg.Type != "user" {
			continue
		}
		text := msg.Content
		if text == "" {
			text = msg.Message
		}
		if e, ok := timelineEntry(timelineSourceGemini, agent, project, msg.Timestamp, text, cutoff); ok {
			out = append(out, e)
		}
	}
	return out
}

func geminiProjectKey(path, root string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 {
		return ""
	}
	return parts[0]
}

type copilotTimelineLine struct {
	Type      string `json:"type"`
	Timestamp string `json:"timestamp"`
	Data      struct {
		Content           string          `json:"content"`
		Transformed       string          `json:"transformedContent"`
		ParentAgentTaskID json.RawMessage `json:"parentAgentTaskId"`
	} `json:"data"`
}

func collectCopilotTimeline(root string, ctx timelineContext, cutoff time.Time) []TimelineEntry {
	files := recentTimelineFiles(root, cutoff, func(path string) bool { return filepath.Base(path) == "events.jsonl" })
	var out []TimelineEntry
	for _, path := range files {
		out = append(out, scanCopilotPrompts(path, ctx, cutoff)...)
	}
	return out
}

func scanCopilotPrompts(path string, ctx timelineContext, cutoff time.Time) []TimelineEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	sessionDir := filepath.Dir(path)
	sessionID := filepath.Base(sessionDir)
	project := ctx.projectForPath(readSimpleYAMLValue(filepath.Join(sessionDir, "workspace.yaml"), "cwd"))
	agent := ctx.agentBySession[sessionID]
	var out []TimelineEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 32<<20)
	for sc.Scan() {
		var line copilotTimelineLine
		if json.Unmarshal(sc.Bytes(), &line) != nil || line.Type != "user.message" || hasParentAgentTask(line.Data.ParentAgentTaskID) {
			continue
		}
		text := line.Data.Content
		if text == "" {
			text = line.Data.Transformed
		}
		if e, ok := timelineEntry(timelineSourceCopilot, agent, project, line.Timestamp, text, cutoff); ok {
			out = append(out, e)
		}
	}
	return out
}

func hasParentAgentTask(raw json.RawMessage) bool {
	v := strings.TrimSpace(string(raw))
	return v != "" && v != "null" && v != "0" && v != `""` && v != `"0"`
}

func readSimpleYAMLValue(path, key string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	prefix := key + ":"
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
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
