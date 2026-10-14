package installscript

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// releaseWorkflow is release.yml, with Unix line ends.
func releaseWorkflow(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.ReplaceAll(string(data), "\r\n", "\n")
}

// The tag's name reaches the release's scripts through the environment
// alone. A git tag name may hold ; $ ( ) and quotes, so one written into a
// script's text, as ${{ github.ref_name }} is, runs as script in a job whose
// programs the release signs.
func TestReleaseWorkflowTakesTheTagOnlyThroughTheEnvironment(t *testing.T) {
	// Act
	workflow := releaseWorkflow(t)

	// Assert
	if count := strings.Count(workflow, "${{ github.ref_name }}"); count != 1 || !strings.Contains(workflow, "\nenv:\n  RELEASE_TAG: ${{ github.ref_name }}\n") {
		t.Errorf("release.yml names ${{ github.ref_name }} %d times; want once, as the workflow's RELEASE_TAG", count)
	}
	for _, expression := range []string{"github.head_ref", "github.event.", "inputs."} {
		if strings.Contains(workflow, "${{ "+expression) {
			t.Errorf("release.yml writes ${{ %s... into its text; want what a pusher names only through the environment", expression)
		}
	}
}

// The release checks its tag and commit before it builds anything: the
// guard runs right after the checkout, which takes the whole history the
// guard reads.
func TestReleaseWorkflowChecksItsTagBeforeItBuilds(t *testing.T) {
	// Act
	workflow := releaseWorkflow(t)
	_, build, _ := strings.Cut(workflow, "\n  build:\n")
	build, _, _ = strings.Cut(build, "\n  publish:\n")

	// Assert
	guard := strings.Index(build, "run: ./tools/release-on-main.ps1 -Tag $env:RELEASE_TAG -Commit $env:GITHUB_SHA -Main origin/main\n")
	first := regexp.MustCompile(`\n      - (uses|name): `).FindAllStringIndex(build, -1)
	if guard < 0 || len(first) < 3 || guard > first[2][0] {
		t.Errorf("the build job's steps are:\n%s\nwant the guard the step right after the checkout", build)
	}
	if !strings.Contains(build, "          fetch-depth: 0\n") {
		t.Error("the build's checkout takes part of the history; want all of it, which the guard reads")
	}
}

// Every action the release uses is pinned by commit SHA, since an action's
// tag can be moved to other code, and these run with the signing identity
// or the right to publish.
func TestReleaseWorkflowPinsEveryActionToASHA(t *testing.T) {
	// Act
	workflow := releaseWorkflow(t)

	// Assert
	uses := regexp.MustCompile(`(?m)^\s*(?:- )?uses: (\S+)(.*)$`).FindAllStringSubmatch(workflow, -1)
	if len(uses) < 8 {
		t.Fatalf("release.yml uses %d actions; want every one read", len(uses))
	}
	pinned := regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._/-]+@[0-9a-f]{40}$`)
	for _, use := range uses {
		if !pinned.MatchString(use[1]) || !regexp.MustCompile(`^ # v[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(use[2]) {
			t.Errorf("release.yml uses %q%s; want an action pinned by commit SHA, with its version as a comment", use[1], use[2])
		}
	}
}
