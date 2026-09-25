package main

import (
	"fmt"
	"io"

	"github.com/fpresta0607/code-goblins/internal/host"
)

// runHost runs one goblin terminal in this process until the terminal ends.
// cfo starts it through host.Launch; it is not for running by hand.
func runHost(args []string, stderr io.Writer) int {
	if err := host.RunArgs(args); err != nil {
		fmt.Fprintln(stderr, "cfo host:", err)
		return 1
	}
	return 0
}
