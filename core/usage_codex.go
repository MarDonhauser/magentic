package core

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// UsageWindow is one quota window of one provider, normalized so the UI can
// render every provider the same way.
type UsageWindow struct {
	Label   string
	Percent float64
	Reset   time.Time
}

// ProviderUsage is what one provider reports about its own limits. Err names
// why nothing is known instead of letting an empty window list look like a
// fresh quota.
type ProviderUsage struct {
	Windows   []UsageWindow
	FetchedAt time.Time
	Err       string
}

var (
	codexUsageMu    sync.Mutex
	codexUsageCache ProviderUsage
)

// CachedCodexUsage reads Codex's own rate-limit records, which Codex writes
// into its rollout files. No network call is involved: the limits are a local
// fact of the last conversation.
func CachedCodexUsage() ProviderUsage {
	codexUsageMu.Lock()
	defer codexUsageMu.Unlock()
	if !codexUsageCache.FetchedAt.IsZero() && time.Since(codexUsageCache.FetchedAt) < 5*time.Minute {
		return codexUsageCache
	}
	codexUsageCache = readCodexUsage(codexHome())
	return codexUsageCache
}

func codexHome() string {
	if home := strings.TrimSpace(os.Getenv("CODEX_HOME")); home != "" {
		return home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex")
}

type codexRateLimitWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	ResetsAt      int64   `json:"resets_at"`
}

type codexRateLimitLine struct {
	Payload struct {
		RateLimits *struct {
			Primary   *codexRateLimitWindow `json:"primary"`
			Secondary *codexRateLimitWindow `json:"secondary"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

// readCodexUsage takes the newest rollout file and its last rate-limit record.
// Older files are ignored on purpose: a stale quota is worse than none.
func readCodexUsage(home string) ProviderUsage {
	usage := ProviderUsage{FetchedAt: time.Now()}
	if strings.TrimSpace(home) == "" {
		usage.Err = "kein Codex-Verzeichnis gefunden"
		return usage
	}
	path, err := newestCodexRollout(filepath.Join(home, "sessions"))
	if err != nil {
		usage.Err = err.Error()
		return usage
	}
	file, err := os.Open(path)
	if err != nil {
		usage.Err = err.Error()
		return usage
	}
	defer file.Close()
	var latest codexRateLimitLine
	found := false
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !strings.Contains(string(line), "rate_limits") {
			continue
		}
		var parsed codexRateLimitLine
		if err := json.Unmarshal(line, &parsed); err != nil || parsed.Payload.RateLimits == nil {
			continue
		}
		latest = parsed
		found = true
	}
	if err := scanner.Err(); err != nil {
		usage.Err = err.Error()
		return usage
	}
	if !found {
		usage.Err = "Codex hat noch keine Limits aufgezeichnet"
		return usage
	}
	for _, window := range []*codexRateLimitWindow{latest.Payload.RateLimits.Primary, latest.Payload.RateLimits.Secondary} {
		if window == nil || window.WindowMinutes <= 0 {
			continue
		}
		entry := UsageWindow{Label: usageWindowLabel(window.WindowMinutes), Percent: window.UsedPercent}
		if window.ResetsAt > 0 {
			entry.Reset = time.Unix(window.ResetsAt, 0).Local()
		}
		usage.Windows = append(usage.Windows, entry)
	}
	if len(usage.Windows) == 0 {
		usage.Err = "Codex hat noch keine Limits aufgezeichnet"
	}
	return usage
}

func newestCodexRollout(root string) (string, error) {
	newest, newestAt := "", time.Time{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "rollout-") || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		if newest == "" || info.ModTime().After(newestAt) {
			newest, newestAt = path, info.ModTime()
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if newest == "" {
		return "", fmt.Errorf("keine Codex-Sitzung gefunden")
	}
	return newest, nil
}

// usageWindowLabel names a quota window the way the providers themselves do.
func usageWindowLabel(minutes int) string {
	switch {
	case minutes%1440 == 0:
		return fmt.Sprintf("%dd", minutes/1440)
	case minutes%60 == 0:
		return fmt.Sprintf("%dh", minutes/60)
	default:
		return fmt.Sprintf("%dm", minutes)
	}
}
