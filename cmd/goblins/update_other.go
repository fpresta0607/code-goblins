//go:build !windows

package main

// endWithParent does nothing yet: ending an update's work with the update
// that bounds it is not ported to this system, whose updater comes later.
func endWithParent() {}
