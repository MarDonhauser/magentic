package core

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

type GitInfo struct {
	IsRepo    bool
	Branch    string
	Ahead     int
	Behind    int
	Staged    int
	Modified  int
	Untracked int
	LastMsg   string
	Files     []string
}

func (g GitInfo) Clean() bool {
	return g.Staged == 0 && g.Modified == 0 && g.Untracked == 0
}

type WorktreeInfo struct {
	Path   string
	Branch string
}

func GitCmd(dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	out, err := exec.Command("git", full...).Output()
	return string(out), err
}

func CollectGitInfo(dir string) GitInfo {
	info := GitInfo{}
	out, err := GitCmd(dir, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return info
	}
	info.IsRepo = true
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			info.Branch = strings.TrimPrefix(line, "# branch.head ")
		case strings.HasPrefix(line, "# branch.ab "):
			fmt.Sscanf(strings.TrimPrefix(line, "# branch.ab "), "+%d -%d", &info.Ahead, &info.Behind)
		case strings.HasPrefix(line, "1 "):
			xy := line[2:4]
			if xy[0] != '.' {
				info.Staged++
			}
			if xy[1] != '.' {
				info.Modified++
			}
			if parts := strings.SplitN(line, " ", 9); len(parts) == 9 {
				info.Files = append(info.Files, parts[8])
			}
		case strings.HasPrefix(line, "2 "):
			xy := line[2:4]
			if xy[0] != '.' {
				info.Staged++
			}
			if xy[1] != '.' {
				info.Modified++
			}
			if parts := strings.SplitN(line, " ", 10); len(parts) == 10 {
				info.Files = append(info.Files, strings.SplitN(parts[9], "\t", 2)[0])
			}
		case strings.HasPrefix(line, "? "):
			info.Untracked++
			info.Files = append(info.Files, line[2:])
		}
	}
	if msg, err := GitCmd(dir, "log", "-1", "--format=%s"); err == nil {
		info.LastMsg = strings.TrimSpace(msg)
	}
	return info
}

type SessionChanges struct {
	Known   bool
	Files   []string
	Commits int
}

func CaptureBaseline(dir string) (string, []string) {
	head, err := GitCmd(dir, "rev-parse", "HEAD")
	if err != nil {
		return "", nil
	}
	gi := CollectGitInfo(dir)
	return strings.TrimSpace(head), gi.Files
}

func CollectSessionChanges(a Agent, gi GitInfo) SessionChanges {
	sc := SessionChanges{Known: a.BaseCommit != ""}
	if !sc.Known {
		sc.Files = gi.Files
		return sc
	}
	base := map[string]bool{}
	for _, f := range a.BaseDirty {
		base[f] = true
	}
	for _, f := range gi.Files {
		if !base[f] {
			sc.Files = append(sc.Files, f)
		}
	}
	if out, err := GitCmd(a.Dir, "rev-list", "--count", a.BaseCommit+"..HEAD"); err == nil {
		fmt.Sscanf(strings.TrimSpace(out), "%d", &sc.Commits)
	}
	return sc
}

func AheadBehind(dir, baseBranch string) (ahead, behind int) {
	out, err := GitCmd(dir, "rev-list", "--left-right", "--count", baseBranch+"...HEAD")
	if err != nil {
		return 0, 0
	}
	fmt.Sscanf(strings.TrimSpace(out), "%d\t%d", &behind, &ahead)
	return ahead, behind
}

func CollectWorktrees(projectPath string) []WorktreeInfo {
	out, err := GitCmd(projectPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	var wts []WorktreeInfo
	var cur WorktreeInfo
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			if cur.Path != "" {
				wts = append(wts, cur)
			}
			cur = WorktreeInfo{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "branch "):
			cur.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	if cur.Path != "" {
		wts = append(wts, cur)
	}
	return wts
}

func CreateWorktree(projectPath, agentName string) (string, error) {
	base := filepath.Dir(projectPath)
	projName := filepath.Base(projectPath)
	wtPath := filepath.Join(base, projName+"-agents", agentName)
	branch := "agent/" + agentName
	if _, err := GitCmd(projectPath, "worktree", "add", "-b", branch, wtPath); err != nil {
		if _, err2 := GitCmd(projectPath, "worktree", "add", wtPath, branch); err2 != nil {
			return "", fmt.Errorf("worktree add fehlgeschlagen: %w", err)
		}
	}
	return wtPath, nil
}
