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
	repositoryOwnCommitFormat           = "%ct%x1f%ae%x1f%an%x1e"
	repositoryProblemOwnCommitIdentity  = "own_commit_identity"
	repositoryProblemOwnCommitLog       = "own_commit_log"
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
		email, emailErr = parseRepositoryIdentityValue(emailOut, "user.email")
	}
	nameOut, nameErr := r.runner.Run(ctx, dir, "config", "user.name")
	if nameErr == nil {
		name, nameErr = parseRepositoryIdentityValue(nameOut, "user.name")
	}
	if emailErr != nil && emailOut != "" {
		return "", "", fmt.Errorf("Git identity user.email is malformed: %w", emailErr)
	}
	if nameErr != nil && nameOut != "" {
		return "", "", fmt.Errorf("Git identity user.name is malformed: %w", nameErr)
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

func parseRepositoryIdentityValue(out, field string) (string, error) {
	value, err := parseRepositoryTerminatedLine(out, field)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("%s has surrounding whitespace", field)
	}
	for _, char := range value {
		if char == '\x00' || char < ' ' || char == '\x7f' {
			return "", fmt.Errorf("%s contains control characters", field)
		}
	}
	return strings.ToLower(value), nil
}

func parseRepositoryOwnCommitSeries(out, email, name string) (RepositoryOwnCommitSeries, int) {
	series := RepositoryOwnCommitSeries{}
	if out == "" {
		return series, 0
	}

	normalized := strings.ReplaceAll(out, "\r\n", "\n")
	finalTerminated := strings.HasSuffix(normalized, "\x1e") || strings.HasSuffix(normalized, "\x1e\n")
	malformed := 0
	if !finalTerminated {
		malformed++
		// Even malformed/truncated pretty-format output retains Git's one
		// structural line ending. Keep the otherwise-valid record as a known
		// subtotal, but never trim more than that single framing byte.
		normalized = strings.TrimSuffix(normalized, "\n")
	}
	records := strings.Split(normalized, "\x1e")
	for recordIndex, raw := range records {
		// Git separates pretty-format records with a line feed after the record
		// terminator. Remove only that framing byte: trimming either edge would
		// conceal a corrupt timestamp or author field.
		if recordIndex > 0 && strings.HasPrefix(raw, "\n") {
			raw = strings.TrimPrefix(raw, "\n")
		}
		if recordIndex == len(records)-1 && finalTerminated {
			if raw != "" {
				malformed++
			}
			continue
		}
		if raw == "" {
			malformed++
			continue
		}
		fields := strings.Split(raw, "\x1f")
		if len(fields) != 3 {
			malformed++
			continue
		}
		seconds, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil || !repositoryDecimal(fields[0]) || strconv.FormatInt(seconds, 10) != fields[0] {
			malformed++
			continue
		}
		if !validRepositoryOwnCommitIdentityField(fields[1]) || !validRepositoryOwnCommitIdentityField(fields[2]) {
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

func validRepositoryOwnCommitIdentityField(value string) bool {
	for _, char := range value {
		if char < ' ' || char == '\x7f' {
			return false
		}
	}
	return true
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
