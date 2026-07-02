package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "add":
			cliAddProject()
			return
		case "web":
			port := webPort
			if len(os.Args) > 2 {
				fmt.Sscanf(os.Args[2], "%d", &port)
			}
			url := fmt.Sprintf("http://localhost:%d", port)
			fmt.Println("Übersicht:", url)
			go func() {
				time.Sleep(300 * time.Millisecond)
				openBrowser(url)
			}()
			if err := ServeWeb(nil, port); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		case "-h", "--help", "help":
			fmt.Println("magentic — TUI zum Verwalten von Claude-Code-Agents über tmux")
			fmt.Println()
			fmt.Println("  magentic             TUI starten")
			fmt.Println("  magentic add [pfad]  Projekt hinzufügen (Default: aktuelles Verzeichnis)")
			fmt.Println("  magentic web [port]  Session-/Worktree-Übersicht im Browser (Default-Port 4321)")
			return
		}
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Fprintln(os.Stderr, "magentic braucht tmux — bitte installieren (brew install tmux)")
		os.Exit(1)
	}
	s, err := LoadState()
	if err != nil {
		fmt.Fprintln(os.Stderr, "State konnte nicht geladen werden:", err)
		os.Exit(1)
	}
	p := tea.NewProgram(newModel(s), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func cliAddProject() {
	path := "."
	if len(os.Args) > 2 {
		path = os.Args[2]
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		fmt.Fprintln(os.Stderr, "Verzeichnis nicht gefunden:", abs)
		os.Exit(1)
	}
	s, err := LoadState()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	name := filepath.Base(abs)
	if s.ProjectByName(name) != nil {
		fmt.Println("Projekt existiert schon:", name)
		return
	}
	s.Projects = append(s.Projects, Project{Name: name, Path: abs})
	if err := s.Save(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Projekt %q hinzugefügt (%s)\n", name, abs)
}
