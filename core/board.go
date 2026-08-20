package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type BoardTask struct {
	Text    string `json:"text"`
	Done    bool   `json:"done"`
	Section string `json:"section,omitempty"`
}

type BoardItem struct {
	Key        string                  `json:"key"`
	Reference  SpecificationRef        `json:"reference"`
	StartToken SpecificationStartToken `json:"startToken,omitempty"`
	ID         string                  `json:"id"`
	Title      string                  `json:"title"`
	Summary    string                  `json:"summary,omitempty"`
	Path       string                  `json:"path,omitempty"` // deprecated: use StartToken
	Kind       string                  `json:"kind"`
	Column     string                  `json:"column"`
	Total      int                     `json:"total"`
	Done       int                     `json:"done"`
	Specs      int                     `json:"specs"`
	HasPlan    bool                    `json:"hasPlan"`
	Updated    string                  `json:"updated,omitempty"`
	Tasks      []BoardTask             `json:"tasks,omitempty"`
	Agents     []string                `json:"agents,omitempty"`
	Branches   []string                `json:"branches,omitempty"`
	Problems   []string                `json:"problems,omitempty"`
}

type BoardSource struct {
	Kind         string   `json:"kind"`
	Location     string   `json:"location"`
	Root         string   `json:"root,omitempty"` // deprecated: use Location
	Items        int      `json:"items"`
	Archived     int      `json:"archived"`
	Specs        int      `json:"specs"`
	Availability string   `json:"availability,omitempty"`
	Problems     []string `json:"problems,omitempty"`
}

type Board struct {
	ProjectID ProjectID     `json:"projectId,omitempty"`
	Project   string        `json:"project"`
	Kind      string        `json:"kind"`
	Root      string        `json:"root,omitempty"`
	Sources   []BoardSource `json:"sources,omitempty"`
	Items     []BoardItem   `json:"items"`
	Archived  int           `json:"archived"`
	Specs     int           `json:"specs"`
	Err       string        `json:"err,omitempty"`
}

const (
	ColBacklog = "backlog"
	ColActive  = "active"
	ColReview  = "review"
	ColDone    = "done"
)

var taskLineRe = regexp.MustCompile(`^\s*[-*]\s+\[([ xX~/-])\]\s+(.*)$`)
var sectionRe = regexp.MustCompile(`^#{2,3}\s+(.*)$`)

// BuildBoard is the compatibility Adapter for the current TUI and desktop
// transport. New callers consume Specifications.Discover and hand its opaque
// start token to Specifications.ResolveStart before invoking Session Lifecycle.
func BuildBoard(state *State, projectName string) Board {
	return BuildBoardWithQuery(state, projectName, SpecificationQuery{})
}

// BuildBoardWithQuery permits the compatibility callers that intentionally
// render bounded archives to opt in; BuildBoard itself remains current-only.
func BuildBoardWithQuery(state *State, projectName string, query SpecificationQuery) Board {
	board := Board{Project: projectName}
	if state == nil {
		board.Err = "Projekt nicht gefunden"
		return board
	}
	project := state.ProjectByName(projectName)
	if project == nil {
		board.Err = "Projekt nicht gefunden"
		return board
	}
	board.ProjectID = project.ID

	ctx := context.Background()
	specifications := NewSpecifications()
	discovery, err := specifications.Discover(ctx, *project, query)
	if err != nil {
		board.Kind = "none"
		board.Err = err.Error()
		return board
	}
	agents, repositoryProblems := liveAgentContext(ctx, state, *project)
	problems := append(formatSpecificationProblems(discovery.Problems), repositoryProblems...)

	itemsBySource := make(map[SpecificationSourceKind]int)
	for _, specification := range discovery.Specifications {
		item := boardItemFromSpecification(specification, agents)
		board.Items = append(board.Items, item)
		itemsBySource[specification.Source]++
	}

	for _, source := range discovery.Sources {
		items := itemsBySource[source.Source]
		if source.Current == 0 && items == 0 {
			continue
		}
		boardSource := BoardSource{
			Kind:         string(source.Source),
			Location:     source.Location,
			Items:        items,
			Archived:     source.Archived,
			Specs:        source.ReferenceSpecifications,
			Availability: string(source.Availability),
			Problems:     formatSpecificationProblems(source.Problems),
		}
		board.Sources = append(board.Sources, boardSource)
		board.Archived += source.Archived
		board.Specs += source.ReferenceSpecifications
	}

	if len(board.Sources) == 0 {
		board.Kind = "none"
	} else {
		board.Kind = board.Sources[0].Kind
	}
	sortBoardItems(board.Items)
	board.Err = strings.Join(appendUniqueStrings(nil, problems...), "; ")
	return board
}

func boardItemFromSpecification(specification Specification, agents []agentCtx) BoardItem {
	item := BoardItem{
		Key:        string(specification.Reference),
		Reference:  specification.Reference,
		StartToken: specification.StartToken,
		ID:         specification.ID,
		Title:      specification.Title,
		Summary:    specification.Summary,
		Kind:       string(specification.Source),
		Column:     string(specification.Lifecycle.Stage),
		Total:      specification.Progress.Total,
		Done:       specification.Progress.Completed,
		Specs:      specificationDocumentCount(specification),
		HasPlan:    specificationHasPlan(specification),
		Updated:    specificationUpdatedString(specification.UpdatedAt),
		Problems:   formatSpecificationProblems(specification.Problems),
	}
	if specification.Lifecycle.Stage == SpecificationStageUnknown {
		item.Column = ColBacklog
	}
	for _, task := range specification.Tasks {
		item.Tasks = append(item.Tasks, BoardTask{
			Text:    task.Text,
			Done:    task.State == SpecificationTaskDone,
			Section: task.Section,
		})
	}
	for _, agent := range agents {
		if specification.Lifecycle.Terminal || !matchesItem(agent, specification.ID, "") {
			continue
		}
		item.Agents = append(item.Agents, agent.name)
		if agent.branch != "" {
			item.Branches = appendUnique(item.Branches, agent.branch)
		}
	}
	if len(item.Agents) > 0 {
		item.Column = ColActive
	}
	return item
}

type agentCtx struct {
	name   string
	branch string
	dir    string
}

func liveAgentContext(ctx context.Context, state *State, project Project) ([]agentCtx, []string) {
	var sessions []Session
	for _, session := range state.AgentsFor(project.Name) {
		if session.LaterAt.IsZero() {
			sessions = append(sessions, session)
		}
	}
	if len(sessions) == 0 {
		return nil, nil
	}

	survey, err := NewRepositories().Survey(ctx, []Project{project})
	if err != nil {
		return agentContextWithoutBranches(sessions), []string{"Worktree-Zuordnung: " + err.Error()}
	}
	if len(survey.Projects) != 1 {
		return agentContextWithoutBranches(sessions), []string{"Worktree-Zuordnung: Repository-Ergebnis fehlt"}
	}
	observed := survey.Projects[0]
	if observed.Presence != RepositoryKnown || !observed.Worktrees.Known() {
		message := "Repository-Kenntnis nicht verfügbar"
		if observed.Problem != nil {
			message = observed.Problem.Message
		}
		return agentContextWithoutBranches(sessions), []string{"Worktree-Zuordnung: " + message}
	}

	var result []agentCtx
	var problems []string
	for _, session := range sessions {
		agent := agentCtx{name: session.Name, dir: session.Dir}
		if worktree, found := repositoryWorktreeForDirectory(observed.Worktrees.Value, session.Dir); found {
			if worktree.Checkout.Known() && worktree.Checkout.Value.Kind == RepositoryBranchCheckout {
				agent.branch = worktree.Checkout.Value.Branch
			} else {
				problems = append(problems, "Worktree-Zuordnung für "+session.Name+": Branch unbekannt")
			}
		} else {
			problems = append(problems, "Worktree-Zuordnung für "+session.Name+": Worktree unbekannt")
		}
		result = append(result, agent)
	}
	return result, problems
}

func agentContextWithoutBranches(sessions []Session) []agentCtx {
	result := make([]agentCtx, 0, len(sessions))
	for _, session := range sessions {
		result = append(result, agentCtx{name: session.Name, dir: session.Dir})
	}
	return result
}

func sortBoardItems(items []BoardItem) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Updated != items[j].Updated {
			return items[i].Updated > items[j].Updated
		}
		return items[i].Key < items[j].Key
	})
}

// boardColumn remains for source compatibility while delegating the lifecycle
// policy to the Specifications Module.
func boardColumn(item BoardItem) string {
	if len(item.Agents) > 0 {
		return ColActive
	}
	lifecycle := specificationLifecycle(SpecificationProgress{Total: item.Total, Completed: item.Done}, true, false, false)
	return string(lifecycle.Stage)
}

func matchesItem(agent agentCtx, id, _ string) bool {
	slug := normalizeSlug(id)
	if slug == "" {
		return false
	}
	if normalizeSlug(agent.branch) == slug || strings.Contains(normalizeSlug(agent.branch), slug) {
		return true
	}
	if normalizeSlug(filepath.Base(agent.dir)) == slug {
		return true
	}
	return normalizeSlug(agent.name) == slug
}

func normalizeSlug(value string) string {
	var builder strings.Builder
	for _, character := range strings.ToLower(value) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func parseTasks(path string) []BoardTask {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	tasks := parseSpecificationTasks(data)
	result := make([]BoardTask, 0, len(tasks))
	for _, task := range tasks {
		result = append(result, BoardTask{Text: task.Text, Done: task.State == SpecificationTaskDone, Section: task.Section})
	}
	return result
}

func readDocHead(path string) (title, summary string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	return parseSpecificationDocumentHead(data)
}

func humanizeID(id string) string {
	value := strings.NewReplacer("-", " ", "_", " ").Replace(id)
	parts := strings.Fields(value)
	for index, part := range parts {
		if index == 0 && len(part) > 0 {
			parts[index] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}

func appendUnique(list []string, value string) []string {
	for _, existing := range list {
		if existing == value {
			return list
		}
	}
	return append(list, value)
}

func appendUniqueStrings(list []string, values ...string) []string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			list = appendUnique(list, value)
		}
	}
	return list
}

func formatSpecificationProblems(problems []SpecificationProblem) []string {
	result := make([]string, 0, len(problems))
	for _, problem := range problems {
		prefix := string(problem.Source)
		if problem.Reference != "" {
			if parts, err := parseSpecificationRef(problem.Reference); err == nil {
				prefix += "/" + parts.id
			}
		}
		result = append(result, fmt.Sprintf("%s (%s): %s", prefix, problem.Operation, problem.Message))
	}
	return result
}
