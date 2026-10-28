package main

// UpdateStatus describes the updater of this desktop installation. It never
// checks the network; Sparkle owns the update UI and installation lifecycle.
type UpdateStatus struct {
	Version           string `json:"version"`
	Enabled           bool   `json:"enabled"`
	CanCheck          bool   `json:"canCheck"`
	SessionInProgress bool   `json:"sessionInProgress"`
	Reason            string `json:"reason"`
}

func (a *App) Updates() UpdateStatus {
	return nativeUpdateStatus()
}

func (a *App) CheckForUpdates() error {
	return nativeCheckForUpdates()
}
