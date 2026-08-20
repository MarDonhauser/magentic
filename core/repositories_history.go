package core

import (
	"context"
	"errors"
	"fmt"
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

// CommitHistory returns either a wholly validated bounded history or unknown
// knowledge. A successful command with malformed output is not an empty log.
func (r *Repositories) CommitHistory(ctx context.Context, dir string, limit int) RepositoryFact[[]RepositoryCommit] {
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
	var commits []RepositoryCommit
	recordNumber := 0
	for _, raw := range strings.Split(out, "\x1e") {
		record := strings.Trim(raw, "\r\n")
		if record == "" {
			continue
		}
		recordNumber++
		fields := strings.Split(record, "\x1f")
		if len(fields) != 7 {
			return nil, fmt.Errorf("malformed commit record %d: got %d fields, want 7", recordNumber, len(fields))
		}
		if strings.TrimSpace(fields[0]) == "" || strings.TrimSpace(fields[1]) == "" {
			return nil, fmt.Errorf("malformed commit record %d: hash is missing", recordNumber)
		}
		timestamp, err := strconv.ParseInt(strings.TrimSpace(fields[5]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("malformed commit record %d timestamp: %w", recordNumber, err)
		}
		commits = append(commits, RepositoryCommit{
			Hash: fields[0], Short: fields[1], Parents: strings.Fields(fields[2]),
			Subject: fields[3], Author: fields[4], Timestamp: timestamp, Decorations: fields[6],
		})
	}
	return commits, nil
}

// MergedBranches reports the complete set of local branches Git proves are
// merged into base. Command failure remains unknown instead of "none merged".
func (r *Repositories) MergedBranches(ctx context.Context, dir, base string) RepositoryFact[map[string]bool] {
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
	merged := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if branch := strings.TrimSpace(line); branch != "" {
			merged[branch] = true
		}
	}
	return repositoryKnownFact(merged)
}

// CompareRefs reports ahead/behind counts with validated numeric output.
func (r *Repositories) CompareRefs(ctx context.Context, dir, base, ref string) RepositoryFact[RepositoryDivergence] {
	if r == nil || r.runner == nil {
		return repositoryUnknownFact[RepositoryDivergence]("compare_refs", errors.New("Repositories is unavailable"))
	}
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(base) == "" || strings.TrimSpace(ref) == "" {
		return repositoryUnknownFact[RepositoryDivergence]("compare_refs", errors.New("repository directory, base, and ref are required"))
	}
	out, err := r.runner.Run(ctx, dir, "rev-list", "--left-right", "--count", base+"..."+ref)
	if err != nil {
		return repositoryFactForError[RepositoryDivergence]("compare_refs", err)
	}
	fields := strings.Fields(out)
	if len(fields) != 2 {
		return repositoryUnknownFact[RepositoryDivergence]("compare_refs", fmt.Errorf("malformed divergence: got %d fields, want 2", len(fields)))
	}
	behind, behindErr := strconv.Atoi(fields[0])
	ahead, aheadErr := strconv.Atoi(fields[1])
	if behindErr != nil || aheadErr != nil || behind < 0 || ahead < 0 {
		return repositoryUnknownFact[RepositoryDivergence]("compare_refs", fmt.Errorf("malformed divergence counts %q", strings.TrimSpace(out)))
	}
	return repositoryKnownFact(RepositoryDivergence{Base: base, Ahead: ahead, Behind: behind})
}
