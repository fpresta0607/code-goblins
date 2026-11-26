package pty

import (
	"fmt"
	"math/bits"
	"os/exec"
	"runtime"

	"golang.org/x/sys/unix"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// startTree starts cmd as a tree (execx.StartTree) whose first process may
// run on the processor threads of the mask processors only, 0 for no limit.
// Linux keeps a mask for each thread, and a process starts with that of the
// thread that started it, so cmd starts from this goroutine's thread kept to
// processors, which then gets its own threads back. Go starts no thread from
// a locked one, so no other takes the mask meanwhile.
func startTree(cmd *exec.Cmd, processors uintptr) (*execx.Tree, error) {
	if processors == 0 {
		return execx.StartTree(cmd)
	}
	runtime.LockOSThread()
	var own, set unix.CPUSet
	if err := unix.SchedGetaffinity(0, &own); err != nil {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("read the processor threads this thread may run on: %w", err)
	}
	for thread := range bits.UintSize {
		if processors&(1<<thread) != 0 {
			set.Set(thread)
		}
	}
	if err := unix.SchedSetaffinity(0, &set); err != nil {
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("keep the process to the processor threads %#x: %w", processors, err)
	}
	tree, err := execx.StartTree(cmd)
	// A thread that cannot get its threads back stays locked to this
	// goroutine, so no other goroutine runs on it.
	if restore := unix.SchedSetaffinity(0, &own); restore == nil {
		runtime.UnlockOSThread()
	}
	return tree, err
}
