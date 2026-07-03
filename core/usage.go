package core

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

type UsageInfo struct {
	FiveHour      float64
	FiveHourReset time.Time
	SevenDay      float64
	SevenDayReset time.Time
	FetchedAt     time.Time
	Err           string
}

var (
	usageMu    sync.Mutex
	usageCache UsageInfo
)

func claudeOAuthToken() string {
	home, _ := os.UserHomeDir()
	if data, err := os.ReadFile(filepath.Join(home, ".claude", ".credentials.json")); err == nil {
		if tok := parseOAuthToken(data); tok != "" {
			return tok
		}
	}
	out, err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return ""
	}
	return parseOAuthToken(out)
}

func parseOAuthToken(data []byte) string {
	var creds struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		return ""
	}
	return creds.ClaudeAiOauth.AccessToken
}

func CachedUsage() UsageInfo {
	usageMu.Lock()
	defer usageMu.Unlock()
	if !usageCache.FetchedAt.IsZero() && time.Since(usageCache.FetchedAt) < 5*time.Minute {
		return usageCache
	}
	usageCache = fetchUsage()
	return usageCache
}

func fetchUsage() UsageInfo {
	info := UsageInfo{FetchedAt: time.Now()}
	token := claudeOAuthToken()
	if token == "" {
		info.Err = "kein Claude-Token gefunden"
		return info
	}
	req, err := http.NewRequest("GET", "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		info.Err = err.Error()
		return info
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		info.Err = err.Error()
		return info
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		info.Err = resp.Status
		return info
	}
	var payload struct {
		FiveHour struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    string  `json:"resets_at"`
		} `json:"seven_day"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		info.Err = err.Error()
		return info
	}
	info.FiveHour = payload.FiveHour.Utilization
	info.SevenDay = payload.SevenDay.Utilization
	if t, err := time.Parse(time.RFC3339, payload.FiveHour.ResetsAt); err == nil {
		info.FiveHourReset = t.Local()
	}
	if t, err := time.Parse(time.RFC3339, payload.SevenDay.ResetsAt); err == nil {
		info.SevenDayReset = t.Local()
	}
	return info
}
