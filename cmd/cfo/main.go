// Command cfo is the Chief Fuckaround Officer's tool belt: the compiled,
// Windows-native replacement for upstream First Mate's bash script layer.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/fpresta0607/code-goblins/internal/doctor"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// version is stamped by the release build:
//
//	go build -ldflags "-X main.version=v1.2.3" ./cmd/cfo
var version = "dev"

const usage = `usage: cfo <command> [args]

commands:
  version   print the cfo version
  doctor    check the tools cfo needs (git, gh, claude, herdr, treehouse)
  drain     print or acknowledge the wake queue and recovery episode
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		fmt.Fprintf(stdout, "cfo %s\n", version)
		return 0
	case "doctor":
		checks := doctor.Run()
		for _, c := range checks {
			if c.Err != "" {
				fmt.Fprintf(stdout, "MISSING  %-10s %s (install: %s)\n", c.Name, c.Err, c.Hint)
			} else {
				fmt.Fprintf(stdout, "ok       %-10s %s\n", c.Name, c.Version)
			}
		}
		if !doctor.Healthy(checks) {
			return 1
		}
		return 0
	case "drain":
		h, err := home.Resolve()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return runDrain(h, args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "cfo: unknown command %q\n%s", args[0], usage)
		return 2
	}
}
