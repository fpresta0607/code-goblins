package codegoblins

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/train"
	"gopkg.in/yaml.v3"
)

// releaseNotes is .github/release.yml, which tells GitHub how to write the
// list of what changed into a release's notes.
type releaseNotes struct {
	Changelog struct {
		Exclude struct {
			Labels []string `yaml:"labels"`
		} `yaml:"exclude"`
		Categories []yaml.Node `yaml:"categories"`
	} `yaml:"changelog"`
}

func readReleaseNotes(t *testing.T) releaseNotes {
	t.Helper()
	source, err := os.ReadFile(filepath.Join(".github", "release.yml"))
	if err != nil {
		t.Fatalf("the repository does not say how GitHub writes a release's notes: %v", err)
	}
	var notes releaseNotes
	if err := yaml.Unmarshal(source, &notes); err != nil {
		t.Fatal(err)
	}
	return notes
}

// The release workflow asks GitHub for the list of what changed, and GitHub
// lists every merged pull request. A merge train that lands merges its own
// pull request, so that list named the train beside each pull request it
// landed, and the board's Update item shows the list's first lines as what
// is new. The list leaves out the label a train's pull request wears. That
// label is text in a YAML file, while the train names it from a constant of
// its own, and nothing but this ties the two: were it renamed in one place,
// every train would be listed again and every check would still pass.
func TestGeneratedReleaseNotesLeaveOutATrainsOwnPullRequest(t *testing.T) {
	// Arrange
	workflow, err := os.ReadFile(filepath.Join(".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workflow), "generate_release_notes: true") {
		t.Fatal("the release workflow no longer asks GitHub for the list of what changed, so this test guards nothing: say here how a release's notes are written now")
	}
	if train.OwnLabel == "" {
		t.Fatal("a train's pull request wears no label, so no list could leave it out")
	}

	// Act
	left := readReleaseNotes(t).Changelog.Exclude.Labels

	// Assert
	if !slices.Contains(left, train.OwnLabel) {
		t.Fatalf(".github/release.yml leaves out the labels %q, want the train's own, %q, among them", left, train.OwnLabel)
	}
}

// The board reads what is new from the items under the notes' one What's
// Changed heading, and stops at the next heading. A category in
// .github/release.yml puts a heading of its own over its items, so the board
// would find none.
func TestGeneratedReleaseNotesKeepTheOneListTheBoardReads(t *testing.T) {
	// Act
	categories := readReleaseNotes(t).Changelog.Categories

	// Assert
	if len(categories) != 0 {
		t.Fatalf(".github/release.yml sorts what changed into %d categories, each under a heading of its own: the board's Update item reads the items under What's Changed alone (release.WhatsNew), so teach it the headings first", len(categories))
	}
}
