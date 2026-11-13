//go:build !windows

package main

// Only Windows-only tests start this binary as one of their programs or
// stand-ins, or leave session programs behind, so on any other system each of
// these finds nothing to do.

func runSourceResumeTestProgram() (int, bool) { return 0, false }

func runWindowsTestProgram() (int, bool) { return 0, false }

func runWindowsStandIn() (int, bool) { return 0, false }

func removeSessionPrograms() {}
