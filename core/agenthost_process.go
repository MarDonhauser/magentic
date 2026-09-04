//go:build !windows

package core

import (
	"errors"
	"io"
	"os/exec"
	"syscall"
	"time"
)

// managedRuntimeSupported reports whether this platform can own a managed
// agent process at all. It is asked before an agent host claims its socket,
// so an unsupported platform refuses with the reason stated instead of
// listening first and failing at the spawn.
func managedRuntimeSupported() error { return nil }

// agentHostProcess wraps the vendor process an AgentHost owns. It runs in
// its own process group so stopping it can also stop every child it
// spawned, without matching anything by name or command line. Everything
// that addresses the process — writing a prompt, reading its protocol
// output, interrupting its turn, stopping it — lives here, so process
// ownership has one place per platform.
type agentHostProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
	exited  chan struct{}
	exitErr error
}

// startAgentHostProcess launches binary with argv in dir, in its own process
// group. The vendor's stdin stays open: the stream-json protocol accepts
// further prompts on it for the life of the process. Its stdout is the
// protocol stream the host reads turn and delivery facts from.
func startAgentHostProcess(binary string, argv []string, dir string) (*agentHostProcess, error) {
	cmd := exec.Command(binary, argv...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, err
	}
	process := &agentHostProcess{cmd: cmd, stdin: stdin, stdout: stdout, exited: make(chan struct{})}
	go process.watch()
	return process, nil
}

// watch waits for the owned process exactly once and records how it ended,
// so an unexpected exit is a stated fact rather than a silent absence. It
// never restarts anything: a process that exited without being asked to
// stays exited.
func (p *agentHostProcess) watch() {
	p.exitErr = p.cmd.Wait()
	close(p.exited)
}

// events is the vendor's protocol stream, or nil for a host that owns no
// process.
func (p *agentHostProcess) events() io.Reader {
	if p == nil {
		return nil
	}
	return p.stdout
}

// send writes one protocol line to the vendor's stdin. It is how a queued
// prompt reaches a managed Session — acknowledged by the protocol's echo,
// never by watching a pane afterwards.
func (p *agentHostProcess) send(line []byte) error {
	if p == nil || p.stdin == nil {
		return errors.New("dieser Agent-Host besitzt keinen Prozess, an den geliefert werden könnte")
	}
	if _, err := p.stdin.Write(append(append([]byte{}, line...), '\n')); err != nil {
		return err
	}
	return nil
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
	select {
	case <-p.exited:
	case <-time.After(agentHostStopGrace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-p.exited
	}
	_ = p.stdin.Close()
	return nil
}

// interrupt asks the owned process to abort its running turn while staying
// alive for the next prompt. SIGINT is what a terminal user sends for exactly
// this, and only the recorded process itself is signalled: the process group
// is deliberately left alone, because stopping the group is what stop does
// and an interrupt must not become a stop.
func (p *agentHostProcess) interrupt() error {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return errors.New("kein laufender Agent-Prozess zum Unterbrechen")
	}
	return syscall.Kill(p.cmd.Process.Pid, syscall.SIGINT)
}

// alive reports whether the owned process has not yet exited.
func (p *agentHostProcess) alive() bool {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return false
	}
	select {
	case <-p.exited:
		return false
	default:
		return true
	}
}

// exitReason states how the owned process ended, or "" while it is alive.
func (p *agentHostProcess) exitReason() string {
	if p == nil {
		return ""
	}
	select {
	case <-p.exited:
		if p.exitErr == nil {
			return "Prozess endete ohne Fehlermeldung"
		}
		return "Prozess endete: " + p.exitErr.Error()
	default:
		return ""
	}
}

// pid is the owned process's identity, for tests that must confirm no child
// of it survives a stop.
func (p *agentHostProcess) pid() int {
	if p == nil || p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}
