package fleettree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// The newest page of a long conversation waits for no more than a read of
// its tail: it comes while the start of the conversation cannot be read, and
// the older pages come once it can.
func TestTheNewestPageOfALongConversationReadsOnlyItsTail(t *testing.T) {
	// Arrange
	home := t.TempDir()
	path := conversation(t, home, longConversation()...)
	head, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer head.Close()
	const locked = 512 << 10
	start := new(windows.Overlapped)
	if err := windows.LockFileEx(windows.Handle(head.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, locked, 0, start); err != nil {
		t.Fatal(err)
	}
	reader := Reader{Home: home}

	// Act
	newest, _, newestErr := reader.MessagePage(path, -1)
	if err := windows.UnlockFileEx(windows.Handle(head.Fd()), 0, locked, 0, start); err != nil {
		t.Fatal(err)
	}

	// Assert
	if newestErr != nil || len(newest.Messages) != MessagesPerPage || newest.Messages[0].Text != "prompt 11" || !newest.More {
		t.Fatalf("MessagePage while the conversation's start is locked = %d messages, more %t, %v; want the newest page from prompt 11, and more", len(newest.Messages), newest.More, newestErr)
	}
	older, _, olderErr := reader.MessagePage(path, newest.Messages[0].Seq)
	if olderErr != nil || len(older.Messages) != 13 || older.Messages[0].Text != "the first words" || older.More {
		t.Errorf("the page before it once the start is unlocked = %d messages, more %t, %v; want the first words to prompt 10", len(older.Messages), older.More, olderErr)
	}
}

// A spilled result is read only from a regular file directly in its own
// conversation's tool-results folder, reached through no junction; a result
// that names any other file keeps the preview the conversation holds.
func TestASpillIsReadOnlyFromItsConversationsToolResults(t *testing.T) {
	for _, tc := range []struct {
		name string
		// saved is where the result names its file, under the conversation's
		// folder, and isRead whether the file is read.
		saved   string
		isRead  bool
		arrange func(t *testing.T, session string)
	}{
		{name: "its own tool-results folder", saved: `tool-results\toolu_1.txt`, isRead: true},
		{name: "a path that climbs out of it", saved: `tool-results\..\..\toolu_1.txt`},
		{name: "a folder beside it", saved: `tool-results-x\toolu_1.txt`},
		{name: "a folder under it", saved: `tool-results\nested\toolu_1.txt`},
		{name: "a junction in its place", saved: `tool-results\toolu_1.txt`, arrange: func(t *testing.T, session string) {
			outside := filepath.Join(filepath.Dir(session), "outside")
			writeFile(t, filepath.Join(outside, "toolu_1.txt"), "the saved output", at)
			if err := os.MkdirAll(session, 0o755); err != nil {
				t.Fatal(err)
			}
			if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(session, "tool-results"), outside).CombinedOutput(); err != nil {
				t.Fatalf("mklink /J: %v: %s", err, out)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			home := t.TempDir()
			session := filepath.Join(home, ".claude", "projects", "C--work-repo", "session-1")
			saved := filepath.Clean(filepath.Join(session, tc.saved))
			if tc.arrange != nil {
				tc.arrange(t, session)
			} else {
				writeFile(t, saved, "the saved output", at)
			}
			path := conversation(t, home,
				answer("a1", "msg_1", useBlock("toolu_1", "Bash", object{"command": "go test ./..."})),
				answered("u1", "toolu_1", "<persisted-output>\nOutput too large (2.0KB). Full output saved to: "+saved+"\n\nPreview (first 2KB):\nthe preview\n</persisted-output>", nil, nil))
			reader := Reader{Home: home}

			// Act
			page, _, err := reader.MessagePage(path, -1)

			// Assert
			want := "the preview"
			if tc.isRead {
				want = "the saved output"
			}
			if err != nil || len(page.Messages) != 1 || page.Messages[0].Blocks[0].Result != want {
				t.Errorf("MessagePage = %+v, %v; want the result %q", page.Messages, err, want)
			}
		})
	}
}
