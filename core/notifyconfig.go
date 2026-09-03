package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

var notifyConfigMu sync.Mutex

type notifyConfigFile struct {
	Enabled *bool `json:"enabled,omitempty"`
}

func notifyConfigPath() string {
	return filepath.Join(filepath.Dir(StatePath()), "notify.json")
}

func NotificationsEnabled() bool {
	notifyConfigMu.Lock()
	defer notifyConfigMu.Unlock()
	data, err := os.ReadFile(notifyConfigPath())
	if err != nil {
		return true
	}
	var f notifyConfigFile
	if json.Unmarshal(data, &f) != nil || f.Enabled == nil {
		return true
	}
	return *f.Enabled
}

func SetNotificationsEnabled(on bool) error {
	notifyConfigMu.Lock()
	defer notifyConfigMu.Unlock()
	data, err := json.Marshal(notifyConfigFile{Enabled: &on})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(notifyConfigPath()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(notifyConfigPath(), data, 0o644)
}
