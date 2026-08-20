package core

import (
	"strings"
	"testing"
)

func TestSpecificationWorkPromptPreservesAdapterInstructions(t *testing.T) {
	intent := SpecificationStartIntent{
		ProjectID:              "project-1",
		ID:                     "login",
		ProjectDirectory:       "/repo",
		SpecificationDirectory: "/repo/specs/login",
		WorkInstructions: SpecificationWorkInstructions{
			ReadInOrder: []SpecificationDocumentKind{
				SpecificationDocumentRequirements,
				SpecificationDocumentDesign,
				SpecificationDocumentTasks,
			},
			KeepTasksUpdated: true,
			ReviewBeforeWork: true,
			ArchiveAfterWork: true,
		},
	}

	prompt := specificationWorkPrompt(intent)
	order := []string{"requirements", "design", "tasks"}
	last := -1
	for _, part := range order {
		position := strings.Index(prompt, part)
		if position <= last {
			t.Fatalf("prompt does not preserve document order %v: %q", order, prompt)
		}
		last = position
	}
	for _, required := range []string{"Zeige mir zuerst deinen Plan", "Halte den Aufgabenstatus", "Archivierung"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("prompt lacks %q: %q", required, prompt)
		}
	}
}

func TestStartSpecificationSessionRejectsIncompleteIntentBeforeSideEffects(t *testing.T) {
	if _, err := StartSpecificationSession(&State{}, SpecificationStartIntent{ID: "login"}); err == nil {
		t.Fatal("StartSpecificationSession accepted an incomplete controlled intent")
	}
}
