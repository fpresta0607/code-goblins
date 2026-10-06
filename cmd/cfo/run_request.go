package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// runRunRequest asks the Overlord to run a command with one click from the
// Command Center, or withdraws such an item nobody ran, from the registered
// primary CFO only; replacing an item is withdrawing it and publishing the
// new command under a new ID:
//
//	cfo run-request --id <stable-id> --title "<why>" --shell powershell|pwsh|bash [--admin] [--interactive] [--cwd <dir>] --command-file <path>
//	cfo run-request --withdraw <id> --reason "<why>"
func runRunRequest(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("run-request", flag.ContinueOnError)
	f.SetOutput(stderr)
	var req supervisor.RunRequest
	f.StringVar(&req.ID, "id", "", "stable ID for this item: 8 to 128 letters, digits, dots, dashes or underscores")
	f.StringVar(&req.Title, "title", "", "why the Overlord should run it")
	f.StringVar(&req.Shell, "shell", "", "powershell (Windows PowerShell 5.1), pwsh (PowerShell 7) or bash (Git Bash)")
	f.BoolVar(&req.Admin, "admin", false, "run it as administrator through Windows UAC")
	f.BoolVar(&req.Interactive, "interactive", false, "run it in the window itself and keep the window open and usable, for a sign-in or anything that needs the console; its output is not kept")
	f.StringVar(&req.Cwd, "cwd", "", "the folder it runs in; the CFO home when omitted")
	f.StringVar(&req.CommandFile, "command-file", "", "the file holding the exact command; read once")
	withdraw := f.String("withdraw", "", "take the item with this ID, which nobody ran yet, off the Command Center")
	reason := f.String("reason", "", "why the item is withdrawn; kept on the item and in state/runs.audit")
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return 2
	}
	switch {
	case *withdraw != "" && strings.TrimSpace(*reason) == "":
		fmt.Fprintln(stderr, "cfo run-request: --withdraw needs --reason")
		return 2
	case *withdraw != "" && (req != supervisor.RunRequest{}):
		fmt.Fprintln(stderr, "cfo run-request: --withdraw takes only --reason")
		return 2
	case *withdraw == "" && *reason != "":
		fmt.Fprintln(stderr, "cfo run-request: --reason goes with --withdraw")
		return 2
	case *withdraw == "" && req.CommandFile == "":
		fmt.Fprintln(stderr, "cfo run-request: --command-file is required")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *withdraw != "" {
		if err := supervisor.WithdrawRun(h, *withdraw, *reason); err != nil {
			fmt.Fprintln(stderr, "cfo run-request: "+err.Error())
			return 1
		}
		fmt.Fprintln(stdout, "Run item withdrawn:", *withdraw)
		return 0
	}
	if err := supervisor.PublishRun(h, req); err != nil {
		fmt.Fprintln(stderr, "cfo run-request: "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, "Run item published:", req.ID)
	return 0
}
