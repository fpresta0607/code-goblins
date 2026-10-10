package conpty

import (
	"math/bits"
	"os"
	"strconv"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/processor"
)

// processorThreads is the mask of the processor threads process may run on.
func processorThreads(t *testing.T, process windows.Handle) uintptr {
	t.Helper()
	threads, err := processor.ProcessThreads(process)
	if err != nil {
		t.Fatal(err)
	}
	return threads
}

// startedBy has the terminal's process start a child of its own with command,
// which the child answers with reply and its pid, and returns a handle on that
// child. The test ends it, since one that left the job outlives the console.
func startedBy(t *testing.T, console *Console, output *screen, command, reply string) windows.Handle {
	t.Helper()
	typeLine(t, console, command)
	pid, err := strconv.Atoi(output.waitFor(t, reply+` (\d+)`)[1])
	if err != nil {
		t.Fatal(err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(process, 1)
		windows.CloseHandle(process)
	})
	return process
}

// Goblin work took every processor core at the priority of the Overlord's own
// apps (2026-10-10: a process launch took 1.70 s under 16 busy threads on
// every core, against 0.13 s with the same work kept off two performance
// cores). A terminal given the processor threads it may run on keeps its
// process and everything it starts to them, a process that left the
// terminal's job too. Any other terminal runs on the threads of whatever
// started it.
func TestATerminalKeepsToTheProcessorThreadsItIsGiven(t *testing.T) {
	own := processorThreads(t, windows.CurrentProcess())
	if bits.OnesCount(uint(own)) < 2 {
		if os.Getenv("CI") == "true" {
			t.Fatalf("CI runs this test on the processor threads %#x, fewer than two, so a terminal given fewer went untested", own)
		}
		t.Skipf("this test runs on the processor threads %#x, fewer than two, so a terminal cannot be given fewer", own)
	}
	fewer := own &^ (1 << (bits.Len(uint(own)) - 1))
	for _, test := range []struct {
		name             string
		processors, want uintptr
	}{
		{"a terminal given processor threads", fewer, fewer},
		{"any other terminal", 0, own},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			console, output := startChild(t, Spec{Cols: 80, Rows: 25, Processors: test.processors})

			// Act
			inJob := startedBy(t, console, output, "spawn-attached", "grandchild")
			outOfJob := startedBy(t, console, output, "spawn-breakaway", "broke away")

			// Assert
			for _, process := range []struct {
				what   string
				handle windows.Handle
			}{
				{"the terminal's process", console.process},
				{"a process it started", inJob},
				{"a process it started outside the terminal's job", outOfJob},
			} {
				if got := processorThreads(t, process.handle); got != test.want {
					t.Errorf("%s may run on the processor threads %#x, want %#x", process.what, got, test.want)
				}
			}
		})
	}
}
