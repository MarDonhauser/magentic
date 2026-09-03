//go:build !windows

package core

import (
	"os/exec"
	"syscall"
	"time"
)

// agentHostProcess wraps the vendor process an AgentHost owns. It runs in
// its own process group so stopping it can also stop every child it
// spawned, without matching anything by name or command line.
type agentHostProcess struct {
	cmd *exec.Cmd
}

// startAgentHostProcess launches binary with argv in dir, in its own process
// group.
func startAgentHostProcess(binary string, argv []string, dir string) (*agentHostProcess, error) {
	cmd := exec.Command(binary, argv...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &agentHostProcess{cmd: cmd}, nil
}

// agentHostStopGrace bounds how long stop waits for a clean exit after
// SIGTERM before it escalates to SIGKILL.
const agentHostStopGrace = 3 * time.Second

// stop terminates the process and every child in its process group. It
// signals only the recorded process group — never a lookup by name or path.
func (p *agentHostProcess) stop() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	pgid := p.cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_ = p.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(agentHostStopGrace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done
	}
	return nil
}

// alive reports whether the owned process has not yet exited.
func (p *agentHostProcess) alive() bool {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	return p.cmd.ProcessState == nil
}

// pid is the owned process's identity, for tests that must confirm no child
// of it survives a stop.
func (p *agentHostProcess) pid() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}
