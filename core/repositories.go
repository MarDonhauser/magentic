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

// WorktreeDiff reports a human-readable checkout diff without collapsing
// command failures into a clean result.
func (r *Repositories) WorktreeDiff(ctx context.Context, target RepositoryWorktree) RepositoryFact[string] {
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
	full := append([]string{"-C", dir}, args...)
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

// Survey takes one coherent, uncached pass over every Project. It enumerates
// topology once per Project and status once per Worktree; history and diffs are
// deliberately left to Inspect.
func (r *Repositories) Survey(ctx context.Context, projects []Project) (RepositoriesSurvey, error) {
	survey := RepositoriesSurvey{ObservedAt: r.now()}
	projects = append([]Project(nil), projects...)
	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return RepositoriesSurvey{}, err
		}
		observed, err := r.surveyProject(ctx, project)
		if err != nil {
			return RepositoriesSurvey{}, err
		}
		survey.Projects = append(survey.Projects, observed)
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
	worktrees := make([]RepositoryWorktree, 0, len(topology))
	for _, raw := range topology {
		wt := repositoryWorktreeFromTopology(project.Path, raw)
		wt.Reference = repositoryWorktreeReference(project, wt.Path)
		wt.Location = repositoryWorktreeLocation(project.Path, wt.Path)
		status, statusErr := r.loadStatus(ctx, raw.Path)
		if statusErr != nil {
			if ctxErr := repositoryContextError(ctx, statusErr); ctxErr != nil {
				return result, ctxErr
			}
			wt.Changes = repositoryFactForError[RepositoryWorkingChanges]("status", statusErr)
		} else {
			wt.Changes = repositoryKnownFact(status.Changes)
			if status.Checkout.Kind != "" {
				wt.Checkout = repositoryKnownFact(status.Checkout)
			}
			if status.Head != "" {
				wt.Head = repositoryKnownFact(status.Head)
			}
		}
		worktrees = append(worktrees, wt)
	}

	result.MainBranch = resolveRepositoryMainBranch(project, worktrees)
	for i := range worktrees {
		worktrees[i].Divergence = r.divergence(ctx, worktrees[i], result.MainBranch)
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
	count, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		delta.Commits = repositoryUnknownFact[int]("baseline_delta", fmt.Errorf("invalid commit count: %w", err))
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
	var result []repositoriesTopologyWorktree
	var current repositoriesTopologyWorktree
	flush := func() {
		if current.Path == "" {
			return
		}
		current.Path = filepath.Clean(current.Path)
		result = append(result, current)
		current = repositoriesTopologyWorktree{}
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current.Path = decodeRepositoryPath(strings.TrimPrefix(line, "worktree "))
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimSpace(strings.TrimPrefix(line, "HEAD "))
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(line, "branch ")), "refs/heads/")
		case line == "detached":
			current.Detached = true
		case line == "bare":
			current.Bare = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			current.Locked = true
			current.LockReason = decodeRepositoryPath(strings.TrimSpace(strings.TrimPrefix(line, "locked")))
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			current.Prunable = true
		}
	}
	flush()
	if len(result) == 0 {
		return nil, errors.New("git returned no Worktree topology")
	}
	return result, nil
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
	var result repositoriesStatus
	unborn := false
	seenOID := false
	seenHead := false
	seenPaths := map[string]bool{}
	addPath := func(path string) {
		path = strings.TrimSpace(path)
		if path == "" || seenPaths[path] {
			return
		}
		seenPaths[path] = true
		result.Changes.Paths = append(result.Changes.Paths, path)
	}
	for lineNumber, line := range strings.Split(out, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "# branch.oid "):
			oid := strings.TrimSpace(strings.TrimPrefix(line, "# branch.oid "))
			if seenOID || oid == "" {
				return repositoriesStatus{}, fmt.Errorf("invalid branch.oid at status line %d", lineNumber+1)
			}
			seenOID = true
			if oid == "(initial)" || allZeroOID(oid) {
				unborn = true
			} else {
				result.Head = oid
			}
		case strings.HasPrefix(line, "# branch.head "):
			head := strings.TrimSpace(strings.TrimPrefix(line, "# branch.head "))
			if seenHead || head == "" {
				return repositoriesStatus{}, fmt.Errorf("invalid branch.head at status line %d", lineNumber+1)
			}
			seenHead = true
			switch head {
			case "(detached)":
				result.Checkout = RepositoryCheckout{Kind: RepositoryDetached}
			case "(initial)":
				result.Checkout = RepositoryCheckout{Kind: RepositoryUnborn}
			default:
				result.Checkout = RepositoryCheckout{Kind: RepositoryBranchCheckout, Branch: head}
			}
		case strings.HasPrefix(line, "# branch.upstream "), strings.HasPrefix(line, "# branch.ab "), strings.HasPrefix(line, "# stash "):
			// Optional documented porcelain-v2 headers do not change the facts
			// returned by this Interface.
		case strings.HasPrefix(line, "1 "):
			fields := strings.SplitN(line, " ", 9)
			if len(fields) != 9 || !validRepositoryXY(fields[1]) || strings.TrimSpace(fields[8]) == "" {
				return repositoriesStatus{}, fmt.Errorf("invalid ordinary change at status line %d", lineNumber+1)
			}
			countRepositoryXY(&result.Changes, fields[1])
			addPath(decodeRepositoryPath(fields[8]))
		case strings.HasPrefix(line, "2 "):
			fields := strings.SplitN(line, " ", 10)
			if len(fields) != 10 || !validRepositoryXY(fields[1]) {
				return repositoriesStatus{}, fmt.Errorf("invalid renamed change at status line %d", lineNumber+1)
			}
			path, original, hasOriginal := strings.Cut(fields[9], "\t")
			if strings.TrimSpace(path) == "" || !hasOriginal || strings.TrimSpace(original) == "" {
				return repositoriesStatus{}, fmt.Errorf("invalid renamed paths at status line %d", lineNumber+1)
			}
			countRepositoryXY(&result.Changes, fields[1])
			addPath(decodeRepositoryPath(path))
		case strings.HasPrefix(line, "u "):
			fields := strings.SplitN(line, " ", 11)
			if len(fields) != 11 || !validRepositoryXY(fields[1]) || strings.TrimSpace(fields[10]) == "" {
				return repositoriesStatus{}, fmt.Errorf("invalid unmerged change at status line %d", lineNumber+1)
			}
			result.Changes.Conflicted++
			addPath(decodeRepositoryPath(fields[10]))
		case strings.HasPrefix(line, "? "):
			if strings.TrimSpace(strings.TrimPrefix(line, "? ")) == "" {
				return repositoriesStatus{}, fmt.Errorf("invalid untracked path at status line %d", lineNumber+1)
			}
			result.Changes.Untracked++
			addPath(decodeRepositoryPath(strings.TrimPrefix(line, "? ")))
		case strings.HasPrefix(line, "! "):
			if strings.TrimSpace(strings.TrimPrefix(line, "! ")) == "" {
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
		result.Checkout.Kind = RepositoryUnborn
	}
	sort.Strings(result.Changes.Paths)
	return result, nil
}

func validRepositoryXY(xy string) bool {
	if len(xy) != 2 {
		return false
	}
	const allowed = ".MADRCUT?"
	return strings.ContainsRune(allowed, rune(xy[0])) && strings.ContainsRune(allowed, rune(xy[1]))
}

func countRepositoryXY(changes *RepositoryWorkingChanges, xy string) {
	if xy[0] != '.' {
		changes.Staged++
	}
	if xy[1] != '.' {
		changes.Modified++
	}
}

func decodeRepositoryPath(path string) string {
	path = strings.TrimSpace(path)
	if len(path) >= 2 && path[0] == '"' && path[len(path)-1] == '"' {
		if decoded, err := strconv.Unquote(path); err == nil {
			return decoded
		}
	}
	return path
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
	out, err := r.runner.Run(ctx, wt.Path, "rev-list", "--left-right", "--count", main.Value+"...HEAD")
	if err != nil {
		return repositoryFactForError[RepositoryDivergence]("divergence", err)
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return repositoryUnknownFact[RepositoryDivergence]("divergence", fmt.Errorf("invalid divergence output %q", strings.TrimSpace(out)))
	}
	behind, errBehind := strconv.Atoi(fields[0])
	ahead, errAhead := strconv.Atoi(fields[1])
	if errBehind != nil || errAhead != nil {
		return repositoryUnknownFact[RepositoryDivergence]("divergence", fmt.Errorf("invalid divergence output %q", strings.TrimSpace(out)))
	}
	return repositoryKnownFact(RepositoryDivergence{Base: main.Value, Ahead: ahead, Behind: behind})
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
