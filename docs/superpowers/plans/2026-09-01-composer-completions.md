# Composer-Completions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Der Composer schlägt beim Tippen von `@` Dateien aus dem Worktree und bei `/` die Befehle und Skills des Session-Vendors vor, und die Prompt-Zeile des Agenten wird verdeckt, solange sie nicht gebraucht wird.

**Architecture:** Zwei reine Datenquellen in Go (`core/completions.go`) hinter zwei Wails-Bindungen. Die Auslöser- und Einfügelogik im Frontend ist eine reine Textfunktion in einem eigenen Modul und wird als solche getestet; das Popover ist nur ihre Darstellung. Die Zustellung an den Agenten wird nicht angefasst.

**Tech Stack:** Go 1.x (Standardbibliothek, `os/exec` für `git`), Wails v2, Vanilla JS mit Vite, `node --test`.

**Spec:** `docs/superpowers/specs/2026-09-01-composer-completions-design.md`

## Global Constraints

- Oberflächentexte und Kommentare in Code, den dieses Projekt selbst schreibt: Deutsch. Bezeichner und Godoc-Kommentare folgen dem umgebenden Stil der Datei.
- Keine neue Abhängigkeit. `git` wird über `os/exec` aufgerufen, sonst nur Standardbibliothek.
- `core/provider.go` und das Interface `AgentProvider` werden **nicht** verändert. Die parallel laufende Vendor-Umbauarbeit darf nicht kollidieren.
- Kein Completion-Fehler darf das Tippen oder Senden blockieren. Jeder Fehlerfall endet in einer leeren Liste.
- `limit` an der Wails-Bindung ist 50, der Cache lebt 2 Sekunden, der Verzeichnisdurchlauf bricht bei 20000 Einträgen ab.
- Übernehmen aus dem Menü schreibt nur Text in das Feld. Es sendet nie.
- Keine Statuspillen mit vorangestelltem Punkt, keine Mikrohinweise in Ecken (siehe globale UI-Regeln).

---

### Task 1: Dateiliste aus dem Worktree

**Files:**
- Create: `core/completions.go`
- Test: `core/completions_test.go`

**Interfaces:**
- Consumes: `Session` aus `core/state.go` (Felder `Name`, `Dir`).
- Produces: `func WorktreeFiles(session Session, query string, limit int) ([]string, error)`

- [ ] **Step 1: Write the failing test**

Erstelle `core/completions_test.go`:

```go
package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// completionsTestRepo legt ein Git-Repository mit versionierter, nicht
// versionierter und ignorierter Datei an und gibt sein Verzeichnis zurück.
func completionsTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("Verzeichnis anlegen: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("Datei schreiben: %v", err)
		}
	}
	write(".gitignore", "geheim.txt\n")
	write("core/tmux.go", "package core\n")
	write("README.md", "# Test\n")
	write("geheim.txt", "nicht sichtbar\n")
	write("neu.txt", "noch nicht versioniert\n")

	for _, args := range [][]string{
		{"init", "-q"},
		{"add", ".gitignore", "core/tmux.go", "README.md"},
		{"-c", "user.email=t@example.com", "-c", "user.name=Test", "commit", "-qm", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

func TestWorktreeFilesIncludesUntrackedAndSkipsIgnored(t *testing.T) {
	session := Session{Name: "test", Dir: completionsTestRepo(t)}
	paths, err := WorktreeFiles(session, "", 50)
	if err != nil {
		t.Fatalf("WorktreeFiles: %v", err)
	}
	if !containsPath(paths, "core/tmux.go") {
		t.Errorf("versionierte Datei fehlt: %v", paths)
	}
	if !containsPath(paths, "neu.txt") {
		t.Errorf("nicht versionierte Datei fehlt: %v", paths)
	}
	if containsPath(paths, "geheim.txt") {
		t.Errorf("ignorierte Datei ist enthalten: %v", paths)
	}
}

func TestWorktreeFilesRanksNameMatchesFirst(t *testing.T) {
	session := Session{Name: "test", Dir: completionsTestRepo(t)}
	paths, err := WorktreeFiles(session, "tmux", 50)
	if err != nil {
		t.Fatalf("WorktreeFiles: %v", err)
	}
	if len(paths) == 0 || paths[0] != "core/tmux.go" {
		t.Errorf("Namenstreffer steht nicht vorn: %v", paths)
	}
}

func TestWorktreeFilesWithoutGitRepositoryWalksDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "lose.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("Datei schreiben: %v", err)
	}
	paths, err := WorktreeFiles(Session{Name: "test", Dir: dir}, "", 50)
	if err != nil {
		t.Fatalf("WorktreeFiles: %v", err)
	}
	if !containsPath(paths, "lose.txt") {
		t.Errorf("Durchlauf hat die Datei nicht gefunden: %v", paths)
	}
}

func TestWorktreeFilesRespectsLimit(t *testing.T) {
	session := Session{Name: "test", Dir: completionsTestRepo(t)}
	paths, err := WorktreeFiles(session, "", 2)
	if err != nil {
		t.Fatalf("WorktreeFiles: %v", err)
	}
	if len(paths) > 2 {
		t.Errorf("Limit missachtet: %d Einträge", len(paths))
	}
}

func TestWorktreeFilesWithoutDirectoryFails(t *testing.T) {
	if _, err := WorktreeFiles(Session{Name: "test"}, "", 50); err == nil {
		t.Error("Session ohne Verzeichnis muss einen Fehler liefern")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/ -run TestWorktreeFiles -v`
Expected: FAIL, `undefined: WorktreeFiles`

- [ ] **Step 3: Write minimal implementation**

Erstelle `core/completions.go`:

```go
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
	completionCacheTTL   = 2 * time.Second
	completionWalkLimit  = 20000
	completionResultCap  = 50
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

// walkWorktreePaths ist der Weg ohne Git. Er überspringt die Verzeichnisse,
// die in jedem Projekt groß und uninteressant sind, und bricht hart ab.
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/ -run TestWorktreeFiles -v`
Expected: PASS, fünf Tests

- [ ] **Step 5: Commit**

```bash
git add core/completions.go core/completions_test.go
git commit -m "feat: Dateiliste eines Session-Worktrees für Composer-Completions"
```

---

### Task 2: Befehle und Skills pro Vendor

**Files:**
- Modify: `core/completions.go`
- Modify: `core/completions_test.go`

**Interfaces:**
- Consumes: `AgentVendor`, `AgentVendorClaude` aus `core/state.go`.
- Produces:
  - `type SlashCommand struct { Name, Description, Source string }`
  - `func SlashCommands(vendor AgentVendor, dir string) []SlashCommand`
  - `func PromptLinePattern(vendor AgentVendor) string`

- [ ] **Step 1: Write the failing test**

Ergänze in `core/completions_test.go`:

```go
func writeCompletionFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("Verzeichnis anlegen: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("Datei schreiben: %v", err)
	}
}

func findCommand(commands []SlashCommand, name string) (SlashCommand, bool) {
	for _, command := range commands {
		if command.Name == name {
			return command, true
		}
	}
	return SlashCommand{}, false
}

func TestSlashCommandsReadsCommandsSkillsAndPlugins(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeCompletionFile(t, filepath.Join(home, ".claude", "commands", "adapt.md"),
		"---\ndescription: Responsive layout pass\n---\n\nInhalt\n")
	writeCompletionFile(t, filepath.Join(home, ".claude", "skills", "apple-hig", "SKILL.md"),
		"---\nname: apple-hig\ndescription: Apple HIG Entscheidungshilfen\n---\n\nInhalt\n")
	writeCompletionFile(t, filepath.Join(home, ".claude", "plugins", "cache", "official", "design", "1.0.0", "skills", "polish", "SKILL.md"),
		"---\nname: polish\ndescription: Letzter Schliff\n---\n\nInhalt\n")

	commands := SlashCommands(AgentVendorClaude, "")

	adapt, found := findCommand(commands, "adapt")
	if !found || adapt.Description != "Responsive layout pass" || adapt.Source != SlashCommandSourceUser {
		t.Errorf("Command adapt falsch gelesen: %+v (gefunden: %v)", adapt, found)
	}
	skill, found := findCommand(commands, "apple-hig")
	if !found || skill.Description != "Apple HIG Entscheidungshilfen" {
		t.Errorf("Skill apple-hig falsch gelesen: %+v (gefunden: %v)", skill, found)
	}
	plugin, found := findCommand(commands, "design:polish")
	if !found || plugin.Source != SlashCommandSourcePlugin {
		t.Errorf("Plugin-Skill falsch gelesen: %+v (gefunden: %v)", plugin, found)
	}
}

func TestSlashCommandsProjectEntryOverridesUserEntry(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	t.Setenv("HOME", home)
	writeCompletionFile(t, filepath.Join(home, ".claude", "commands", "deploy.md"),
		"---\ndescription: global\n---\n")
	writeCompletionFile(t, filepath.Join(project, ".claude", "commands", "deploy.md"),
		"---\ndescription: projektlokal\n---\n")

	commands := SlashCommands(AgentVendorClaude, project)

	deploy, found := findCommand(commands, "deploy")
	if !found || deploy.Description != "projektlokal" || deploy.Source != SlashCommandSourceProject {
		t.Errorf("projektlokaler Eintrag hat nicht gewonnen: %+v (gefunden: %v)", deploy, found)
	}
	count := 0
	for _, command := range commands {
		if command.Name == "deploy" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("deploy steht %d mal in der Liste, erwartet 1", count)
	}
}

func TestSlashCommandsUnknownVendorIsEmpty(t *testing.T) {
	if commands := SlashCommands(AgentVendorCopilot, ""); commands != nil {
		t.Errorf("unbekannter Vendor muss nil liefern, war: %+v", commands)
	}
}

func TestPromptLinePattern(t *testing.T) {
	if got := PromptLinePattern(AgentVendorClaude); got != "❯" {
		t.Errorf("Claude-Muster war %q", got)
	}
	if got := PromptLinePattern(AgentVendorCopilot); got != "" {
		t.Errorf("unbekannter Vendor muss ein leeres Muster liefern, war %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./core/ -run 'TestSlashCommands|TestPromptLinePattern' -v`
Expected: FAIL, `undefined: SlashCommands`

- [ ] **Step 3: Write minimal implementation**

Ergänze in `core/completions.go` — zuerst die Importe `bufio`, `os` hinzufügen, dann:

```go
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
// flat bedeutet eine Datei je Befehl, nested ein Verzeichnis je Skill mit
// SKILL.md darin. prefix trägt den Plugin-Namen, wenn einer dazugehört.
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
	order := []string{}
	for _, root := range roots {
		for _, command := range readCommandRoot(root) {
			if _, known := byName[command.Name]; !known {
				order = append(order, command.Name)
			}
			byName[command.Name] = command
		}
	}
	sort.Strings(order)
	commands := make([]SlashCommand, 0, len(order))
	for _, name := range order {
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

// frontmatterField liest einen Schlüssel aus dem führenden ----Block. Fehlt der
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./core/ -run 'TestSlashCommands|TestPromptLinePattern' -v`
Expected: PASS, vier Tests

- [ ] **Step 5: Commit**

```bash
git add core/completions.go core/completions_test.go
git commit -m "feat: Befehle und Skills eines Vendors für Composer-Completions"
```

---

### Task 3: Wails-Bindungen

**Files:**
- Modify: `app/app.go` (neue Methoden neben `SendMessage`, ab Zeile 524)

**Interfaces:**
- Consumes: `core.WorktreeFiles`, `core.SlashCommands`, `core.PromptLinePattern`, `loadSessionByID` (`app/app.go:144`).
- Produces:
  - `func (a *App) CompleteFiles(sessionID, query string) ([]string, error)`
  - `func (a *App) CompleteCommands(sessionID, query string) ([]core.SlashCommand, error)`
  - `func (a *App) PromptLinePattern(sessionID string) string`

- [ ] **Step 1: Implementierung schreiben**

Ergänze in `app/app.go` direkt nach `SendMessage`:

```go
// completionResultLimit deckelt, was eine Completion-Abfrage zurückgibt. Mehr
// als das liest niemand in einem Popover.
const completionResultLimit = 50

// CompleteFiles liefert Worktree-Pfade der Session für das @-Menü. Ein Fehler
// beim Lesen ist kein Fehler der Eingabe: die Liste bleibt leer.
func (a *App) CompleteFiles(sessionID, query string) ([]string, error) {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return nil, err
	}
	paths, err := core.WorktreeFiles(session, query, completionResultLimit)
	if err != nil {
		return []string{}, nil
	}
	return paths, nil
}

// CompleteCommands liefert die Befehle des Session-Vendors für das /-Menü.
func (a *App) CompleteCommands(sessionID, query string) ([]core.SlashCommand, error) {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return nil, err
	}
	needle := strings.ToLower(strings.TrimSpace(query))
	matches := []core.SlashCommand{}
	for _, command := range core.SlashCommands(session.SessionVendor(), session.Dir) {
		if needle != "" && !strings.HasPrefix(strings.ToLower(command.Name), needle) {
			continue
		}
		matches = append(matches, command)
		if len(matches) >= completionResultLimit {
			break
		}
	}
	return matches, nil
}

// PromptLinePattern sagt dem Frontend, woran es die Eingabezeile des Agenten
// im Terminalpuffer erkennt. Leer heißt: nichts verdecken.
func (a *App) PromptLinePattern(sessionID string) string {
	_, session, err := loadSessionByID(sessionID)
	if err != nil {
		return ""
	}
	return core.PromptLinePattern(session.SessionVendor())
}
```

- [ ] **Step 2: Bindungen neu erzeugen und übersetzen**

Run: `go build ./... && cd app && wails generate module`
Expected: `app/frontend/wailsjs/go/main/App.js` enthält `CompleteFiles`, `CompleteCommands`, `PromptLinePattern`; `models.ts` enthält `SlashCommand`.

Falls `wails` nicht auf PATH liegt, entfällt der Generierungsschritt und die drei Namen werden von Hand in `app/frontend/wailsjs/go/main/App.js` und `App.d.ts` ergänzt, dem Muster der bestehenden Einträge folgend.

- [ ] **Step 3: Bestehende Tests laufen lassen**

Run: `go test ./core/ ./app/`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add app/app.go app/frontend/wailsjs
git commit -m "feat: Wails-Bindungen für Composer-Completions"
```

---

### Task 4: Auslöser- und Einfügelogik

Reine Textfunktionen, ohne DOM. Sie sind der Teil, der Fehler machen kann, und werden deshalb getrennt getestet.

**Files:**
- Create: `app/frontend/src/features/composer/completion-state.js`
- Test: `app/frontend/src/features/composer/completion-state.test.js`

**Interfaces:**
- Produces:
  - `function completionTrigger(text, caret)` → `null` oder `{ kind: 'file' | 'command', query: string, start: number }`
  - `function applyCompletion(text, trigger, value)` → `{ text: string, caret: number }`

- [ ] **Step 1: Write the failing test**

Erstelle `app/frontend/src/features/composer/completion-state.test.js`:

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import { completionTrigger, applyCompletion } from './completion-state.js';

test('@ öffnet das Dateimenü an einer Wortgrenze', () => {
  const trigger = completionTrigger('lies @core/tm', 13);
  assert.deepEqual(trigger, { kind: 'file', query: 'core/tm', start: 5 });
});

test('@ mitten in einem Wort löst nicht aus', () => {
  assert.equal(completionTrigger('mail@example', 12), null);
});

test('/ löst nur als erstes Zeichen der Nachricht aus', () => {
  assert.deepEqual(completionTrigger('/rev', 4), { kind: 'command', query: 'rev', start: 0 });
  assert.equal(completionTrigger('bitte /rev', 10), null);
});

test('ein Leerzeichen nach dem Auslöser schliesst das Menü', () => {
  assert.equal(completionTrigger('lies @core ', 11), null);
  assert.equal(completionTrigger('/review jetzt', 13), null);
});

test('ohne Auslöser gibt es kein Menü', () => {
  assert.equal(completionTrigger('einfacher Text', 14), null);
  assert.equal(completionTrigger('', 0), null);
});

test('der Auslöser richtet sich nach der Schreibmarke, nicht nach dem Ende', () => {
  assert.deepEqual(completionTrigger('@core und mehr', 5), { kind: 'file', query: 'core', start: 0 });
});

test('Übernehmen ersetzt nur den Auslöserbereich und setzt die Marke dahinter', () => {
  const trigger = completionTrigger('lies @core/tm', 13);
  const result = applyCompletion('lies @core/tm', trigger, 'core/tmux.go');
  assert.equal(result.text, 'lies @core/tmux.go ');
  assert.equal(result.caret, 19);
});

test('Übernehmen eines Befehls behält den Rest der Zeile', () => {
  const trigger = completionTrigger('/rev danach', 4);
  const result = applyCompletion('/rev danach', trigger, 'review');
  assert.equal(result.text, '/review danach');
  assert.equal(result.caret, 8);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd app/frontend && node --test src/features/composer/completion-state.test.js`
Expected: FAIL, `Cannot find module './completion-state.js'`

- [ ] **Step 3: Write minimal implementation**

Erstelle `app/frontend/src/features/composer/completion-state.js`:

```js
// Auslöser- und Einfügelogik des Composer-Menüs, ohne DOM, damit sie prüfbar
// bleibt. Der Composer selbst hängt nur die Darstellung daran.

const TRIGGERS = { '@': 'file', '/': 'command' };

// completionTrigger sucht von der Schreibmarke aus rückwärts nach einem
// Auslöser. Ein Leerzeichen dazwischen beendet die Suche: wer weitergeschrieben
// hat, meint keine Vervollständigung mehr.
export function completionTrigger(text, caret) {
  for (let i = caret - 1; i >= 0; i--) {
    const char = text[i];
    if (char === ' ' || char === '\n' || char === '\t') return null;
    const kind = TRIGGERS[char];
    if (!kind) continue;
    // Ein Schrägstrich zählt nur als erstes Zeichen der Nachricht; der Agent
    // deutet ihn auch nur dort als Befehl. Überall sonst ist er ein gewöhnliches
    // Pfadzeichen und darf die Suche nach einem @ davor nicht abbrechen.
    if (kind === 'command' && i !== 0) continue;
    // Ein @ braucht eine Wortgrenze davor, sonst trifft es jede Mailadresse.
    const previous = i > 0 ? text[i - 1] : '';
    if (kind === 'file' && previous && !' \n\t('.includes(previous)) return null;
    return { kind, query: text.slice(i + 1, caret), start: i };
  }
  return null;
}

// applyCompletion ersetzt den Auslöserbereich durch den gewählten Wert. Danach
// steht genau ein Leerzeichen, und die Schreibmarke steht dahinter — egal ob
// eines nachrückte oder schon da war, damit man in beiden Fällen gleich
// weitertippt. Der Text dahinter bleibt unangetastet.
export function applyCompletion(text, trigger, value) {
  if (!trigger) return { text, caret: text.length };
  const marker = text[trigger.start];
  const head = text.slice(0, trigger.start);
  const tail = text.slice(trigger.start + 1 + trigger.query.length);
  const rest = tail.startsWith(' ') ? tail.slice(1) : tail;
  const inserted = `${marker}${value}`;
  return {
    text: `${head}${inserted} ${rest}`,
    caret: head.length + inserted.length + 1,
  };
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd app/frontend && node --test src/features/composer/completion-state.test.js`
Expected: PASS, acht Tests

- [ ] **Step 5: Gesamtes Frontend-Testfeld laufen lassen**

Run: `cd app/frontend && npm test`
Expected: PASS, keine Regression in den bestehenden Dateien

- [ ] **Step 6: Commit**

```bash
git add app/frontend/src/features/composer/
git commit -m "feat: Auslöser- und Einfügelogik für Composer-Completions"
```

---

### Task 5: Das Menü im Composer

**Files:**
- Modify: `app/frontend/index.html:90-103` (Popover-Element im Composer-Formular)
- Modify: `app/frontend/src/main.js` (Import, Verdrahtung am Textfeld)
- Modify: `app/frontend/src/style.css` (Ende der Datei)

**Interfaces:**
- Consumes: `completionTrigger`, `applyCompletion` aus Task 4; `CompleteFiles`, `CompleteCommands` aus Task 3.
- Produces: nichts für spätere Tasks.

- [ ] **Step 1: Popover-Element ergänzen**

In `app/frontend/index.html`, direkt vor `<label class="sr-only" for="term-prompt">`:

```html
<div id="term-completions" class="term-completions" role="listbox" aria-label="Vorschläge" hidden></div>
```

- [ ] **Step 2: Stile ergänzen**

Am Ende von `app/frontend/src/style.css`:

```css
.term-completions {
  position: absolute;
  bottom: calc(100% + var(--space-sm));
  left: 0;
  right: 0;
  max-height: calc(10 * 30px);  /* zehn Zeilen, danach wird gescrollt */
  overflow-y: auto;
  z-index: 20;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: var(--radius-panel);
  box-shadow: var(--popover-shadow);
  padding: var(--space-xs);
}

.term-completions .row {
  display: flex;
  gap: var(--space-sm);
  align-items: baseline;
  padding: 6px var(--space-sm);
  border-radius: var(--radius-control);
  cursor: pointer;
}

.term-completions .row[aria-selected="true"] { background: var(--accent-soft); }

.term-completions .name {
  font-family: var(--font-mono);
  font-size: var(--fs-label);
  color: var(--ink);
  white-space: nowrap;
}

.term-completions .desc {
  font-size: var(--fs-meta);
  color: var(--ink-2);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
```

`#term-composer` braucht dafür `position: relative`. Prüfe mit `grep -n "#term-composer" app/frontend/src/style.css`, ob das schon gesetzt ist, und ergänze es sonst in der bestehenden Regel.

- [ ] **Step 3: Verdrahtung in main.js**

Import oben ergänzen:

```js
import { completionTrigger, applyCompletion } from './features/composer/completion-state.js';
```

`CompleteFiles` und `CompleteCommands` zum bestehenden Import aus `../wailsjs/go/main/App` hinzufügen (der Block ab Zeile 25).

Nach `const termComposerEl = $('term-composer');` ergänzen:

```js
const termCompletionsEl = $('term-completions');
let completionState = { trigger: null, items: [], index: 0, token: 0 };

function renderCompletions() {
  const { items, index } = completionState;
  if (!items.length) {
    termCompletionsEl.hidden = true;
    termCompletionsEl.innerHTML = '';
    return;
  }
  termCompletionsEl.innerHTML = items.map((item, i) => `
    <div class="row" role="option" data-i="${i}" aria-selected="${i === index}">
      <span class="name">${esc(item.name)}</span>
      <span class="desc">${esc(item.description || '')}</span>
    </div>`).join('');
  termCompletionsEl.hidden = false;
  termCompletionsEl.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: 'nearest' });
}

function closeCompletions() {
  completionState = { trigger: null, items: [], index: 0, token: completionState.token + 1 };
  renderCompletions();
}

// refreshCompletions holt die Liste passend zum Auslöser an der Schreibmarke.
// Ein Token verwirft Antworten, die zu einer älteren Eingabe gehören.
async function refreshCompletions() {
  const sessionID = terms.get(activeTerm)?.sessionID;
  const trigger = sessionID ? completionTrigger(termPromptEl.value, termPromptEl.selectionStart) : null;
  if (!trigger) { closeCompletions(); return; }
  const token = completionState.token + 1;
  completionState = { ...completionState, trigger, token };
  try {
    const items = trigger.kind === 'file'
      ? (await CompleteFiles(String(sessionID), trigger.query)).map(p => ({ name: p, description: '' }))
      : await CompleteCommands(String(sessionID), trigger.query);
    if (token !== completionState.token) return;
    completionState = { trigger, items: items || [], index: 0, token };
  } catch {
    completionState = { trigger, items: [], index: 0, token };
  }
  renderCompletions();
}

function acceptCompletion() {
  const { trigger, items, index } = completionState;
  if (!trigger || !items.length) return false;
  const result = applyCompletion(termPromptEl.value, trigger, items[index].name);
  termPromptEl.value = result.text;
  termPromptEl.setSelectionRange(result.caret, result.caret);
  closeCompletions();
  updateComposerControls(false);
  return true;
}

termPromptEl.addEventListener('input', refreshCompletions);
termPromptEl.addEventListener('blur', () => setTimeout(closeCompletions, 120));

termCompletionsEl.addEventListener('mousedown', e => {
  const row = e.target.closest('.row');
  if (!row) return;
  e.preventDefault();
  completionState = { ...completionState, index: Number(row.dataset.i) };
  acceptCompletion();
});
```

- [ ] **Step 4: Tastaturbedienung vor die bestehende Enter-Behandlung hängen**

Die vorhandene `keydown`-Behandlung am Textfeld (um Zeile 520, die auf `⌘/Strg + Enter` reagiert) bekommt davor:

```js
termPromptEl.addEventListener('keydown', e => {
  if (!completionState.items.length) return;
  const last = completionState.items.length - 1;
  if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
    e.preventDefault();
    const step = e.key === 'ArrowDown' ? 1 : -1;
    const next = completionState.index + step;
    completionState = { ...completionState, index: next < 0 ? last : next > last ? 0 : next };
    renderCompletions();
    return;
  }
  if (e.key === 'Enter' || e.key === 'Tab') {
    e.preventDefault();
    e.stopImmediatePropagation();
    acceptCompletion();
    return;
  }
  if (e.key === 'Escape') {
    e.preventDefault();
    closeCompletions();
  }
}, true);
```

Der `true` als dritter Parameter setzt diesen Zuhörer in die Capture-Phase, damit er vor der bestehenden Enter-Behandlung greift. Bei geschlossenem Menü kehrt er sofort zurück und ändert nichts.

- [ ] **Step 5: Übersetzen und von Hand prüfen**

Run: `cd app/frontend && npm run build && npm test`
Expected: Build ohne Fehler, alle JS-Tests grün.

Danach in der laufenden Anwendung gegen eine echte Session prüfen:
- `@` öffnet die Dateiliste, Pfeiltasten wählen, Enter übernimmt und sendet **nicht**.
- `/` am Zeilenanfang öffnet die Befehlsliste, `bitte /x` nicht.
- Ein Klick auf eine Zeile übernimmt genauso.
- Escape schließt, Enter danach sendet wie bisher.

- [ ] **Step 6: Commit**

```bash
git add app/frontend/index.html app/frontend/src/main.js app/frontend/src/style.css
git commit -m "feat: @- und /-Menü im Composer"
```

---

### Task 6: Die Prompt-Zeile verdecken

Letzter Schritt mit Absicht: Der Nutzen aus Task 5 steht auch ohne ihn. Erweist sich die Verdeckung als unruhig, kann sie fallen.

**Files:**
- Create: `app/frontend/src/features/composer/prompt-cover.js`
- Test: `app/frontend/src/features/composer/prompt-cover.test.js`
- Modify: `app/frontend/src/main.js`
- Modify: `app/frontend/src/style.css`

**Interfaces:**
- Consumes: `PromptLinePattern` aus Task 3.
- Produces: `function promptCoverRows(bufferLines, pattern)` → Zahl der zu verdeckenden Zeilen vom unteren Rand, `0` wenn nichts gefunden wurde.

- [ ] **Step 1: Write the failing test**

Erstelle `app/frontend/src/features/composer/prompt-cover.test.js`:

```js
import test from 'node:test';
import assert from 'node:assert/strict';
import { promptCoverRows } from './prompt-cover.js';

const claudePane = [
  'irgendeine Ausgabe',
  '────────────────',
  '❯ ',
  '────────────────',
  '                /rc',
  '  ⏵⏵ auto mode on',
];

test('verdeckt von der Zeile über dem Prompt bis zum unteren Rand', () => {
  assert.equal(promptCoverRows(claudePane, '❯'), 5);
});

test('ohne Muster wird nichts verdeckt', () => {
  assert.equal(promptCoverRows(claudePane, ''), 0);
});

test('ein nicht gefundenes Muster verdeckt nichts', () => {
  assert.equal(promptCoverRows(claudePane, '>>>'), 0);
});

test('die unterste Prompt-Zeile gewinnt', () => {
  const lines = ['❯ alt', 'Ausgabe', '────', '❯ neu', '────'];
  assert.equal(promptCoverRows(lines, '❯'), 3);
});

test('ein leerer Puffer verdeckt nichts', () => {
  assert.equal(promptCoverRows([], '❯'), 0);
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd app/frontend && node --test src/features/composer/prompt-cover.test.js`
Expected: FAIL, `Cannot find module './prompt-cover.js'`

- [ ] **Step 3: Write minimal implementation**

Erstelle `app/frontend/src/features/composer/prompt-cover.js`:

```js
// Wie viele Zeilen am unteren Rand des Panes zur Eingabe des Agenten gehören.
//
// Gesucht wird die unterste Zeile, die mit dem Muster des Vendors beginnt;
// verdeckt wird von einer Zeile darüber bis zum Rand, weil dort der obere
// Rahmen des Eingabekastens steht. Wird das Muster nicht gefunden, wird nichts
// verdeckt — Inhalt zu verstecken, den wir nicht sicher erkannt haben, wäre
// schlimmer als ein doppeltes Eingabefeld.
export function promptCoverRows(bufferLines, pattern) {
  if (!pattern || !bufferLines?.length) return 0;
  for (let i = bufferLines.length - 1; i >= 0; i--) {
    if (!bufferLines[i].trimStart().startsWith(pattern)) continue;
    const from = Math.max(0, i - 1);
    return bufferLines.length - from;
  }
  return 0;
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd app/frontend && node --test src/features/composer/prompt-cover.test.js`
Expected: PASS, fünf Tests

- [ ] **Step 5: Im Composer verdrahten**

Stil am Ende von `app/frontend/src/style.css`:

```css
.term-wrap .prompt-cover {
  position: absolute;
  left: 0;
  right: 0;
  bottom: 0;
  background: var(--term-bg);
  pointer-events: none;
}
```

In `app/frontend/src/main.js` importieren und eine Funktion ergänzen, die nach jedem Datenpaket und bei jedem Statuswechsel läuft:

```js
import { promptCoverRows } from './features/composer/prompt-cover.js';

// updatePromptCover legt die Eingabezeile des Agenten unter eine Blende,
// solange sie nicht gebraucht wird. Wartet die Session auf eine Entscheidung
// oder hat das Terminal den Fokus, bleibt alles sichtbar.
function updatePromptCover(t, status) {
  if (!t?.cover) return;
  const reveal = status === 'blocked' || t.wrap.contains(document.activeElement);
  if (reveal || !t.promptPattern) { t.cover.style.height = '0px'; return; }
  const buffer = t.term.buffer.active;
  const lines = [];
  for (let i = buffer.viewportY; i < buffer.viewportY + t.term.rows; i++) {
    lines.push(buffer.getLine(i)?.translateToString(true) ?? '');
  }
  const rows = promptCoverRows(lines, t.promptPattern);
  const cellHeight = t.term.element?.querySelector('.xterm-rows')?.firstElementChild?.offsetHeight
    || (t.term.options.fontSize * (t.term.options.lineHeight || 1));
  t.cover.style.height = `${rows * cellHeight}px`;
}
```

In `makeTerm`, direkt nach `wrap.appendChild(inner);`, die Blende anlegen:

```js
  const cover = document.createElement('div');
  cover.className = 'prompt-cover';
  cover.style.height = '0px';
  wrap.appendChild(cover);
```

Die Blende und das Muster gehören an den Term-Eintrag. Im `return`-Objekt von `makeTerm` ergänzen: `cover,` und `promptPattern: ''`.

Das Muster einmal beim Öffnen holen — in `showTerm`, direkt nach dem `await OpenTerm(...)` im `if (fresh)`-Zweig:

```js
    try { t.promptPattern = await PromptLinePattern(String(sessionID)); }
    catch { t.promptPattern = ''; }
```

`PromptLinePattern` dabei zum bestehenden Import aus `../wailsjs/go/main/App` hinzufügen.

Aufgerufen wird die Blende an zwei Stellen. Am Ende von `updateTermComposer`, wo der Status ohnehin vorliegt:

```js
  updatePromptCover(terms.get(activeTerm), gone ? 'exited' : a?.status);
```

Und beim Verlassen oder Betreten des Terminals, damit ein Klick sofort aufdeckt — in `makeTerm` nach `term.open(inner);`:

```js
  inner.addEventListener('focusin', () => updatePromptCover(terms.get(name), 'focus'));
  inner.addEventListener('focusout', () => setTimeout(() => updatePromptCover(terms.get(name), lastStatus.get(name)), 0));
```

`lastStatus` ist eine `Map`, die neben `terms` gepflegt wird: `const lastStatus = new Map();`, gesetzt in `updateTermComposer` mit `lastStatus.set(activeTerm, a?.status)`.

- [ ] **Step 6: Übersetzen und von Hand prüfen**

Run: `cd app/frontend && npm run build && npm test`
Expected: Build ohne Fehler, alle JS-Tests grün.

In der laufenden Anwendung:
- Bei ruhender Session ist nur ein Eingabefeld sichtbar.
- Wartet die Session auf eine Bestätigung, ist der Bereich wieder sichtbar.
- Ein Klick ins Terminal deckt auf.
- Bei einem Vendor ohne Muster bleibt alles wie vorher.

- [ ] **Step 7: Commit**

```bash
git add app/frontend/src/features/composer/ app/frontend/src/main.js app/frontend/src/style.css
git commit -m "feat: Eingabezeile des Agenten verdecken, solange sie nicht gebraucht wird"
```
