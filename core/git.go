package core

import (
	"os/exec"
)

type gitRunner func(dir string, args ...string) (string, error)

func GitCmd(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).Output()
	return string(out), err
}
