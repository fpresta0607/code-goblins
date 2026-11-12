package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/memory"
)

// runMemoryLesson is the CEO's word on a lesson a done report proposed:
// accept writes it into memory as a new fact scoped to its repository, and
// reject records why it stays out.
//
//	goblins memory lesson accept <id> [--incident] [--supersedes <fact-id>]...
//	goblins memory lesson reject <id> --reason "<why>"
func runMemoryLesson(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 || args[0] != "accept" && args[0] != "reject" {
		fmt.Fprintln(stderr, "goblins memory lesson: accept <id> [--incident] [--supersedes <fact-id>]... or reject <id> --reason \"<why>\"")
		return 2
	}
	action, id := args[0], args[1]
	flags := flag.NewFlagSet("goblins memory lesson "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	acceptance := memory.Acceptance{Now: time.Now()}
	reason := ""
	if action == "accept" {
		flags.BoolVar(&acceptance.IsIncident, "incident", false, "it records something that happened, which fades with age; without it the lesson is a rule, which never fades")
		flags.Func("supersedes", "a memory fact this lesson replaces, which stays as it is and drops out of recall; repeat it for each", func(v string) error {
			acceptance.Supersedes = append(acceptance.Supersedes, v)
			return nil
		})
	} else {
		flags.StringVar(&reason, "reason", "", "why the lesson stays out of memory")
	}
	if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
		return 2
	}
	// A goblin that decided a lesson could put its own into memory unread.
	if os.Getenv(harness.RoleVariable) == harness.RoleGoblin || os.Getenv(gateAgentVariable) != "" {
		fmt.Fprintln(stderr, "goblins memory lesson: a lesson is the CEO's to accept or reject at the drain, never a goblin's or a gate agent's")
		return 2
	}
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if action == "reject" {
		if err := memory.RejectLesson(h.State, id, reason, acceptance.Now); err != nil {
			fmt.Fprintln(stderr, "goblins memory lesson reject: "+err.Error())
			return 1
		}
		fmt.Fprintf(stdout, "rejected %s: %s\n", id, reason)
		return 0
	}
	name, err := memory.AcceptLesson(h.Data, h.State, id, acceptance)
	if err != nil {
		fmt.Fprintln(stderr, "goblins memory lesson accept: "+err.Error())
		return 1
	}
	fmt.Fprintf(stdout, "accepted %s as %s; recall has it within ten seconds, and the next context pack for its repository carries it\n", id, filepath.Join(h.Data, "memory", name+".md"))
	return 0
}
