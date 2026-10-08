package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/proc"
)

// runDevDrive says what the home's Dev Drive is, or moves the home's
// worktrees, scratch and package caches onto one:
//
//	cfo dev-drive
//	cfo dev-drive setup
//	cfo dev-drive move --to <folder>
func runDevDrive(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) > 0 && args[0] == "move" {
		return runDevDriveMove(args[1:], stdout, stderr, runtime)
	}
	if len(args) == 1 && args[0] == "setup" {
		return runDevDriveSetup(stdout, stderr, runtime)
	}
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: cfo dev-drive [setup | move --to <folder>]")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	m, err := runtime.readDevDrive(context.Background())
	if err != nil {
		fmt.Fprintln(stderr, "cfo dev-drive: "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, "dev drive: "+devdrive.Describe(m, h).Line)
	fmt.Fprintln(stdout, devdrive.Explain)
	for _, v := range m.Volumes {
		kind := v.FileSystem
		if v.Dev {
			kind += ", Dev Drive, " + string(v.Trust)
		}
		fmt.Fprintf(stdout, "  %s %q %s, %.0f GB free of %.0f GB\n", v.Letter(), v.Label, kind, float64(v.Free)/(1<<30), float64(v.Total)/(1<<30))
	}
	return 0
}

// runDevDriveSetup asks for the next Dev Drive step from a terminal, as Set up
// on the board does: the board puts it in the Command Center.
func runDevDriveSetup(stdout, stderr io.Writer, runtime commandRuntime) int {
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := home.AnswerDevDrive(h.Root, true, time.Now().UTC()); err != nil {
		fmt.Fprintln(stderr, "cfo dev-drive setup: "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, "asked: the board puts the next Dev Drive step in the Command Center within a minute, each step one item to run")
	return 0
}

func runDevDriveMove(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("dev-drive move", flag.ContinueOnError)
	f.SetOutput(stderr)
	to := f.String("to", "", "the folder on a trusted Dev Drive that takes the home's worktrees, scratch and caches, such as D:\\CodeGoblins")
	if err := f.Parse(args); err != nil || f.NArg() != 0 || *to == "" {
		fmt.Fprintln(stderr, "usage: cfo dev-drive move --to <folder>")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	m, err := runtime.readDevDrive(context.Background())
	if err != nil {
		fmt.Fprintln(stderr, "cfo dev-drive move: "+err.Error())
		return 1
	}
	moved, err := devdrive.Move(m, h, *to, time.Now().UTC())
	if err != nil {
		fmt.Fprintln(stderr, "cfo dev-drive move: "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, moved)
	if h.DevDrive == "" {
		restartBoardOntoTheDrive(h, stdout)
	}
	return 0
}

// restartBoardOntoTheDrive restarts the supervisor serving h, if one does, on
// the build it runs: it read the home when it started, so until it starts
// again what it starts itself, a goblin it brings back included, would build
// in the home's own folders. Every goblin's and the CFO's terminal keeps
// running, as they do through cfo update.
func restartBoardOntoTheDrive(h home.Home, stdout io.Writer) {
	running, ok := homeSupervisor(h.State)
	if !ok || provedHomeSupervisor(h, running) != nil {
		return
	}
	keeps := "The board keeps the home's own folders for what it starts itself until it next starts"
	identity, err := proc.Identify(running.pid, running.start)
	if err != nil {
		fmt.Fprintf(stdout, "%s %s: %v.\n", notePrefix, keeps, err)
		return
	}
	address := boardAddress()
	if record, err := readBoardRecord(h.State); err == nil && record.PID == running.pid {
		address = strings.TrimPrefix(record.URL, "http://")
	}
	if h, err = pinHome(h); err == nil {
		err = endSupervisor(h, running)
	}
	if err != nil {
		fmt.Fprintf(stdout, "%s %s: %v.\n", notePrefix, keeps, err)
		return
	}
	started, err := startSupervisor(h, identity.Image, address)
	if err == nil {
		err = awaitSupervisor(h.State, started, true)
	}
	if err != nil {
		fmt.Fprintf(stdout, "%s The board did not start again (%v); opening Code Goblins starts it.\n", notePrefix, err)
		return
	}
	fmt.Fprintf(stdout, "The board restarted (pid %d) and starts new goblins on the Dev Drive.\n", started.pid)
}
