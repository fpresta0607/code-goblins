package lifecycle

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// handoffWritingChildVariable makes this test binary a pause that writes the
// goblin's handoff to the path it names and stops as it is about to put it
// in place.
const handoffWritingChildVariable = "LIFECYCLE_HANDOFF_WRITING_CHILD"

func TestHandoffWritingChild(t *testing.T) {
	path := os.Getenv(handoffWritingChildVariable)
	if path == "" {
		return
	}
	writingHandoff = func() {
		fmt.Println("writing")
		time.Sleep(time.Hour)
	}
	service := Service{Operations: Operations{Handoff: func(context.Context, state.TaskMeta) []byte { return []byte("Written for the goblin.") }}}
	service.writeHandoff(context.Background(), state.TaskMeta{}, &state.Lifecycle{Handoff: path})
}

// A pause ended while it writes the handoff for a goblin that wrote none, as
// goblins ending mid-pause ends it, left the handoff created and empty. Every
// later pause of that task for the same operation then waited on a handoff
// it never counts as saved and could not write one over it (#1707). A
// handoff being written is not there for a reader, and a writer ended part
// way leaves none, so the next pause writes it.
func TestAPauseEndedWhileWritingAHandoffLeavesNoneForTheNextToTrip(t *testing.T) {
	// Arrange: a child process stops while it writes the handoff.
	path := filepath.Join(t.TempDir(), "pause-memory-pause-1.md")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	child := execx.CommandContext(ctx, os.Args[0], "-test.run=^TestHandoffWritingChild$")
	child.Env = append(os.Environ(), handoffWritingChildVariable+"="+path)
	child.Stderr = os.Stderr
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = child.Process.Kill()
		_ = child.Wait()
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "writing\n" {
		t.Fatalf("child did not start writing: %q %v", line, err)
	}

	// Act: end the writer part way, then pause again.
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = child.Wait()
	_, afterEnd := os.Stat(path)
	service := Service{Operations: Operations{Handoff: func(context.Context, state.TaskMeta) []byte { return []byte("Written again.") }}}
	record := state.Lifecycle{Handoff: path}
	service.writeHandoff(ctx, state.TaskMeta{}, &record)

	// Assert
	if !errors.Is(afterEnd, os.ErrNotExist) {
		t.Errorf("the handoff after its writer ended part way: %v, want none", afterEnd)
	}
	if data, err := os.ReadFile(path); !record.HandoffSaved || err != nil || string(data) != "Written again." {
		t.Errorf("the next pause's handoff = %q, %v, saved %t, problems %q; want it written and saved", data, err, record.HandoffSaved, record.Problems)
	}
}
