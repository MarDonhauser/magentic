package main

import (
	"strconv"
	"time"

	"magentic/core"
)

func (a *App) SetActiveTerm(name string) {
	a.mu.Lock()
	a.activeTerm = name
	a.mu.Unlock()
}

func (a *App) getActiveTerm() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.activeTerm
}

func (a *App) watchLoop() {
	prev := map[string]core.AgentStatus{}
	pending := map[string]core.AgentStatus{}
	for {
		time.Sleep(4 * time.Second)
		st, err := core.LoadState()
		if err != nil {
			continue
		}
		statuses, _, _ := core.CollectStatuses(st.Agents)
		blocked := 0
		for _, s := range statuses {
			if s == core.StatusBlocked {
				blocked++
			}
		}
		label := ""
		if blocked > 0 {
			label = strconv.Itoa(blocked)
		}
		setDockBadge(label)

		active := a.getActiveTerm()
		for name, s := range statuses {
			if p, ok := pending[name]; ok {
				delete(pending, name)
				if s == p && name != active {
					core.NotifyDesktop("magentic · "+name, "Agent ist fertig — bereit für den nächsten Prompt", "Ping")
				}
			}
			pv, seen := prev[name]
			if !seen || pv == s || name == active {
				continue
			}
			if s == core.StatusBlocked && (pv == core.StatusRunning || pv == core.StatusAgents || pv == core.StatusIdle) {
				core.NotifyDesktop("magentic · "+name, "Agent wartet auf deine Eingabe", "Glass")
			} else if (pv == core.StatusRunning || pv == core.StatusAgents) && s == core.StatusIdle {
				pending[name] = core.StatusIdle
			}
		}
		prev = statuses
	}
}
