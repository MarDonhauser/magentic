package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"magentic/core"
)

// controlService is the control API this process serves. It is nil when the API
// is disabled or when another Magentic already serves the socket; the
// observation pass then simply has nobody to hand its result to.
var controlService *core.ControlService

// controlServer keeps the listener alive for the lifetime of the process.
var controlServer *core.ControlServer

// controlAPIEnabled is on by default: a control surface nobody can rely on
// being there is not one an agent will learn to use. MAGENTIC_CONTROL=0 turns
// it off.
func controlAPIEnabled() bool {
	return os.Getenv("MAGENTIC_CONTROL") != "0"
}

// startControlAPI claims the socket for this process. A socket another live
// Magentic serves is left alone, and this process runs on without the API.
func startControlAPI() {
	if !controlAPIEnabled() {
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
	controlService, controlServer = service, server
}

func stopControlAPI() {
	if controlServer != nil {
		controlServer.Close()
		controlServer, controlService = nil, nil
	}
}

// publishControlObservation hands the interface's own observation pass to the
// control API. There is no second observation loop.
func publishControlObservation(sessions []core.Session, snapshot core.ObservationSnapshot) {
	if controlService != nil {
		controlService.Observed(sessions, snapshot)
	}
}

// cliServe runs the control API without an interface, so a Session started
// through it survives every UI being closed.
func cliServe() {
	if !controlAPIEnabled() {
		fmt.Fprintln(os.Stderr, "Die Steuer-API ist über MAGENTIC_CONTROL=0 abgeschaltet.")
		os.Exit(1)
	}
	service := core.NewControlService(core.ControlServiceConfig{})
	server, err := core.ServeControl(service, core.ControlSocketPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer server.Close()
	fmt.Printf("Die Steuer-API hört auf %s. Beenden mit Strg-C.\n", server.Path())

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// Without an interface, this process runs the observation pass the events
	// and the pending waits are derived from.
	go serveControlObservations(ctx, service)
	<-ctx.Done()
	fmt.Println("Die Steuer-API wurde beendet.")
}

// serveControlObservations is the headless mode's observation pass. It uses the
// same cadence and the same Observation Module the TUI uses.
func serveControlObservations(ctx context.Context, service *core.ControlService) {
	for {
		state, err := LoadState()
		if err == nil {
			service.Observed(state.Agents, core.Observe(ctx, state.Agents))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(observationInterval):
		}
	}
}
