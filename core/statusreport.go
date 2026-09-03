package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ClaudeStatusEvent is the pseudo event the status line reports under. Claude
// Code has no hook for it: the facts arrive through its statusLine command,
// which Magentic points at the same hook-report entry.
const ClaudeStatusEvent = "Status"

// StatusReport is one vendor's statement about the run it is executing: which
// model at which effort, how full the context is, what the run has cost so
// far. It carries no lifecycle state, so the latest report simply replaces the
// one before it.
type StatusReport struct {
	Vendor         AgentVendor `json:"vendor"`
	RuntimeName    string      `json:"runtimeName"`
	RunRef         string      `json:"runRef,omitempty"`
	At             time.Time   `json:"at"`
	UID            int         `json:"uid"`
	Model          string      `json:"model,omitempty"`
	ModelID        string      `json:"modelId,omitempty"`
	Effort         string      `json:"effort,omitempty"`
	ContextPercent float64     `json:"contextPercent"`
	ContextWindow  int         `json:"contextWindow,omitempty"`
	ContextTokens  int         `json:"contextTokens,omitempty"`
	CostUSD        float64     `json:"costUsd"`
	Version        string      `json:"version,omitempty"`
	OutputStyle    string      `json:"outputStyle,omitempty"`
	FastMode       bool        `json:"fastMode"`
	Dir            string      `json:"dir,omitempty"`
}

// StatusReportFromClaudePayload reads the JSON Claude Code hands its status
// line on stdin. The RuntimeName comes from Claude's own session name, which
// Magentic set to the tmux Session at start; a payload without it leaves the
// field empty for the caller to resolve.
func StatusReportFromClaudePayload(payload []byte, now time.Time) (StatusReport, error) {
	var body struct {
		SessionID   string `json:"session_id"`
		SessionName string `json:"session_name"`
		Cwd         string `json:"cwd"`
		Version     string `json:"version"`
		FastMode    bool   `json:"fast_mode"`
		Model       struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"model"`
		Effort struct {
			Level string `json:"level"`
		} `json:"effort"`
		OutputStyle struct {
			Name string `json:"name"`
		} `json:"output_style"`
		Cost struct {
			TotalUSD float64 `json:"total_cost_usd"`
		} `json:"cost"`
		Context struct {
			Size    int     `json:"context_window_size"`
			Used    float64 `json:"used_percentage"`
			Current struct {
				Input         int `json:"input_tokens"`
				CacheCreation int `json:"cache_creation_input_tokens"`
				CacheRead     int `json:"cache_read_input_tokens"`
			} `json:"current_usage"`
		} `json:"context_window"`
	}
	if len(strings.TrimSpace(string(payload))) == 0 {
		return StatusReport{}, fmt.Errorf("Statusmeldung ohne Nutzlast")
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return StatusReport{}, fmt.Errorf("Statusmeldung ist kein gültiges JSON: %v", err)
	}
	model := strings.TrimSpace(body.Model.DisplayName)
	if model == "" {
		model = strings.TrimSpace(body.Model.ID)
	}
	current := body.Context.Current
	return StatusReport{
		Vendor:         AgentVendorClaude,
		RuntimeName:    strings.TrimSpace(body.SessionName),
		RunRef:         strings.TrimSpace(body.SessionID),
		At:             now.UTC(),
		UID:            os.Getuid(),
		Model:          model,
		ModelID:        strings.TrimSpace(body.Model.ID),
		Effort:         strings.TrimSpace(body.Effort.Level),
		ContextPercent: body.Context.Used,
		ContextWindow:  body.Context.Size,
		ContextTokens:  current.Input + current.CacheCreation + current.CacheRead,
		CostUSD:        body.Cost.TotalUSD,
		Version:        strings.TrimSpace(body.Version),
		OutputStyle:    strings.TrimSpace(body.OutputStyle.Name),
		FastMode:       body.FastMode,
		Dir:            body.Cwd,
	}, nil
}

// ContextPercent100 is the context usage rounded and clamped to 0..100.
func (r StatusReport) ContextPercent100() int {
	percent := int(r.ContextPercent + 0.5)
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

var statusEffortBolts = map[string]int{"low": 1, "medium": 2, "high": 3, "xhigh": 4, "max": 5}

const (
	ansiReset  = "\x1b[0m"
	ansiDim    = "\x1b[2m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
)

func statusContextColor(percent int) string {
	switch {
	case percent >= 85:
		return ansiRed
	case percent >= 60:
		return ansiYellow
	default:
		return ansiGreen
	}
}

// StatusLineText is the one line Claude Code draws under its prompt. Claude
// Code keeps that row for its own pills whenever Remote Control is on, so the
// facts go there instead of into a second bar under the terminal.
func StatusLineText(report StatusReport) string {
	percent := report.ContextPercent100()
	filled := int(float64(percent)/10 + 0.5)
	meter := strings.Repeat("▰", filled) + strings.Repeat("▱", 10-filled)
	parts := []string{statusContextColor(percent) + "🧠 " + meter + " " + strconv.Itoa(percent) + "%" + ansiReset}
	if report.Model != "" {
		model := "🤖 " + report.Model
		if report.FastMode {
			model += " · fast"
		}
		parts = append(parts, model)
	}
	if report.Effort != "" {
		bolts := statusEffortBolts[report.Effort]
		if bolts == 0 {
			bolts = 1
		}
		parts = append(parts, strings.Repeat("⚡", bolts)+" "+report.Effort)
	}
	if report.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.2f", report.CostUSD))
	}
	return strings.Join(parts, ansiDim+" · "+ansiReset)
}

// StatusReportDir holds one file per runtime. A file per runtime instead of an
// event log: only the latest report matters, and the status line fires far too
// often for an append-only file to stay small.
func StatusReportDir() string {
	if path := os.Getenv("MAGENTIC_STATUS_REPORTS"); path != "" {
		return path
	}
	return filepath.Join(filepath.Dir(StatePath()), "status-reports")
}

func statusReportFileName(runtimeName string) string {
	var b strings.Builder
	for _, r := range runtimeName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String() + ".json"
}

// WriteStatusReport replaces the runtime's file atomically, so a reader never
// sees a half-written report.
func WriteStatusReport(dir string, report StatusReport) error {
	if strings.TrimSpace(report.RuntimeName) == "" {
		return fmt.Errorf("Statusmeldung nennt keine Laufzeit")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(report)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".status-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return err
	}
	return os.Rename(tmpPath, filepath.Join(dir, statusReportFileName(report.RuntimeName)))
}

// ReadStatusReports returns every readable report of the owning user. A file
// that is not a report, or not the user's own, is skipped rather than reported:
// the directory is telemetry, not a ledger.
func ReadStatusReports(dir string) []StatusReport {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	owner := os.Getuid()
	var reports []StatusReport
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var report StatusReport
		if err := json.Unmarshal(data, &report); err != nil {
			continue
		}
		if report.UID != owner || strings.TrimSpace(report.RuntimeName) == "" || report.At.IsZero() {
			continue
		}
		reports = append(reports, report)
	}
	return reports
}

// StatusReportsForSessions resolves reports to Sessions by runtime and vendor.
// Unlike the hook channel it does not insist on the vendor's run reference:
// the file describes whatever run is executing in that pane right now, and
// after a /clear the run id changes while the pane the developer looks at
// does not. A report from another vendor is a leftover of a vendor switch and
// is ignored.
func StatusReportsForSessions(reports []StatusReport, sessions []Session) map[SessionID]StatusReport {
	out := map[SessionID]StatusReport{}
	for _, report := range reports {
		if strings.TrimSpace(report.RuntimeName) == "" {
			continue
		}
		for _, session := range sessions {
			if session.RuntimeName != report.RuntimeName || session.SessionVendor() != report.Vendor {
				continue
			}
			if existing, known := out[session.ID]; known && !report.At.After(existing.At) {
				continue
			}
			out[session.ID] = report
		}
	}
	return out
}
