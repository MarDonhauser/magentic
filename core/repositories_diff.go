package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// StructuredDiff reads one comparison mode over a freshly resolved Worktree
// and normalizes it into the one file/hunk/line shape the review surface
// renders. Working tree is `git diff HEAD` plus untracked files rendered as
// added files; branch-vs-base is `git diff <merge-base> HEAD`. Output that
// cannot be interpreted is unavailable knowledge, never a partial diff.
func (r *Repositories) StructuredDiff(ctx context.Context, target RepositoryWorktreeTarget, mode DiffComparisonMode) RepositoryFact[StructuredDiff] {
	if r == nil || r.runner == nil {
		return repositoryUnknownFact[StructuredDiff]("diff", errors.New("Repositories is unavailable"))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if mode == "" {
		mode = DiffComparisonWorkingTree
	}
	if mode != DiffComparisonWorkingTree && mode != DiffComparisonBranch {
		return repositoryUnknownFact[StructuredDiff]("diff", fmt.Errorf("unbekannter Vergleichsmodus %q", mode))
	}
	dir := strings.TrimSpace(target.Worktree.Path)
	if dir == "" {
		return repositoryUnknownFact[StructuredDiff]("diff", errors.New("resolved Worktree path is required"))
	}
	if mode == DiffComparisonBranch {
		return r.structuredBranchDiff(ctx, dir, target.MainBranch)
	}
	return r.structuredWorkingTreeDiff(ctx, dir)
}

func (r *Repositories) structuredWorkingTreeDiff(ctx context.Context, dir string) RepositoryFact[StructuredDiff] {
	out, err := r.runner.Run(ctx, dir, "diff", "--no-color", "--no-ext-diff", "--unified=3", "HEAD")
	if err != nil {
		return repositoryFactForError[StructuredDiff]("diff_working_tree", err)
	}
	diff, err := parseStructuredDiff(out, DiffComparisonWorkingTree, "")
	if err != nil {
		return repositoryUnknownFact[StructuredDiff]("diff_working_tree", err)
	}
	untrackedOut, err := r.runner.Run(ctx, dir, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return repositoryFactForError[StructuredDiff]("diff_untracked", err)
	}
	untracked, err := parseRepositoryUntrackedZeroTerminated(untrackedOut)
	if err != nil {
		return repositoryUnknownFact[StructuredDiff]("diff_untracked", err)
	}
	for _, path := range untracked {
		diff.Files = append(diff.Files, readUntrackedDiffFile(dir, path))
	}
	return repositoryKnownFact(applyStructuredDiffCaps(diff))
}

func (r *Repositories) structuredBranchDiff(ctx context.Context, dir string, main RepositoryFact[string]) RepositoryFact[StructuredDiff] {
	if !main.Known() || strings.TrimSpace(main.Value) == "" {
		problem := errors.New("main branch is unavailable")
		if main.Problem != nil && strings.TrimSpace(main.Problem.Message) != "" {
			problem = errors.New(main.Problem.Message)
		}
		return repositoryUnknownFact[StructuredDiff]("main_branch", problem)
	}
	base := strings.TrimSpace(main.Value)
	mergeOut, err := r.runner.Run(ctx, dir, "merge-base", "HEAD", base)
	if err != nil {
		return repositoryFactForError[StructuredDiff]("diff_merge_base", err)
	}
	mergeBase, err := parseRepositoryTerminatedLine(mergeOut, "merge base")
	if err != nil || !validRepositoryObjectID(mergeBase) {
		if err == nil {
			err = fmt.Errorf("invalid merge base %q", mergeBase)
		}
		return repositoryUnknownFact[StructuredDiff]("diff_merge_base", err)
	}
	out, err := r.runner.Run(ctx, dir, "diff", "--no-color", "--no-ext-diff", "--unified=3", mergeBase, "HEAD")
	if err != nil {
		return repositoryFactForError[StructuredDiff]("diff_branch", err)
	}
	diff, err := parseStructuredDiff(out, DiffComparisonBranch, base)
	if err != nil {
		return repositoryUnknownFact[StructuredDiff]("diff_branch", err)
	}
	return repositoryKnownFact(applyStructuredDiffCaps(diff))
}

func parseRepositoryUntrackedZeroTerminated(out string) ([]string, error) {
	if out == "" {
		return nil, nil
	}
	normalized := strings.ReplaceAll(out, "\r\n", "\n")
	if strings.Contains(normalized, "\r") {
		return nil, errors.New("git returned an uninterpretable untracked file list")
	}
	if !strings.HasSuffix(normalized, "\x00") {
		return nil, errors.New("git returned a truncated untracked file list")
	}
	var paths []string
	for _, raw := range strings.Split(strings.TrimSuffix(normalized, "\x00"), "\x00") {
		if raw == "" || strings.Contains(raw, "\n") {
			return nil, errors.New("git returned an uninterpretable untracked file list")
		}
		paths = append(paths, raw)
	}
	return paths, nil
}

// readUntrackedDiffFile renders one untracked file as an added file. Binaries
// and very large files are listed without content, like tracked binaries.
func readUntrackedDiffFile(dir, path string) StructuredDiffFile {
	file := StructuredDiffFile{Path: path, Added: true}
	full := filepath.Join(dir, filepath.FromSlash(path))
	info, err := os.Stat(full)
	if err != nil || info.IsDir() {
		return file
	}
	if info.Size() > structuredDiffMaxUntrackedBytes {
		file.Capped = true
		return file
	}
	content, err := os.ReadFile(full)
	if err != nil {
		return file
	}
	if len(content) == 0 {
		file.Hunks = []StructuredDiffHunk{{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: 0}}
		return file
	}
	if containsNUL(content) {
		file.Binary = true
		return file
	}
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	if strings.Contains(text, "\r") {
		file.Binary = true
		return file
	}
	lines := strings.Split(text, "\n")
	if last := len(lines) - 1; lines[last] == "" {
		lines = lines[:last]
	}
	hunk := StructuredDiffHunk{OldStart: 0, OldCount: 0, NewStart: 1, NewCount: len(lines)}
	for index, line := range lines {
		hunk.Lines = append(hunk.Lines, StructuredDiffLine{Kind: StructuredDiffLineAdded, NewLine: index + 1, Text: line})
	}
	file.Hunks = []StructuredDiffHunk{hunk}
	return file
}

func containsNUL(content []byte) bool {
	for _, b := range content {
		if b == 0 {
			return true
		}
	}
	return false
}

// applyStructuredDiffCaps marks files beyond the render caps as
// present-but-not-rendered instead of dropping them silently. A capped file
// carries no hunks and cannot be commented on.
func applyStructuredDiffCaps(diff StructuredDiff) StructuredDiff {
	if diff.Files == nil {
		diff.Files = []StructuredDiffFile{}
	}
	for index := range diff.Files {
		file := &diff.Files[index]
		if index >= StructuredDiffMaxFiles || structuredDiffFileLines(file) > StructuredDiffMaxLinesPerFile {
			*file = StructuredDiffFile{
				Path: file.Path, OldPath: file.OldPath,
				Added: file.Added, Deleted: file.Deleted, Renamed: file.Renamed,
				Binary: file.Binary, Capped: true,
			}
		}
	}
	return diff
}

func structuredDiffFileLines(file *StructuredDiffFile) int {
	count := 0
	for _, hunk := range file.Hunks {
		count += len(hunk.Lines)
	}
	return count
}

// parseStructuredDiff parses unified `git diff` output into files, hunks and
// lines. Anything it cannot interpret is an error, so the caller reports
// unavailable knowledge instead of a partial diff that looks complete.
func parseStructuredDiff(out string, mode DiffComparisonMode, base string) (StructuredDiff, error) {
	diff := StructuredDiff{Mode: mode, Base: base, Files: []StructuredDiffFile{}}
	normalized := strings.ReplaceAll(out, "\r\n", "\n")
	if strings.Contains(normalized, "\r") {
		return StructuredDiff{}, errors.New("git returned a diff with unexpected control characters")
	}
	if strings.TrimSpace(normalized) == "" {
		return diff, nil
	}
	if !strings.HasSuffix(normalized, "\n") {
		return StructuredDiff{}, errors.New("git returned a truncated unified diff")
	}
	lines := strings.Split(strings.TrimSuffix(normalized, "\n"), "\n")
	var current *StructuredDiffFile
	flush := func() {
		if current == nil {
			return
		}
		diff.Files = append(diff.Files, *current)
		current = nil
	}
	for index := 0; index < len(lines); {
		line := lines[index]
		if !strings.HasPrefix(line, "diff --git ") {
			return StructuredDiff{}, fmt.Errorf("unified diff line %d is outside any file entry", index+1)
		}
		flush()
		file, err := parseStructuredDiffHeader(line)
		if err != nil {
			return StructuredDiff{}, err
		}
		current = file
		index++
		index, err = parseStructuredDiffBody(lines, index, current)
		if err != nil {
			return StructuredDiff{}, err
		}
	}
	flush()
	return diff, nil
}

func parseStructuredDiffHeader(line string) (*StructuredDiffFile, error) {
	oldRaw, newRaw, err := splitDiffGitPaths(strings.TrimPrefix(line, "diff --git "))
	if err != nil {
		return nil, err
	}
	oldPath, err := decodeDiffPath(oldRaw, "a/")
	if err != nil {
		return nil, err
	}
	newPath, err := decodeDiffPath(newRaw, "b/")
	if err != nil {
		return nil, err
	}
	file := &StructuredDiffFile{Path: newPath}
	if oldPath != newPath {
		file.OldPath = oldPath
	}
	return file, nil
}

// parseStructuredDiffBody consumes the header continuation lines and hunks of
// one file entry starting at lines[index]. It returns the index of the first
// line belonging to the next entry.
func parseStructuredDiffBody(lines []string, index int, file *StructuredDiffFile) (int, error) {
	for ; index < len(lines); index++ {
		line := lines[index]
		switch {
		case strings.HasPrefix(line, "diff --git "):
			return index, nil
		case strings.HasPrefix(line, "@@ "):
			hunk, next, err := parseStructuredDiffHunk(lines, index)
			if err != nil {
				return 0, err
			}
			file.Hunks = append(file.Hunks, hunk)
			index = next - 1
		case strings.HasPrefix(line, "Binary files ") && strings.HasSuffix(line, " differ"):
			file.Binary = true
		case strings.HasPrefix(line, "old mode ") || strings.HasPrefix(line, "new mode ") ||
			strings.HasPrefix(line, "new file mode ") || strings.HasPrefix(line, "deleted file mode "):
			fields := strings.Fields(line)
			mode := ""
			if len(fields) > 0 {
				mode = fields[len(fields)-1]
			}
			if !validRepositoryMode(mode) {
				return 0, fmt.Errorf("invalid file mode at unified diff line %d", index+1)
			}
			if strings.HasPrefix(line, "new file mode ") {
				file.Added = true
			}
			if strings.HasPrefix(line, "deleted file mode ") {
				file.Deleted = true
			}
		case strings.HasPrefix(line, "similarity index ") || strings.HasPrefix(line, "dissimilarity index "):
			value := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "similarity index "), "dissimilarity index "))
			if !strings.HasSuffix(value, "%") {
				return 0, fmt.Errorf("invalid similarity index at unified diff line %d", index+1)
			}
		case strings.HasPrefix(line, "rename from "):
			path, err := decodeDiffPath(strings.TrimPrefix(line, "rename from "), "")
			if err != nil {
				return 0, fmt.Errorf("invalid rename source at unified diff line %d", index+1)
			}
			file.OldPath = path
			file.Renamed = true
		case strings.HasPrefix(line, "rename to "):
			path, err := decodeDiffPath(strings.TrimPrefix(line, "rename to "), "")
			if err != nil {
				return 0, fmt.Errorf("invalid rename target at unified diff line %d", index+1)
			}
			file.Path = path
			file.Renamed = true
		case strings.HasPrefix(line, "index "):
			if !strings.Contains(strings.TrimPrefix(line, "index "), "..") {
				return 0, fmt.Errorf("invalid index line at unified diff line %d", index+1)
			}
		case strings.HasPrefix(line, "--- "):
			path, err := decodeDiffPath(strings.TrimPrefix(line, "--- "), "a/")
			if err != nil {
				return 0, fmt.Errorf("invalid source path at unified diff line %d", index+1)
			}
			if path == "/dev/null" {
				file.Added = true
			} else {
				file.OldPath = path
			}
		case strings.HasPrefix(line, "+++ "):
			path, err := decodeDiffPath(strings.TrimPrefix(line, "+++ "), "b/")
			if err != nil {
				return 0, fmt.Errorf("invalid target path at unified diff line %d", index+1)
			}
			if path == "/dev/null" {
				file.Deleted = true
			} else {
				file.Path = path
			}
		default:
			return 0, fmt.Errorf("uninterpretable unified diff line %d", index+1)
		}
	}
	return index, nil
}

func parseStructuredDiffHunk(lines []string, index int) (StructuredDiffHunk, int, error) {
	var hunk StructuredDiffHunk
	oldStart, oldCount, newStart, newCount, err := parseHunkHeader(lines[index])
	if err != nil {
		return hunk, 0, fmt.Errorf("invalid hunk header at unified diff line %d: %w", index+1, err)
	}
	hunk = StructuredDiffHunk{OldStart: oldStart, OldCount: oldCount, NewStart: newStart, NewCount: newCount}
	oldLine, newLine := oldStart, newStart
	index++
	seenContent := false
	for ; index < len(lines); index++ {
		line := lines[index]
		if strings.HasPrefix(line, "@@ ") || strings.HasPrefix(line, "diff --git ") {
			break
		}
		if line == "" {
			return hunk, 0, fmt.Errorf("uninterpretable unified diff line %d", index+1)
		}
		switch line[0] {
		case ' ':
			hunk.Lines = append(hunk.Lines, StructuredDiffLine{Kind: StructuredDiffLineContext, OldLine: oldLine, NewLine: newLine, Text: line[1:]})
			oldLine++
			newLine++
			seenContent = true
		case '+':
			hunk.Lines = append(hunk.Lines, StructuredDiffLine{Kind: StructuredDiffLineAdded, NewLine: newLine, Text: line[1:]})
			newLine++
			seenContent = true
		case '-':
			hunk.Lines = append(hunk.Lines, StructuredDiffLine{Kind: StructuredDiffLineRemoved, OldLine: oldLine, Text: line[1:]})
			oldLine++
			seenContent = true
		case '\\':
			if line != "\\ No newline at end of file" {
				return hunk, 0, fmt.Errorf("uninterpretable unified diff line %d", index+1)
			}
			if !seenContent {
				return hunk, 0, fmt.Errorf("misplaced no-newline marker at unified diff line %d", index+1)
			}
		default:
			return hunk, 0, fmt.Errorf("uninterpretable unified diff line %d", index+1)
		}
	}
	if oldLine-oldStart != oldCount || newLine-newStart != newCount {
		return hunk, 0, fmt.Errorf("hunk at unified diff line %d covers %d old and %d new lines, header promises %d and %d",
			index+1, oldLine-oldStart, newLine-newStart, oldCount, newCount)
	}
	return hunk, index, nil
}

func parseHunkHeader(line string) (oldStart, oldCount, newStart, newCount int, err error) {
	rest := strings.TrimPrefix(line, "@@ ")
	middle, _, _ := strings.Cut(rest, " @@")
	fields := strings.Split(middle, " ")
	if len(fields) != 2 || !strings.HasPrefix(fields[0], "-") || !strings.HasPrefix(fields[1], "+") {
		return 0, 0, 0, 0, errors.New("malformed hunk range")
	}
	if oldStart, oldCount, err = parseHunkRange(fields[0][1:]); err != nil {
		return 0, 0, 0, 0, err
	}
	if newStart, newCount, err = parseHunkRange(fields[1][1:]); err != nil {
		return 0, 0, 0, 0, err
	}
	return oldStart, oldCount, newStart, newCount, nil
}

func parseHunkRange(raw string) (start, count int, err error) {
	startRaw, countRaw, hasCount := strings.Cut(raw, ",")
	if !repositoryDecimal(startRaw) {
		return 0, 0, fmt.Errorf("malformed hunk range %q", raw)
	}
	start, err = strconv.Atoi(startRaw)
	if err != nil {
		return 0, 0, fmt.Errorf("malformed hunk range %q", raw)
	}
	if !hasCount {
		return start, 1, nil
	}
	if !repositoryDecimal(countRaw) {
		return 0, 0, fmt.Errorf("malformed hunk range %q", raw)
	}
	count, err = strconv.Atoi(countRaw)
	if err != nil {
		return 0, 0, fmt.Errorf("malformed hunk range %q", raw)
	}
	return start, count, nil
}

// splitDiffGitPaths splits the two paths of a `diff --git` line, honoring
// the C-style quoting Git uses for paths with spaces or special characters.
func splitDiffGitPaths(rest string) (string, string, error) {
	first, remaining, err := splitDiffPathToken(rest)
	if err != nil {
		return "", "", err
	}
	second, leftover, err := splitDiffPathToken(strings.TrimPrefix(remaining, " "))
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(first) == "" || strings.TrimSpace(second) == "" || strings.TrimSpace(leftover) != "" {
		return "", "", errors.New("malformed diff file header")
	}
	return first, second, nil
}

func splitDiffPathToken(raw string) (token, rest string, err error) {
	if raw == "" {
		return "", "", errors.New("malformed diff file header")
	}
	if raw[0] != '"' {
		if index := strings.Index(raw, " "); index < 0 {
			return raw, "", nil
		} else {
			return raw[:index], raw[index:], nil
		}
	}
	for index := 1; index < len(raw); index++ {
		if raw[index] == '\\' {
			index++
			continue
		}
		if raw[index] == '"' {
			return raw[:index+1], raw[index+1:], nil
		}
	}
	return "", "", errors.New("unterminated quoted diff path")
}

// decodeDiffPath unquotes a diff path and strips the a/ or b/ source prefix
// Git emits by default. /dev/null passes through for added/deleted detection.
func decodeDiffPath(raw, prefix string) (string, error) {
	path := raw
	if strings.HasPrefix(path, "\"") {
		decoded, err := strconv.Unquote(path)
		if err != nil || decoded == "" {
			return "", errors.New("malformed quoted diff path")
		}
		path = decoded
	}
	if path == "/dev/null" {
		return path, nil
	}
	if prefix != "" && strings.HasPrefix(path, prefix) {
		path = strings.TrimPrefix(path, prefix)
	}
	if path == "" || strings.ContainsAny(path, "\x00\r\n") {
		return "", errors.New("malformed diff path")
	}
	return path, nil
}
