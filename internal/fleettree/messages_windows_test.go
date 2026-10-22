package fleettree

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

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
