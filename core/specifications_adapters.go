package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type specificationsFilesystem interface {
	ReadDir(string) ([]os.DirEntry, error)
	ReadFile(string) ([]byte, error)
	Stat(string) (os.FileInfo, error)
	Lstat(string) (os.FileInfo, error)
	EvalSymlinks(string) (string, error)
	WalkDir(string, fs.WalkDirFunc) error
}

type osSpecificationsFilesystem struct{}

func (osSpecificationsFilesystem) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}

func (osSpecificationsFilesystem) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (osSpecificationsFilesystem) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func (osSpecificationsFilesystem) Lstat(path string) (os.FileInfo, error) {
	return os.Lstat(path)
}

func (osSpecificationsFilesystem) EvalSymlinks(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

func (osSpecificationsFilesystem) WalkDir(root string, walk fs.WalkDirFunc) error {
	return filepath.WalkDir(root, walk)
}

type specificationSourceDiscovery struct {
	summary          SpecificationSourceSummary
	specifications   []Specification
	archivedReturned int
}

type specificationSourceAdapter interface {
	Kind() SpecificationSourceKind
	WorkInstructions() SpecificationWorkInstructions
	Discover(context.Context, specificationsFilesystem, Project, bool, int) specificationSourceDiscovery
	Resolve(context.Context, specificationsFilesystem, Project, string) (string, error)
}

type directorySpecificationSourceAdapter struct {
	kind             SpecificationSourceKind
	label            string
	root             []string
	archive          string
	referenceRoot    []string
	summaryDocuments []string
	work             SpecificationWorkInstructions
}

func builtinSpecificationSourceAdapters() []specificationSourceAdapter {
	return []specificationSourceAdapter{
		directorySpecificationSourceAdapter{
			kind:             SpecificationOpenSpec,
			label:            "OpenSpec",
			root:             []string{"openspec", "changes"},
			archive:          "archive",
			referenceRoot:    []string{"openspec", "specs"},
			summaryDocuments: []string{"proposal.md", "spec.md", "requirements.md", "design.md", "README.md"},
			work: specificationWorkInstructions(
				SpecificationDocumentProposal,
				SpecificationDocumentSpecification,
				SpecificationDocumentDesign,
				SpecificationDocumentTasks,
			),
		},
		directorySpecificationSourceAdapter{
			kind:             SpecificationSpecKit,
			label:            "Spec Kit",
			root:             []string{"specs"},
			archive:          "archive",
			summaryDocuments: []string{"spec.md", "plan.md", "README.md"},
			work: specificationWorkInstructions(
				SpecificationDocumentSpecification,
				SpecificationDocumentPlan,
				SpecificationDocumentTasks,
			),
		},
		directorySpecificationSourceAdapter{
			kind:             SpecificationKiro,
			label:            "Kiro",
			root:             []string{".kiro", "specs"},
			archive:          "archive",
			summaryDocuments: []string{"requirements.md", "bugfix.md", "design.md", "README.md"},
			work: specificationWorkInstructions(
				SpecificationDocumentRequirements,
				SpecificationDocumentDesign,
				SpecificationDocumentTasks,
			),
		},
		directorySpecificationSourceAdapter{
			kind:             SpecificationAgentOS,
			label:            "Agent OS",
			root:             []string{".agent-os", "specs"},
			archive:          "archive",
			summaryDocuments: []string{"shape.md", "spec.md", "plan.md", "requirements.md", "README.md"},
			work: specificationWorkInstructions(
				SpecificationDocumentShape,
				SpecificationDocumentSpecification,
				SpecificationDocumentPlan,
				SpecificationDocumentTasks,
			),
		},
	}
}

func specificationWorkInstructions(read ...SpecificationDocumentKind) SpecificationWorkInstructions {
	return SpecificationWorkInstructions{
		ReadInOrder:      append([]SpecificationDocumentKind(nil), read...),
		KeepTasksUpdated: true,
		ReviewBeforeWork: true,
		ArchiveAfterWork: false,
	}
}

func (a directorySpecificationSourceAdapter) Kind() SpecificationSourceKind { return a.kind }

func (a directorySpecificationSourceAdapter) WorkInstructions() SpecificationWorkInstructions {
	work := a.work
	work.ReadInOrder = append([]SpecificationDocumentKind(nil), a.work.ReadInOrder...)
	return work
}

func (a directorySpecificationSourceAdapter) Discover(
	ctx context.Context,
	filesystem specificationsFilesystem,
	project Project,
	includeArchived bool,
	archiveLimit int,
) specificationSourceDiscovery {
	location := filepath.Join(a.root...)
	root := filepath.Join(append([]string{project.Path}, a.root...)...)
	result := specificationSourceDiscovery{summary: SpecificationSourceSummary{
		Source:       a.kind,
		Label:        a.label,
		Location:     filepath.ToSlash(location),
		Availability: SpecificationAbsent,
	}}

	info, err := filesystem.Stat(root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			result.summary.Availability = SpecificationUnavailable
			result.addProblem("discover_source", "", err)
		}
		return result
	}
	if !info.IsDir() {
		result.summary.Availability = SpecificationUnavailable
		result.addProblem("discover_source", "", errors.New("source location is not a directory"))
		return result
	}
	entries, err := filesystem.ReadDir(root)
	if err != nil {
		result.summary.Availability = SpecificationUnavailable
		result.addProblem("read_source", "", err)
		return result
	}
	result.summary.Availability = SpecificationAvailable

	current := specificationDirectoryEntries(entries, a.archive)
	result.summary.Current = len(current)
	for _, entry := range current {
		if err := ctx.Err(); err != nil {
			result.addProblem("discover_canceled", "", err)
			return result
		}
		specification := a.readSpecification(ctx, filesystem, project, root, entry.Name(), false)
		result.addSpecification(specification)
	}

	if a.archive != "" {
		archiveRoot := filepath.Join(root, a.archive)
		archived, archiveErr := filesystem.ReadDir(archiveRoot)
		switch {
		case archiveErr == nil:
			archived = specificationDirectoryEntries(archived, "")
			sort.Slice(archived, func(i, j int) bool { return archived[i].Name() > archived[j].Name() })
			result.summary.Archived = len(archived)
			if includeArchived {
				selected := archived
				if len(selected) > archiveLimit {
					selected = selected[:max(archiveLimit, 0)]
					result.summary.ArchiveTruncated = true
				}
				for _, entry := range selected {
					if err := ctx.Err(); err != nil {
						result.addProblem("discover_canceled", "", err)
						return result
					}
					specification := a.readSpecification(ctx, filesystem, project, archiveRoot, entry.Name(), true)
					result.addSpecification(specification)
					result.archivedReturned++
				}
			}
		case !errors.Is(archiveErr, os.ErrNotExist):
			result.addProblem("read_archive", "", archiveErr)
		}
	}

	if len(a.referenceRoot) > 0 {
		referenceRoot := filepath.Join(append([]string{project.Path}, a.referenceRoot...)...)
		entries, referenceErr := filesystem.ReadDir(referenceRoot)
		if referenceErr == nil {
			result.summary.ReferenceSpecifications = len(specificationDirectoryEntries(entries, ""))
		} else if !errors.Is(referenceErr, os.ErrNotExist) {
			result.addProblem("read_reference_specifications", "", referenceErr)
		}
	}
	return result
}

func (a directorySpecificationSourceAdapter) readSpecification(
	ctx context.Context,
	filesystem specificationsFilesystem,
	project Project,
	parent string,
	id string,
	archived bool,
) Specification {
	ref := makeSpecificationRef(project, a.kind, id, archived)
	directory := filepath.Join(parent, id)
	specification := Specification{
		Reference:        ref,
		Source:           a.kind,
		SourceLabel:      a.label,
		ID:               id,
		Title:            humanizeID(id),
		Availability:     SpecificationAvailable,
		WorkInstructions: a.WorkInstructions(),
	}
	entries, err := filesystem.ReadDir(directory)
	if err != nil {
		specification.Availability = SpecificationUnavailable
		specification.Problems = append(specification.Problems, specificationProblem(a.kind, ref, "read_specification", err))
		specification.Lifecycle = specificationLifecycle(SpecificationProgress{}, false, archived, false)
		return specification
	}

	entryByName := make(map[string]os.DirEntry, len(entries))
	for _, entry := range entries {
		entryByName[entry.Name()] = entry
	}
	tasksKnown := true
	if tasksEntry, ok := entryByName["tasks.md"]; ok && !tasksEntry.IsDir() {
		data, readErr := filesystem.ReadFile(filepath.Join(directory, "tasks.md"))
		if readErr != nil {
			tasksKnown = false
			specification.Problems = append(specification.Problems, specificationProblem(a.kind, ref, "read_tasks", readErr))
		} else {
			specification.Tasks = parseSpecificationTasks(data)
		}
	}

	for _, name := range a.summaryDocuments {
		entry, ok := entryByName[name]
		if !ok || entry.IsDir() {
			continue
		}
		data, readErr := filesystem.ReadFile(filepath.Join(directory, name))
		if readErr != nil {
			specification.Problems = append(specification.Problems, specificationProblem(a.kind, ref, "read_document", readErr))
			continue
		}
		title, summary := parseSpecificationDocumentHead(data)
		if title != "" {
			specification.Title = title
		}
		if summary != "" {
			specification.Summary = summary
		}
		if title != "" || summary != "" {
			break
		}
	}

	documentSeen := map[string]bool{}
	walkErr := filesystem.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			specification.Problems = append(specification.Problems, specificationProblem(a.kind, ref, "read_document_tree", walkErr))
			return nil
		}
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			return nil
		}
		relative, relativeErr := filepath.Rel(directory, path)
		if relativeErr == nil {
			relative = filepath.ToSlash(relative)
			if !documentSeen[relative] {
				documentSeen[relative] = true
				specification.Documents = append(specification.Documents, SpecificationDocument{
					Kind: specificationDocumentKind(relative),
					Name: relative,
				})
			}
		}
		if info, infoErr := entry.Info(); infoErr == nil && info.ModTime().After(specification.UpdatedAt) {
			specification.UpdatedAt = info.ModTime()
		} else if infoErr != nil {
			specification.Problems = append(specification.Problems, specificationProblem(a.kind, ref, "read_document_time", infoErr))
		}
		return nil
	})
	if walkErr != nil && !errors.Is(walkErr, context.Canceled) {
		specification.Problems = append(specification.Problems, specificationProblem(a.kind, ref, "read_document_tree", walkErr))
	}
	sort.Slice(specification.Documents, func(i, j int) bool { return specification.Documents[i].Name < specification.Documents[j].Name })

	for _, task := range specification.Tasks {
		specification.Progress.Total++
		switch task.State {
		case SpecificationTaskDone:
			specification.Progress.Completed++
		case SpecificationTaskSkipped:
			specification.Progress.Skipped++
		}
	}
	specification.Lifecycle = specificationLifecycle(specification.Progress, tasksKnown, archived, false)
	if len(specification.Problems) > 0 && specification.Availability == SpecificationAvailable {
		specification.Availability = SpecificationPartial
	}
	if !archived && !specification.Lifecycle.Terminal && specification.Availability != SpecificationUnavailable {
		specification.StartToken = startTokenForSpecification(ref)
	}
	return specification
}

func (a directorySpecificationSourceAdapter) Resolve(
	ctx context.Context,
	filesystem specificationsFilesystem,
	project Project,
	id string,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !validSpecificationDirectoryName(id) {
		return "", ErrInvalidSpecificationStartToken
	}
	root := filepath.Join(append([]string{project.Path}, a.root...)...)
	target := filepath.Join(root, id)
	projectAbsolute, projectErr := filepath.Abs(filepath.Clean(project.Path))
	rootAbsolute, rootErr := filepath.Abs(root)
	targetAbsolute, targetErr := filepath.Abs(target)
	if projectErr != nil || rootErr != nil || targetErr != nil {
		return "", ErrInvalidSpecificationStartToken
	}
	projectPhysical, err := filesystem.EvalSymlinks(projectAbsolute)
	if err != nil {
		return "", fmt.Errorf("resolve Project: %w", err)
	}
	rootPhysical, err := filesystem.EvalSymlinks(rootAbsolute)
	if err != nil {
		return "", fmt.Errorf("resolve Specification source: %w", err)
	}
	if !specificationPathContained(projectPhysical, rootPhysical) {
		return "", errors.New("resolve Specification: source escapes Project")
	}
	if !specificationPathContained(rootAbsolute, targetAbsolute) {
		return "", ErrInvalidSpecificationStartToken
	}
	info, err := filesystem.Lstat(targetAbsolute)
	if err != nil {
		return "", fmt.Errorf("resolve Specification: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("resolve Specification: target is not a directory")
	}
	targetPhysical, err := filesystem.EvalSymlinks(targetAbsolute)
	if err != nil {
		return "", fmt.Errorf("resolve Specification: %w", err)
	}
	if !specificationPathContained(projectPhysical, targetPhysical) || !specificationPathContained(rootPhysical, targetPhysical) {
		return "", errors.New("resolve Specification: target escapes source")
	}
	return filepath.Clean(targetPhysical), nil
}

func specificationPathContained(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (d *specificationSourceDiscovery) addProblem(operation string, ref SpecificationRef, err error) {
	problem := specificationProblem(d.summary.Source, ref, operation, err)
	d.summary.Problems = append(d.summary.Problems, problem)
	if d.summary.Availability == SpecificationAvailable {
		d.summary.Availability = SpecificationPartial
	}
}

func (d *specificationSourceDiscovery) addSpecification(specification Specification) {
	d.specifications = append(d.specifications, specification)
	d.summary.Returned++
	if len(specification.Problems) > 0 {
		d.summary.Problems = append(d.summary.Problems, specification.Problems...)
		if d.summary.Availability == SpecificationAvailable {
			d.summary.Availability = SpecificationPartial
		}
	}
}

func specificationProblem(source SpecificationSourceKind, ref SpecificationRef, operation string, err error) SpecificationProblem {
	message := "Specification knowledge unavailable"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = err.Error()
	}
	return SpecificationProblem{Source: source, Reference: ref, Operation: operation, Message: message}
}

func specificationDirectoryEntries(entries []os.DirEntry, excluded string) []os.DirEntry {
	var result []os.DirEntry
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == excluded || !validSpecificationDirectoryName(entry.Name()) {
			continue
		}
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name() < result[j].Name() })
	return result
}

func parseSpecificationTasks(data []byte) []SpecificationTask {
	var tasks []SpecificationTask
	section := ""
	for _, line := range strings.Split(string(data), "\n") {
		if match := sectionRe.FindStringSubmatch(line); match != nil {
			section = strings.TrimSpace(match[1])
			continue
		}
		match := taskLineRe.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		text := strings.TrimSpace(match[2])
		if len(text) > 160 {
			text = text[:159] + "…"
		}
		state := SpecificationTaskOpen
		switch match[1] {
		case "x", "X":
			state = SpecificationTaskDone
		case "~", "/", "-":
			state = SpecificationTaskSkipped
		}
		tasks = append(tasks, SpecificationTask{Text: text, State: state, Section: section})
	}
	return tasks
}

func parseSpecificationDocumentHead(data []byte) (title, summary string) {
	lines := strings.Split(string(data), "\n")
	var body []string
	inSummary := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if title == "" && strings.HasPrefix(trimmed, "# ") {
			title = strings.TrimSpace(strings.TrimPrefix(trimmed, "# "))
			continue
		}
		if strings.HasPrefix(trimmed, "## ") {
			lower := strings.ToLower(trimmed)
			inSummary = strings.Contains(lower, "why") || strings.Contains(lower, "warum") ||
				strings.Contains(lower, "summary") || strings.Contains(lower, "overview")
			if len(body) > 0 && !inSummary {
				break
			}
			continue
		}
		if !inSummary && len(body) > 0 {
			break
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "<!--") {
			continue
		}
		body = append(body, trimmed)
		if len(body) >= 4 {
			break
		}
	}
	summary = strings.Join(body, " ")
	if len(summary) > 320 {
		summary = summary[:319] + "…"
	}
	return title, summary
}

func specificationDocumentKind(name string) SpecificationDocumentKind {
	switch strings.ToLower(filepath.Base(name)) {
	case "proposal.md":
		return SpecificationDocumentProposal
	case "requirements.md", "bugfix.md":
		return SpecificationDocumentRequirements
	case "spec.md":
		return SpecificationDocumentSpecification
	case "design.md":
		return SpecificationDocumentDesign
	case "plan.md":
		return SpecificationDocumentPlan
	case "tasks.md":
		return SpecificationDocumentTasks
	case "shape.md":
		return SpecificationDocumentShape
	case "standards.md":
		return SpecificationDocumentStandards
	case "references.md":
		return SpecificationDocumentReferences
	case "readme.md":
		return SpecificationDocumentOverview
	default:
		return SpecificationDocumentSupporting
	}
}

func specificationDocumentCount(specification Specification) int {
	count := 0
	for _, document := range specification.Documents {
		if document.Kind != SpecificationDocumentTasks {
			count++
		}
	}
	return count
}

func specificationHasPlan(specification Specification) bool {
	for _, document := range specification.Documents {
		if document.Kind == SpecificationDocumentPlan || document.Kind == SpecificationDocumentDesign {
			return true
		}
	}
	return false
}

func specificationUpdatedString(updated time.Time) string {
	if updated.IsZero() {
		return ""
	}
	return updated.Format(time.RFC3339)
}
