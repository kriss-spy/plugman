package input

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestExpandClassifiesDirectPluginInputs(t *testing.T) {
	t.Parallel()

	got, err := Expand(t.TempDir(), []string{
		"dataview",
		"homepage@1.4.3",
		"https://github.com/kriss-spy/another-plugin",
		"https://github.com/kriss-spy/obsidian-opencode/releases/tag/1.3.13",
	})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}

	want := []Declaration{
		{Kind: Official, ID: "dataview", Origin: Origin{Input: "dataview"}},
		{Kind: Official, ID: "homepage", Version: "1.4.3", Origin: Origin{Input: "homepage@1.4.3"}},
		{Kind: GitHubRepository, Repository: "https://github.com/kriss-spy/another-plugin", Origin: Origin{Input: "https://github.com/kriss-spy/another-plugin"}},
		{Kind: GitHubRelease, Repository: "https://github.com/kriss-spy/obsidian-opencode", Release: "1.3.13", Origin: Origin{Input: "https://github.com/kriss-spy/obsidian-opencode/releases/tag/1.3.13"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Expand() = %#v, want %#v", got, want)
	}
}

func TestExpandReadsPluginListsAdditivelyWithoutChangingThem(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := filepath.Join(dir, "first.plugins")
	second := filepath.Join(dir, "second.plugins")
	firstContents := "# essentials\n\ndataview\nhomepage@1.4.3 # exact homepage release\n"
	secondContents := "dataview\nhttps://github.com/kriss-spy/obsidian-opencode\n"
	if err := os.WriteFile(first, []byte(firstContents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(second, []byte(secondContents), 0o600); err != nil {
		t.Fatal(err)
	}

	declarations, err := Expand(dir, []string{"first.plugins", "second.plugins"})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if len(declarations) != 3 {
		t.Fatalf("len(Expand()) = %d, want 3: %#v", len(declarations), declarations)
	}
	got, readErr := os.ReadFile(first)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != firstContents {
		t.Fatalf("plugin list changed: got %q, want %q", got, firstContents)
	}
}

func TestExpandIgnoresBlankLinesAndFullLineComments(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "base.plugins")
	if err := os.WriteFile(path, []byte("\n  # comment\n dataview \n homepage@1.4.3\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Expand(dir, []string{"base.plugins"})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if len(got) != 2 || got[0].ID != "dataview" || got[1].ID != "homepage" {
		t.Fatalf("Expand() = %#v", got)
	}
	if got[0].Origin.Path != path || got[0].Origin.Line != 3 {
		t.Fatalf("first origin = %#v", got[0].Origin)
	}
}

func TestExpandDeduplicatesCompatibleDeclarationsInFirstDeclarationOrder(t *testing.T) {
	t.Parallel()

	got, err := Expand(t.TempDir(), []string{
		"dataview",
		"homepage",
		"dataview@0.5.67",
		"https://github.com/owner/repo",
		"https://github.com/owner/repo/releases/tag/v2.0.0",
	})
	if err != nil {
		t.Fatalf("Expand() error = %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(Expand()) = %d, want 3: %#v", len(got), got)
	}
	if got[0].ID != "dataview" || got[0].Version != "0.5.67" || got[1].ID != "homepage" {
		t.Fatalf("official declarations = %#v", got[:2])
	}
	if got[2].Kind != GitHubRelease || got[2].Release != "v2.0.0" {
		t.Fatalf("GitHub declaration = %#v", got[2])
	}
}

func TestExpandReportsSyntacticConflicts(t *testing.T) {
	t.Parallel()

	_, err := Expand(t.TempDir(), []string{
		"dataview@0.5.66",
		"dataview@0.5.67",
		"https://github.com/owner/repo/releases/tag/1.0.0",
		"https://github.com/owner/repo/releases/tag/2.0.0",
	})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Expand() error = %v, want *ValidationError", err)
	}
	if countCode(validation.Problems, ConflictingExactVersion) != 2 {
		t.Fatalf("problems = %#v, want two conflicts", validation.Problems)
	}
}

func TestExpandTreatsMissingFileLikeInputAsMissingFile(t *testing.T) {
	t.Parallel()

	_, err := Expand(t.TempDir(), []string{"base.plugins"})
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Problems) != 1 {
		t.Fatalf("Expand() error = %v, want one *ValidationError problem", err)
	}
	if validation.Problems[0].Code != MissingFile {
		t.Fatalf("problem code = %q, want %q", validation.Problems[0].Code, MissingFile)
	}
}

func TestExpandReturnsStableErrorsForInvalidInputs(t *testing.T) {
	t.Parallel()

	_, err := Expand(t.TempDir(), []string{
		"homepage@^1.2.0",
		"http://github.com/owner/repo",
		"https://github.com/owner/repo/issues/1",
		"Bad ID",
	})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("Expand() error = %v, want *ValidationError", err)
	}
	if countCode(validation.Problems, InvalidDeclaration) != 4 {
		t.Fatalf("problems = %#v", validation.Problems)
	}
}

func hasCode(problems []Problem, code ErrorCode) bool {
	return countCode(problems, code) > 0
}

func countCode(problems []Problem, code ErrorCode) int {
	count := 0
	for _, problem := range problems {
		if problem.Code == code {
			count++
		}
	}
	return count
}
