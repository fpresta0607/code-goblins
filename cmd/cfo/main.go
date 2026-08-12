// Command cfo is the Chief Fuckaround Officer's tool belt: the compiled,
// Windows-native replacement for upstream First Mate's bash script layer.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is stamped by the release build:
//
//	go build -ldflags "-X main.version=v1.2.3" ./cmd/cfo
var version = "dev"

const usage = `usage: cfo <command> [args]

commands:
  version   print the cfo version
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
	default:
		fmt.Fprintf(stderr, "cfo: unknown command %q\n%s", args[0], usage)
		return 2
	}
}
