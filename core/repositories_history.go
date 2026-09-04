package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const repositoryCommitHistoryFormat = "%H\x1f%h\x1f%P\x1f%s\x1f%an\x1f%ct\x1f%D\x1e"

// RepositoryCommit is the validated, presentation-neutral history fact used
// by repository consumers such as GitGraph. Decorations remain raw Git facts;
// the consumer owns their visual classification.
type RepositoryCommit struct {
	Hash        string
	Short       string
	Parents     []string
	Subject     string
	Author      string
	Timestamp   int64
	Decorations string
}

// commitHistory returns either a wholly validated bounded history or unknown
// knowledge. A successful command with malformed output is not an empty log.
func (r *Repositories) commitHistory(ctx context.Context, dir string, limit int) RepositoryFact[[]RepositoryCommit] {
	if r == nil || r.runner == nil {
		return repositoryUnknownFact[[]RepositoryCommit]("commit_history", errors.New("Repositories is unavailable"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(dir) == "" {
		return repositoryUnknownFact[[]RepositoryCommit]("commit_history", errors.New("repository directory is required"))
	}
	if limit < 1 {
		limit = 1
	}
	out, err := r.runner.Run(ctx, dir, "log", "--all", "--date-order", "--max-count="+strconv.Itoa(limit), "--format="+repositoryCommitHistoryFormat)
	if err != nil {
		return repositoryFactForError[[]RepositoryCommit]("commit_history", err)
	}
	commits, err := parseRepositoryCommitHistory(out)
	if err != nil {
		return repositoryUnknownFact[[]RepositoryCommit]("commit_history", err)
	}
	return repositoryKnownFact(commits)
}

func parseRepositoryCommitHistory(out string) ([]RepositoryCommit, error) {
	if out == "" {
		return []RepositoryCommit{}, nil
	}
	normalized := strings.ReplaceAll(out, "\r\n", "\n")
	if strings.Contains(normalized, "\r") {
		return nil, errors.New("malformed commit history: unexpected carriage return")
	}
	if strings.HasSuffix(normalized, "\n") {
		normalized = strings.TrimSuffix(normalized, "\n")
	}
	if normalized == "" || !strings.HasSuffix(normalized, "\x1e") {
		return nil, errors.New("malformed commit history: missing final record terminator")
	}

	var commits []RepositoryCommit
	records := strings.Split(normalized, "\x1e")
	for recordIndex, raw := range records {
		if recordIndex == len(records)-1 {
			if raw != "" {
				return nil, errors.New("malformed commit history terminator")
			}
			continue
		}
		recordNumber := recordIndex + 1
		record := raw
		if recordIndex > 0 {
			if !strings.HasPrefix(record, "\n") {
				return nil, fmt.Errorf("malformed commit record %d boundary", recordNumber)
			}
			record = strings.TrimPrefix(record, "\n")
		}
		if record == "" || strings.Contains(record, "\n") {
			return nil, fmt.Errorf("malformed commit record %d boundary", recordNumber)
		}
		fields := strings.Split(record, "\x1f")
		if len(fields) != 7 {
			return nil, fmt.Errorf("malformed commit record %d: got %d fields, want 7", recordNumber, len(fields))
		}
		if !validRepositoryObjectID(fields[0]) {
			return nil, fmt.Errorf("malformed commit record %d: invalid object ID", recordNumber)
		}
		if !validRepositoryAbbreviatedObjectID(fields[1], fields[0]) {
			return nil, fmt.Errorf("malformed commit record %d: invalid abbreviated object ID", recordNumber)
		}
		parents, err := parseRepositoryParentIDs(fields[2], len(fields[0]))
		if err != nil {
			return nil, fmt.Errorf("malformed commit record %d parents: %w", recordNumber, err)
		}
		timestamp, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil || timestamp < 0 || !repositoryDecimal(fields[5]) {
			if err == nil {
				err = errors.New("timestamp must be nonnegative decimal seconds")
			}
			return nil, fmt.Errorf("malformed commit record %d timestamp: %w", recordNumber, err)
		}
		commits = append(commits, RepositoryCommit{
			Hash: fields[0], Short: fields[1], Parents: parents,
			Subject: fields[3], Author: fields[4], Timestamp: timestamp, Decorations: fields[6],
		})
	}
	return commits, nil
}

func validRepositoryAbbreviatedObjectID(short, full string) bool {
	if len(short) < 4 || len(short) > len(full) || !strings.HasPrefix(full, short) {
		return false
	}
	for _, char := range short {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

func parseRepositoryParentIDs(raw string, objectIDLength int) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	if strings.TrimSpace(raw) != raw || strings.Contains(raw, "  ") || strings.ContainsAny(raw, "\t\n") {
		return nil, errors.New("invalid parent separator")
	}
	parents := strings.Split(raw, " ")
	for _, parent := range parents {
		if len(parent) != objectIDLength || !validRepositoryObjectID(parent) {
			return nil, fmt.Errorf("invalid parent object ID %q", parent)
		}
	}
	return parents, nil
}

func repositoryDecimal(value string) bool {
	if value == "" {
		return false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

// mergedBranches reports the complete set of local branches Git proves are
// merged into base. Command failure remains unknown instead of "none merged".
func (r *Repositories) mergedBranches(ctx context.Context, dir, base string) RepositoryFact[map[string]bool] {
	if r == nil || r.runner == nil {
		return repositoryUnknownFact[map[string]bool]("merged_branches", errors.New("Repositories is unavailable"))
	}
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(base) == "" {
		return repositoryUnknownFact[map[string]bool]("merged_branches", errors.New("repository directory and base ref are required"))
	}
	out, err := r.runner.Run(ctx, dir, "branch", "--merged", base, "--format=%(refname:short)")
	if err != nil {
		return repositoryFactForError[map[string]bool]("merged_branches", err)
	}
	merged, err := parseRepositoryMergedBranches(out)
	if err != nil {
		return repositoryUnknownFact[map[string]bool]("merged_branches", err)
	}
	return repositoryKnownFact(merged)
}

func parseRepositoryMergedBranches(out string) (map[string]bool, error) {
	merged := map[string]bool{}
	if out == "" {
		return merged, nil
	}
	normalized := strings.ReplaceAll(out, "\r\n", "\n")
	if strings.Contains(normalized, "\r") || !strings.HasSuffix(normalized, "\n") {
		return nil, errors.New("malformed merged branch list: missing line terminator")
	}
	body := strings.TrimSuffix(normalized, "\n")
	if body == "" {
		return nil, errors.New("malformed merged branch list: unexpected empty line")
	}
	for lineIndex, branch := range strings.Split(body, "\n") {
		if branch == "" || strings.TrimSpace(branch) != branch || !validRepositoryBranchRef("refs/heads/"+branch) {
			return nil, fmt.Errorf("malformed merged branch at line %d", lineIndex+1)
		}
		if merged[branch] {
			return nil, fmt.Errorf("duplicate merged branch at line %d", lineIndex+1)
		}
		merged[branch] = true
	}
	return merged, nil
}

// compareRefsFact reports ahead/behind counts with validated numeric output.
func (r *Repositories) compareRefsFact(ctx context.Context, dir, base, ref string) RepositoryFact[RepositoryDivergence] {
	if r == nil || r.runner == nil {
		return repositoryUnknownFact[RepositoryDivergence]("compare_refs", errors.New("Repositories is unavailable"))
	}
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(base) == "" || strings.TrimSpace(ref) == "" {
		return repositoryUnknownFact[RepositoryDivergence]("compare_refs", errors.New("repository directory, base, and ref are required"))
	}
	return r.compareRefs(ctx, dir, base, ref, "compare_refs")
}

func (r *Repositories) compareRefs(ctx context.Context, dir, base, ref, operation string) RepositoryFact[RepositoryDivergence] {
	out, err := r.runner.Run(ctx, dir, "rev-list", "--left-right", "--count", base+"..."+ref)
	if err != nil {
		return repositoryFactForError[RepositoryDivergence](operation, err)
	}
	behind, ahead, err := parseRepositoryDivergence(out)
	if err != nil {
		return repositoryUnknownFact[RepositoryDivergence](operation, err)
	}
	return repositoryKnownFact(RepositoryDivergence{Base: base, Ahead: ahead, Behind: behind})
}

func parseRepositoryDivergence(out string) (behind, ahead int, err error) {
	line, err := parseRepositoryTerminatedLine(out, "divergence")
	if err != nil {
		return 0, 0, err
	}
	fields := strings.Split(line, "\t")
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("malformed divergence %q", line)
	}
	behind, err = parseRepositoryNonnegativeDecimal(fields[0], "behind count")
	if err != nil {
		return 0, 0, err
	}
	ahead, err = parseRepositoryNonnegativeDecimal(fields[1], "ahead count")
	if err != nil {
		return 0, 0, err
	}
	return behind, ahead, nil
}

// Decoration kinds are raw Git facts; a consumer maps them to its own visual
// classification but never re-parses the decoration itself.
const (
	repositoryDecorationHead   = "head"
	repositoryDecorationTag    = "tag"
	repositoryDecorationRemote = "remote"
	repositoryDecorationBranch = "branch"
)

type repositoryDecorationRef struct {
	Name    string
	Kind    string
	Current bool
}

// parseRepositoryDecorations splits the raw decoration of one commit into its
// individual refs. It is the only reading of that field in the codebase.
func parseRepositoryDecorations(raw string) []repositoryDecorationRef {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var refs []repositoryDecorationRef
	for _, part := range strings.Split(raw, ", ") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		current := false
		if index := strings.Index(part, " -> "); index >= 0 {
			current = strings.HasPrefix(part, "HEAD")
			part = part[index+4:]
		}
		switch {
		case part == "HEAD":
			refs = append(refs, repositoryDecorationRef{Name: "HEAD", Kind: repositoryDecorationHead, Current: true})
		case strings.HasPrefix(part, "tag: "):
			refs = append(refs, repositoryDecorationRef{Name: strings.TrimPrefix(part, "tag: "), Kind: repositoryDecorationTag})
		case strings.HasPrefix(part, "origin/"):
			refs = append(refs, repositoryDecorationRef{Name: part, Kind: repositoryDecorationRemote})
		default:
			refs = append(refs, repositoryDecorationRef{Name: part, Kind: repositoryDecorationBranch, Current: current})
		}
	}
	return refs
}

// RepositoryGraphFacts is the one history fact a graph needs: the bounded
// history, whether it was cut short, the branches Git proves merged into the
// main branch, and the divergence of every branch visible in that history.
type RepositoryGraphFacts struct {
	Main       string                                          `json:"main"`
	Commits    RepositoryFact[[]RepositoryCommit]              `json:"commits"`
	Truncated  bool                                            `json:"truncated"`
	Merged     RepositoryFact[map[string]bool]                 `json:"merged"`
	Divergence map[string]RepositoryFact[RepositoryDivergence] `json:"divergence,omitempty"`
	Problems   []RepositoryProblem                             `json:"problems,omitempty"`
}

// GraphFacts reads the three graph facts of one already observed Project in a
// single pass. It takes the observation rather than the Project so it shares
// the caller's Survey instead of observing the repository a second time.
func (r *Repositories) GraphFacts(ctx context.Context, repository RepositoryProjectSurvey, limit int) RepositoryGraphFacts {
	facts := RepositoryGraphFacts{
		Merged:     RepositoryFact[map[string]bool]{State: RepositoryUnknown},
		Divergence: map[string]RepositoryFact[RepositoryDivergence]{},
	}
	if r == nil || r.runner == nil {
		facts.Commits = repositoryUnknownFact[[]RepositoryCommit]("graph_facts", errors.New("Repositories is unavailable"))
		return facts
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if limit < 1 {
		limit = 1
	}
	if repository.MainBranch.Known() {
		facts.Main = repository.MainBranch.Value
	}

	// Ein Commit mehr als verlangt lesen: nur so ist bewiesen, dass der
	// Verlauf abgeschnitten ist, statt es aus der Trefferzahl zu raten.
	facts.Commits = r.commitHistory(ctx, repository.Path, limit+1)
	if !facts.Commits.Known() {
		return facts
	}
	if len(facts.Commits.Value) > limit {
		facts.Commits.Value = facts.Commits.Value[:limit]
		facts.Truncated = true
	}
	if facts.Main == "" {
		return facts
	}

	facts.Merged = r.mergedBranches(ctx, repository.Path, facts.Main)
	if facts.Merged.Problem != nil {
		facts.Problems = append(facts.Problems, *facts.Merged.Problem)
	}

	assignments := repositoryBranchAssignments(repository, nil)
	var branches []string
	for _, commit := range facts.Commits.Value {
		for _, ref := range parseRepositoryDecorations(commit.Decorations) {
			if ref.Kind != repositoryDecorationBranch || ref.Name == facts.Main {
				continue
			}
			if _, seen := facts.Divergence[ref.Name]; seen {
				continue
			}
			facts.Divergence[ref.Name] = RepositoryFact[RepositoryDivergence]{}
			branches = append(branches, ref.Name)
		}
	}
	for _, branch := range branches {
		if worktree, checkedOut := assignments.WorktreeForBranch(branch); checkedOut {
			facts.Divergence[branch] = worktree.Divergence
			continue
		}
		facts.Divergence[branch] = r.compareRefsFact(ctx, repository.Path, facts.Main, branch)
	}
	sort.Strings(branches)
	for _, branch := range branches {
		if divergence := facts.Divergence[branch]; !divergence.Known() && divergence.Problem != nil {
			facts.Problems = append(facts.Problems, *divergence.Problem)
		}
	}
	return facts
}
