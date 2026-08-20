package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSpecificationTestFile(t *testing.T, root string, parts []string, content string) string {
	t.Helper()
	path := filepath.Join(append([]string{root}, parts...)...)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func specificationByID(t *testing.T, discovery SpecificationsDiscovery, id string) Specification {
	t.Helper()
	for _, specification := range discovery.Specifications {
		if specification.ID == id {
			return specification
		}
	}
	t.Fatalf("Specification %q not found in %#v", id, discovery.Specifications)
	return Specification{}
}

func specificationSourceByKind(t *testing.T, discovery SpecificationsDiscovery, kind SpecificationSourceKind) SpecificationSourceSummary {
	t.Helper()
	for _, source := range discovery.Sources {
		if source.Source == kind {
			return source
		}
	}
	t.Fatalf("Specification source %q not found in %#v", kind, discovery.Sources)
	return SpecificationSourceSummary{}
}

func TestSpecificationsDiscoverNormalizesAllSources(t *testing.T) {
	root := t.TempDir()
	writeSpecificationTestFile(t, root, []string{"openspec", "changes", "login", "proposal.md"}, "# Login\n\n## Why\n\nUsers need accounts.\n")
	writeSpecificationTestFile(t, root, []string{"openspec", "changes", "login", "tasks.md"}, "- [ ] Add login\n")
	writeSpecificationTestFile(t, root, []string{"specs", "001-checkout", "spec.md"}, "# Checkout\n\nFast checkout.\n")
	writeSpecificationTestFile(t, root, []string{"specs", "001-checkout", "tasks.md"}, "- [x] Ship checkout\n")
	writeSpecificationTestFile(t, root, []string{".kiro", "specs", "search", "requirements.md"}, "# Search\n\n## Overview\n\nFind content.\n")
	writeSpecificationTestFile(t, root, []string{".kiro", "specs", "search", "tasks.md"}, "- [x] Index\n- [ ] Query\n")
	writeSpecificationTestFile(t, root, []string{".agent-os", "specs", "2026-08-20-billing", "shape.md"}, "# Billing\n\nCollect payment.\n")

	project := Project{ID: "project-1", Name: "demo", Path: root}
	discovery, err := NewSpecifications().Discover(context.Background(), project, SpecificationQuery{})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(discovery.Sources) != 4 || len(discovery.Specifications) != 4 {
		t.Fatalf("Discover() returned %d sources and %d Specifications", len(discovery.Sources), len(discovery.Specifications))
	}
	if discovery.ProjectID != project.ID || discovery.Project != project.Name {
		t.Fatalf("Discover() Project scope = %q/%q", discovery.ProjectID, discovery.Project)
	}

	wantKinds := map[SpecificationSourceKind]string{
		SpecificationOpenSpec: "OpenSpec",
		SpecificationSpecKit:  "Spec Kit",
		SpecificationKiro:     "Kiro",
		SpecificationAgentOS:  "Agent OS",
	}
	seenRefs := map[SpecificationRef]bool{}
	for _, specification := range discovery.Specifications {
		if wantKinds[specification.Source] != specification.SourceLabel {
			t.Fatalf("source label for %q = %q", specification.Source, specification.SourceLabel)
		}
		if specification.Reference == "" || seenRefs[specification.Reference] {
			t.Fatalf("unstable or duplicate Reference %q", specification.Reference)
		}
		seenRefs[specification.Reference] = true
		if specification.StartToken == "" {
			t.Fatalf("current Specification %q has no start token", specification.ID)
		}
		if strings.Contains(string(specification.Reference), root) || strings.Contains(string(specification.StartToken), root) {
			t.Fatalf("public identity leaked Project path: %q / %q", specification.Reference, specification.StartToken)
		}
	}
	for _, source := range discovery.Sources {
		if filepath.IsAbs(source.Location) {
			t.Fatalf("source location leaked an absolute path: %q", source.Location)
		}
		if source.Availability != SpecificationAvailable || source.Current != 1 {
			t.Fatalf("source summary = %#v", source)
		}
	}

	checkout := specificationByID(t, discovery, "001-checkout")
	if checkout.Lifecycle.Stage != SpecificationReview || checkout.Lifecycle.Terminal {
		t.Fatalf("all-done current Specification lifecycle = %#v, want non-terminal Review", checkout.Lifecycle)
	}
	search := specificationByID(t, discovery, "search")
	if search.Lifecycle.Stage != SpecificationActive || search.Progress.Completed != 1 || search.Progress.Total != 2 {
		t.Fatalf("partial Kiro Specification = %#v", search)
	}
}

func TestSpecificationsDefaultIsCurrentOnlyAndArchiveMakesDoneReachable(t *testing.T) {
	root := t.TempDir()
	writeSpecificationTestFile(t, root, []string{"openspec", "changes", "current", "tasks.md"}, "- [x] Complete implementation\n")
	writeSpecificationTestFile(t, root, []string{"openspec", "changes", "archive", "2026-08-01-old", "tasks.md"}, "- [ ] Historical task\n")
	project := Project{ID: "project-1", Name: "demo", Path: root}
	module := NewSpecifications()

	current, err := module.Discover(context.Background(), project, SpecificationQuery{})
	if err != nil {
		t.Fatalf("Discover(current) error = %v", err)
	}
	if len(current.Specifications) != 1 || current.Specifications[0].ID != "current" {
		t.Fatalf("default Discover() = %#v", current.Specifications)
	}
	if current.Specifications[0].Lifecycle.Stage != SpecificationReview {
		t.Fatalf("tasks-all-done stage = %q, want Review", current.Specifications[0].Lifecycle.Stage)
	}
	openSpec := specificationSourceByKind(t, current, SpecificationOpenSpec)
	if openSpec.Archived != 1 || openSpec.Returned != 1 {
		t.Fatalf("OpenSpec summary = %#v", openSpec)
	}

	withArchive, err := module.Discover(context.Background(), project, SpecificationQuery{IncludeArchived: true})
	if err != nil {
		t.Fatalf("Discover(archive) error = %v", err)
	}
	if len(withArchive.Specifications) != 2 {
		t.Fatalf("Discover(archive) returned %d Specifications", len(withArchive.Specifications))
	}
	archived := specificationByID(t, withArchive, "2026-08-01-old")
	if archived.Lifecycle.Stage != SpecificationDone || !archived.Lifecycle.Archived || !archived.Lifecycle.Terminal {
		t.Fatalf("archived lifecycle = %#v", archived.Lifecycle)
	}
	if archived.StartToken != "" {
		t.Fatalf("archived Specification has start token %q", archived.StartToken)
	}
}

func TestSpecificationsArchiveIsGloballyHardBounded(t *testing.T) {
	root := t.TempDir()
	for index := 0; index < SpecificationArchiveHardLimit+9; index++ {
		path := filepath.Join(root, "openspec", "changes", "archive", "2026-08-20-item-"+leftPadSpecificationIndex(index))
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	discovery, err := NewSpecifications().Discover(context.Background(), Project{ID: "project-1", Name: "demo", Path: root}, SpecificationQuery{
		IncludeArchived: true,
		ArchiveLimit:    SpecificationArchiveHardLimit + 1000,
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(discovery.Specifications) != SpecificationArchiveHardLimit {
		t.Fatalf("returned %d archived Specifications, hard limit = %d", len(discovery.Specifications), SpecificationArchiveHardLimit)
	}
	source := specificationSourceByKind(t, discovery, SpecificationOpenSpec)
	if source.Archived != SpecificationArchiveHardLimit+9 || !source.ArchiveTruncated {
		t.Fatalf("archive summary = %#v", source)
	}
}

func leftPadSpecificationIndex(index int) string {
	return fmt.Sprintf("%03d", index)
}

type faultSpecificationsFilesystem struct {
	specificationsFilesystem
	readDirErrors  map[string]error
	readFileErrors map[string]error
}

func (f faultSpecificationsFilesystem) ReadDir(path string) ([]os.DirEntry, error) {
	if err := f.readDirErrors[filepath.Clean(path)]; err != nil {
		return nil, err
	}
	return f.specificationsFilesystem.ReadDir(path)
}

func (f faultSpecificationsFilesystem) ReadFile(path string) ([]byte, error) {
	if err := f.readFileErrors[filepath.Clean(path)]; err != nil {
		return nil, err
	}
	return f.specificationsFilesystem.ReadFile(path)
}

func TestSpecificationsReturnsPartialResultsAndExplicitSourceFailures(t *testing.T) {
	root := t.TempDir()
	bad := filepath.Join(root, "openspec", "changes", "bad")
	kiroRoot := filepath.Join(root, ".kiro", "specs")
	writeSpecificationTestFile(t, root, []string{"openspec", "changes", "good", "tasks.md"}, "- [ ] Good task\n")
	if err := os.MkdirAll(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(kiroRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	filesystem := faultSpecificationsFilesystem{
		specificationsFilesystem: osSpecificationsFilesystem{},
		readDirErrors: map[string]error{
			filepath.Clean(bad):      errors.New("bad Specification unreadable"),
			filepath.Clean(kiroRoot): errors.New("Kiro source unreadable"),
		},
	}
	module := newSpecifications(filesystem, builtinSpecificationSourceAdapters()...)
	discovery, err := module.Discover(context.Background(), Project{ID: "project-1", Name: "demo", Path: root}, SpecificationQuery{})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}

	openSpec := specificationSourceByKind(t, discovery, SpecificationOpenSpec)
	if openSpec.Availability != SpecificationPartial || len(openSpec.Problems) == 0 {
		t.Fatalf("partial OpenSpec source = %#v", openSpec)
	}
	if specificationByID(t, discovery, "good").Availability != SpecificationAvailable {
		t.Fatalf("readable Specification was lost: %#v", discovery.Specifications)
	}
	if specificationByID(t, discovery, "bad").Availability != SpecificationUnavailable {
		t.Fatalf("unreadable Specification was not explicit: %#v", discovery.Specifications)
	}
	kiro := specificationSourceByKind(t, discovery, SpecificationKiro)
	if kiro.Availability != SpecificationUnavailable || len(kiro.Problems) == 0 || kiro.Current != 0 {
		t.Fatalf("unavailable Kiro source = %#v", kiro)
	}
	if len(discovery.Problems) < 2 {
		t.Fatalf("top-level partial diagnostics = %#v", discovery.Problems)
	}
}

func TestSpecificationReferenceAndStartTokenAreStableAndControlled(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	writeSpecificationTestFile(t, firstRoot, []string{"specs", "001-login", "spec.md"}, "# Login v1\n")
	writeSpecificationTestFile(t, secondRoot, []string{"specs", "001-login", "spec.md"}, "# Login moved\n")
	module := NewSpecifications()
	firstProject := Project{ID: "stable-project", Name: "demo", Path: firstRoot}
	secondProject := Project{ID: "stable-project", Name: "renamed", Path: secondRoot}

	first, err := module.Discover(context.Background(), firstProject, SpecificationQuery{Sources: []SpecificationSourceKind{SpecificationSpecKit}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := module.Discover(context.Background(), secondProject, SpecificationQuery{Sources: []SpecificationSourceKind{SpecificationSpecKit}})
	if err != nil {
		t.Fatal(err)
	}
	if first.Specifications[0].Reference != second.Specifications[0].Reference || first.Specifications[0].StartToken != second.Specifications[0].StartToken {
		t.Fatalf("Reference changed across Project relocation: %q/%q vs %q/%q",
			first.Specifications[0].Reference, first.Specifications[0].StartToken,
			second.Specifications[0].Reference, second.Specifications[0].StartToken)
	}

	intent, err := module.ResolveStart(context.Background(), secondProject, second.Specifications[0].StartToken)
	if err != nil {
		t.Fatalf("ResolveStart() error = %v", err)
	}
	wantDirectory := filepath.Join(secondRoot, "specs", "001-login")
	if intent.SpecificationDirectory != wantDirectory || intent.Reference != second.Specifications[0].Reference {
		t.Fatalf("ResolveStart() = %#v, want directory %q", intent, wantDirectory)
	}
	_, err = module.ResolveStart(context.Background(), Project{ID: "another-project", Path: secondRoot}, second.Specifications[0].StartToken)
	if !errors.Is(err, ErrInvalidSpecificationStartToken) {
		t.Fatalf("cross-Project ResolveStart() error = %v", err)
	}
}

func TestSpecificationLifecycleOnlyUsesTerminalFactsForDone(t *testing.T) {
	allDone := SpecificationProgress{Total: 2, Completed: 2}
	if lifecycle := specificationLifecycle(allDone, true, false, false); lifecycle.Stage != SpecificationReview || lifecycle.Terminal {
		t.Fatalf("all-done tasks lifecycle = %#v", lifecycle)
	}
	if lifecycle := specificationLifecycle(allDone, true, false, true); lifecycle.Stage != SpecificationDone || !lifecycle.Terminal {
		t.Fatalf("explicit source terminal lifecycle = %#v", lifecycle)
	}
}

func TestBuildBoardPreservesRepositoryDiagnosticsWithPartialResult(t *testing.T) {
	root := t.TempDir()
	writeSpecificationTestFile(t, root, []string{"specs", "001-login", "tasks.md"}, "- [ ] Implement\n")
	state := &State{
		Projects: []Project{{ID: "project-1", Name: "demo", Path: root}},
		Agents:   []Session{{Name: "001-login", Project: "demo", Dir: root}},
	}
	board := BuildBoard(state, "demo")
	if len(board.Items) != 1 {
		t.Fatalf("BuildBoard() lost partial result: %#v", board)
	}
	if !strings.Contains(board.Err, "Worktree-Zuordnung") {
		t.Fatalf("BuildBoard() diagnostics = %q", board.Err)
	}
}

func TestBuildBoardProjectionUsesOpaqueStartAndOptInArchive(t *testing.T) {
	root := t.TempDir()
	writeSpecificationTestFile(t, root, []string{"openspec", "changes", "current", "tasks.md"}, "- [ ] Implement\n")
	writeSpecificationTestFile(t, root, []string{"openspec", "changes", "archive", "2026-08-01-old", "tasks.md"}, "- [x] Historical\n")
	state := &State{Projects: []Project{{ID: "project-1", Name: "demo", Path: root}}}

	current := BuildBoard(state, "demo")
	if current.ProjectID != "project-1" || len(current.Items) != 1 {
		t.Fatalf("BuildBoard() = %#v", current)
	}
	if current.Items[0].Path != "" || current.Items[0].Reference == "" || current.Items[0].StartToken == "" {
		t.Fatalf("current Board item leaked a path or omitted identity: %#v", current.Items[0])
	}
	if len(current.Sources) != 1 || current.Sources[0].Root != "" || current.Sources[0].Location != "openspec/changes" || current.Root != "" {
		t.Fatalf("Board source location = %#v", current.Sources)
	}

	withArchive := BuildBoardWithQuery(state, "demo", SpecificationQuery{IncludeArchived: true, ArchiveLimit: 1})
	if len(withArchive.Items) != 2 {
		t.Fatalf("BuildBoardWithQuery() returned %d items", len(withArchive.Items))
	}
	var archived BoardItem
	for _, item := range withArchive.Items {
		if item.ID == "2026-08-01-old" {
			archived = item
		}
	}
	if archived.ID == "" || archived.Column != ColDone || archived.StartToken != "" || archived.Path != "" {
		t.Fatalf("archived Board item = %#v", archived)
	}
}
