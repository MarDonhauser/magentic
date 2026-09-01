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
