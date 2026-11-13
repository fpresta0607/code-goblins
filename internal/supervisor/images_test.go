package supervisor

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// writePNG writes a one-pixel PNG and returns its bytes.
func writePNG(t *testing.T, path string) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A goblin's review image must be a real image inside its own worktree, an
// extra worktree it recorded, its task scratch, its scratch folder or its data
// directory, reached without following a link.
func TestReviewImagesStayInsideTheTaskAndAreImages(t *testing.T) {
	_, h := testStore(t)
	meta, err := state.ReadTaskMeta(h.State, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	meta.TaskTmp = filepath.Join(h.Root, "tasktmp")
	meta.Scratch = filepath.Join(h.Root, "scratch", "task-1")
	meta.Extras = []string{filepath.Join(h.Root, "worktrees", "app", "task-1-docs")}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}
	inside := []string{filepath.Join(meta.Worktree, "shots", "a.png"), filepath.Join(h.Data, "task-1", "b.png"), filepath.Join(meta.TaskTmp, "c.png"), filepath.Join(meta.Scratch, "d.png"), filepath.Join(meta.Extras[0], "e.png")}
	for _, path := range inside {
		writePNG(t, path)
	}
	checked, err := ReviewImages(h, "task-1", inside)
	if err != nil || len(checked) != len(inside) {
		t.Fatalf("images inside the task refused: %v %v", checked, err)
	}
	outside := t.TempDir()
	writePNG(t, filepath.Join(outside, "escape.png"))
	text := filepath.Join(meta.Worktree, "notes.png")
	if err := os.WriteFile(text, []byte("not an image"), 0600); err != nil {
		t.Fatal(err)
	}
	huge := filepath.Join(meta.Worktree, "huge.png")
	writePNG(t, huge)
	if err := os.Truncate(huge, maxReviewImage+1); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		path, refusal string
		link          func() error
	}{
		"outside the task":  {filepath.Join(outside, "escape.png"), "must be inside", nil},
		"not an image":      {text, "only PNG", nil},
		"over the size cap": {huge, "at most 10 MiB", nil},
		"a directory":       {filepath.Join(meta.Worktree, "shots"), "regular file", nil},
		"a parent junction": {filepath.Join(meta.Worktree, "junction", "escape.png"), "regular file", func() error {
			if runtime.GOOS != "windows" {
				return errors.New("junctions exist only on Windows")
			}
			if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(meta.Worktree, "junction"), outside).CombinedOutput(); err != nil {
				return fmt.Errorf("%v: %s", err, out)
			}
			return nil
		}},
		"a file symlink": {filepath.Join(meta.Worktree, "linked.png"), "regular file", func() error {
			return os.Symlink(filepath.Join(outside, "escape.png"), filepath.Join(meta.Worktree, "linked.png"))
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if c.link != nil {
				if err := c.link(); err != nil {
					t.Skipf("this link cannot be created here: %v", err)
				}
				if _, err := os.Stat(c.path); err != nil {
					t.Fatalf("premise: the link does not reach the outside image: %v", err)
				}
			}
			if _, err := ReviewImages(h, "task-1", []string{inside[0], c.path}); err == nil || !strings.Contains(err.Error(), c.refusal) {
				t.Fatalf("err = %v, want a refusal naming %q", err, c.refusal)
			}
		})
	}
}

// Only a goblin's question takes images, one absolute path for each choice.
func TestQuestionImagesMatchTheGoblinsChoices(t *testing.T) {
	shot := filepath.Join(t.TempDir(), "a.png")
	goblin := Question{ID: "notify-task-1-1", Identity: strings.Repeat("g", 64), Text: "Which layout?", Options: []string{"Grid", "List"}, CreatedAt: time.Now().UTC(), Task: "task-1", Generation: "g1", Seq: 1, Images: []string{shot, shot}}
	if err := validQuestion(goblin); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]Question{
		"the CEO's question":     func() Question { q := goblin; q.Task, q.Generation, q.Seq = "", "", 0; return q }(),
		"one image too few":      func() Question { q := goblin; q.Images = q.Images[:1]; return q }(),
		"images without choices": func() Question { q := goblin; q.Options = nil; return q }(),
		"a relative path":        func() Question { q := goblin; q.Images = []string{"a.png", shot}; return q }(),
	} {
		if err := validQuestion(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
