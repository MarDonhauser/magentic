package core

import (
	"os"
	"path/filepath"
	"testing"
)

// Eine gespeicherte AgentRunRef ist nur ein Versprechen: Magentic schreibt sie
// beim Anlegen der Session, der Anbieter legt die Konversation aber erst an,
// wenn dort tatsächlich gearbeitet wird. Welche Startform gültig ist,
// entscheidet deshalb der Anbieter-Speicher, nicht die Registry.
func TestStartCommandFollowsTheVendorStorage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))

	claudeRun := "53c991c4-62b8-4d7b-8372-64a9e55f9b7f"
	codexRun := "01a05c48-90a9-7281-a9f8-e80fc3dfd9e7"

	session := func(vendor AgentVendor, runID string) Session {
		return Session{
			Name: "navi", RuntimeName: "mgt-navi", SessionKind: SessionKindCodingAgent,
			Vendor: vendor, AgentRuns: []AgentRunRef{{Vendor: vendor, ExternalID: runID}},
		}
	}

	t.Run("Claude ohne angelegte Konversation legt sie unter derselben Identität an", func(t *testing.T) {
		got, err := startCommandForSession(session(AgentVendorClaude, claudeRun), "resume")
		if err != nil {
			t.Fatal(err)
		}
		want := "claude --name 'mgt-navi' --session-id '" + claudeRun + "'"
		if got != want {
			t.Fatalf("StartCommand = %q, want %q", got, want)
		}
	})

	writeFile(t, filepath.Join(home, ".claude", "projects", "-work-navi", claudeRun+".jsonl"), "{}\n")

	t.Run("Claude mit vorhandener Konversation setzt fort", func(t *testing.T) {
		got, err := startCommandForSession(session(AgentVendorClaude, claudeRun), "resume")
		if err != nil {
			t.Fatal(err)
		}
		want := "claude --name 'mgt-navi' --resume '" + claudeRun + "'"
		if got != want {
			t.Fatalf("StartCommand = %q, want %q", got, want)
		}
	})

	t.Run("Codex ohne Rollout-Datei startet frisch", func(t *testing.T) {
		got, err := startCommandForSession(session(AgentVendorCodex, codexRun), "resume")
		if err != nil {
			t.Fatal(err)
		}
		if got != "codex" {
			t.Fatalf("StartCommand = %q, want %q", got, "codex")
		}
	})

	writeFile(t, filepath.Join(home, ".codex", "sessions", "2026", "09", "01",
		"rollout-2026-09-01T11-24-14-"+codexRun+".jsonl"), "{}\n")

	t.Run("Codex mit Rollout-Datei setzt fort", func(t *testing.T) {
		got, err := startCommandForSession(session(AgentVendorCodex, codexRun), "resume")
		if err != nil {
			t.Fatal(err)
		}
		want := "codex resume '" + codexRun + "'"
		if got != want {
			t.Fatalf("StartCommand = %q, want %q", got, want)
		}
	})
}

func TestRunExistsPerVendor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	run := "abc-123"
	writeFile(t, filepath.Join(home, ".copilot", "session-state", run, "events.jsonl"), "{}\n")

	copilot, _ := providerForVendor(AgentVendorCopilot)
	if !copilot.RunExists(run) {
		t.Fatal("Copilot-Lauf mit Verzeichnis wurde nicht erkannt")
	}
	if copilot.RunExists("nicht-da") {
		t.Fatal("fehlender Copilot-Lauf wurde als vorhanden gemeldet")
	}
	gemini, _ := providerForVendor(AgentVendorGemini)
	if gemini.RunExists(run) {
		t.Fatal("Gemini kann keinen Lauf belegen und muss false liefern")
	}
	antigravity, _ := providerForVendor(AgentVendorAntigravity)
	if antigravity.RunExists(run) {
		t.Fatal("fehlender Antigravity-Lauf wurde als vorhanden gemeldet")
	}
	writeFile(t, filepath.Join(home, ".gemini", "antigravity-cli", "brain", run, ".system_generated", "logs", "transcript.jsonl"), "{}\n")
	if !antigravity.RunExists(run) {
		t.Fatal("Antigravity-Lauf mit Brain-Verzeichnis wurde nicht erkannt")
	}
	if antigravity.RunExists("") {
		t.Fatal("eine leere Run-ID ist nie vorhanden")
	}
	claude, _ := providerForVendor(AgentVendorClaude)
	if claude.RunExists("") {
		t.Fatal("eine leere Run-ID ist nie vorhanden")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
