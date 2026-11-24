package fleettree

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A file under the folder a sub-agent works in is named relative to it: the
// folder spelled in another case is that folder exactly where the file system
// opens the one by the other, and a folder whose name only begins with it is
// not it.
func TestCallSubjectNamesAFileUnderItsFolderByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	cwd := filepath.Join(parent, "repo")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(parent, "REPO")
	_, err := os.Stat(upper)
	isOnePath := err == nil
	inUpper := filepath.Join(upper, "main.go")
	wantInUpper := inUpper
	if isOnePath {
		wantInUpper = "main.go"
	}
	cases := []struct {
		name string
		file string
		want string
	}{
		{name: "a file in it", file: filepath.Join(cwd, "main.go"), want: "main.go"},
		{name: "a file in a folder below it", file: filepath.Join(cwd, "internal", "a.go"), want: filepath.Join("internal", "a.go")},
		{name: "a file in it spelled in another case", file: inUpper, want: wantInUpper},
		{name: "a file in a folder whose name it begins", file: filepath.Join(parent, "repos", "main.go"), want: filepath.Join(parent, "repos", "main.go")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input, err := json.Marshal(map[string]string{"file_path": tc.file})
			if err != nil {
				t.Fatal(err)
			}

			// Act
			subject := callSubject(input, cwd)

			// Assert
			if subject != tc.want {
				t.Fatalf("callSubject(%s, %s) = %s, want %s, as the file system opens %s by %s: %t", tc.file, cwd, subject, tc.want, cwd, upper, isOnePath)
			}
		})
	}
}

// A result Claude Code spilled to a file is read from the conversation's own
// tool-results folder. A result naming that folder in another case names it
// exactly where the file system opens the one by the other; elsewhere it
// names another folder, and the result keeps the preview it holds.
func TestASpillNamingToolResultsInAnotherCaseIsReadWhereTheFileSystemSaysSo(t *testing.T) {
	// Arrange
	home := t.TempDir()
	session := filepath.Join(home, ".claude", "projects", "C--work-repo", "session-1")
	writeFile(t, filepath.Join(session, "tool-results", "toolu_1.txt"), "the saved output", at)
	saved := filepath.Join(session, "TOOL-RESULTS", "toolu_1.txt")
	_, err := os.Stat(saved)
	isOnePath := err == nil
	path := conversation(t, home,
		answer("a1", "msg_1", useBlock("toolu_1", "Bash", object{"command": "go test ./..."})),
		answered("u1", "toolu_1", "<persisted-output>\nOutput too large (2.0KB). Full output saved to: "+saved+"\n\nPreview (first 2KB):\nthe preview\n</persisted-output>", nil, nil))
	reader := Reader{Home: home}

	// Act
	page, _, err := reader.MessagePage(path, -1)

	// Assert
	want := "the preview"
	if isOnePath {
		want = "the saved output"
	}
	if err != nil || len(page.Messages) != 1 || page.Messages[0].Blocks[0].Result != want {
		t.Fatalf("MessagePage = %+v, %v; want the result %q, as the file system opens %s: %t", page.Messages, err, want, saved, isOnePath)
	}
}

// A native Codex goblin's conversation is the rollout bound to its worktree.
// A rollout bound to the worktree's folder spelled in another case is bound
// to it exactly where the file system opens the one by the other; elsewhere
// that spelling is another folder, here made beside it.
func TestCodexConversationIsTheRolloutOfItsWorktreeByTheFileSystemsCase(t *testing.T) {
	// Arrange
	parent := t.TempDir()
	worktree := filepath.Join(parent, "repo")
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	upper := filepath.Join(parent, "REPO")
	_, err := os.Stat(upper)
	isOnePath := err == nil
	if !isOnePath {
		if err := os.Mkdir(upper, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	home := t.TempDir()
	rollout := codexRollout(t, home, "s1", codexMeta("s1", upper))
	reader := &Reader{Home: home}
	ctx := t.Context()

	// Act
	conversation := reader.codexConversation(ctx, worktree, "", reader.rollouts(ctx))

	// Assert
	want := ""
	if isOnePath {
		want = rollout
	}
	if conversation != want {
		t.Fatalf("codexConversation(%s) = %q, want %q, as the file system opens %s by %s: %t", worktree, conversation, want, worktree, upper, isOnePath)
	}
}

// A PowerShell job runs for the background shell whose output file it writes
// to, a file named for the shell's task after the system's own separator.
func TestShellOfFindsTheShellWhoseOutputFileAJobWritesTo(t *testing.T) {
	// Arrange
	started := at.Add(-time.Minute)
	output := filepath.Join(t.TempDir(), "tasks", "b5.output")
	children := []Node{{ID: "shell:b5", Kind: KindShell, State: Working, Started: started}}
	cases := []struct {
		name        string
		commandLine string
		want        string
	}{
		{name: "its own output file", commandLine: `cmd.exe /d /s /c ""powershell.exe" -NoProfile > "` + output + `" 2>&1"`, want: "shell:b5"},
		{name: "a task whose id ends with the shell's", commandLine: `cmd.exe /d /s /c ""powershell.exe" -NoProfile > "` + filepath.Join(filepath.Dir(output), "xb5.output") + `" 2>&1"`, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			shell := shellOf(job{root: Process{Started: started}}, tc.commandLine, nil, children)

			// Assert
			if shell != tc.want {
				t.Fatalf("shellOf(%q) = %q, want %q", tc.commandLine, shell, tc.want)
			}
		})
	}
}
