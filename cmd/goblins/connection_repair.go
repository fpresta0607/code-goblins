package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/project"
)

func runConnectionRepair(args []string, stdout, stderr io.Writer) int {
	if len(args) != 3 {
		fmt.Fprintln(stderr, "usage: goblins connection-repair <project> <connection> <action>")
		return 2
	}
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, "Connection home is unavailable.")
		return 1
	}
	path, err := project.Resolve(args[0], install.MachineProjectsRoot)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		fmt.Fprintln(stderr, "Project folder was not found.")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	environment := connections.NewEnvironment(h.Root, "", h.Data, h.State, nil)
	if err := environment.RepairConnection(ctx, connections.Project{Name: auth.ProjectName(path), Path: path}, args[1], args[2]); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintln(stdout, "Sign-in completed. Rechecking the connection.")
	return 0
}
