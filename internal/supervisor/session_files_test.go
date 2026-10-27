package supervisor

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// filesBoard is a commandBoard whose task-1 worktree is a git repository of
// its own, which git reads no further up than the worktree.
func filesBoard(t *testing.T) (*HTTP, string) {
	t.Helper()
	board, _, worktree := commandBoard(t, "claude")
	if err := os.Remove(filepath.Join(worktree, ".git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(worktree))
	if out, err := exec.Command("git", "init", "-q", worktree).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return board, worktree
}

// getFiles asks the board for the files a query names.
func getFiles(board *HTTP, query string) *httptest.ResponseRecorder {
	request := httptest.NewRequest("GET", "http://"+board.Host+"/api/files?"+query, nil)
	response := httptest.NewRecorder()
	board.ServeHTTP(response, request)
	return response
}

// filesIn reads a successful answer's files.
func filesIn(t *testing.T, response *httptest.ResponseRecorder) []string {
	t.Helper()
	var answer struct{ Files []string }
	if response.Code != http.StatusOK {
		t.Fatalf("answered %d %s", response.Code, response.Body)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &answer); err != nil || answer.Files == nil {
		t.Fatalf("answered %s (%v)", response.Body, err)
	}
	return answer.Files
}

// The "@" list names the session's tracked files and its untracked ones git
// does not ignore, by forward-slash path, and never the ignored ones.
func TestFilesListTrackedAndUntrackedButNotIgnoredFiles(t *testing.T) {
	// Arrange
	board, worktree := filesBoard(t)
	writeCommandFixture(t, filepath.Join(worktree, ".gitignore"), "build/\n*.log\n")
	writeCommandFixture(t, filepath.Join(worktree, "src", "tracked.go"), "x")
	writeCommandFixture(t, filepath.Join(worktree, "src", "untracked.go"), "x")
	writeCommandFixture(t, filepath.Join(worktree, "build", "ignored.go"), "x")
	writeCommandFixture(t, filepath.Join(worktree, "debug.log"), "x")
	if out, err := exec.Command("git", "-C", worktree, "add", ".gitignore", "src/tracked.go").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	// Act
	files := filesIn(t, getFiles(board, "task=task-1&generation=g1&token=test-instance"))

	// Assert
	if want := []string{".gitignore", "src/tracked.go", "src/untracked.go"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want %v", files, want)
	}
}

// A session folder outside any git repository holds no files to list.
func TestFilesOfAFolderOutsideAnyRepositoryAreEmpty(t *testing.T) {
	// Arrange
	board, _, worktree := commandBoard(t, "claude")
	if err := os.Remove(filepath.Join(worktree, ".git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(worktree))
	writeCommandFixture(t, filepath.Join(worktree, "notes.md"), "x")

	// Act
	files := filesIn(t, getFiles(board, "task=task-1&token=test-instance&q=notes"))

	// Assert
	if len(files) != 0 {
		t.Fatalf("files = %v, want none", files)
	}
}

// The board keeps a folder's listing for a while, so keystrokes that follow
// one another read it rather than run git again.
func TestFilesReadTheKeptListingWithinItsLife(t *testing.T) {
	// Arrange
	board, worktree := filesBoard(t)
	writeCommandFixture(t, filepath.Join(worktree, "first.go"), "x")
	filesIn(t, getFiles(board, "task=task-1&token=test-instance&q=f"))
	writeCommandFixture(t, filepath.Join(worktree, "second.go"), "x")

	// Act
	files := filesIn(t, getFiles(board, "task=task-1&token=test-instance&q=s"))

	// Assert
	if want := []string{"first.go"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want the kept listing's %v", files, want)
	}
}

// A listing whose request went, as the composer drops one at each keystroke,
// still finishes and is kept, so the keystroke after it reads that listing
// rather than run git again.
func TestFilesKeepAListingWhoseRequestWent(t *testing.T) {
	// Arrange
	board, worktree := filesBoard(t)
	writeCommandFixture(t, filepath.Join(worktree, "first.go"), "x")
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	board.ServeHTTP(httptest.NewRecorder(), httptest.NewRequestWithContext(gone, "GET", "http://"+board.Host+"/api/files?task=task-1&token=test-instance&q=f", nil))
	writeCommandFixture(t, filepath.Join(worktree, "second.go"), "x")

	// Act
	files := filesIn(t, getFiles(board, "task=task-1&token=test-instance&q=s"))

	// Assert
	if want := []string{"first.go"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("files = %v, want the dropped request's kept listing %v", files, want)
	}
}

// A match by base name's start comes first, then one inside the base name,
// then one anywhere in the path, ignoring case and in path order within each.
func TestFilesRankMatchesByWhereTheQueryFalls(t *testing.T) {
	paths := []string{"Docs/Readme.md", "cmd/main.go", "internal/board/view.go", "internal/main_test.go", "web/BoardView.tsx", "z/board.go"}
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"board", []string{"web/BoardView.tsx", "z/board.go", "internal/board/view.go"}},
		{"MAIN", []string{"cmd/main.go", "internal/main_test.go"}},
		{"view", []string{"internal/board/view.go", "web/BoardView.tsx"}},
		{"", paths},
		{"absent", []string{}},
	} {
		t.Run(c.query, func(t *testing.T) {
			// Act
			ranked := rankFiles(paths, c.query)

			// Assert
			if !reflect.DeepEqual(ranked, c.want) {
				t.Fatalf("rankFiles(%q) = %v, want %v", c.query, ranked, c.want)
			}
		})
	}
}

// An answer names at most fifty files, the first fifty by path for an empty
// query.
func TestFilesAnswerAtMostFiftyMatches(t *testing.T) {
	// Arrange
	var paths []string
	for i := range 60 {
		paths = append(paths, fmt.Sprintf("file-%02d.go", i))
	}

	// Act
	ranked := rankFiles(paths, "")

	// Assert
	if !reflect.DeepEqual(ranked, paths[:50]) {
		t.Fatalf("answered %d files ending %v, want the first 50", len(ranked), ranked[len(ranked)-1])
	}
}

// The "@" list is refused as the "/" list is: without the board's token, for
// a folder instead of a session, and for a session the board cannot name.
func TestFilesRefuseASessionTheBoardCannotName(t *testing.T) {
	board, _ := filesBoard(t)
	for _, c := range []struct {
		what, query string
		status      int
	}{
		{"no token", "task=task-1", http.StatusForbidden},
		{"a token that is not this board's", "task=task-1&token=other", http.StatusForbidden},
		{"a path selector", "task=task-1&token=test-instance&path=C:/other", http.StatusBadRequest},
		{"a node selector", "task=task-1&token=test-instance&node=other", http.StatusBadRequest},
		{"a task this home does not hold", "task=task-9&token=test-instance", http.StatusNotFound},
		{"a generation a later start replaced", "task=task-1&generation=old&token=test-instance", http.StatusConflict},
		{"a CFO that is not running", "cfo=1&token=test-instance", http.StatusNotFound},
	} {
		t.Run(c.what, func(t *testing.T) {
			// Act
			response := getFiles(board, c.query)

			// Assert
			if response.Code != c.status {
				t.Fatalf("answered %d %s, want %d", response.Code, response.Body, c.status)
			}
		})
	}
}
