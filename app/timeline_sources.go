package main

import (
	"time"

	"magentic/core"
)

// These labels are part of the Wails DTO consumed by the existing desktop UI.
// Provider parsing and source identity live in core.WorkHistory.
const (
	timelineSourceClaude  = "Claude Code"
	timelineSourceCodex   = "Codex"
	timelineSourceGemini  = "Gemini CLI"
	timelineSourceCopilot = "GitHub Copilot"
)

// timelineEntries is deliberately a concrete transport copy. The desktop
// layer owns localized presentation; WorkHistory owns discovery, filtering,
// attribution, deduplication, and provider semantics.
func timelineEntries(page core.HistoryEventPage) []TimelineEntry {
	out := make([]TimelineEntry, 0, len(page.Events))
	for _, event := range page.Events {
		if event.Kind != core.HistoryEventPrompt || event.Lineage != core.HistoryLineagePrimary ||
			event.OccurredAt.State != core.HistoryFactKnown || event.Text.State != core.HistoryFactKnown {
			continue
		}
		occurredAt := event.OccurredAt.Value
		local := occurredAt.In(time.Local)
		project := "ohne Projekt"
		if event.Attribution.ProjectName.State == core.HistoryFactKnown && event.Attribution.ProjectName.Value != "" {
			project = event.Attribution.ProjectName.Value
		}
		agent := ""
		if event.Attribution.SessionName.State == core.HistoryFactKnown {
			agent = event.Attribution.SessionName.Value
		}
		out = append(out, TimelineEntry{
			Agent:   agent,
			Project: project,
			Source:  event.Provider.Label(),
			Day:     tlWeekdays[local.Weekday()] + " " + local.Format("02.01."),
			Time:    local.Format("15:04"),
			TimeRaw: occurredAt.UTC().Format(time.RFC3339Nano),
			Text:    capStr(event.Text.Value, 500),
		})
	}
	return out
}
