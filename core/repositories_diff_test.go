package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var errForStructuredDiffTest = errors.New("main branch is unavailable")

const structuredDiffMultiFixture = `diff --git a/app/foo.go b/app/foo.go
index 1111111..2222222 100644
--- a/app/foo.go
+++ b/app/foo.go
@@ -1,4 +1,5 @@
 package app
+// neu
 func Foo() {
-	return 1
+	return 2
 }
diff --git a/alt.go b/neu.go
similarity index 90%
rename from alt.go
rename to neu.go
index 1111111..2222222 100644
--- a/alt.go
+++ b/neu.go
@@ -1,2 +1,2 @@
-x
+y
 z
diff --git a/run.sh b/run.sh
old mode 100644
new mode 100755
diff --git a/bild.png b/bild.png
index 1111111..2222222 100644
Binary files a/bild.png and b/bild.png differ
diff --git a/neu.txt b/neu.txt
new file mode 100644
index 0000000..1111111
--- /dev/null
+++ b/neu.txt
@@ -0,0 +1,2 @@
+eins
+zwei
`

func TestParseStructuredDiffCoversEntryKinds(t *testing.T) {
	diff, err := parseStructuredDiff(structuredDiffMultiFixture, DiffComparisonWorkingTree, "")
	if err != nil {
		t.Fatalf("parseStructuredDiff: %v", err)
	}
	if len(diff.Files) != 5 {
		t.Fatalf("files = %d, want 5", len(diff.Files))
	}

	changed := diff.Files[0]
	if changed.Path != "app/foo.go" || changed.Added || changed.Deleted || changed.Renamed || changed.Binary {
		t.Fatalf("changed file flags = %+v", changed)
	}
	if len(changed.Hunks) != 1 {
		t.Fatalf("changed hunks = %d, want 1", len(changed.Hunks))
	}
	hunk := changed.Hunks[0]
	if hunk.OldStart != 1 || hunk.OldCount != 4 || hunk.NewStart != 1 || hunk.NewCount != 5 {
		t.Fatalf("hunk header = %+v", hunk)
	}
	var kinds []StructuredDiffLineKind
	for _, line := range hunk.Lines {
		kinds = append(kinds, line.Kind)
	}
	wantKinds := []StructuredDiffLineKind{
		StructuredDiffLineContext, StructuredDiffLineAdded, StructuredDiffLineContext,
		StructuredDiffLineRemoved, StructuredDiffLineAdded, StructuredDiffLineContext,
	}
	if strings.Join(structuredDiffKindNames(kinds), ",") != strings.Join(structuredDiffKindNames(wantKinds), ",") {
		t.Fatalf("line kinds = %v, want %v", kinds, wantKinds)
	}
	if hunk.Lines[1].NewLine != 2 || hunk.Lines[1].OldLine != 0 {
		t.Fatalf("added line numbers = %+v", hunk.Lines[1])
	}
	if hunk.Lines[3].OldLine != 3 || hunk.Lines[3].NewLine != 0 {
		t.Fatalf("removed line numbers = %+v", hunk.Lines[3])
	}
	if hunk.Lines[0].OldLine != 1 || hunk.Lines[0].NewLine != 1 {
		t.Fatalf("context line numbers = %+v", hunk.Lines[0])
	}

	renamed := diff.Files[1]
	if !renamed.Renamed || renamed.Path != "neu.go" || renamed.OldPath != "alt.go" {
		t.Fatalf("renamed file = %+v", renamed)
	}

	modeChange := diff.Files[2]
	if modeChange.Path != "run.sh" || len(modeChange.Hunks) != 0 || modeChange.Binary || modeChange.Capped {
		t.Fatalf("mode-change file = %+v", modeChange)
	}

	binary := diff.Files[3]
	if !binary.Binary || len(binary.Hunks) != 0 {
		t.Fatalf("binary file = %+v", binary)
	}

	added := diff.Files[4]
	if !added.Added || added.Path != "neu.txt" {
		t.Fatalf("added file = %+v", added)
	}
	for _, line := range added.Hunks[0].Lines {
		if line.Kind != StructuredDiffLineAdded || line.NewLine == 0 {
			t.Fatalf("added file line = %+v", line)
		}
	}
}

func structuredDiffKindNames(kinds []StructuredDiffLineKind) []string {
	names := make([]string, len(kinds))
	for i, kind := range kinds {
		names[i] = string(kind)
	}
	return names
}

func TestStructuredDiffRejectsGarbledOutput(t *testing.T) {
	dir := t.TempDir()
	for name, output := range map[string]string{
		"kein Diff-Format":    "das ist kein unified diff\n",
		"abgeschnitten":       "diff --git a/f b/f\nindex 111",
		"unbekannte Zeile":    "diff --git a/f b/f\nBahnhof\n",
		"falsche Hunk-Bilanz": "diff --git a/f b/f\n--- a/f\n+++ b/f\n@@ -1,2 +1,2 @@\n context\n",
	} {
		t.Run(name, func(t *testing.T) {
			runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
				{dir: dir, args: []string{"diff", "--no-color", "--no-ext-diff", "--unified=3", "HEAD"}, output: output},
			}}
			repos := newRepositories(runner)
			// Der Untracked-Schritt wird bei einem Parse-Fehler nie erreicht.
			fact := repos.StructuredDiff(context.Background(), structuredDiffTarget(dir, ""), DiffComparisonWorkingTree)
			if fact.Known() {
				t.Fatalf("garbled diff produced known knowledge: %+v", fact.Value)
			}
			if fact.State != RepositoryUnknown {
				t.Fatalf("fact state = %q, want unknown", fact.State)
			}
			if fact.Problem == nil || !strings.Contains(fact.Problem.Operation, "diff") {
				t.Fatalf("problem = %+v, want the failing diff operation", fact.Problem)
			}
			if len(fact.Value.Files) != 0 {
				t.Fatalf("unavailable diff must not carry files: %+v", fact.Value.Files)
			}
		})
	}
}

func structuredDiffTarget(dir, main string) RepositoryWorktreeTarget {
	target := RepositoryWorktreeTarget{Worktree: RepositoryWorktree{Path: dir}}
	if main == "" {
		target.MainBranch = repositoryUnknownFact[string]("main_branch", errForStructuredDiffTest)
	} else {
		target.MainBranch = repositoryKnownFact(main)
	}
	return target
}

func TestStructuredWorkingTreeDiffRendersUntrackedAsAdded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "neu.md"), []byte("# Titel\nText\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tracked := "diff --git a/app/foo.go b/app/foo.go\n" +
		"index 1111111..2222222 100644\n--- a/app/foo.go\n+++ b/app/foo.go\n" +
		"@@ -1 +1 @@\n-alt\n+neu\n"
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
		{dir: dir, args: []string{"diff", "--no-color", "--no-ext-diff", "--unified=3", "HEAD"}, output: tracked},
		{dir: dir, args: []string{"ls-files", "--others", "--exclude-standard", "-z"}, output: "neu.md\x00"},
	}}
	fact := newRepositories(runner).StructuredDiff(context.Background(), structuredDiffTarget(dir, ""), DiffComparisonWorkingTree)
	if !fact.Known() {
		t.Fatalf("fact = %+v, want known", fact)
	}
	if fact.Value.Mode != DiffComparisonWorkingTree {
		t.Fatalf("mode = %q", fact.Value.Mode)
	}
	if len(fact.Value.Files) != 2 {
		t.Fatalf("files = %+v", fact.Value.Files)
	}
	untracked := fact.Value.Files[1]
	if untracked.Path != "neu.md" || !untracked.Added || untracked.Binary || untracked.Capped {
		t.Fatalf("untracked file = %+v", untracked)
	}
	if len(untracked.Hunks) != 1 || len(untracked.Hunks[0].Lines) != 2 {
		t.Fatalf("untracked hunks = %+v", untracked.Hunks)
	}
	for index, line := range untracked.Hunks[0].Lines {
		if line.Kind != StructuredDiffLineAdded || line.NewLine != index+1 {
			t.Fatalf("untracked line %d = %+v", index, line)
		}
	}
	runner.assertDone()
}

func TestStructuredBranchDiffMeasuresAgainstMergeBase(t *testing.T) {
	dir := t.TempDir()
	mergeBase := strings.Repeat("a", 40)
	branch := "diff --git a/app/foo.go b/app/foo.go\n" +
		"index 1111111..2222222 100644\n--- a/app/foo.go\n+++ b/app/foo.go\n" +
		"@@ -1 +1 @@\n-alt\n+neu\n"
	runner := &repositoriesRecordingRunner{t: t, steps: []repositoriesRunnerStep{
		{dir: dir, args: []string{"merge-base", "HEAD", "main"}, output: mergeBase + "\n"},
		{dir: dir, args: []string{"diff", "--no-color", "--no-ext-diff", "--unified=3", mergeBase, "HEAD"}, output: branch},
	}}
	fact := newRepositories(runner).StructuredDiff(context.Background(), structuredDiffTarget(dir, "main"), DiffComparisonBranch)
	if !fact.Known() {
		t.Fatalf("fact = %+v, want known", fact)
	}
	if fact.Value.Mode != DiffComparisonBranch {
		t.Fatalf("mode = %q, want branch", fact.Value.Mode)
	}
	if fact.Value.Base != "main" {
		t.Fatalf("base = %q, want main", fact.Value.Base)
	}
	if len(fact.Value.Files) != 1 {
		t.Fatalf("files = %+v", fact.Value.Files)
	}
	runner.assertDone()
}

func TestStructuredBranchDiffWithoutMainBranchIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	runner := &repositoriesRecordingRunner{t: t}
	fact := newRepositories(runner).StructuredDiff(context.Background(), structuredDiffTarget(dir, ""), DiffComparisonBranch)
	if fact.Known() || fact.State != RepositoryUnknown {
		t.Fatalf("fact = %+v, want unknown", fact)
	}
	runner.assertDone()
}

func TestStructuredDiffCapsFilesInsteadOfDroppingThem(t *testing.T) {
	var out strings.Builder
	for i := 0; i < StructuredDiffMaxFiles+1; i++ {
		path := structuredDiffCappedPath(i)
		out.WriteString("diff --git a/" + path + " b/" + path + "\n")
		out.WriteString("--- a/" + path + "\n+++ b/" + path + "\n")
		out.WriteString("@@ -1 +1 @@\n-alt\n+neu\n")
	}
	diff, err := parseStructuredDiff(out.String(), DiffComparisonWorkingTree, "")
	if err != nil {
		t.Fatal(err)
	}
	capped := applyStructuredDiffCaps(diff)
	if len(capped.Files) != StructuredDiffMaxFiles+1 {
		t.Fatalf("files = %d, want %d", len(capped.Files), StructuredDiffMaxFiles+1)
	}
	last := capped.Files[StructuredDiffMaxFiles]
	if !last.Capped || len(last.Hunks) != 0 {
		t.Fatalf("capped file = %+v, want listed without hunks", last)
	}
	if last.Path == "" {
		t.Fatal("capped file lost its path")
	}
	if capped.Files[0].Capped {
		t.Fatalf("first file must stay rendered: %+v", capped.Files[0])
	}
}

func structuredDiffCappedPath(i int) string {
	return fmt.Sprintf("datei-%d.go", i)
}

func itoaStructuredDiff(i int) string {
	return fmt.Sprintf("%d", i)
}

func TestStructuredDiffCapsLongFiles(t *testing.T) {
	var out strings.Builder
	out.WriteString("diff --git a/gross.go b/gross.go\n--- a/gross.go\n+++ b/gross.go\n")
	out.WriteString("@@ -0,0 +1," + itoaStructuredDiff(StructuredDiffMaxLinesPerFile+1) + " @@\n")
	for i := 0; i < StructuredDiffMaxLinesPerFile+1; i++ {
		out.WriteString("+zeile\n")
	}
	diff, err := parseStructuredDiff(out.String(), DiffComparisonWorkingTree, "")
	if err != nil {
		t.Fatal(err)
	}
	capped := applyStructuredDiffCaps(diff)
	if len(capped.Files) != 1 || !capped.Files[0].Capped || len(capped.Files[0].Hunks) != 0 {
		t.Fatalf("long file = %+v, want capped without hunks", capped.Files)
	}
	if capped.Files[0].Path != "gross.go" {
		t.Fatalf("capped file lost its path: %+v", capped.Files[0])
	}
}
