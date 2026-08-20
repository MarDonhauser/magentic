package core

import (
	"os/exec"
	"strings"
	"sync"
	"time"
)

type gitRunner func(dir string, args ...string) (string, error)

func GitCmd(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).Output()
	return string(out), err
}

type gitMemoEntry struct {
	out string
	err error
	at  time.Time
}

var (
	gitMemoMu  sync.Mutex
	gitMemo    = map[string]gitMemoEntry{}
	gitMemoTTL = 10 * time.Second
)

func FlushGitMemo() {
	gitMemoMu.Lock()
	gitMemo = map[string]gitMemoEntry{}
	gitMemoMu.Unlock()
}

func GitCmdCached(dir string, args ...string) (string, error) {
	key := dir + "\x00" + strings.Join(args, "\x00")
	gitMemoMu.Lock()
	e, ok := gitMemo[key]
	gitMemoMu.Unlock()
	if ok && time.Since(e.at) < gitMemoTTL {
		return e.out, e.err
	}
	out, err := GitCmd(dir, args...)
	gitMemoMu.Lock()
	gitMemo[key] = gitMemoEntry{out: out, err: err, at: time.Now()}
	gitMemoMu.Unlock()
	return out, err
}
