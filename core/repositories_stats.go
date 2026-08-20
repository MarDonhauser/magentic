package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	repositoryOwnCommitFormat          = "%ct%x1f%ae%x1f%an%x1e"
	repositoryProblemOwnCommitIdentity = "own_commit_identity"
	repositoryProblemOwnCommitLog      = "own_commit_log"
	repositoryProblemOwnCommitMalformed = "own_commit_log_malformed"
)

// RepositoryOwnCommitSeries is the validated timestamp series of commits
// authored by the repository's effective Git identity. Statistics owns date
// bucketing; Repositories owns identity and Git-log semantics.
type RepositoryOwnCommitSeries struct {
	Timestamps []int64
}

// OwnCommitsSince returns an exact series, a partial known subtotal when only
// some successful log records are malformed, or unavailable knowledge when
// identity/log commands fail. It never treats missing identity as all authors.
func (r *Repositories) OwnCommitsSince(ctx context.Context, dir, since string) RepositoryFact[RepositoryOwnCommitSeries] {
	if r == nil || r.runner == nil {
		return repositoryUnknownFact[RepositoryOwnCommitSeries](
			repositoryProblemOwnCommitLog, errors.New("Repositories is unavailable"),
		)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	dir = strings.TrimSpace(dir)
	since = strings.TrimSpace(since)
	if dir == "" || since == "" {
		return repositoryUnknownFact[RepositoryOwnCommitSeries](
			repositoryProblemOwnCommitLog, errors.New("repository directory and since boundary are required"),
		)
	}

	email, name, err := r.effectiveIdentity(ctx, dir)
	if err != nil {
		return repositoryFactForOwnCommitError(repositoryProblemOwnCommitIdentity, err)
	}
	out, err := r.runner.Run(ctx, dir, "log", "--all", "--since="+since, "--format="+repositoryOwnCommitFormat)
	if err != nil {
		return repositoryFactForOwnCommitError(repositoryProblemOwnCommitLog, err)
	}
	series, malformed := parseRepositoryOwnCommitSeries(out, email, name)
	if malformed > 0 {
		return repositoryPartialFact(
			series, repositoryProblemOwnCommitMalformed,
			fmt.Errorf("Git history contains %d malformed commit records", malformed),
		)
	}
	return repositoryKnownFact(series)
}

func repositoryFactForOwnCommitError(operation string, err error) RepositoryFact[RepositoryOwnCommitSeries] {
	if errors.Is(err, errRepositoriesNotRepository) {
		return repositoryNotRepositoryFact[RepositoryOwnCommitSeries](operation, err)
	}
	return repositoryUnknownFact[RepositoryOwnCommitSeries](operation, err)
}

func (r *Repositories) effectiveIdentity(ctx context.Context, dir string) (email, name string, err error) {
	emailOut, emailErr := r.runner.Run(ctx, dir, "config", "user.email")
	if emailErr == nil {
		email = strings.ToLower(strings.TrimSpace(emailOut))
	}
	nameOut, nameErr := r.runner.Run(ctx, dir, "config", "user.name")
	if nameErr == nil {
		name = strings.ToLower(strings.TrimSpace(nameOut))
	}
	if email != "" || name != "" {
		return email, name, nil
	}

	switch {
	case emailErr != nil && nameErr != nil:
		return "", "", fmt.Errorf("Git identity unavailable (user.email: %v; user.name: %v)", emailErr, nameErr)
	case emailErr != nil:
		return "", "", fmt.Errorf("Git identity is not configured (user.email unavailable: %v)", emailErr)
	case nameErr != nil:
		return "", "", fmt.Errorf("Git identity is not configured (user.name unavailable: %v)", nameErr)
	default:
		return "", "", errors.New("Git identity is not configured")
	}
}

func parseRepositoryOwnCommitSeries(out, email, name string) (RepositoryOwnCommitSeries, int) {
	series := RepositoryOwnCommitSeries{}
	malformed := 0
	for _, raw := range strings.Split(out, "\x1e") {
		record := strings.Trim(raw, "\r\n")
		if record == "" {
			continue
		}
		fields := strings.Split(record, "\x1f")
		if len(fields) != 3 {
			malformed++
			continue
		}
		seconds, err := strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64)
		if err != nil || seconds < 0 {
			malformed++
			continue
		}
		if repositoryCommitMatchesIdentity(fields[1], fields[2], email, name) {
			series.Timestamps = append(series.Timestamps, seconds)
		}
	}
	sort.Slice(series.Timestamps, func(i, j int) bool { return series.Timestamps[i] < series.Timestamps[j] })
	return series, malformed
}

func repositoryCommitMatchesIdentity(commitEmail, commitName, email, name string) bool {
	if email == "" && name == "" {
		return false
	}
	if email != "" && strings.EqualFold(strings.TrimSpace(commitEmail), email) {
		return true
	}
	return name != "" && strings.EqualFold(strings.TrimSpace(commitName), name)
}
