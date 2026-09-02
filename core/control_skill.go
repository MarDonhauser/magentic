package core

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed skills/agent-control.md
var shippedControlSkill string

// The markers delimit Magentic's own section in a Project's agent instructions.
// They make a second installation a replacement rather than a duplicate.
const (
	controlSkillStart = "<!-- magentic:agent-control:start -->"
	controlSkillEnd   = "<!-- magentic:agent-control:end -->"
	// controlSkillFile is where both Claude Code and Codex load a Project's
	// instructions from.
	controlSkillFile = "AGENTS.md"
)

// ControlSkillDocument is the shipped instruction file: one document that
// teaches the verbs, the addressing rules, the wait contract, and the
// delegation pattern.
func ControlSkillDocument() string { return shippedControlSkill }

// ControlSkillPath is where the document is installed inside a Project.
func ControlSkillPath(project Project) string {
	return filepath.Join(project.Path, controlSkillFile)
}

// InstallControlSkill writes the instruction document into a Project's agent
// instructions. Installing it again replaces the existing section instead of
// appending a second copy. It reports whether the file changed.
func InstallControlSkill(project Project) (bool, error) {
	if strings.TrimSpace(project.Path) == "" {
		return false, fmt.Errorf("Projekt %q hat kein Verzeichnis", project.Name)
	}
	path := ControlSkillPath(project)
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	updated := replaceControlSkillSection(string(existing), controlSkillSection())
	if updated == string(existing) {
		return false, nil
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return false, err
	}
	return true, nil
}

func controlSkillSection() string {
	return controlSkillStart + "\n" + strings.TrimRight(shippedControlSkill, "\n") + "\n" + controlSkillEnd + "\n"
}

// replaceControlSkillSection swaps Magentic's section in place, keeping
// everything the developer wrote around it.
func replaceControlSkillSection(document, section string) string {
	start := strings.Index(document, controlSkillStart)
	end := strings.Index(document, controlSkillEnd)
	if start >= 0 && end > start {
		return document[:start] + section + strings.TrimPrefix(document[end+len(controlSkillEnd):], "\n")
	}
	if strings.TrimSpace(document) == "" {
		return section
	}
	return strings.TrimRight(document, "\n") + "\n\n" + section
}
