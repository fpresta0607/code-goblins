package main

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/state"
)

// numstatOf is what git diff --numstat -z prints of files that each add the
// given lines.
func numstatOf(files map[string]int) string {
	var out strings.Builder
	for name, lines := range files {
		fmt.Fprintf(&out, "%d\t0\t%s\x00", lines, name)
	}
	return out.String()
}

// A goblin's branch that changes more lines than one review pass takes is
// sent back to be split before its gate run starts, naming its size and its
// largest files, and nothing is spent or recorded; one at the limit starts
// (#2002).
func TestPipelineRunSendsBackABranchTooLargeForOneReviewPass(t *testing.T) {
	for _, test := range []struct {
		name      string
		files     map[string]int
		isRefused bool
	}{
		{"over the limit", map[string]int{"internal/big.go": 1200, "internal/bigger.go": 700, "docs/notes.md": 101}, true},
		{"at the limit", map[string]int{"internal/big.go": 1200, "internal/bigger.go": 700, "docs/notes.md": 100}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h, nm, runner := intentFixture(t)
			runner.numstat = numstatOf(test.files)
			var out bytes.Buffer

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely"}, &out)

			// Assert
			if !test.isRefused {
				if err != nil || len(runner.started) != 1 {
					t.Fatalf("a branch of 2000 changed lines: %v, native=%+v; want one gate run", err, runner.started)
				}
				return
			}
			if err == nil || len(runner.started) != 0 {
				t.Fatalf("a branch of 2001 changed lines: %v, native=%+v; want it refused with no gate run", err, runner.started)
			}
			for _, said := range []string{"branch feat/policy changes 2001 lines", "over the 2000", "no run started", "internal/big.go (1200), internal/bigger.go (700), docs/notes.md (101)", "Split it", "only the files the default branch's .gitattributes marks linguist-generated are not counted", "--size-override"} {
				if !strings.Contains(err.Error(), said) {
					t.Errorf("refusal = %v; want it to say %q", err, said)
				}
			}
			if lines, _ := state.TailStatus(h.State, "task", 10); len(lines) != 0 {
				t.Errorf("status log %q; want nothing recorded for a refused start", lines)
			}
		})
	}
}

// Starting a gate run on a branch too large for one review pass is the CEO's
// word alone: its reason goes to the task's status log before the run starts,
// and a goblin that gives one is refused before anything is read or sent.
func TestPipelineRunSizeOverrideIsTheCEOsAlone(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the primary CEO registration is verified on Windows only")
	}
	for _, caller := range []string{"goblin", "ceo"} {
		t.Run(caller, func(t *testing.T) {
			// Arrange
			h, nm, runner := intentFixture(t)
			runner.numstat = numstatOf(map[string]int{"vendor/drop.go": 4800})
			registeredPrimary(t, h, caller)
			if caller == "goblin" {
				inGoblinTerminal(t, h.State, "task")
			} else {
				outsideGoblinTerminal(t)
			}
			args := []string{"run", "task", "--intent", "ship safely", "--size-override", "a vendored drop that cannot be split"}

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, args, &bytes.Buffer{})

			// Assert
			lines, _ := state.TailStatus(h.State, "task", 10)
			if caller == "goblin" {
				if err == nil || !strings.Contains(err.Error(), "--size-override is honoured only from the registered primary CEO") || len(runner.started) != 0 || len(lines) != 0 {
					t.Fatalf("a goblin's --size-override: %v, native=%+v, status %q; want it refused with nothing recorded or started", err, runner.started, lines)
				}
				return
			}
			if err != nil || len(runner.started) != 1 {
				t.Fatalf("the CEO's --size-override: %v, native=%+v; want one gate run", err, runner.started)
			}
			if len(lines) != 1 || !strings.HasSuffix(lines[0], `pipeline-size-override: branch=feat/policy lines=4800 limit=2000 reason="a vendored drop that cannot be split" by=ceo`) {
				t.Fatalf("status log %q; want the CEO's reason recorded", lines)
			}
		})
	}
}

// A blank or many-line --size-override is refused, not read as a reason.
func TestPipelineRunSizeOverrideNeedsAReason(t *testing.T) {
	for _, reason := range []string{"", "  ", "one line\nand another"} {
		t.Run(fmt.Sprintf("%q", reason), func(t *testing.T) {
			// Arrange
			h, nm, runner := intentFixture(t)

			// Act
			err := pipelineCommand(context.Background(), h, nm, runner, []string{"run", "task", "--intent", "ship safely", "--size-override", reason}, &bytes.Buffer{})

			// Assert
			if err == nil || !strings.Contains(err.Error(), "--size-override must give the CEO's reason on one line") || len(runner.started) != 0 {
				t.Errorf("--size-override %q: %v, native=%+v; want it refused before anything starts", reason, err, runner.started)
			}
		})
	}
}
