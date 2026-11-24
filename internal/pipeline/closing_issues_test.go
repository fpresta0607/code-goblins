package pipeline

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/gatedb"
)

func TestClosingIssuesReadsEveryRunOfTheBranchOnce(t *testing.T) {
	// Arrange
	root := t.TempDir()
	project := filepath.Join(root, "project")
	writer, err := sql.Open("sqlite", filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = writer.Exec(`
CREATE TABLE repos(id TEXT, working_path TEXT);
CREATE TABLE runs(id TEXT, repo_id TEXT, branch TEXT, closing_issue_refs TEXT, created_at INTEGER);
INSERT INTO repos VALUES ('repo', ?), ('other-repo', ?);
INSERT INTO runs VALUES
 ('first', 'repo', 'fix/x', '12', 100),
 ('none', 'repo', 'fix/x', NULL, 150),
 ('second', 'repo', 'fix/x', '12
northwind/api#7', 200),
 ('other-branch', 'repo', 'fix/y', '99', 250),
 ('other-repo', 'other-repo', 'fix/x', '98', 250);
`, project, filepath.Join(root, "other"))
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader := Reader{Root: root, Commands: gatedb.Runner{Next: execx.OSRunner{}}}

	// Act
	issues, err := reader.ClosingIssues(context.Background(), project, "fix/x")

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"12", "northwind/api#7"}; !slices.Equal(issues, want) {
		t.Fatalf("closing issues = %q, want %q", issues, want)
	}
}

func TestClosingIssuesFailsWithoutADatabase(t *testing.T) {
	// Arrange
	reader := Reader{Root: t.TempDir(), Commands: gatedb.Runner{Next: execx.OSRunner{}}}

	// Act
	_, err := reader.ClosingIssues(context.Background(), "C:/work/project", "fix/x")

	// Assert
	if err == nil {
		t.Fatal("closing issues read from no database, want an error")
	}
}
