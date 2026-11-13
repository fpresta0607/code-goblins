package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx/treetest"
)

// runWindowsTestProgram runs this binary as a program a Windows-only test
// starts that goblins' own commands must not take first, or says that its
// arguments name none: a build stand-in takes serve as its own.
func runWindowsTestProgram() (int, bool) {
	// A build stand-in an update test installs runs as that build.
	if code, ok := runStandInBuild(); ok {
		return code, true
	}
	if len(os.Args) > 2 && os.Args[1] == gateTurnTestCtrlC {
		return sendConsoleCtrlC(os.Args[2]), true
	}
	// The tests of what ends with a gate's run run this binary as a gate's
	// runner and as each process of the tree it runs.
	if code, ok := runGateTreeTestProgram(); ok {
		return code, true
	}
	if len(os.Args) > 2 && os.Args[1] == leftConsoleTestProgram {
		return runLeftConsoleTestProgram(os.Args[2]), true
	}
	if len(os.Args) > 2 && os.Args[1] == ownConsoleTestProgram {
		return runOwnConsoleTestProgram(os.Args[2:]), true
	}
	treetest.Run()
	return 0, false
}

// runWindowsStandIn runs this binary as one of the stand-ins Windows-only
// tests start, or says that it is none of them. It comes after goblins' own
// commands, since a test starts those as the home's goblins.exe too, which
// the session stand-in would otherwise take.
func runWindowsStandIn() (int, bool) {
	if stateDir := os.Getenv(watchLockStandInVariable); stateDir != "" {
		return holdWatchLockAs(stateDir, os.Getenv(watchLockRoleVariable)), true
	}
	if report := os.Getenv(consoleProbeVariable); report != "" {
		return probeConsole(report), true
	}
	if report := os.Getenv(environmentReportVariable); report != "" {
		return reportEnvironment(report), true
	}
	if len(os.Args) > 1 && os.Args[1] == attachTestTerminal {
		attachTestProgram()
		return 0, true
	}
	if len(os.Args) > 1 && os.Args[1] == nativeSendPasteComposer {
		return runNativeSendPasteComposer(os.Args[2:]), true
	}
	if len(os.Args) > 2 && os.Args[1] == attachTestViewer {
		return attachTestView(os.Args[2], os.Args[3:]), true
	}
	if len(os.Args) > 1 && os.Args[1] == authStoreConsole {
		return runAuth(os.Args[2:], os.Stdout, os.Stderr, commandRuntime{}), true
	}
	// A stand-in session's harness runs this binary as goblins.exe, as Claude
	// Code runs the home's goblins for a hook or for a command of its tools.
	if strings.EqualFold(filepath.Base(os.Args[0]), sessionCLI) {
		return runSessionCLI(), true
	}
	// The native CEO test starts this binary as goblins host, and as the
	// claude.exe its terminal runs.
	if len(os.Args) > 1 && os.Args[1] == "host" {
		return runHost(os.Args[2:], os.Stderr), true
	}
	if strings.EqualFold(filepath.Base(os.Args[0]), "claude.exe") {
		return runFakeClaude(), true
	}
	return 0, false
}
