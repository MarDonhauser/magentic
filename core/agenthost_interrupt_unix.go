//go:build !windows

package core

import (
	"errors"
	"syscall"
)

// interruptVendorTurn asks the owned vendor process to abort its running turn
// while staying alive for the next prompt. SIGINT is what a terminal user
// sends for exactly this; the process group is deliberately left alone —
// stopping the group is what Close does, and an interrupt must not become a
// stop.
func interruptVendorTurn(pid int) error {
	if pid <= 0 {
		return errors.New("kein laufender Agent-Prozess zum Unterbrechen")
	}
	return syscall.Kill(pid, syscall.SIGINT)
}
