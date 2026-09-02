package main

import (
	"os"

	"magentic/core"
)

// controlService is the control API this desktop app serves. It stays nil when
// the API is switched off or when another Magentic already serves the socket.
var (
	controlService *core.ControlService
	controlServer  *core.ControlServer
)

// startControlAPI claims the control socket for the desktop app. Serving is on
// by default; MAGENTIC_CONTROL=0 turns it off.
func startControlAPI() {
	if os.Getenv("MAGENTIC_CONTROL") == "0" {
		return
	}
	service := core.NewControlService(core.ControlServiceConfig{})
	server, err := core.ServeControl(service, core.ControlSocketPath())
	if err != nil {
		if err != core.ErrControlServedElsewhere {
			core.Logf("Steuer-API nicht gestartet: %v", err)
		}
		return
	}
	core.Logf("Steuer-API hört auf %s", server.Path())
	controlService, controlServer = service, server
}

func stopControlAPI() {
	if controlServer != nil {
		controlServer.Close()
		controlServer, controlService = nil, nil
	}
}

// publishControlObservation hands the app's own observation pass to the control
// API, so events and pending waits derive from it instead of a second loop.
func publishControlObservation(sessions []core.Session, snapshot core.ObservationSnapshot) {
	if controlService != nil {
		controlService.Observed(sessions, snapshot)
	}
}
