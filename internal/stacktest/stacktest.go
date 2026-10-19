// Package stacktest moves a goroutine's stack inside a call, for the tests of
// what hands Windows a buffer: a buffer on the stack, passed as a bare
// uintptr, is left behind when the stack moves on the way to the system call
// (issues 1060 and 1110).
package stacktest

// FailingDepths calls check on a fresh goroutine below every depth up to
// 2,000 frames, which brings the point where the stack must grow, and move,
// to every step of check through four sizes of stack, and returns the depths
// at which check fails.
func FailingDepths(check func() bool) []int {
	var failing []int
	for depth := range 2000 {
		isPassed := false
		done := make(chan struct{})
		go func() {
			defer close(done)
			descend(depth, func() { isPassed = check() })
		}()
		<-done
		if !isPassed {
			failing = append(failing, depth)
		}
	}
	return failing
}

// descend calls call depth frames further down the goroutine's stack.
//
//go:noinline
func descend(depth int, call func()) {
	if depth == 0 {
		call()
		return
	}
	descend(depth-1, call)
}
