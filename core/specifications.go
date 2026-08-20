package core

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SpecificationSourceKind string

const (
	SpecificationOpenSpec SpecificationSourceKind = "openspec"
	SpecificationSpecKit  SpecificationSourceKind = "speckit"
	SpecificationKiro     SpecificationSourceKind = "kiro"
	SpecificationAgentOS  SpecificationSourceKind = "agent-os"
)

type SpecificationAvailability string

const (
	SpecificationAbsent      SpecificationAvailability = "absent"
	SpecificationAvailable   SpecificationAvailability = "available"
	SpecificationPartial     SpecificationAvailability = "partial"
	SpecificationUnavailable SpecificationAvailability = "unavailable"
)

type SpecificationStage string

const (
	SpecificationStageUnknown SpecificationStage = "unknown"
	SpecificationBacklog      SpecificationStage = "backlog"
	SpecificationActive       SpecificationStage = "active"
	SpecificationReview       SpecificationStage = "review"
	SpecificationDone         SpecificationStage = "done"
)

type SpecificationLifecycleReason string

const (
	SpecificationLifecycleUnavailable    SpecificationLifecycleReason = "unavailable"
	SpecificationLifecycleNoTasks        SpecificationLifecycleReason = "no_tasks"
	SpecificationLifecycleTasksOpen      SpecificationLifecycleReason = "tasks_open"
	SpecificationLifecycleTasksStarted   SpecificationLifecycleReason = "tasks_started"
	SpecificationLifecycleTasksComplete  SpecificationLifecycleReason = "tasks_complete"
	SpecificationLifecycleArchived       SpecificationLifecycleReason = "archived"
	SpecificationLifecycleSourceTerminal SpecificationLifecycleReason = "source_terminal"
)

type SpecificationTaskState string

const (
	SpecificationTaskOpen    SpecificationTaskState = "open"
	SpecificationTaskDone    SpecificationTaskState = "done"
	SpecificationTaskSkipped SpecificationTaskState = "skipped"
)

type SpecificationDocumentKind string

const (
	SpecificationDocumentProposal      SpecificationDocumentKind = "proposal"
	SpecificationDocumentRequirements  SpecificationDocumentKind = "requirements"
	SpecificationDocumentSpecification SpecificationDocumentKind = "specification"
	SpecificationDocumentDesign        SpecificationDocumentKind = "design"
	SpecificationDocumentPlan          SpecificationDocumentKind = "plan"
	SpecificationDocumentTasks         SpecificationDocumentKind = "tasks"
	SpecificationDocumentShape         SpecificationDocumentKind = "shape"
	SpecificationDocumentStandards     SpecificationDocumentKind = "standards"
	SpecificationDocumentReferences    SpecificationDocumentKind = "references"
	SpecificationDocumentOverview      SpecificationDocumentKind = "overview"
	SpecificationDocumentSupporting    SpecificationDocumentKind = "supporting"
)

type SpecificationRef string
type SpecificationStartToken string

type SpecificationProblem struct {
	Source    SpecificationSourceKind `json:"source"`
	Reference SpecificationRef        `json:"reference,omitempty"`
	Operation string                  `json:"operation"`
	Message   string                  `json:"message"`
}

type SpecificationTask struct {
	Text    string                 `json:"text"`
	State   SpecificationTaskState `json:"state"`
	Section string                 `json:"section,omitempty"`
}

type SpecificationDocument struct {
	Kind SpecificationDocumentKind `json:"kind"`
	Name string                    `json:"name"`
}

type SpecificationProgress struct {
	Total     int `json:"total"`
	Completed int `json:"completed"`
	Skipped   int `json:"skipped"`
}

type SpecificationLifecycle struct {
	Stage    SpecificationStage           `json:"stage"`
	Reason   SpecificationLifecycleReason `json:"reason"`
	Archived bool                         `json:"archived"`
	Terminal bool                         `json:"terminal"`
}

// SpecificationWorkInstructions describe source-independent work semantics.
// The source Adapter supplies the document order; Session Lifecycle decides
// how to turn the intent into a coding-agent prompt.
type SpecificationWorkInstructions struct {
	ReadInOrder      []SpecificationDocumentKind `json:"readInOrder"`
	KeepTasksUpdated bool                        `json:"keepTasksUpdated"`
	ReviewBeforeWork bool                        `json:"reviewBeforeWork"`
	ArchiveAfterWork bool                        `json:"archiveAfterWork"`
}

type Specification struct {
	Reference        SpecificationRef              `json:"reference"`
	Source           SpecificationSourceKind       `json:"source"`
	SourceLabel      string                        `json:"sourceLabel"`
	ID               string                        `json:"id"`
	Title            string                        `json:"title"`
	Summary          string                        `json:"summary,omitempty"`
	Availability     SpecificationAvailability     `json:"availability"`
	Documents        []SpecificationDocument       `json:"documents,omitempty"`
	Tasks            []SpecificationTask           `json:"tasks,omitempty"`
	Progress         SpecificationProgress         `json:"progress"`
	Lifecycle        SpecificationLifecycle        `json:"lifecycle"`
	UpdatedAt        time.Time                     `json:"updatedAt,omitzero"`
	StartToken       SpecificationStartToken       `json:"startToken,omitempty"`
	WorkInstructions SpecificationWorkInstructions `json:"workInstructions"`
	Problems         []SpecificationProblem        `json:"problems,omitempty"`
}

type SpecificationSourceSummary struct {
	Source                  SpecificationSourceKind   `json:"source"`
	Label                   string                    `json:"label"`
	Location                string                    `json:"location"`
	Availability            SpecificationAvailability `json:"availability"`
	Current                 int                       `json:"current"`
	Archived                int                       `json:"archived"`
	Returned                int                       `json:"returned"`
	ReferenceSpecifications int                       `json:"referenceSpecifications"`
	ArchiveTruncated        bool                      `json:"archiveTruncated"`
	Problems                []SpecificationProblem    `json:"problems,omitempty"`
}

type SpecificationQuery struct {
	Sources         []SpecificationSourceKind `json:"sources,omitempty"`
	Stages          []SpecificationStage      `json:"stages,omitempty"`
	IncludeArchived bool                      `json:"includeArchived,omitempty"`
	ArchiveLimit    int                       `json:"archiveLimit,omitempty"`
}

type SpecificationsDiscovery struct {
	ObservedAt     time.Time                    `json:"observedAt"`
	ProjectID      ProjectID                    `json:"projectId,omitempty"`
	Project        string                       `json:"project"`
	Sources        []SpecificationSourceSummary `json:"sources"`
	Specifications []Specification              `json:"specifications"`
	Problems       []SpecificationProblem       `json:"problems,omitempty"`
}

// SpecificationStartIntent is the controlled handoff from the read-only
// Specifications Module to Session Lifecycle. Paths are resolved only after a
// token has been checked against the supplied Project and source Adapter.
type SpecificationStartIntent struct {
	Reference              SpecificationRef              `json:"reference"`
	Source                 SpecificationSourceKind       `json:"source"`
	ID                     string                        `json:"id"`
	ProjectDirectory       string                        `json:"projectDirectory"`
	SpecificationDirectory string                        `json:"specificationDirectory"`
	WorkInstructions       SpecificationWorkInstructions `json:"workInstructions"`
}

const (
	SpecificationArchiveDefaultLimit = 25
	SpecificationArchiveHardLimit    = 100
)

var ErrInvalidSpecificationStartToken = errors.New("invalid Specification start token")

// Specifications hides source layouts and filesystem traversal behind one
// source-agnostic Interface.
type Specifications struct {
	filesystem specificationsFilesystem
	adapters   []specificationSourceAdapter
	now        func() time.Time
}

func NewSpecifications() *Specifications {
	filesystem := osSpecificationsFilesystem{}
	return newSpecifications(filesystem, builtinSpecificationSourceAdapters()...)
}

func newSpecifications(filesystem specificationsFilesystem, adapters ...specificationSourceAdapter) *Specifications {
	return &Specifications{filesystem: filesystem, adapters: append([]specificationSourceAdapter(nil), adapters...), now: time.Now}
}

// Discover returns current Specifications by default. Archived Specifications
// require an explicit query and are always capped by
// SpecificationArchiveHardLimit across all selected sources.
func (s *Specifications) Discover(ctx context.Context, project Project, query SpecificationQuery) (SpecificationsDiscovery, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(project.Path) == "" {
		return SpecificationsDiscovery{}, errors.New("Project path is required")
	}
	if err := validateSpecificationQuery(query); err != nil {
		return SpecificationsDiscovery{}, err
	}

	result := SpecificationsDiscovery{ObservedAt: s.now(), ProjectID: project.ID, Project: project.Name}
	archiveRemaining := 0
	if query.IncludeArchived {
		archiveRemaining = query.ArchiveLimit
		if archiveRemaining <= 0 {
			archiveRemaining = SpecificationArchiveDefaultLimit
		}
		if archiveRemaining > SpecificationArchiveHardLimit {
			archiveRemaining = SpecificationArchiveHardLimit
		}
	}

	for _, adapter := range s.adapters {
		if err := ctx.Err(); err != nil {
			return SpecificationsDiscovery{}, err
		}
		if !specificationSourceSelected(query.Sources, adapter.Kind()) {
			continue
		}
		discovered := adapter.Discover(ctx, s.filesystem, project, query.IncludeArchived, archiveRemaining)
		if err := ctx.Err(); err != nil {
			return SpecificationsDiscovery{}, err
		}
		archiveRemaining -= discovered.archivedReturned
		if archiveRemaining < 0 {
			archiveRemaining = 0
		}
		returned := 0
		for _, specification := range discovered.specifications {
			if specificationStageSelected(query.Stages, specification.Lifecycle.Stage) {
				result.Specifications = append(result.Specifications, specification)
				returned++
			}
		}
		discovered.summary.Returned = returned
		result.Sources = append(result.Sources, discovered.summary)
		result.Problems = append(result.Problems, discovered.summary.Problems...)
	}
	sortSpecifications(result.Specifications)
	return result, nil
}

// ResolveStart validates a start token for one Project and resolves it to the
// filesystem intent consumed by Session Lifecycle. Archived or terminal
// Specifications never receive a start token.
func (s *Specifications) ResolveStart(ctx context.Context, project Project, token SpecificationStartToken) (SpecificationStartIntent, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return SpecificationStartIntent{}, err
	}
	ref, err := specificationRefFromStartToken(token)
	if err != nil {
		return SpecificationStartIntent{}, err
	}
	parts, err := parseSpecificationRef(ref)
	if err != nil || parts.project != specificationProjectQualifier(project) || parts.archived {
		return SpecificationStartIntent{}, ErrInvalidSpecificationStartToken
	}
	for _, adapter := range s.adapters {
		if adapter.Kind() != parts.source {
			continue
		}
		directory, err := adapter.Resolve(ctx, s.filesystem, project, parts.id)
		if err != nil {
			return SpecificationStartIntent{}, err
		}
		return SpecificationStartIntent{
			Reference:              ref,
			Source:                 parts.source,
			ID:                     parts.id,
			ProjectDirectory:       filepath.Clean(project.Path),
			SpecificationDirectory: directory,
			WorkInstructions:       adapter.WorkInstructions(),
		}, nil
	}
	return SpecificationStartIntent{}, ErrInvalidSpecificationStartToken
}

func validateSpecificationQuery(query SpecificationQuery) error {
	for _, source := range query.Sources {
		switch source {
		case SpecificationOpenSpec, SpecificationSpecKit, SpecificationKiro, SpecificationAgentOS:
		default:
			return fmt.Errorf("unknown Specification source %q", source)
		}
	}
	for _, stage := range query.Stages {
		switch stage {
		case SpecificationStageUnknown, SpecificationBacklog, SpecificationActive, SpecificationReview, SpecificationDone:
		default:
			return fmt.Errorf("unknown Specification stage %q", stage)
		}
	}
	return nil
}

func specificationSourceSelected(selected []SpecificationSourceKind, source SpecificationSourceKind) bool {
	if len(selected) == 0 {
		return true
	}
	for _, candidate := range selected {
		if candidate == source {
			return true
		}
	}
	return false
}

func specificationStageSelected(selected []SpecificationStage, stage SpecificationStage) bool {
	if len(selected) == 0 {
		return true
	}
	for _, candidate := range selected {
		if candidate == stage {
			return true
		}
	}
	return false
}

func sortSpecifications(specifications []Specification) {
	sort.SliceStable(specifications, func(i, j int) bool {
		if !specifications[i].UpdatedAt.Equal(specifications[j].UpdatedAt) {
			return specifications[i].UpdatedAt.After(specifications[j].UpdatedAt)
		}
		return specifications[i].Reference < specifications[j].Reference
	})
}

func specificationLifecycle(progress SpecificationProgress, tasksKnown, archived, sourceTerminal bool) SpecificationLifecycle {
	switch {
	case archived:
		return SpecificationLifecycle{Stage: SpecificationDone, Reason: SpecificationLifecycleArchived, Archived: true, Terminal: true}
	case sourceTerminal:
		return SpecificationLifecycle{Stage: SpecificationDone, Reason: SpecificationLifecycleSourceTerminal, Terminal: true}
	case !tasksKnown:
		return SpecificationLifecycle{Stage: SpecificationStageUnknown, Reason: SpecificationLifecycleUnavailable}
	case progress.Total == 0:
		return SpecificationLifecycle{Stage: SpecificationBacklog, Reason: SpecificationLifecycleNoTasks}
	case progress.Completed == 0:
		return SpecificationLifecycle{Stage: SpecificationBacklog, Reason: SpecificationLifecycleTasksOpen}
	case progress.Completed >= progress.Total:
		return SpecificationLifecycle{Stage: SpecificationReview, Reason: SpecificationLifecycleTasksComplete}
	default:
		return SpecificationLifecycle{Stage: SpecificationActive, Reason: SpecificationLifecycleTasksStarted}
	}
}

type specificationRefParts struct {
	project  string
	source   SpecificationSourceKind
	id       string
	archived bool
}

func makeSpecificationRef(project Project, source SpecificationSourceKind, id string, archived bool) SpecificationRef {
	scope := "current"
	if archived {
		scope = "archive"
	}
	projectPart := base64.RawURLEncoding.EncodeToString([]byte(specificationProjectQualifier(project)))
	idPart := base64.RawURLEncoding.EncodeToString([]byte(id))
	return SpecificationRef("spec:v1:" + projectPart + ":" + string(source) + ":" + scope + ":" + idPart)
}

func parseSpecificationRef(ref SpecificationRef) (specificationRefParts, error) {
	parts := strings.Split(string(ref), ":")
	if len(parts) != 6 || parts[0] != "spec" || parts[1] != "v1" {
		return specificationRefParts{}, ErrInvalidSpecificationStartToken
	}
	projectBytes, projectErr := base64.RawURLEncoding.DecodeString(parts[2])
	idBytes, idErr := base64.RawURLEncoding.DecodeString(parts[5])
	if projectErr != nil || idErr != nil || (parts[4] != "current" && parts[4] != "archive") {
		return specificationRefParts{}, ErrInvalidSpecificationStartToken
	}
	source := SpecificationSourceKind(parts[3])
	switch source {
	case SpecificationOpenSpec, SpecificationSpecKit, SpecificationKiro, SpecificationAgentOS:
	default:
		return specificationRefParts{}, ErrInvalidSpecificationStartToken
	}
	id := string(idBytes)
	if !validSpecificationDirectoryName(id) {
		return specificationRefParts{}, ErrInvalidSpecificationStartToken
	}
	return specificationRefParts{project: string(projectBytes), source: source, id: id, archived: parts[4] == "archive"}, nil
}

func specificationProjectQualifier(project Project) string {
	if project.ID != "" {
		return "id:" + string(project.ID)
	}
	path, err := filepath.Abs(filepath.Clean(project.Path))
	if err != nil {
		path = filepath.Clean(project.Path)
	}
	sum := sha256.Sum256([]byte(path))
	return fmt.Sprintf("path:%x", sum[:12])
}

func startTokenForSpecification(ref SpecificationRef) SpecificationStartToken {
	encoded := base64.RawURLEncoding.EncodeToString([]byte(ref))
	return SpecificationStartToken("spec-start:v1:" + encoded)
}

func specificationRefFromStartToken(token SpecificationStartToken) (SpecificationRef, error) {
	const prefix = "spec-start:v1:"
	if !strings.HasPrefix(string(token), prefix) {
		return "", ErrInvalidSpecificationStartToken
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(string(token), prefix))
	if err != nil {
		return "", ErrInvalidSpecificationStartToken
	}
	ref := SpecificationRef(decoded)
	if _, err := parseSpecificationRef(ref); err != nil {
		return "", err
	}
	return ref, nil
}

func validSpecificationDirectoryName(id string) bool {
	return id != "" && id != "." && id != ".." && filepath.Base(id) == id && !strings.HasPrefix(id, ".")
}
