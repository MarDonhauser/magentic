package core

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// RepositoryKnowledge makes the difference between a negative fact and a
// failed observation explicit. In particular, a failed git command must never
// make a repository look clean or absent.
type RepositoryKnowledge string

const (
	RepositoryKnown         RepositoryKnowledge = "known"
	RepositoryPartial       RepositoryKnowledge = "partial"
	RepositoryUnknown       RepositoryKnowledge = "unknown"
	RepositoryNotRepository RepositoryKnowledge = "not_repository"
)

type RepositoryProblem struct {
	Operation string `json:"operation"`
	Message   string `json:"message"`
}

type RepositoryFact[T any] struct {
	State   RepositoryKnowledge `json:"state"`
	Value   T                   `json:"value"`
	Problem *RepositoryProblem  `json:"problem,omitempty"`
}

func (f RepositoryFact[T]) Known() bool { return f.State == RepositoryKnown }

type RepositoryCheckoutKind string

const (
	RepositoryBranchCheckout RepositoryCheckoutKind = "branch"
	RepositoryDetached       RepositoryCheckoutKind = "detached"
	RepositoryUnborn         RepositoryCheckoutKind = "unborn"
	RepositoryBare           RepositoryCheckoutKind = "bare"
)

type RepositoryCheckout struct {
	Kind   RepositoryCheckoutKind `json:"kind"`
	Branch string                 `json:"branch,omitempty"`
}

type RepositoryWorkingChanges struct {
	Staged     int      `json:"staged"`
	Modified   int      `json:"modified"`
	Untracked  int      `json:"untracked"`
	Conflicted int      `json:"conflicted"`
	Paths      []string `json:"paths"`
}

func (c RepositoryWorkingChanges) Clean() bool {
	return c.Staged == 0 && c.Modified == 0 && c.Untracked == 0 && c.Conflicted == 0
}

type RepositoryDivergence struct {
	Base   string `json:"base"`
	Ahead  int    `json:"ahead"`
	Behind int    `json:"behind"`
}

// WorktreeRef is an opaque, stable handle for a checkout. Desktop callers
// retain this value and pass it back to ResolveWorktree; filesystem paths stay
// private to the Repositories Module.
type WorktreeRef string

type RepositoryWorktree struct {
	Reference  WorktreeRef                              `json:"reference"`
	Location   string                                   `json:"location"`
	Path       string                                   `json:"-"`
	Main       bool                                     `json:"main"`
	Checkout   RepositoryFact[RepositoryCheckout]       `json:"checkout"`
	Head       RepositoryFact[string]                   `json:"head"`
	Changes    RepositoryFact[RepositoryWorkingChanges] `json:"changes"`
	Divergence RepositoryFact[RepositoryDivergence]     `json:"divergence"`
	Locked     bool                                     `json:"locked"`
	LockReason string                                   `json:"lockReason,omitempty"`
	Prunable   bool                                     `json:"prunable"`
}

type RepositoryProjectSurvey struct {
	ID         ProjectID                            `json:"id"`
	Name       string                               `json:"name"`
	Path       string                               `json:"path"`
	Presence   RepositoryKnowledge                  `json:"presence"`
	Problem    *RepositoryProblem                   `json:"problem,omitempty"`
	MainBranch RepositoryFact[string]               `json:"mainBranch"`
	Worktrees  RepositoryFact[[]RepositoryWorktree] `json:"worktrees"`
}

type RepositoriesSurvey struct {
	ObservedAt time.Time                 `json:"observedAt"`
	Projects   []RepositoryProjectSurvey `json:"projects"`
}

// RepositoryWorktreeTarget is the fresh, server-side result of resolving an
// opaque WorktreeRef. It is intentionally not part of a desktop projection.
type RepositoryWorktreeTarget struct {
	Project    Project
	Worktree   RepositoryWorktree
	MainBranch RepositoryFact[string]
}

// RemoteURL reads one named fetch remote without turning command failure or
// malformed successful output into an absent remote. Remote URLs may be HTTP,
// SSH, scp-like, file URLs, or local paths, so this Adapter validates the
// porcelain shape rather than imposing a transport scheme.
func (r *Repositories) RemoteURL(ctx context.Context, dir, remote string) RepositoryFact[string] {
	if r == nil || r.runner == nil {
		return repositoryUnknownFact[string]("remote_url", errors.New("Repositories is unavailable"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dir = strings.TrimSpace(dir)
	remote = strings.TrimSpace(remote)
	if dir == "" || remote == "" {
		return repositoryUnknownFact[string]("remote_url", errors.New("repository directory and remote name are required"))
	}
	out, err := r.runner.Run(ctx, dir, "remote", "get-url", remote)
	if err != nil {
		return repositoryFactForError[string]("remote_url", err)
	}
	url, err := parseRepositoryRemoteURL(out)
	if err != nil {
		return repositoryUnknownFact[string]("remote_url", err)
	}
	return repositoryKnownFact(url)
}

func parseRepositoryRemoteURL(out string) (string, error) {
	value, err := parseRepositoryTerminatedLine(out, "remote URL")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) != value {
		return "", errors.New("git returned an empty or ambiguous remote URL")
	}
	for _, char := range value {
		if char == '\x00' || (char < ' ' && char != '\t') {
			return "", errors.New("git returned a remote URL with control characters")
		}
	}
	return value, nil
}

func parseRepositoryTerminatedLine(out, label string) (string, error) {
	normalized := strings.ReplaceAll(out, "\r\n", "\n")
	if normalized == "" || strings.Contains(normalized, "\r") || !strings.HasSuffix(normalized, "\n") {
		return "", fmt.Errorf("git returned a truncated or malformed %s", label)
	}
	value := strings.TrimSuffix(normalized, "\n")
	if value == "" || strings.Contains(value, "\n") {
		return "", fmt.Errorf("git returned an empty or ambiguous %s", label)
	}
	return value, nil
}

func parseRepositoryNonnegativeDecimalLine(out, label string) (int, error) {
	value, err := parseRepositoryTerminatedLine(out, label)
	if err != nil {
		return 0, err
	}
	return parseRepositoryNonnegativeDecimal(value, label)
}

func parseRepositoryNonnegativeDecimal(value, label string) (int, error) {
	if !repositoryDecimal(value) {
		return 0, fmt.Errorf("invalid %s %q", label, value)
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("invalid %s %q", label, value)
	}
	return parsed, nil
}

// WorktreeDiff reports a human-readable checkout diff without collapsing
// command failures into a clean result.
func (r *Repositories) WorktreeDiff(ctx context.Context, target RepositoryWorktree) RepositoryFact[string] {
	if ctx == nil {
		ctx = context.Background()
	}
	dir := strings.TrimSpace(target.Path)
	if dir == "" {
		return repositoryUnknownFact[string]("diff", errors.New("resolved Worktree path is required"))
	}
	status, err := r.runner.Run(ctx, dir, "status", "--short")
	if err != nil {
		return repositoryFactForError[string]("diff_status", err)
	}
	diff, err := r.runner.Run(ctx, dir, "diff", "HEAD")
	if err != nil {
		return repositoryFactForError[string]("diff_content", err)
	}
	untracked, err := r.runner.Run(ctx, dir, "ls-files", "--others", "--exclude-standard")
	if err != nil {
		return repositoryFactForError[string]("diff_untracked", err)
	}

	var output strings.Builder
	if strings.TrimSpace(status) != "" {
		output.WriteString("── Status ──\n")
		output.WriteString(status)
		if !strings.HasSuffix(status, "\n") {
			output.WriteByte('\n')
		}
	}
	if strings.TrimSpace(diff) != "" {
		output.WriteString("── Diff (gegen HEAD) ──\n")
		output.WriteString(diff)
		if !strings.HasSuffix(diff, "\n") {
			output.WriteByte('\n')
		}
	}
	files := strings.Fields(strings.TrimSpace(untracked))
	if len(files) > 0 {
		output.WriteString("── Neue Dateien (untracked) ──\n")
		for _, file := range files {
			output.WriteString("+ " + file + "\n")
		}
	}
	text := strings.TrimRight(output.String(), "\n")
	if text == "" {
		text = "Keine Änderungen."
	}
	return repositoryKnownFact(text)
}

// RepositoryBaseline is portable Registry data. The Repositories Module owns
// its interpretation; callers only retain it and pass it back to Inspect.
type RepositoryBaseline struct {
	Directory  string   `json:"directory"`
	Head       string   `json:"head"`
	DirtyPaths []string `json:"dirtyPaths"`
}

type RepositoryBaselineDelta struct {
	Paths   RepositoryFact[[]string] `json:"paths"`
	Commits RepositoryFact[int]      `json:"commits"`
}

// RepositoryInspectRequest asks for fresh knowledge about one checkout. If
// Against is present, Inspect also computes the Session's delta from it.
type RepositoryInspectRequest struct {
	Directory  string              `json:"directory"`
	MainBranch string              `json:"mainBranch,omitempty"`
	Against    *RepositoryBaseline `json:"against,omitempty"`
}

type RepositoryInspection struct {
	ObservedAt time.Time                                `json:"observedAt"`
	Directory  string                                   `json:"directory"`
	Presence   RepositoryKnowledge                      `json:"presence"`
	Problem    *RepositoryProblem                       `json:"problem,omitempty"`
	Checkout   RepositoryFact[RepositoryCheckout]       `json:"checkout"`
	Head       RepositoryFact[string]                   `json:"head"`
	Changes    RepositoryFact[RepositoryWorkingChanges] `json:"changes"`
	Divergence RepositoryFact[RepositoryDivergence]     `json:"divergence"`
	Baseline   RepositoryFact[RepositoryBaseline]       `json:"baseline"`
	Delta      *RepositoryBaselineDelta                 `json:"delta,omitempty"`
}

// Repositories centralizes repository and managed-Worktree meanings. The only
// replaceable Seam is the private command runner used by the production Git
// Adapter and deterministic test Adapters.
type Repositories struct {
	runner   repositoriesCommandRunner
	now      func() time.Time
	changeMu sync.Mutex
}

func NewRepositories() *Repositories {
	return newRepositories(repositoriesGitRunner{})
}

func newRepositories(runner repositoriesCommandRunner) *Repositories {
	return &Repositories{runner: runner, now: time.Now}
}

type repositoriesCommandRunner interface {
	Run(ctx context.Context, dir string, args ...string) (string, error)
}

type repositoriesGitRunner struct{}

func (repositoriesGitRunner) Run(ctx context.Context, dir string, args ...string) (string, error) {
	// --no-optional-locks: Statusabfragen dürfen den Index nicht refreshen und
	// zurückschreiben, sonst kostet jeder Poll Platten-I/O und kollidiert mit
	// parallelen Git-Befehlen des Nutzers.
	full := append([]string{"--no-optional-locks", "-C", dir}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), nil
	}
	failure := &repositoriesCommandFailure{Args: append([]string(nil), args...), Output: strings.TrimSpace(string(out)), Err: err}
	if repositoriesNotRepositoryText(failure.Output) {
		return string(out), fmt.Errorf("%w: %v", errRepositoriesNotRepository, failure)
	}
	return string(out), failure
}

type repositoriesCommandFailure struct {
	Args   []string
	Output string
	Err    error
}

func (e *repositoriesCommandFailure) Error() string {
	if e.Output == "" {
		return fmt.Sprintf("git %s: %v", strings.Join(e.Args, " "), e.Err)
	}
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.Args, " "), e.Err, e.Output)
}

func (e *repositoriesCommandFailure) Unwrap() error { return e.Err }

var errRepositoriesNotRepository = errors.New("not a git repository")

func repositoriesNotRepositoryText(text string) bool {
	text = strings.ToLower(text)
	return strings.Contains(text, "not a git repository") ||
		strings.Contains(text, "not a git work tree") ||
		strings.Contains(text, "not a work tree")
}

func repositoryKnownFact[T any](value T) RepositoryFact[T] {
	return RepositoryFact[T]{State: RepositoryKnown, Value: value}
}

func repositoryUnknownFact[T any](operation string, err error) RepositoryFact[T] {
	return RepositoryFact[T]{State: RepositoryUnknown, Problem: repositoryProblem(operation, err)}
}

func repositoryPartialFact[T any](value T, operation string, err error) RepositoryFact[T] {
	return RepositoryFact[T]{State: RepositoryPartial, Value: value, Problem: repositoryProblem(operation, err)}
}

func repositoryNotRepositoryFact[T any](operation string, err error) RepositoryFact[T] {
	return RepositoryFact[T]{State: RepositoryNotRepository, Problem: repositoryProblem(operation, err)}
}

func repositoryProblem(operation string, err error) *RepositoryProblem {
	message := "knowledge unavailable"
	if err != nil && strings.TrimSpace(err.Error()) != "" {
		message = err.Error()
	}
	return &RepositoryProblem{Operation: operation, Message: message}
}

func repositoryFactForError[T any](operation string, err error) RepositoryFact[T] {
	if errors.Is(err, errRepositoriesNotRepository) {
		return repositoryNotRepositoryFact[T](operation, err)
	}
	return repositoryUnknownFact[T](operation, err)
}

func repositoryContextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return nil
}

// repositoriesInParallel runs one independent git observation per index. Each
// call writes only its own slice element, so the survey keeps its sequential
// result order while the process spawns overlap.
const repositoriesParallelism = 8

func repositoriesInParallel(count int, observe func(index int)) {
	if count <= 0 {
		return
	}
	if count == 1 {
		observe(0)
		return
	}
	slots := make(chan struct{}, repositoriesParallelism)
	var wg sync.WaitGroup
	for index := 0; index < count; index++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			observe(index)
		}(index)
	}
	wg.Wait()
}

// Survey takes one coherent, uncached pass over every Project. It enumerates
// topology once per Project and status once per Worktree; history and diffs are
// deliberately left to Inspect.
func (r *Repositories) Survey(ctx context.Context, projects []Project) (RepositoriesSurvey, error) {
	survey := RepositoriesSurvey{ObservedAt: r.now()}
	projects = append([]Project(nil), projects...)
	observed := make([]RepositoryProjectSurvey, len(projects))
	failures := make([]error, len(projects))
	repositoriesInParallel(len(projects), func(index int) {
		if err := ctx.Err(); err != nil {
			failures[index] = err
			return
		}
		observed[index], failures[index] = r.surveyProject(ctx, projects[index])
	})
	for index := range projects {
		if failures[index] != nil {
			return RepositoriesSurvey{}, failures[index]
		}
		survey.Projects = append(survey.Projects, observed[index])
	}
	return survey, nil
}

func (r *Repositories) surveyProject(ctx context.Context, project Project) (RepositoryProjectSurvey, error) {
	result := RepositoryProjectSurvey{ID: project.ID, Name: project.Name, Path: filepath.Clean(project.Path)}
	topology, err := r.loadTopology(ctx, project.Path)
	if err != nil {
		if ctxErr := repositoryContextError(ctx, err); ctxErr != nil {
			return result, ctxErr
		}
		state := RepositoryUnknown
		if errors.Is(err, errRepositoriesNotRepository) {
			state = RepositoryNotRepository
		}
		result.Presence = state
		result.Problem = repositoryProblem("worktree_topology", err)
		result.MainBranch = repositoryFactForError[string]("main_branch", err)
		result.Worktrees = repositoryFactForError[[]RepositoryWorktree]("worktree_topology", err)
		return result, nil
	}

	result.Presence = RepositoryKnown
	worktrees := make([]RepositoryWorktree, len(topology))
	statusFailures := make([]error, len(topology))
	for index, raw := range topology {
		wt := repositoryWorktreeFromTopology(project.Path, raw)
		wt.Reference = repositoryWorktreeReference(project, wt.Path)
		wt.Location = repositoryWorktreeLocation(project.Path, wt.Path)
		worktrees[index] = wt
	}
	repositoriesInParallel(len(topology), func(index int) {
		status, statusErr := r.loadStatus(ctx, topology[index].Path)
		if statusErr != nil {
			statusFailures[index] = statusErr
			worktrees[index].Changes = repositoryFactForError[RepositoryWorkingChanges]("status", statusErr)
			return
		}
		worktrees[index].Changes = repositoryKnownFact(status.Changes)
		if status.Checkout.Kind != "" {
			worktrees[index].Checkout = repositoryKnownFact(status.Checkout)
		}
		if status.Head != "" {
			worktrees[index].Head = repositoryKnownFact(status.Head)
		}
	})
	for _, statusErr := range statusFailures {
		if statusErr == nil {
			continue
		}
		if ctxErr := repositoryContextError(ctx, statusErr); ctxErr != nil {
			return result, ctxErr
		}
	}

	result.MainBranch = resolveRepositoryMainBranch(project, worktrees)
	repositoriesInParallel(len(worktrees), func(index int) {
		worktrees[index].Divergence = r.divergence(ctx, worktrees[index], result.MainBranch)
	})
	for i := range worktrees {
		if problem := worktrees[i].Divergence.Problem; problem != nil {
			if err := ctx.Err(); err != nil {
				return result, err
			}
		}
	}
	result.Worktrees = repositoryKnownFact(worktrees)
	return result, nil
}

// ResolveWorktree turns an opaque browser-safe reference into fresh repository
// knowledge. Resolution always performs a new Survey, so stale projections can
// never authorize an action after the checkout topology has changed.
func (r *Repositories) ResolveWorktree(ctx context.Context, project Project, reference WorktreeRef) (RepositoryWorktreeTarget, error) {
	if strings.TrimSpace(string(reference)) == "" {
		return RepositoryWorktreeTarget{}, errors.New("Worktree reference is required")
	}
	survey, err := r.Survey(ctx, []Project{project})
	if err != nil {
		return RepositoryWorktreeTarget{}, err
	}
	if len(survey.Projects) != 1 {
		return RepositoryWorktreeTarget{}, errors.New("Project is missing from repository Survey")
	}
	repository := survey.Projects[0]
	if repository.Presence != RepositoryKnown || !repository.Worktrees.Known() {
		message := "Worktree topology is unavailable"
		if repository.Worktrees.Problem != nil && strings.TrimSpace(repository.Worktrees.Problem.Message) != "" {
			message = repository.Worktrees.Problem.Message
		} else if repository.Problem != nil && strings.TrimSpace(repository.Problem.Message) != "" {
			message = repository.Problem.Message
		}
		return RepositoryWorktreeTarget{}, errors.New(message)
	}
	for _, worktree := range repository.Worktrees.Value {
		if worktree.Reference == reference {
			return RepositoryWorktreeTarget{Project: project, Worktree: worktree, MainBranch: repository.MainBranch}, nil
		}
	}
	return RepositoryWorktreeTarget{}, errors.New("Worktree reference is stale or unknown")
}

func repositoryWorktreeReference(project Project, path string) WorktreeRef {
	identity := strings.TrimSpace(string(project.ID))
	if identity == "" {
		identity = strings.TrimSpace(project.Name)
	}
	sum := sha256.Sum256([]byte(identity + "\x00" + repositoryComparablePath(path)))
	return WorktreeRef(fmt.Sprintf("wt_%x", sum[:16]))
}

func repositoryWorktreeLocation(projectPath, worktreePath string) string {
	projectPath = filepath.Clean(projectPath)
	worktreePath = filepath.Clean(worktreePath)
	if sameRepositoryPath(projectPath, worktreePath) {
		return filepath.Base(projectPath)
	}
	base := filepath.Base(worktreePath)
	parent := filepath.Base(filepath.Dir(worktreePath))
	if parent == "" || parent == "." || parent == string(filepath.Separator) {
		return base
	}
	return filepath.ToSlash(filepath.Join(parent, base))
}

func resolveRepositoryMainBranch(project Project, worktrees []RepositoryWorktree) RepositoryFact[string] {
	if main := strings.TrimSpace(project.MainBranch); main != "" {
		return repositoryKnownFact(main)
	}
	for _, wt := range worktrees {
		if !wt.Main || !wt.Checkout.Known() || wt.Checkout.Value.Kind != RepositoryBranchCheckout {
			continue
		}
		if wt.Checkout.Value.Branch != "" {
			return repositoryKnownFact(wt.Checkout.Value.Branch)
		}
	}
	return repositoryUnknownFact[string]("main_branch", errors.New("Project root is not on a named branch"))
}

// Inspect performs a fresh observation of one checkout. It never consumes a
// Survey result, so baseline and lifecycle decisions cannot inherit display
// cache state when callers migrate to this Interface.
func (r *Repositories) Inspect(ctx context.Context, request RepositoryInspectRequest) (RepositoryInspection, error) {
	dir := strings.TrimSpace(request.Directory)
	if dir == "" {
		return RepositoryInspection{}, errors.New("repository directory is required")
	}
	dir = filepath.Clean(dir)
	result := RepositoryInspection{ObservedAt: r.now(), Directory: dir}
	status, err := r.loadStatus(ctx, dir)
	if err != nil {
		if ctxErr := repositoryContextError(ctx, err); ctxErr != nil {
			return RepositoryInspection{}, ctxErr
		}
		state := RepositoryUnknown
		if errors.Is(err, errRepositoriesNotRepository) {
			state = RepositoryNotRepository
		}
		result.Presence = state
		result.Problem = repositoryProblem("status", err)
		result.Checkout = repositoryFactForError[RepositoryCheckout]("status", err)
		result.Head = repositoryFactForError[string]("status", err)
		result.Changes = repositoryFactForError[RepositoryWorkingChanges]("status", err)
		result.Divergence = repositoryFactForError[RepositoryDivergence]("status", err)
		result.Baseline = repositoryFactForError[RepositoryBaseline]("baseline", err)
		if request.Against != nil {
			result.Delta = &RepositoryBaselineDelta{
				Paths:   repositoryFactForError[[]string]("baseline_delta", err),
				Commits: repositoryFactForError[int]("baseline_delta", err),
			}
		}
		return result, nil
	}

	result.Presence = RepositoryKnown
	result.Checkout = repositoryKnownFact(status.Checkout)
	result.Changes = repositoryKnownFact(status.Changes)
	if status.Head == "" {
		missingHead := errors.New("HEAD is unavailable")
		result.Head = repositoryUnknownFact[string]("head", missingHead)
		result.Baseline = repositoryUnknownFact[RepositoryBaseline]("baseline", missingHead)
	} else {
		result.Head = repositoryKnownFact(status.Head)
		dirty := append([]string(nil), status.Changes.Paths...)
		sort.Strings(dirty)
		result.Baseline = repositoryKnownFact(RepositoryBaseline{Directory: dir, Head: status.Head, DirtyPaths: dirty})
	}

	main := repositoryKnownFact(strings.TrimSpace(request.MainBranch))
	if main.Value == "" {
		main = repositoryUnknownFact[string]("main_branch", errors.New("main branch was not supplied"))
	}
	wt := RepositoryWorktree{Path: dir, Checkout: result.Checkout, Head: result.Head}
	result.Divergence = r.divergence(ctx, wt, main)
	if err := ctx.Err(); err != nil {
		return RepositoryInspection{}, err
	}
	if request.Against != nil {
		result.Delta = r.baselineDelta(ctx, dir, result, *request.Against)
		if err := ctx.Err(); err != nil {
			return RepositoryInspection{}, err
		}
	}
	return result, nil
}

func (r *Repositories) baselineDelta(ctx context.Context, dir string, current RepositoryInspection, baseline RepositoryBaseline) *RepositoryBaselineDelta {
	delta := &RepositoryBaselineDelta{}
	if baseline.Directory != "" && !sameRepositoryPath(baseline.Directory, dir) {
		err := errors.New("baseline belongs to a different checkout")
		delta.Paths = repositoryUnknownFact[[]string]("baseline_delta", err)
		delta.Commits = repositoryUnknownFact[int]("baseline_delta", err)
		return delta
	}
	if !current.Changes.Known() {
		delta.Paths = repositoryFactForError[[]string]("baseline_delta", errors.New("working changes are unavailable"))
	} else {
		before := make(map[string]bool, len(baseline.DirtyPaths))
		for _, path := range baseline.DirtyPaths {
			before[path] = true
		}
		var changed []string
		for _, path := range current.Changes.Value.Paths {
			if !before[path] {
				changed = append(changed, path)
			}
		}
		sort.Strings(changed)
		delta.Paths = repositoryKnownFact(changed)
	}

	if baseline.Head == "" || !current.Head.Known() {
		delta.Commits = repositoryUnknownFact[int]("baseline_delta", errors.New("baseline or current HEAD is unavailable"))
		return delta
	}
	if baseline.Head == current.Head.Value {
		delta.Commits = repositoryKnownFact(0)
		return delta
	}
	out, err := r.runner.Run(ctx, dir, "rev-list", "--count", baseline.Head+"..HEAD")
	if err != nil {
		delta.Commits = repositoryFactForError[int]("baseline_delta", err)
		return delta
	}
	count, err := parseRepositoryNonnegativeDecimalLine(out, "commit count")
	if err != nil {
		delta.Commits = repositoryUnknownFact[int]("baseline_delta", err)
		return delta
	}
	delta.Commits = repositoryKnownFact(count)
	return delta
}

type repositoriesTopologyWorktree struct {
	Path       string
	Head       string
	Branch     string
	Detached   bool
	Unborn     bool
	Bare       bool
	Locked     bool
	LockReason string
	Prunable   bool
}

func (r *Repositories) loadTopology(ctx context.Context, projectPath string) ([]repositoriesTopologyWorktree, error) {
	out, err := r.runner.Run(ctx, projectPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	return parseRepositoriesTopology(out)
}

func parseRepositoriesTopology(out string) ([]repositoriesTopologyWorktree, error) {
	type topologyRecord struct {
		worktree      repositoriesTopologyWorktree
		startLine     int
		seenHead      bool
		seenCheckout  bool
		seenLocked    bool
		seenPrunable  bool
		checkoutLabel string
	}

	normalized := strings.ReplaceAll(out, "\r\n", "\n")
	if strings.Contains(normalized, "\r") {
		return nil, errors.New("invalid carriage return in Worktree topology")
	}
	if !strings.HasSuffix(normalized, "\n\n") {
		return nil, errors.New("git returned a truncated Worktree topology record")
	}

	var result []repositoriesTopologyWorktree
	var current *topologyRecord
	seenPaths := map[string]bool{}
	flush := func() error {
		if current == nil {
			return nil
		}
		record := current.worktree
		if !current.seenCheckout {
			return fmt.Errorf("Worktree record at line %d omitted its checkout kind", current.startLine)
		}
		switch current.checkoutLabel {
		case "bare":
			if current.seenHead {
				return fmt.Errorf("bare Worktree record at line %d must not contain HEAD", current.startLine)
			}
		case "branch", "detached":
			if !current.seenHead {
				return fmt.Errorf("%s Worktree record at line %d omitted HEAD", current.checkoutLabel, current.startLine)
			}
		default:
			return fmt.Errorf("invalid checkout kind in Worktree record at line %d", current.startLine)
		}
		if record.Path == "" || !filepath.IsAbs(record.Path) {
			return fmt.Errorf("invalid Worktree path at topology line %d", current.startLine)
		}
		record.Path = filepath.Clean(record.Path)
		pathKey := repositoryComparablePath(record.Path)
		if seenPaths[pathKey] {
			return fmt.Errorf("duplicate Worktree path at topology line %d", current.startLine)
		}
		seenPaths[pathKey] = true
		if current.checkoutLabel == "branch" && allZeroOID(record.Head) {
			record.Unborn = true
		}
		result = append(result, record)
		current = nil
		return nil
	}
	lines := strings.Split(strings.TrimSuffix(normalized, "\n"), "\n")
	for lineIndex, line := range lines {
		lineNumber := lineIndex + 1
		if line == "" {
			if current == nil {
				return nil, fmt.Errorf("unexpected Worktree record separator at topology line %d", lineNumber)
			}
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current != nil {
				return nil, fmt.Errorf("misplaced worktree at topology line %d: missing record separator", lineNumber)
			}
			path, err := decodeRepositoriesTopologyValue(strings.TrimPrefix(line, "worktree "))
			if err != nil || path == "" {
				return nil, fmt.Errorf("invalid Worktree path at topology line %d", lineNumber)
			}
			current = &topologyRecord{
				worktree: repositoriesTopologyWorktree{Path: path}, startLine: lineNumber,
			}
		case strings.HasPrefix(line, "HEAD "):
			if current == nil || current.seenHead || current.seenCheckout || current.seenLocked || current.seenPrunable {
				return nil, fmt.Errorf("duplicate or misplaced HEAD at topology line %d", lineNumber)
			}
			head := strings.TrimPrefix(line, "HEAD ")
			if !validRepositoryObjectID(head) {
				return nil, fmt.Errorf("invalid HEAD at topology line %d", lineNumber)
			}
			current.worktree.Head = head
			current.seenHead = true
		case strings.HasPrefix(line, "branch "):
			if current == nil || !current.seenHead || current.seenCheckout || current.seenLocked || current.seenPrunable {
				return nil, fmt.Errorf("duplicate or misplaced branch at topology line %d", lineNumber)
			}
			branchRef := strings.TrimPrefix(line, "branch ")
			if !validRepositoryBranchRef(branchRef) {
				return nil, fmt.Errorf("invalid branch at topology line %d", lineNumber)
			}
			current.worktree.Branch = strings.TrimPrefix(branchRef, "refs/heads/")
			current.seenCheckout = true
			current.checkoutLabel = "branch"
		case line == "detached":
			if current == nil || !current.seenHead || current.seenCheckout || current.seenLocked || current.seenPrunable {
				return nil, fmt.Errorf("duplicate or misplaced detached at topology line %d", lineNumber)
			}
			if allZeroOID(current.worktree.Head) {
				return nil, fmt.Errorf("detached Worktree has an unborn HEAD at topology line %d", lineNumber)
			}
			current.worktree.Detached = true
			current.seenCheckout = true
			current.checkoutLabel = "detached"
		case line == "bare":
			if current == nil || current.seenHead || current.seenCheckout || current.seenLocked || current.seenPrunable {
				return nil, fmt.Errorf("duplicate or misplaced bare at topology line %d", lineNumber)
			}
			current.worktree.Bare = true
			current.seenCheckout = true
			current.checkoutLabel = "bare"
		case line == "locked" || strings.HasPrefix(line, "locked "):
			if current == nil || !current.seenCheckout || current.seenLocked || current.seenPrunable {
				return nil, fmt.Errorf("duplicate or misplaced locked at topology line %d", lineNumber)
			}
			reason, err := decodeRepositoriesTopologyOptionalReason(line, "locked")
			if err != nil {
				return nil, fmt.Errorf("invalid locked reason at topology line %d", lineNumber)
			}
			current.worktree.Locked = true
			current.worktree.LockReason = reason
			current.seenLocked = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			if current == nil || !current.seenCheckout || current.seenPrunable {
				return nil, fmt.Errorf("duplicate or misplaced prunable at topology line %d", lineNumber)
			}
			if _, err := decodeRepositoriesTopologyOptionalReason(line, "prunable"); err != nil {
				return nil, fmt.Errorf("invalid prunable reason at topology line %d", lineNumber)
			}
			current.worktree.Prunable = true
			current.seenPrunable = true
		default:
			return nil, fmt.Errorf("unrecognized Worktree topology line %d", lineNumber)
		}
	}
	if current != nil {
		return nil, fmt.Errorf("unterminated Worktree record at topology line %d", current.startLine)
	}
	if len(result) == 0 {
		return nil, errors.New("git returned no Worktree topology")
	}
	return result, nil
}

func decodeRepositoriesTopologyValue(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("topology value is empty")
	}
	if raw[0] != '"' {
		if strings.TrimSpace(raw) != raw {
			return "", errors.New("topology value has surrounding whitespace")
		}
		for _, character := range raw {
			if character < ' ' || character == '\x7f' {
				return "", errors.New("topology value contains a control character")
			}
		}
		return raw, nil
	}
	if len(raw) < 2 || raw[len(raw)-1] != '"' {
		return "", errors.New("unterminated quoted topology value")
	}
	decoded, err := strconv.Unquote(raw)
	if err != nil {
		return "", err
	}
	return decoded, nil
}

func decodeRepositoriesTopologyOptionalReason(line, field string) (string, error) {
	if line == field {
		return "", nil
	}
	raw := strings.TrimPrefix(line, field+" ")
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("empty topology reason")
	}
	return decodeRepositoriesTopologyValue(raw)
}

func validRepositoryObjectID(oid string) bool {
	if len(oid) != 40 && len(oid) != 64 {
		return false
	}
	for _, char := range oid {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func validRepositoryBranchRef(ref string) bool {
	const prefix = "refs/heads/"
	branch := strings.TrimPrefix(ref, prefix)
	if branch == ref || branch == "" || strings.TrimSpace(branch) != branch ||
		strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") ||
		strings.HasSuffix(branch, ".") || strings.Contains(branch, "//") ||
		strings.Contains(branch, "..") || strings.Contains(branch, "@{") || strings.ContainsAny(branch, " ~^:?*[\\") {
		return false
	}
	for _, char := range branch {
		if char < ' ' || char == '\x7f' {
			return false
		}
	}
	for _, component := range strings.Split(branch, "/") {
		if component == "" || component == "." || strings.HasPrefix(component, ".") || strings.HasSuffix(component, ".lock") {
			return false
		}
	}
	return true
}

func repositoryWorktreeFromTopology(projectPath string, raw repositoriesTopologyWorktree) RepositoryWorktree {
	wt := RepositoryWorktree{
		Path:       raw.Path,
		Main:       sameRepositoryPath(projectPath, raw.Path),
		Locked:     raw.Locked,
		LockReason: raw.LockReason,
		Prunable:   raw.Prunable,
	}
	switch {
	case raw.Bare:
		wt.Checkout = repositoryKnownFact(RepositoryCheckout{Kind: RepositoryBare})
	case raw.Unborn:
		wt.Checkout = repositoryKnownFact(RepositoryCheckout{Kind: RepositoryUnborn, Branch: raw.Branch})
	case raw.Branch != "":
		wt.Checkout = repositoryKnownFact(RepositoryCheckout{Kind: RepositoryBranchCheckout, Branch: raw.Branch})
	case raw.Detached:
		wt.Checkout = repositoryKnownFact(RepositoryCheckout{Kind: RepositoryDetached})
	default:
		wt.Checkout = repositoryUnknownFact[RepositoryCheckout]("checkout", errors.New("checkout kind is unavailable"))
	}
	if raw.Head == "" || allZeroOID(raw.Head) {
		wt.Head = repositoryUnknownFact[string]("head", errors.New("HEAD is unavailable"))
	} else {
		wt.Head = repositoryKnownFact(raw.Head)
	}
	return wt
}

type repositoriesStatus struct {
	Checkout RepositoryCheckout
	Head     string
	Changes  RepositoryWorkingChanges
}

func (r *Repositories) loadStatus(ctx context.Context, dir string) (repositoriesStatus, error) {
	out, err := r.runner.Run(ctx, dir, "status", "--porcelain=v2", "--branch")
	if err != nil {
		return repositoriesStatus{}, err
	}
	return parseRepositoriesStatus(out)
}

func parseRepositoriesStatus(out string) (repositoriesStatus, error) {
	normalized := strings.ReplaceAll(out, "\r\n", "\n")
	if normalized == "" || strings.Contains(normalized, "\r") || !strings.HasSuffix(normalized, "\n") {
		return repositoriesStatus{}, errors.New("git returned a truncated or malformed status record")
	}
	body := strings.TrimSuffix(normalized, "\n")
	if body == "" || strings.Contains(body, "\n\n") {
		return repositoriesStatus{}, errors.New("git returned an empty or malformed status record")
	}

	var result repositoriesStatus
	unborn := false
	seenOID := false
	seenHead := false
	seenUpstream := false
	seenAheadBehind := false
	seenStash := false
	seenChanges := false
	seenPaths := map[string]bool{}
	addPath := func(path string) {
		if path == "" || seenPaths[path] {
			return
		}
		seenPaths[path] = true
		result.Changes.Paths = append(result.Changes.Paths, path)
	}
	for lineNumber, line := range strings.Split(body, "\n") {
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			oid := strings.TrimPrefix(line, "# branch.oid ")
			if seenOID || seenHead || seenChanges || oid == "" || strings.TrimSpace(oid) != oid {
				return repositoriesStatus{}, fmt.Errorf("invalid branch.oid at status line %d", lineNumber+1)
			}
			seenOID = true
			if oid == "(initial)" {
				unborn = true
			} else if validRepositoryObjectID(oid) && !allZeroOID(oid) {
				result.Head = oid
			} else {
				return repositoriesStatus{}, fmt.Errorf("invalid branch.oid at status line %d", lineNumber+1)
			}
		case strings.HasPrefix(line, "# branch.head "):
			head := strings.TrimPrefix(line, "# branch.head ")
			if !seenOID || seenHead || seenChanges || head == "" || strings.TrimSpace(head) != head {
				return repositoriesStatus{}, fmt.Errorf("invalid branch.head at status line %d", lineNumber+1)
			}
			seenHead = true
			switch head {
			case "(detached)":
				result.Checkout = RepositoryCheckout{Kind: RepositoryDetached}
			default:
				if !validRepositoryBranchRef("refs/heads/" + head) {
					return repositoriesStatus{}, fmt.Errorf("invalid branch.head at status line %d", lineNumber+1)
				}
				result.Checkout = RepositoryCheckout{Kind: RepositoryBranchCheckout, Branch: head}
			}
		case strings.HasPrefix(line, "# branch.upstream "):
			upstream := strings.TrimPrefix(line, "# branch.upstream ")
			if !seenHead || seenUpstream || seenChanges || upstream == "" || strings.TrimSpace(upstream) != upstream || strings.ContainsAny(upstream, "\x00\r\n") {
				return repositoriesStatus{}, fmt.Errorf("invalid branch.upstream at status line %d", lineNumber+1)
			}
			seenUpstream = true
		case strings.HasPrefix(line, "# branch.ab "):
			fields := strings.Split(strings.TrimPrefix(line, "# branch.ab "), " ")
			if !seenUpstream || seenAheadBehind || seenChanges || len(fields) != 2 || len(fields[0]) < 2 || len(fields[1]) < 2 || fields[0][0] != '+' || fields[1][0] != '-' {
				return repositoriesStatus{}, fmt.Errorf("invalid branch.ab at status line %d", lineNumber+1)
			}
			if _, err := parseRepositoryNonnegativeDecimal(fields[0][1:], "ahead count"); err != nil {
				return repositoriesStatus{}, fmt.Errorf("invalid branch.ab at status line %d", lineNumber+1)
			}
			if _, err := parseRepositoryNonnegativeDecimal(fields[1][1:], "behind count"); err != nil {
				return repositoriesStatus{}, fmt.Errorf("invalid branch.ab at status line %d", lineNumber+1)
			}
			seenAheadBehind = true
		case strings.HasPrefix(line, "# stash "):
			if !seenHead || seenStash || seenChanges {
				return repositoriesStatus{}, fmt.Errorf("invalid stash header at status line %d", lineNumber+1)
			}
			if _, err := parseRepositoryNonnegativeDecimal(strings.TrimPrefix(line, "# stash "), "stash count"); err != nil {
				return repositoriesStatus{}, fmt.Errorf("invalid stash header at status line %d", lineNumber+1)
			}
			seenStash = true
		case strings.HasPrefix(line, "1 "):
			seenChanges = true
			fields := strings.SplitN(line, " ", 9)
			if len(fields) != 9 || !validRepositoryChangedXY(fields[1], false) ||
				!validRepositorySubmodule(fields[2]) || !validRepositoryModes(fields[3:6]) ||
				!validRepositoryOIDSet(fields[6:8]) {
				return repositoriesStatus{}, fmt.Errorf("invalid ordinary change at status line %d", lineNumber+1)
			}
			path, err := decodeRepositoryStatusPath(fields[8])
			if err != nil {
				return repositoriesStatus{}, fmt.Errorf("invalid ordinary path at status line %d: %w", lineNumber+1, err)
			}
			countRepositoryXY(&result.Changes, fields[1])
			addPath(path)
		case strings.HasPrefix(line, "2 "):
			seenChanges = true
			fields := strings.SplitN(line, " ", 10)
			if len(fields) != 10 || !validRepositoryChangedXY(fields[1], true) ||
				!validRepositorySubmodule(fields[2]) || !validRepositoryModes(fields[3:6]) ||
				!validRepositoryOIDSet(fields[6:8]) || !validRepositoryRenameScore(fields[8], fields[1]) {
				return repositoriesStatus{}, fmt.Errorf("invalid renamed change at status line %d", lineNumber+1)
			}
			pathRaw, originalRaw, hasOriginal := strings.Cut(fields[9], "\t")
			path, pathErr := decodeRepositoryStatusPath(pathRaw)
			original, originalErr := decodeRepositoryStatusPath(originalRaw)
			if !hasOriginal || pathErr != nil || originalErr != nil {
				return repositoriesStatus{}, fmt.Errorf("invalid renamed paths at status line %d", lineNumber+1)
			}
			countRepositoryXY(&result.Changes, fields[1])
			addPath(path)
			addPath(original)
		case strings.HasPrefix(line, "u "):
			seenChanges = true
			fields := strings.SplitN(line, " ", 11)
			if len(fields) != 11 || !validRepositoryUnmergedXY(fields[1]) ||
				!validRepositorySubmodule(fields[2]) || !validRepositoryModes(fields[3:7]) ||
				!validRepositoryOIDSet(fields[7:10]) {
				return repositoriesStatus{}, fmt.Errorf("invalid unmerged change at status line %d", lineNumber+1)
			}
			path, err := decodeRepositoryStatusPath(fields[10])
			if err != nil {
				return repositoriesStatus{}, fmt.Errorf("invalid unmerged path at status line %d: %w", lineNumber+1, err)
			}
			result.Changes.Conflicted++
			addPath(path)
		case strings.HasPrefix(line, "? "):
			seenChanges = true
			path, err := decodeRepositoryStatusPath(strings.TrimPrefix(line, "? "))
			if err != nil {
				return repositoriesStatus{}, fmt.Errorf("invalid untracked path at status line %d", lineNumber+1)
			}
			result.Changes.Untracked++
			addPath(path)
		case strings.HasPrefix(line, "! "):
			seenChanges = true
			if _, err := decodeRepositoryStatusPath(strings.TrimPrefix(line, "! ")); err != nil {
				return repositoriesStatus{}, fmt.Errorf("invalid ignored path at status line %d", lineNumber+1)
			}
		default:
			return repositoriesStatus{}, fmt.Errorf("unrecognized status line %d", lineNumber+1)
		}
	}
	if !seenOID || !seenHead {
		return repositoriesStatus{}, errors.New("git status omitted required branch headers")
	}
	if unborn {
		if result.Checkout.Kind != RepositoryBranchCheckout || result.Checkout.Branch == "" {
			return repositoriesStatus{}, errors.New("initial branch.oid requires a named branch.head")
		}
		result.Checkout.Kind = RepositoryUnborn
	} else if result.Checkout.Kind == RepositoryUnborn {
		return repositoriesStatus{}, errors.New("branch.head and branch.oid are incoherent")
	}
	sort.Strings(result.Changes.Paths)
	return result, nil
}

func validRepositoryChangedXY(xy string, renamed bool) bool {
	if len(xy) != 2 {
		return false
	}
	const allowed = ".MADRCT"
	if !strings.ContainsRune(allowed, rune(xy[0])) || !strings.ContainsRune(allowed, rune(xy[1])) || xy == ".." {
		return false
	}
	hasRename := strings.ContainsAny(xy, "RC")
	return hasRename == renamed
}

func validRepositoryUnmergedXY(xy string) bool {
	switch xy {
	case "DD", "AU", "UD", "UA", "DU", "AA", "UU":
		return true
	default:
		return false
	}
}

func validRepositorySubmodule(value string) bool {
	if value == "N..." {
		return true
	}
	return len(value) == 4 && value[0] == 'S' &&
		(value[1] == '.' || value[1] == 'C') &&
		(value[2] == '.' || value[2] == 'M') &&
		(value[3] == '.' || value[3] == 'U')
}

func validRepositoryMode(value string) bool {
	if len(value) != 6 {
		return false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '7' {
			return false
		}
	}
	return true
}

func validRepositoryModes(values []string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if !validRepositoryMode(value) {
			return false
		}
	}
	return true
}

func validRepositoryOIDSet(values []string) bool {
	if len(values) == 0 {
		return false
	}
	length := len(values[0])
	for _, value := range values {
		if len(value) != length || !validRepositoryObjectID(value) {
			return false
		}
	}
	return true
}

func validRepositoryRenameScore(value, xy string) bool {
	if len(value) < 2 || (value[0] != 'R' && value[0] != 'C') {
		return false
	}
	if !strings.ContainsRune(xy, rune(value[0])) {
		return false
	}
	score, err := parseRepositoryNonnegativeDecimal(value[1:], "rename score")
	return err == nil && score <= 100
}

func countRepositoryXY(changes *RepositoryWorkingChanges, xy string) {
	if xy[0] != '.' {
		changes.Staged++
	}
	if xy[1] != '.' {
		changes.Modified++
	}
}

func decodeRepositoryStatusPath(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\x00\r\n") {
		return "", errors.New("path is empty or not an exact scalar")
	}
	if raw[0] != '"' {
		return raw, nil
	}
	if len(raw) < 2 || raw[len(raw)-1] != '"' {
		return "", errors.New("quoted path is truncated")
	}
	decoded, err := strconv.Unquote(raw)
	if err != nil || decoded == "" || strings.ContainsAny(decoded, "\x00\r\n") {
		return "", errors.New("quoted path is malformed")
	}
	return decoded, nil
}

func (r *Repositories) divergence(ctx context.Context, wt RepositoryWorktree, main RepositoryFact[string]) RepositoryFact[RepositoryDivergence] {
	if main.State == RepositoryNotRepository {
		return repositoryNotRepositoryFact[RepositoryDivergence]("divergence", errors.New("Project is not a repository"))
	}
	if !main.Known() || strings.TrimSpace(main.Value) == "" {
		return repositoryUnknownFact[RepositoryDivergence]("divergence", errors.New("main branch is unavailable"))
	}
	if wt.Checkout.Known() && wt.Checkout.Value.Kind == RepositoryBranchCheckout && wt.Checkout.Value.Branch == main.Value {
		return repositoryKnownFact(RepositoryDivergence{Base: main.Value})
	}
	return r.compareRefs(ctx, wt.Path, main.Value, "HEAD", "divergence")
}

func sameRepositoryPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return repositoryComparablePath(a) == repositoryComparablePath(b)
}

func repositoryWorktreeForDirectory(worktrees []RepositoryWorktree, directory string) (RepositoryWorktree, bool) {
	if strings.TrimSpace(directory) == "" {
		return RepositoryWorktree{}, false
	}
	directory = repositoryComparablePath(directory)
	best := -1
	bestRootLength := -1
	for index, worktree := range worktrees {
		if strings.TrimSpace(worktree.Path) == "" {
			continue
		}
		root := repositoryComparablePath(worktree.Path)
		relative, err := filepath.Rel(root, directory)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			continue
		}
		if len(root) > bestRootLength {
			best = index
			bestRootLength = len(root)
		}
	}
	if best < 0 {
		return RepositoryWorktree{}, false
	}
	return worktrees[best], true
}

func repositoryComparablePath(path string) string {
	absolute, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, resolveErr := filepath.EvalSymlinks(absolute); resolveErr == nil {
		return resolved
	}
	return absolute
}

func allZeroOID(oid string) bool {
	if oid == "" {
		return false
	}
	for _, r := range oid {
		if r != '0' {
			return false
		}
	}
	return true
}
