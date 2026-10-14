//go:build !windows

package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "cg-ci-runner runs only on Windows, where Hyper-V runs its VM")
	os.Exit(1)
}
