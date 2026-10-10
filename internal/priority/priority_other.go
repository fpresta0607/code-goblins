//go:build !windows

// Package priority runs the fleet's own short work one priority class above
// the work it supervises.
package priority

// AboveTheWork changes nothing where Windows' priority classes do not exist.
func AboveTheWork() (restore func()) {
	return func() {}
}
