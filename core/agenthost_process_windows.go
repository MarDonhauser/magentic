//go:build windows

package core

import "errors"

// agentHostProcess has no Windows implementation: the managed runtime is
// refused on Windows (see service-installation spec) rather than driven by
// an unverified process-ownership model.
type agentHostProcess struct{}

func startAgentHostProcess(string, []string, string) (*agentHostProcess, error) {
	return nil, errors.New("der managed Runtime wird unter Windows nicht unterstützt")
}

func (p *agentHostProcess) stop() error { return nil }
func (p *agentHostProcess) alive() bool { return false }
func (p *agentHostProcess) pid() int    { return 0 }
func (p *agentHostProcess) send([]byte) error {
	return errors.New("der managed Runtime wird unter Windows nicht unterstützt")
}
func (p *agentHostProcess) exitReason() string { return "" }
