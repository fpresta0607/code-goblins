package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// runAuthFetch asks the supervisor to fetch the keys a credential request
// still needs from its reviewed key page:
//
//	cfo auth fetch <request> --task <id>
//
// The supervisor reads them in a Chrome with no window on the project's
// login of the page's provider and stores them as the request's card does.
// This command, and so the goblin, hears the names stored and nothing of
// the page. A page that asks for a sign-in puts the Overlord's sign-in card
// on the board, and the fetch carries on once he has signed in.
func runAuthFetch(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, "usage: cfo auth fetch <request> --task <id>")
		return 2
	}
	request := args[0]
	flags := flag.NewFlagSet("auth fetch", flag.ContinueOnError)
	flags.SetOutput(stderr)
	task := flags.String("task", "", "your task's ID")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return 2
	}
	if state.ValidTaskID(*task) != nil {
		fmt.Fprintln(stderr, "cfo auth fetch: --task names your own task's ID")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	lease, err := runtime.requestBrowser(h, supervisor.BrowserRequest{Action: "fetch", Task: *task, Request: request})
	if err != nil {
		fmt.Fprintf(stderr, "cfo auth fetch: %v\n", err)
		return 1
	}
	if lease.SignIn != "" {
		fmt.Fprintf(stdout, "The %s page asks for a sign-in, so the %s sign-in card is in the Command Center (%s). The fetch carries on by itself once he has signed in, and the CFO hears the names it stores.\n", lease.Provider, lease.Account, lease.SignIn)
		return 0
	}
	fmt.Fprintf(stdout, "fetched %s from the %s key page into the vault, through the %s login. Re-source your auth.ps1 when told to.\n", strings.Join(lease.Fetched, ", "), lease.Provider, lease.Account)
	return 0
}
