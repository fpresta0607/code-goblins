//go:build !windows

package watch

// playStandIn reports that this process plays no part: the tests that start
// a stand-in run only on Windows.
func playStandIn() bool { return false }
