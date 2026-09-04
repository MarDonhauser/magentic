package core

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"os"
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

// worktreePaths fragt zuerst Repositories: dort gilt .gitignore, ohne sie
// selbst zu deuten. Erst wenn das Verzeichnis nachweislich kein Repository ist
// oder Git keine Kenntnis liefert, wird es abgelaufen.
func worktreePaths(dir string) []string {
	if fact := NewRepositories().WorktreePaths(context.Background(), dir); fact.Known() {
		return fact.Value
	}
	return walkWorktreePaths(dir)
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

// SlashCommand ist ein Eintrag, den der Composer hinter einem Schrägstrich
// anbietet. Er muss nicht in der Liste des Agenten stehen: aufgelöst wird der
// Text ohnehin erst vom Agenten selbst.
type SlashCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
}

const (
	SlashCommandSourceUser    = "user"
	SlashCommandSourceProject = "project"
	SlashCommandSourcePlugin  = "plugin"
)

// commandRoot ist ein Verzeichnis, in dem ein Vendor Befehle oder Skills hält.
// nested bedeutet ein Verzeichnis je Skill mit SKILL.md darin statt einer Datei
// je Befehl. prefix trägt den Plugin-Namen, wenn einer dazugehört.
type commandRoot struct {
	dir    string
	nested bool
	source string
	prefix string
}

// SlashCommands liefert die Befehle und Skills eines Vendors für ein
// Arbeitsverzeichnis. Ein unbekannter Vendor liefert nil ohne Fehler.
func SlashCommands(vendor AgentVendor, dir string) []SlashCommand {
	roots := commandRoots(vendor, dir)
	if len(roots) == 0 {
		return nil
	}
	// Spätere Wurzeln überschreiben frühere: projektlokal schlägt global.
	byName := map[string]SlashCommand{}
	for _, root := range roots {
		for _, command := range readCommandRoot(root) {
			byName[command.Name] = command
		}
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	commands := make([]SlashCommand, 0, len(names))
	for _, name := range names {
		commands = append(commands, byName[name])
	}
	return commands
}

func commandRoots(vendor AgentVendor, dir string) []commandRoot {
	if vendor != AgentVendorClaude {
		return nil
	}
	roots := []commandRoot{}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		claude := filepath.Join(home, ".claude")
		roots = append(roots,
			commandRoot{dir: filepath.Join(claude, "commands"), source: SlashCommandSourceUser},
			commandRoot{dir: filepath.Join(claude, "skills"), nested: true, source: SlashCommandSourceUser},
		)
		roots = append(roots, pluginCommandRoots(filepath.Join(claude, "plugins", "cache"))...)
	}
	if strings.TrimSpace(dir) != "" {
		claude := filepath.Join(dir, ".claude")
		roots = append(roots,
			commandRoot{dir: filepath.Join(claude, "commands"), source: SlashCommandSourceProject},
			commandRoot{dir: filepath.Join(claude, "skills"), nested: true, source: SlashCommandSourceProject},
		)
	}
	return roots
}

// pluginCommandRoots sucht die Skill-Verzeichnisse installierter Plugins unter
// cache/<owner>/<plugin>/<version>/skills und benennt sie mit ihrem Plugin.
func pluginCommandRoots(cacheDir string) []commandRoot {
	roots := []commandRoot{}
	owners, err := os.ReadDir(cacheDir)
	if err != nil {
		return roots
	}
	for _, owner := range owners {
		if !owner.IsDir() {
			continue
		}
		plugins, err := os.ReadDir(filepath.Join(cacheDir, owner.Name()))
		if err != nil {
			continue
		}
		for _, plugin := range plugins {
			if !plugin.IsDir() {
				continue
			}
			versions, err := os.ReadDir(filepath.Join(cacheDir, owner.Name(), plugin.Name()))
			if err != nil {
				continue
			}
			for _, version := range versions {
				if !version.IsDir() {
					continue
				}
				roots = append(roots, commandRoot{
					dir:    filepath.Join(cacheDir, owner.Name(), plugin.Name(), version.Name(), "skills"),
					nested: true,
					source: SlashCommandSourcePlugin,
					prefix: plugin.Name(),
				})
			}
		}
	}
	return roots
}

func readCommandRoot(root commandRoot) []SlashCommand {
	entries, err := os.ReadDir(root.dir)
	if err != nil {
		return nil
	}
	commands := []SlashCommand{}
	for _, entry := range entries {
		name, file := "", ""
		if root.nested {
			if !entry.IsDir() {
				continue
			}
			name, file = entry.Name(), filepath.Join(root.dir, entry.Name(), "SKILL.md")
		} else {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			name = strings.TrimSuffix(entry.Name(), ".md")
			file = filepath.Join(root.dir, entry.Name())
		}
		if declared := frontmatterField(file, "name"); declared != "" {
			name = declared
		}
		if root.prefix != "" {
			name = root.prefix + ":" + name
		}
		commands = append(commands, SlashCommand{
			Name:        name,
			Description: frontmatterField(file, "description"),
			Source:      root.source,
		})
	}
	return commands
}

// frontmatterField liest einen Schlüssel aus dem führenden ---Block. Fehlt der
// Block oder der Schlüssel, ist das Ergebnis leer; eine fehlende Beschreibung
// ist kein Fehler, sondern nur eine karge Zeile im Menü.
func frontmatterField(path, key string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return ""
	}
	prefix := key + ":"
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			return ""
		}
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		return strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, prefix)), `"'`)
	}
	return ""
}

// PromptLinePattern beschreibt die Eingabezeile eines Vendors als Präfix der
// getrimmten Pufferzeile. Ein leerer Wert bedeutet: nicht verdecken.
func PromptLinePattern(vendor AgentVendor) string {
	if vendor == AgentVendorClaude {
		return "❯"
	}
	return ""
}
