//go:build windows

package core

import (
	"errors"
	"io"
)

// errManagedRuntimeOnWindows is the single stated reason the managed runtime
// is refused on Windows (see the service-installation spec). It is answered
// before an agent host claims a socket, so no managed Session ever reaches
// the process paths below.
var errManagedRuntimeOnWindows = errors.New("der managed Runtime wird unter Windows nicht unterstützt")

// managedRuntimeSupported refuses the managed runtime here rather than
// letting a host listen first and fail at the spawn.
func managedRuntimeSupported() error { return errManagedRuntimeOnWindows }

// agentHostProcess has no Windows implementation.
type agentHostProcess struct{}

func startAgentHostProcess(string, []string, string) (*agentHostProcess, error) {
	return nil, errManagedRuntimeOnWindows
}

func (p *agentHostProcess) stop() error        { return nil }
func (p *agentHostProcess) alive() bool        { return false }
func (p *agentHostProcess) pid() int           { return 0 }
func (p *agentHostProcess) events() io.Reader  { return nil }
func (p *agentHostProcess) send([]byte) error  { return errManagedRuntimeOnWindows }
func (p *agentHostProcess) interrupt() error   { return errManagedRuntimeOnWindows }
func (p *agentHostProcess) exitReason() string { return "" }
