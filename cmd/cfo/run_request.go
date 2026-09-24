package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// runRunRequest asks the Overlord to run a command with one click from the
// Command Center, from the registered primary CFO only:
//
//	cfo run-request --id <stable-id> --title "<why>" --shell powershell|pwsh|bash [--admin] [--cwd <dir>] --command-file <path>
func runRunRequest(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	f := flag.NewFlagSet("run-request", flag.ContinueOnError)
	f.SetOutput(stderr)
	var req supervisor.RunRequest
	f.StringVar(&req.ID, "id", "", "stable ID for this item: 8 to 128 letters, digits, dots, dashes or underscores")
	f.StringVar(&req.Title, "title", "", "why the Overlord should run it")
	f.StringVar(&req.Shell, "shell", "", "powershell (Windows PowerShell 5.1), pwsh (PowerShell 7) or bash (Git Bash)")
	f.BoolVar(&req.Admin, "admin", false, "run it as administrator through Windows UAC")
	f.StringVar(&req.Cwd, "cwd", "", "the folder it runs in; the CFO home when omitted")
	f.StringVar(&req.CommandFile, "command-file", "", "the file holding the exact command; read once")
	if err := f.Parse(args); err != nil || f.NArg() != 0 {
		return 2
	}
	if req.CommandFile == "" {
		fmt.Fprintln(stderr, "cfo run-request: --command-file is required")
		return 2
	}
	h, err := runtime.resolveHome()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := supervisor.PublishRun(ctx, h, &herdr.Client{Commands: execx.OSRunner{}}, req); err != nil {
		fmt.Fprintln(stderr, "cfo run-request: "+err.Error())
		return 1
	}
	fmt.Fprintln(stdout, "Run item published:", req.ID)
	return 0
}
