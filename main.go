package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"magentic/core"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "add":
			cliAddProject()
			return
		case "agents":
			cliValidateAgentKinds()
			return
		case "hooks":
			cliClaudeHooks()
			return
		case "hook-report":
			cliHookReport()
			return
		case "session":
			cliSession(os.Args[2:])
			return
		case "serve":
			cliServe()
			return
		case "-h", "--help", "help":
			fmt.Println("magentic — TUI zum Verwalten von Coding-Agents über tmux")
			fmt.Println()
			fmt.Println("  magentic                  TUI starten")
			fmt.Println("  magentic add [pfad]       Projekt hinzufügen (Default: aktuelles Verzeichnis)")
			fmt.Println("  magentic agents           Status-Manifeste prüfen und ihre Quelle nennen")
			fmt.Println("  magentic hooks install    Claude-Code-Hooks für Status-Meldungen einrichten")
			fmt.Println("  magentic hooks uninstall  Diese Hooks wieder entfernen")
			fmt.Println("  magentic serve            Steuer-API ohne Oberfläche bedienen")
			fmt.Println()
			fmt.Println("Steuer-API — Sessions aus einem Skript oder einem Coding-Agent heraus steuern:")
			fmt.Print(controlSessionHelp())
			return
		}
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Fprintln(os.Stderr, "magentic braucht tmux — bitte installieren (brew install tmux)")
		os.Exit(1)
	}
	if os.Getenv("MAGENTIC_PPROF") != "" {
		go http.ListenAndServe("127.0.0.1:6060", nil)
	}
	s, err := LoadState()
	if err != nil {
		fmt.Fprintln(os.Stderr, "State konnte nicht geladen werden:", err)
		os.Exit(1)
	}
	startControlAPI()
	defer stopControlAPI()
	// Cell Motion statt All Motion: Die TUI wertet nur Mausrad und Linksklick aus,
	// jedes reine Bewegungsevent würde nur einen kompletten View-Render kosten.
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
	name := filepath.Base(abs)
	if _, err := OpenRegistry(StatePath()).Change(context.Background(), RegisterProject(Project{Name: name, Path: abs})); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("Projekt %q hinzugefügt (%s)\n", name, abs)
}

// cliValidateAgentKinds is the validation surface for the detection manifests:
// every shipped and user manifest with its kind, its source, and either an
// accepted result or the reason it was rejected.
func cliValidateAgentKinds() {
	reports := core.ValidateAgentKinds()
	fmt.Printf("Benutzer-Manifeste: %s\n\n", core.AgentKindUserDir())
	rejected := false
	for _, report := range reports {
		name := report.Kind
		if name == "" {
			name = "(ohne Agent-Art)"
		}
		switch {
		case !report.Accepted:
			rejected = true
			fmt.Printf("✗ %-10s %-8s %s\n  %s\n", name, report.Source, report.Path, report.Reason)
		case report.Overruled:
			fmt.Printf("· %-10s %-8s %s (durch ein Benutzer-Manifest ersetzt)\n", name, report.Source, report.Path)
		default:
			fmt.Printf("✓ %-10s %-8s %s\n", name, report.Source, report.Path)
		}
	}
	if rejected {
		os.Exit(1)
	}
}

// cliClaudeHooks installs or removes the hook definitions that let Claude Code
// report its own lifecycle. It says what it writes and where before writing it.
func cliClaudeHooks() {
	mode := ""
	if len(os.Args) > 2 {
		mode = os.Args[2]
	}
	path := core.ClaudeSettingsPath()
	switch mode {
	case "install":
		fmt.Printf("Magentic schreibt in %s:\n", path)
		for _, definition := range core.ClaudeHookDefinitions() {
			fmt.Printf("  %-16s %s\n", definition.Event, definition.Command)
		}
		written, err := core.InstallClaudeHooks(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if len(written) == 0 {
			fmt.Println("Nichts zu tun: die Hooks stehen bereits so in der Datei.")
			return
		}
		fmt.Printf("%d Hook-Definitionen ergänzt.\n", len(written))
	case "uninstall":
		removed, err := core.UninstallClaudeHooks(path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("%d Hook-Definitionen aus %s entfernt.\n", len(removed), path)
	default:
		fmt.Fprintln(os.Stderr, "magentic hooks install | magentic hooks uninstall")
		os.Exit(1)
	}
}

// cliHookReport is what the installed hooks call. It reads the vendor's payload
// from stdin and appends one line to the local event file.
func cliHookReport() {
	event := ""
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--event" && i+1 < len(os.Args) {
			event = os.Args[i+1]
		}
	}
	payload, _ := io.ReadAll(os.Stdin)
	report, err := core.HookReportFromClaudePayload(event, payload, core.HookRuntimeName(), time.Now())
	if err != nil {
		// A hook must never fail the agent it is attached to.
		return
	}
	_ = core.AppendHookReport(core.HookReportPath(), report)
}
