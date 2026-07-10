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

func (a *App) storeStatuses(st map[string]core.AgentStatus, ct map[string]string, act map[string]time.Time) {
	a.statusMu.Lock()
	a.statusCache = st
	a.contentCache = ct
	a.activityCache = act
	a.statusAt = time.Now()
	a.statusMu.Unlock()
}

func (a *App) statusesFor(agents []core.Agent) (map[string]core.AgentStatus, map[string]string, map[string]time.Time) {
	a.statusMu.Lock()
	st, ct, act, at := a.statusCache, a.contentCache, a.activityCache, a.statusAt
	a.statusMu.Unlock()
	if time.Since(at) > 5*time.Second {
		s, c, ac := core.CollectStatuses(agents)
		a.storeStatuses(s, c, ac)
		return s, c, ac
	}
	var missing []core.Agent
	for _, ag := range agents {
		if _, ok := st[ag.Name]; !ok {
			missing = append(missing, ag)
		}
	}
	if len(missing) == 0 {
		return st, ct, act
	}
	ms, mc, ma := core.CollectStatuses(missing)
	outS := make(map[string]core.AgentStatus, len(st)+len(ms))
	outC := make(map[string]string, len(ct)+len(mc))
	outA := make(map[string]time.Time, len(act)+len(ma))
	for k, v := range st {
		outS[k] = v
	}
	for k, v := range ms {
		outS[k] = v
	}
	for k, v := range ct {
		outC[k] = v
	}
	for k, v := range mc {
		outC[k] = v
	}
	for k, v := range act {
		outA[k] = v
	}
	for k, v := range ma {
		outA[k] = v
	}
	return outS, outC, outA
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
		statuses, contents, activity := core.CollectStatuses(st.Agents)
		a.storeStatuses(statuses, contents, activity)
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
