package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"magentic/core"
)

func knownWorktreePath(path string) bool {
	st, err := core.LoadState()
	if err != nil {
		return false
	}
	for _, p := range st.Projects {
		if path == p.Path {
			return true
		}
		for _, wt := range core.CollectWorktrees(p.Path) {
			if wt.Path == path {
				return true
			}
		}
	}
	return false
}

func (a *App) WorktreeDiff(path string) (string, error) {
	if !knownWorktreePath(path) {
		return "", fmt.Errorf("Pfad gehört zu keinem Projekt")
	}
	var b strings.Builder
	if status, err := core.GitCmd(path, "status", "--short"); err == nil && strings.TrimSpace(status) != "" {
		b.WriteString("── Status ──\n")
		b.WriteString(status)
		b.WriteString("\n")
	}
	if diff, err := core.GitCmd(path, "diff", "HEAD"); err == nil && strings.TrimSpace(diff) != "" {
		b.WriteString("── Diff (gegen HEAD) ──\n")
		b.WriteString(diff)
	}
	if untracked, err := core.GitCmd(path, "ls-files", "--others", "--exclude-standard"); err == nil {
		files := strings.Fields(strings.TrimSpace(untracked))
		if len(files) > 0 {
			b.WriteString("\n── Neue Dateien (untracked) ──\n")
			for _, f := range files {
				b.WriteString("+ " + f + "\n")
			}
		}
	}
	out := b.String()
	if out == "" {
		out = "Keine Änderungen."
	}
	const cap = 400_000
	if len(out) > cap {
		out = out[:cap] + "\n… (gekürzt)"
	}
	return out, nil
}

func (a *App) SessionPreview(name string) string {
	sn := core.SessionName(name)
	if !core.TmuxHasSession(sn) {
		return ""
	}
	return core.LastLines(strings.TrimRight(core.TmuxCapturePane(sn, 0), "\n"), 16)
}

type SearchHit struct {
	Project string `json:"project"`
	Role    string `json:"role"`
	Time    string `json:"time"`
	TimeRaw string `json:"timeRaw"`
	Snippet string `json:"snippet"`
	Full    string `json:"full"`
}

func (a *App) SearchTranscripts(query string) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if len(query) < 3 {
		return nil, fmt.Errorf("mindestens 3 Zeichen")
	}
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".claude", "projects")
	dirs, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	qLower := strings.ToLower(query)
	var hits []SearchHit
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		project := decodeProjectDir(d.Name(), home)
		files, _ := filepath.Glob(filepath.Join(base, d.Name(), "*.jsonl"))
		for _, f := range files {
			hits = append(hits, scanTranscript(f, project, qLower)...)
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].TimeRaw > hits[j].TimeRaw })
	if len(hits) > 80 {
		hits = hits[:80]
	}
	return hits, nil
}

type TimelineEntry struct {
	Agent   string `json:"agent"`
	Project string `json:"project"`
	Day     string `json:"day"`
	Time    string `json:"time"`
	TimeRaw string `json:"timeRaw"`
	Text    string `json:"text"`
}

var tlWeekdays = map[time.Weekday]string{
	time.Monday: "Mo", time.Tuesday: "Di", time.Wednesday: "Mi",
	time.Thursday: "Do", time.Friday: "Fr", time.Saturday: "Sa", time.Sunday: "So",
}

func (a *App) Timeline() ([]TimelineEntry, error) {
	home, _ := os.UserHomeDir()
	base := filepath.Join(home, ".claude", "projects")
	dirs, err := os.ReadDir(base)
	if err != nil {
		return nil, err
	}
	sidToAgent := map[string]string{}
	if st, err := core.LoadState(); err == nil {
		for _, ag := range st.Agents {
			if ag.SessionID != "" {
				sidToAgent[ag.SessionID] = ag.Name
			}
		}
	}
	cutoff := time.Now().AddDate(0, 0, -7)
	var out []TimelineEntry
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		project := decodeProjectDir(d.Name(), home)
		files, _ := filepath.Glob(filepath.Join(base, d.Name(), "*.jsonl"))
		for _, f := range files {
			fi, err := os.Stat(f)
			if err != nil || fi.ModTime().Before(cutoff) {
				continue
			}
			sid := strings.TrimSuffix(filepath.Base(f), ".jsonl")
			out = append(out, scanUserPrompts(f, project, sidToAgent[sid], cutoff)...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TimeRaw > out[j].TimeRaw })
	seen := map[string]bool{}
	kept := out[:0]
	for _, e := range out {
		prefix := e.Text
		if len(prefix) > 80 {
			prefix = prefix[:80]
		}
		k := e.Day + e.Time + "|" + prefix
		if seen[k] {
			continue
		}
		seen[k] = true
		kept = append(kept, e)
	}
	out = kept
	if len(out) > 150 {
		out = out[:150]
	}
	return out, nil
}

func scanUserPrompts(path, project, agent string, cutoff time.Time) []TimelineEntry {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var entries []TimelineEntry
	userMark := []byte(`"type":"user"`)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 32*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.Contains(line, userMark) {
			continue
		}
		var entry struct {
			Type        string `json:"type"`
			Timestamp   string `json:"timestamp"`
			IsSidechain bool   `json:"isSidechain"`
			IsMeta      bool   `json:"isMeta"`
			Message     struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry.Type != "user" || entry.IsSidechain || entry.IsMeta {
			continue
		}
		text := strings.TrimSpace(extractText(entry.Message.Content))
		if text == "" || strings.HasPrefix(text, "<") ||
			strings.HasPrefix(text, "[Request interrupted") || strings.HasPrefix(text, "Caveat:") {
			continue
		}
		t, err := time.Parse(time.RFC3339, entry.Timestamp)
		if err != nil || t.Before(cutoff) {
			continue
		}
		lt := t.Local()
		entries = append(entries, TimelineEntry{
			Agent:   agent,
			Project: project,
			Day:     tlWeekdays[lt.Weekday()] + " " + lt.Format("02.01."),
			Time:    lt.Format("15:04"),
			TimeRaw: entry.Timestamp,
			Text:    capStr(text, 500),
		})
	}
	return entries
}

func decodeProjectDir(dir, home string) string {
	homeEnc := strings.ReplaceAll(home, "/", "-")
	if prefix := homeEnc + "-Projects-"; strings.HasPrefix(dir, prefix) {
		return strings.TrimPrefix(dir, prefix)
	}
	if strings.HasPrefix(dir, homeEnc+"-") {
		return strings.TrimPrefix(dir, homeEnc+"-")
	}
	return strings.TrimPrefix(dir, "-")
}

func scanTranscript(path, project, qLower string) []SearchHit {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var hits []SearchHit
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 32*1024*1024)
	for sc.Scan() && len(hits) < 8 {
		line := sc.Bytes()
		if !containsFold(line, qLower) {
			continue
		}
		var entry struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry.Type != "user" && entry.Type != "assistant" {
			continue
		}
		text := extractText(entry.Message.Content)
		idx := strings.Index(strings.ToLower(text), qLower)
		if idx < 0 {
			continue
		}
		hits = append(hits, SearchHit{
			Project: project,
			Role:    entry.Type,
			Time:    formatTranscriptTime(entry.Timestamp),
			TimeRaw: entry.Timestamp,
			Snippet: snippetAround(text, idx, len(qLower)),
			Full:    capStr(text, 6000),
		})
	}
	return hits
}

func containsFold(line []byte, qLower string) bool {
	return strings.Contains(strings.ToLower(string(line)), qLower)
}

func extractText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
		return ""
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, b := range blocks {
		if b.Type == "text" && b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func snippetAround(text string, idx, qlen int) string {
	start := idx - 110
	if start < 0 {
		start = 0
	}
	end := idx + qlen + 110
	if end > len(text) {
		end = len(text)
	}
	for start > 0 && text[start]&0xC0 == 0x80 {
		start--
	}
	for end < len(text) && text[end]&0xC0 == 0x80 {
		end++
	}
	s := strings.ReplaceAll(text[start:end], "\n", " ")
	if start > 0 {
		s = "…" + s
	}
	if end < len(text) {
		s += "…"
	}
	return s
}

func formatTranscriptTime(ts string) string {
	if t, err := time.Parse(time.RFC3339, ts); err == nil {
		return t.Local().Format("02.01. 15:04")
	}
	return ts
}

func capStr(s string, n int) string {
	if len(s) > n {
		return s[:n] + "\n… (gekürzt)"
	}
	return s
}
