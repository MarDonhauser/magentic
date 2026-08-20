package core

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type AgentStatus int

const (
	StatusUnknown AgentStatus = iota
	StatusRunning
	StatusAgents
	StatusShell
	StatusBlocked
	StatusIdle
	StatusExited
	StatusDead
	StatusTerm
)

func (s AgentStatus) Label() string {
	switch s {
	case StatusRunning:
		return "läuft"
	case StatusAgents:
		return "Agents"
	case StatusShell:
		return "Shell läuft"
	case StatusBlocked:
		return "wartet"
	case StatusIdle:
		return "idle"
	case StatusExited:
		return "beendet"
	case StatusDead:
		return "tot"
	case StatusTerm:
		return "Terminal"
	}
	return "?"
}

func (s AgentStatus) Icon() string {
	switch s {
	case StatusRunning:
		return "●"
	case StatusAgents:
		return "◍"
	case StatusShell:
		return "⚙"
	case StatusBlocked:
		return "◆"
	case StatusIdle:
		return "○"
	case StatusExited:
		return "▪"
	case StatusDead:
		return "✗"
	case StatusTerm:
		return "⌨"
	}
	return "?"
}

var bgAgentsRe = regexp.MustCompile(`(?i)waiting for (\d+) background agent`)

var agentTreeRe = regexp.MustCompile(`(?m)^\s*[◯○◌]\s+\S+`)

func BackgroundAgentCount(content string) int {
	if n := len(agentTreeRe.FindAllString(content, -1)); n > 0 {
		return n
	}
	ms := bgAgentsRe.FindAllStringSubmatch(content, -1)
	if len(ms) == 0 {
		return 0
	}
	n, _ := strconv.Atoi(ms[len(ms)-1][1])
	return n
}

func AgentsDetail(n int) string {
	if n <= 0 {
		return ""
	}
	if n == 1 {
		return "wartet auf 1 Agent"
	}
	return fmt.Sprintf("wartet auf %d Agents", n)
}

var bgShellRe = regexp.MustCompile(`(?i)(\d+)\s+shells?\s+still\s+running`)
var bgShellBarRe = regexp.MustCompile(`(?im)·\s+(\d+)\s+shells?\s*$`)

func BackgroundShellCount(content string) int {
	if m := bgShellRe.FindStringSubmatch(content); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	if m := bgShellBarRe.FindStringSubmatch(content); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func ShellDetail(n int) string {
	if n <= 0 {
		return ""
	}
	if n == 1 {
		return "1 Shell läuft"
	}
	return fmt.Sprintf("%d Shells laufen", n)
}

var spinnerRe = regexp.MustCompile(`(?m)^\s*[·✢✳✶✻✽✺✹✸✷+*]\s+[^\n…]{1,80}…`)

var runningPatterns = []string{
	"esc to interrupt",
	"ctrl+b to run in background",
	"· thinking with",
}

var blockedPatterns = []string{
	"do you want",
	"would you like",
	"❯ 1.",
	"(y/n)",
	"[y/n]",
	"enter to confirm",
	"waiting for your",
	"press enter to",
	"trust this folder",
}

var shellCommands = map[string]bool{
	"zsh": true, "bash": true, "fish": true, "sh": true,
	"-zsh": true, "-bash": true, "login": true,
}

const (
	AgentToolClaude  = "claude"
	AgentToolCodex   = "codex"
	AgentToolGemini  = "gemini"
	AgentToolCopilot = "copilot"
	AgentToolBash    = "bash"
)

// DetectAgentTool translates the command tmux reports for a pane into the
// stable frontend identity used by developer-icons. Unknown commands stay
// neutral instead of being mislabeled as Claude.
func DetectAgentTool(paneCommand string, term bool) string {
	if term {
		return AgentToolBash
	}
	command := strings.ToLower(strings.TrimSpace(paneCommand))
	command = strings.TrimPrefix(command, "-")
	switch {
	case command == "claude" || strings.HasPrefix(command, "claude-"):
		return AgentToolClaude
	case command == "codex" || strings.HasPrefix(command, "codex-"):
		return AgentToolCodex
	case command == "gemini" || strings.HasPrefix(command, "gemini-"):
		return AgentToolGemini
	case command == "copilot", command == "github-copilot":
		return AgentToolCopilot
	default:
		return ""
	}
}

// LegacyObservationProjection is the compatibility Adapter for callers that
// still consume name-keyed status, content, activity, and tool maps. New code
// should retain the ObservationSnapshot so availability and presence are not
// collapsed away.
type LegacyObservationProjection struct {
	Statuses map[string]AgentStatus
	Contents map[string]string
	Activity map[string]time.Time
	Tools    map[string]string
}

const legacyObservationCacheTTL = 5 * time.Second

var legacyObservationCache struct {
	sync.RWMutex
	signature string
	storedAt  time.Time
	tools     map[string]string
}

// ProjectLegacyObservation performs a lossy compatibility projection from
// stable SessionID facts to legacy name-keyed maps. Unknown Observation facts
// remain unknown; in particular, unavailable tmux is never projected as dead.
func ProjectLegacyObservation(snapshot ObservationSnapshot, sessions []Session) LegacyObservationProjection {
	projection := LegacyObservationProjection{
		Statuses: make(map[string]AgentStatus, len(sessions)),
		Contents: make(map[string]string, len(sessions)),
		Activity: make(map[string]time.Time, len(sessions)),
		Tools:    make(map[string]string, len(sessions)),
	}
	byID := make(map[SessionID]SessionObservation, len(snapshot.Sessions))
	for _, observed := range snapshot.Sessions {
		if observed.SessionID != "" {
			byID[observed.SessionID] = observed
		}
	}
	for i, session := range sessions {
		observed, found := byID[session.ID]
		// Tests and old in-process fixtures can have no durable SessionID yet.
		// Positional fallback is deliberately confined to this lossy Adapter;
		// durable callers are always joined by SessionID.
		if !found && session.ID == "" && i < len(snapshot.Sessions) {
			observed = snapshot.Sessions[i]
			found = true
		}
		if !found {
			projection.Statuses[session.Name] = StatusUnknown
			projection.Contents[session.Name] = ""
			continue
		}
		projection.Statuses[session.Name] = observed.Status
		projection.Contents[session.Name] = observed.Content
		if observed.ActivityKnown {
			projection.Activity[session.Name] = observed.Activity
		}
		if observed.Tool != "" {
			projection.Tools[session.Name] = observed.Tool
		}
	}
	rememberLegacyObservation(sessions, projection.Tools)
	return projection
}

func rememberLegacyObservation(sessions []Session, tools map[string]string) {
	legacyObservationCache.Lock()
	legacyObservationCache.signature = legacyObservationSignature(sessions)
	legacyObservationCache.storedAt = time.Now()
	legacyObservationCache.tools = cloneStringMap(tools)
	legacyObservationCache.Unlock()
}

func cachedLegacyObservationTools(sessions []Session) (map[string]string, bool) {
	signature := legacyObservationSignature(sessions)
	legacyObservationCache.RLock()
	found := legacyObservationCache.signature == signature &&
		!legacyObservationCache.storedAt.IsZero() &&
		time.Since(legacyObservationCache.storedAt) <= legacyObservationCacheTTL
	tools := cloneStringMap(legacyObservationCache.tools)
	legacyObservationCache.RUnlock()
	return tools, found
}

func cloneStringMap(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func legacyObservationSignature(sessions []Session) string {
	var signature strings.Builder
	for _, session := range sessions {
		signature.WriteString(string(session.ID))
		signature.WriteByte('\x00')
		signature.WriteString(session.Name)
		signature.WriteByte('\x00')
		signature.WriteString(session.TmuxName())
		signature.WriteByte('\x00')
	}
	return signature.String()
}

// observationSessions returns copy-only ephemeral IDs for legacy fixtures.
// Observe itself remains strict: production Sessions must have durable IDs.
func observationSessions(sessions []Session) []Session {
	prepared := append([]Session(nil), sessions...)
	used := make(map[SessionID]bool, len(prepared))
	for _, session := range prepared {
		if session.ID != "" {
			used[session.ID] = true
		}
	}
	for i := range prepared {
		if prepared[i].ID != "" {
			continue
		}
		for suffix := 0; ; suffix++ {
			candidate := SessionID(fmt.Sprintf("__observation_fixture_%d_%d", i, suffix))
			if !used[candidate] {
				prepared[i].ID = candidate
				used[candidate] = true
				break
			}
		}
	}
	return prepared
}

// legacyObservationSnapshot quarantines old map-shaped inputs at the edge of
// Observation-aware code. It cannot recreate availability that the old maps
// discarded, so a missing or unknown status remains explicitly unavailable.
func legacyObservationSnapshot(
	sessions []Session,
	statuses map[string]AgentStatus,
	contents map[string]string,
	activity map[string]time.Time,
	tools map[string]string,
) ([]Session, ObservationSnapshot) {
	prepared := observationSessions(sessions)
	snapshot := ObservationSnapshot{
		ObservedAt:   time.Now().UTC(),
		Availability: ObservationAvailable,
		Sessions:     make([]SessionObservation, 0, len(prepared)),
	}
	unknown := 0
	for _, session := range prepared {
		status, statusKnown := statuses[session.Name]
		if !statusKnown {
			status = StatusUnknown
		}
		content, contentKnown := contents[session.Name]
		activeAt, activityKnown := activity[session.Name]
		observed := SessionObservation{
			SessionID:     session.ID,
			Availability:  ObservationAvailable,
			Presence:      SessionPresencePresent,
			Status:        status,
			Content:       content,
			ContentKnown:  contentKnown,
			Activity:      activeAt,
			ActivityKnown: activityKnown,
			Tool:          tools[session.Name],
			WorktreePath:  session.Dir,
			Worktree:      session.Worktree,
			Occupancy:     OccupancyOccupied,
		}
		switch status {
		case StatusUnknown:
			observed.Availability = ObservationUnavailable
			observed.Presence = SessionPresenceUnknown
			observed.Occupancy = OccupancyUnknown
			unknown++
		case StatusDead:
			observed.Presence = SessionPresenceAbsent
			observed.Occupancy = OccupancyVacant
		}
		if observed.Tool == "" && session.IsTerm() &&
			observed.Presence == SessionPresencePresent {
			observed.Tool = AgentToolBash
		}
		observed.Detail = observationDetail(status, content)
		observed.Attention = observationAttention(status)
		observed.Unread = observationUnread(status, session.SeenAt, activeAt, activityKnown)
		snapshot.Sessions = append(snapshot.Sessions, observed)
	}
	if unknown > 0 {
		snapshot.Availability = ObservationPartial
		if unknown == len(prepared) && len(prepared) > 0 {
			snapshot.Availability = ObservationUnavailable
		}
	}
	return prepared, snapshot
}

// CollectAgentTools is retained for legacy callers. It reuses the most recent
// coherent Observation projection for the same Sessions; a cache miss performs
// one new Observe cycle rather than probing tmux independently.
func CollectAgentTools(agents []Agent) map[string]string {
	sessions := observationSessions(agents)
	if tools, found := cachedLegacyObservationTools(sessions); found {
		return tools
	}
	return ProjectLegacyObservation(Observe(context.Background(), sessions), sessions).Tools
}

var permissionDetails = []struct {
	label    string
	patterns []string
}{
	{"Ordner-Freigabe", []string{"trust this folder", "do you trust the files"}},
	{"Datei-Freigabe", []string{"make this edit", "edit this file", "edit file", "create this file", "create file", "write this file", "write file", "apply this change"}},
	{"Shell-Freigabe", []string{"bash command", "shell command", "run this command", "run the following command", "execute this command"}},
}

func BlockedDetail(content string) string {
	lc := strings.ToLower(content)
	for _, d := range permissionDetails {
		for _, p := range d.patterns {
			if strings.Contains(lc, p) {
				return d.label
			}
		}
	}
	if strings.Contains(lc, "don't ask again") || strings.Contains(lc, "dont ask again") {
		return "Freigabe"
	}
	return ""
}

func LastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func CollectStatuses(agents []Agent) (map[string]AgentStatus, map[string]string, map[string]time.Time) {
	sessions := observationSessions(agents)
	projection := ProjectLegacyObservation(Observe(context.Background(), sessions), sessions)
	return projection.Statuses, projection.Contents, projection.Activity
}

func DetectTermStatus(sessionExists bool) AgentStatus {
	if !sessionExists {
		return StatusDead
	}
	return StatusTerm
}

func DetectClaudeStatus(sessionExists bool, paneCommand, paneContent string) AgentStatus {
	if !sessionExists {
		return StatusDead
	}
	if shellCommands[paneCommand] {
		return StatusExited
	}
	if spinnerRe.MatchString(paneContent) {
		return StatusRunning
	}
	content := strings.ToLower(paneContent)
	for _, p := range runningPatterns {
		if strings.Contains(content, p) {
			return StatusRunning
		}
	}
	for _, p := range blockedPatterns {
		if strings.Contains(content, p) {
			return StatusBlocked
		}
	}
	if bgAgentsRe.MatchString(paneContent) || agentTreeRe.MatchString(paneContent) {
		return StatusAgents
	}
	if BackgroundShellCount(paneContent) > 0 {
		return StatusShell
	}
	return StatusIdle
}
