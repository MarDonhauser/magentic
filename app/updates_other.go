//go:build !darwin

package main

import "errors"

func nativeUpdateStatus() UpdateStatus {
	return UpdateStatus{Version: "Entwicklungsbuild", Reason: "App-Updates sind derzeit nur unter macOS verfügbar."}
}

func nativeCheckForUpdates() error {
	return errors.New(nativeUpdateStatus().Reason)
}

func installUpdaterMenu() {}
