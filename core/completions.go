package core

import (
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	completionCacheTTL  = 2 * time.Second
	completionWalkLimit = 20000
	completionResultCap = 50
)

// WorktreeFiles liefert die zum Präfix passenden Pfade einer Session, relativ
// zu ihrem Arbeitsverzeichnis und auf limit Einträge gedeckelt.
func WorktreeFiles(session Session, query string, limit int) ([]string, error) {
	dir := strings.TrimSpace(session.Dir)
	if dir == "" {
		return nil, fmt.Errorf("Session %q hat kein Arbeitsverzeichnis", session.Name)
	}
	return rankWorktreePaths(cachedWorktreePaths(dir), query, limit), nil
}

type worktreePathEntry struct {
	paths []string
	at    time.Time
}

var (
	worktreePathMu    sync.Mutex
	worktreePathCache = map[string]worktreePathEntry{}
)

// cachedWorktreePaths hält die Liste kurz fest. Ohne das startet jeder
// Tastenanschlag im Composer zwei git-Prozesse.
func cachedWorktreePaths(dir string) []string {
	worktreePathMu.Lock()
	entry, known := worktreePathCache[dir]
	worktreePathMu.Unlock()
	if known && time.Since(entry.at) < completionCacheTTL {
		return entry.paths
	}
	paths := worktreePaths(dir)
	worktreePathMu.Lock()
	worktreePathCache[dir] = worktreePathEntry{paths: paths, at: time.Now()}
	worktreePathMu.Unlock()
	return paths
}

func worktreePaths(dir string) []string {
	if paths, isRepository := gitWorktreePaths(dir); isRepository {
		return paths
	}
	return walkWorktreePaths(dir)
}

// gitWorktreePaths fragt Git nach versionierten und nach nicht ignorierten
// unversionierten Dateien. Damit gilt .gitignore, ohne sie selbst zu deuten.
func gitWorktreePaths(dir string) ([]string, bool) {
	seen := map[string]struct{}{}
	paths := []string{}
	for _, args := range [][]string{
		{"ls-files", "-z"},
		{"ls-files", "-z", "--others", "--exclude-standard"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		raw, err := cmd.Output()
		if err != nil {
			return nil, false
		}
		for _, path := range strings.Split(string(raw), "\x00") {
			if path == "" {
				continue
			}
			if _, duplicate := seen[path]; duplicate {
				continue
			}
			seen[path] = struct{}{}
			paths = append(paths, path)
		}
	}
	return paths, true
}

// walkWorktreePaths ist der Weg ohne Git. Er überspringt die Verzeichnisse, die
// in jedem Projekt groß und uninteressant sind, und bricht hart ab.
func walkWorktreePaths(dir string) []string {
	paths := []string{}
	filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "node_modules", ".venv", "dist", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		relative, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return nil
		}
		paths = append(paths, filepath.ToSlash(relative))
		if len(paths) >= completionWalkLimit {
			return filepath.SkipAll
		}
		return nil
	})
	return paths
}

// rankWorktreePaths ordnet danach, wie direkt ein Pfad die Anfrage beantwortet:
// erst der Dateiname, der so beginnt, dann der Pfad, der so beginnt, dann der
// Pfad, der die Anfrage irgendwo enthält. Innerhalb einer Stufe gewinnt der
// kürzere Pfad.
func rankWorktreePaths(paths []string, query string, limit int) []string {
	if limit <= 0 {
		limit = completionResultCap
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	type ranked struct {
		path string
		rank int
	}
	matches := []ranked{}
	for _, path := range paths {
		lower := strings.ToLower(path)
		base := lower[strings.LastIndexByte(lower, '/')+1:]
		switch {
		case needle == "":
			matches = append(matches, ranked{path, 1})
		case strings.HasPrefix(base, needle):
			matches = append(matches, ranked{path, 0})
		case strings.HasPrefix(lower, needle):
			matches = append(matches, ranked{path, 1})
		case strings.Contains(lower, needle):
			matches = append(matches, ranked{path, 2})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank < matches[j].rank
		}
		if len(matches[i].path) != len(matches[j].path) {
			return len(matches[i].path) < len(matches[j].path)
		}
		return matches[i].path < matches[j].path
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		result = append(result, match.path)
	}
	return result
}
