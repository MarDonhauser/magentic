package core

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var (
	copilotUsageMu    sync.Mutex
	copilotUsageCache ProviderUsage
)

// CachedCopilotUsage reports what GitHub knows about the signed-in user's
// Copilot quota. Unlike Codex, the Copilot CLI records no limits on disk, so
// this asks GitHub over the same endpoint the Copilot clients use.
func CachedCopilotUsage() ProviderUsage {
	copilotUsageMu.Lock()
	defer copilotUsageMu.Unlock()
	if !copilotUsageCache.FetchedAt.IsZero() && time.Since(copilotUsageCache.FetchedAt) < 5*time.Minute {
		return copilotUsageCache
	}
	copilotUsageCache = fetchCopilotUsage()
	return copilotUsageCache
}

// copilotToken takes the GitHub token the user is already signed in with. The
// environment wins over the CLI so a shell can point Magentic at another
// account without touching the CLI's own login.
func copilotToken() string {
	for _, name := range []string{"GH_TOKEN", "GITHUB_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token
		}
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func fetchCopilotUsage() ProviderUsage {
	usage := ProviderUsage{FetchedAt: time.Now()}
	token := copilotToken()
	if token == "" {
		usage.Err = "kein GitHub-Token gefunden"
		return usage
	}
	req, err := http.NewRequest("GET", "https://api.github.com/copilot_internal/user", nil)
	if err != nil {
		usage.Err = err.Error()
		return usage
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
	if err != nil {
		usage.Err = err.Error()
		return usage
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		usage.Err = resp.Status
		return usage
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		usage.Err = err.Error()
		return usage
	}
	parsed, err := parseCopilotUsage(body)
	if err != nil {
		usage.Err = err.Error()
		return usage
	}
	parsed.FetchedAt = usage.FetchedAt
	return parsed
}

type copilotQuotaSnapshot struct {
	PercentRemaining float64 `json:"percent_remaining"`
	Entitlement      float64 `json:"entitlement"`
	HasQuota         bool    `json:"has_quota"`
	Unlimited        bool    `json:"unlimited"`
}

type copilotUserPayload struct {
	QuotaResetDate string                          `json:"quota_reset_date"`
	QuotaSnapshots map[string]copilotQuotaSnapshot `json:"quota_snapshots"`
}

// copilotQuotaOrder names the quotas worth a bar and fixes their order. GitHub
// reports more ids than a plan actually grants, so the list is an allowlist
// rather than a rendering of whatever the payload happens to contain.
var copilotQuotaOrder = []struct{ id, label string }{
	{"premium_interactions", "Premium"},
	{"chat", "Chat"},
	{"completions", "Code"},
}

// parseCopilotUsage turns one quota snapshot into the shared window shape. A
// quota the plan does not grant is left out rather than drawn as an empty bar,
// and an unlimited quota has no meaningful fill at all.
func parseCopilotUsage(body []byte) (ProviderUsage, error) {
	var payload copilotUserPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return ProviderUsage{}, err
	}
	if len(payload.QuotaSnapshots) == 0 {
		return ProviderUsage{}, errors.New("GitHub meldet keine Copilot-Limits")
	}

	reset := parseCopilotResetDate(payload.QuotaResetDate)
	var usage ProviderUsage
	for _, quota := range copilotQuotaOrder {
		snapshot, found := payload.QuotaSnapshots[quota.id]
		if !found || !snapshot.HasQuota || snapshot.Unlimited || snapshot.Entitlement <= 0 {
			continue
		}
		used := 100 - snapshot.PercentRemaining
		if used < 0 {
			used = 0
		}
		if used > 100 {
			used = 100
		}
		usage.Windows = append(usage.Windows, UsageWindow{
			Label: quota.label, Percent: used, Reset: reset,
		})
	}
	if len(usage.Windows) == 0 {
		return ProviderUsage{}, errors.New("dieser Copilot-Tarif führt keine Limits")
	}
	return usage, nil
}

// parseCopilotResetDate reads the monthly reset day. An unreadable date leaves
// the window without one instead of inventing a deadline.
func parseCopilotResetDate(value string) time.Time {
	parsed, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(value), time.Local)
	if err != nil {
		return time.Time{}
	}
	return parsed
}
