package execx

import (
	"cmp"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
)

// Started is how many processes of one program this process started from
// one place in the fleet's code. Every process start makes the kernel a
// token, and on 2026-10-19 about two in five of them outlived their process
// for good, so what a fleet program starts is counted where it starts it.
type Started struct {
	Program string `json:"program"`
	Caller  string `json:"caller"`
	Count   int    `json:"count"`
}

// callerFrames is how many frames outside this package name a start's
// caller: a helper that runs commands for its callers, such as a runner, is
// followed by the code that called it.
const callerFrames = 4

const thisPackage = "github.com/fpresta0607/code-goblins/internal/execx."

var started = struct {
	sync.Mutex
	counts map[[2]string]int
}{counts: map[[2]string]int{}}

// count counts a start of name from the code that called Command or
// CommandContext.
func count(name string) {
	program := strings.TrimSuffix(strings.ToLower(filepath.Base(name)), ".exe")
	pcs := make([]uintptr, 32)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(3, pcs)])
	var callers []string
	for len(callers) < callerFrames {
		frame, more := frames.Next()
		if frame.Function != "" && !strings.HasPrefix(frame.Function, thisPackage) {
			callers = append(callers, place(frame))
		}
		if !more {
			break
		}
	}
	started.Lock()
	started.counts[[2]string{program, strings.Join(callers, " < ")}]++
	started.Unlock()
}

// place names a frame as its package and file, such as
// "supervisor/work_speed.go:317".
func place(frame runtime.Frame) string {
	function := frame.Function[strings.LastIndex(frame.Function, "/")+1:]
	pkg, _, _ := strings.Cut(function, ".")
	return fmt.Sprintf("%s/%s:%d", pkg, filepath.Base(frame.File), frame.Line)
}

// Starts is every process this process started through Command or
// CommandContext, by program and by the code that started it, the most
// first.
func Starts() []Started {
	started.Lock()
	defer started.Unlock()
	starts := make([]Started, 0, len(started.counts))
	for key, n := range started.counts {
		starts = append(starts, Started{Program: key[0], Caller: key[1], Count: n})
	}
	slices.SortFunc(starts, func(a, b Started) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), cmp.Compare(a.Program, b.Program), cmp.Compare(a.Caller, b.Caller))
	})
	return starts
}
