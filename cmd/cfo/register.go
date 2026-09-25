package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/terminal"
)

// runRegister makes the calling session the primary CFO the board delivers
// to. The SessionStart hooks do this on their own; this is the manual refresh.
func runRegister(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("register", flag.ContinueOnError)
	f.SetOutput(stderr)
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return 2
	}
	if os.Getenv(harness.RoleVariable) == harness.RoleGoblin {
		fmt.Fprintln(stderr, "cfo register: a goblin is never the primary CFO")
		return 1
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	described, err := supervisor.Register(ctx, h.State, registerTerminals(), "", "")
	if err != nil {
		fmt.Fprintln(stderr, "cfo register:", err)
		return 1
	}
	fmt.Fprintln(stdout, "Registered the primary CFO:", described)
	return 0
}

// registerTerminals opens Herdr in the session this process runs in, read
// from HERDR_SESSION the way spawn and the watcher read it.
func registerTerminals() terminal.Opener {
	return terminal.HerdrSessions(&herdr.Client{Commands: execx.OSRunner{}, Session: herdrSession()})
}

// registerPrimary refreshes the registration from a SessionStart hook, for
// the session that holds the home only. A session in neither a Herdr pane nor
// a native terminal has nothing the board could reach, so it stays silent.
func registerPrimary(h home.Home, ownerPID int, agent, session string, terminals terminal.Opener, stdout io.Writer) {
	if (os.Getenv("HERDR_PANE_ID") == "" && os.Getenv(host.IDVariable) == "") || !lock.HeldBy(h.State, ownerPID) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	described, err := supervisor.Register(ctx, h.State, terminals, agent, session)
	if err != nil {
		fmt.Fprintf(stdout, "CFO REGISTRATION FAILED: %s. The board cannot reach this session until cfo register succeeds from it.\n", err)
		return
	}
	fmt.Fprintln(stdout, "CFO REGISTRATION:", described)
}
