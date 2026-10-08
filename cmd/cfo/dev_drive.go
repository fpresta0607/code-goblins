package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/fpresta0607/code-goblins/internal/devdrive"
)

// runDevDrive says what the home's Dev Drive is, or moves the home's
// worktrees, scratch and package caches onto one:
//
//	cfo dev-drive
//	cfo dev-drive move --to <folder>
func runDevDrive(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) > 0 && args[0] == "move" {
		return runDevDriveMove(args[1:], stdout, stderr, runtime)
	}
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: cfo dev-drive [move --to <folder>]")
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
	return 0
}
