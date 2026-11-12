package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/cutover"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/githubapi"
	"github.com/fpresta0607/code-goblins/internal/harnessmap"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/installbase"
)

const cutoverUsage = `usage: goblins cutover --dry-run | --plan <digest> | --undo <folder>

Move an installed home from the command's old name to goblins, and the role's
files and records to the CEO's names, run as this build while the fleet is
paused, no supervisor serves the home and no program runs from the home's bin,
such as the desktop window; each form refuses otherwise.

--dry-run changes nothing: it lists every change by kind and ends with the
plan's digest, "plan: <digest>". --plan <digest> plans again, refuses when that
plan is not the one the dry run printed, and otherwise first keeps everything
its undo needs in state\backups\cutover-<time>, then makes the plan. It ends
with the undo command, which names this build by its full path, since the undo
puts the home's bin\goblins.exe back as it was. --undo <folder> undoes the
cutover that folder records, exactly, and removes the folder.
`

func runCutover(args []string, stdout, stderr io.Writer, newMachine func() (cutover.Machine, error)) int {
	flags := flag.NewFlagSet("cutover", flag.ContinueOnError)
	flags.SetOutput(stderr)
	isDryRun := flags.Bool("dry-run", false, "list every change and change nothing")
	planned := flags.String("plan", "", "the digest of the plan a dry run printed, which this makes")
	undo := flags.String("undo", "", "the folder of a cutover to undo")
	chosen := 0
	if err := flags.Parse(args); err == nil {
		for _, isChosen := range []bool{*isDryRun, *planned != "", *undo != ""} {
			if isChosen {
				chosen++
			}
		}
	}
	if chosen != 1 || flags.NArg() > 0 {
		fmt.Fprint(stderr, cutoverUsage)
		return 2
	}
	machine, err := newMachine()
	if err != nil {
		fmt.Fprintf(stderr, "goblins cutover: %v\n", err)
		return 1
	}
	if refusal := supervisorServing(filepath.Join(machine.Root, "state")); refusal != "" {
		fmt.Fprintf(stderr, "goblins cutover: %s\n", refusal)
		return 1
	}
	running, err := machine.RunningFromBin()
	if err != nil {
		fmt.Fprintf(stderr, "goblins cutover: %v\n", err)
		return 1
	}
	// This cutover may run from the home's bin, as the undo of a crossing
	// does: a program it writes or removes there is set aside first, which
	// Windows allows while it runs.
	others := 0
	for _, program := range running {
		if program.PID == os.Getpid() {
			continue
		}
		where := program.Image
		if rel, err := filepath.Rel(machine.Root, program.Image); err == nil {
			where = rel
		}
		fmt.Fprintf(stderr, "goblins cutover: %s (pid %d) runs from this home; close it first\n", where, program.PID)
		others++
	}
	if others > 0 {
		return 1
	}
	if *undo != "" {
		if err := machine.Undo(*undo); err != nil {
			fmt.Fprintf(stderr, "goblins cutover: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "goblins cutover: undid the cutover %s recorded, and removed it\n", *undo)
		return 0
	}
	plan, err := machine.Plan()
	if err != nil {
		fmt.Fprintf(stderr, "goblins cutover: %v\n", err)
		return 1
	}
	if *isDryRun {
		fmt.Fprintf(stdout, "goblins cutover: dry run for the home %s, which changes nothing\n", machine.Root)
		for _, line := range plan.Listing() {
			fmt.Fprintln(stdout, line)
		}
		fmt.Fprintf(stdout, "plan: %s\nMake it with goblins cutover --plan %s\n", plan.Digest(), plan.Digest())
		return 0
	}
	if plan.Digest() != *planned {
		fmt.Fprintf(stderr, "goblins cutover: the plan for %s is not plan %s any more; nothing was changed. Run goblins cutover --dry-run for the plan now.\n", machine.Root, *planned)
		return 1
	}
	folder, err := machine.Apply(plan, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "goblins cutover: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "goblins cutover: moved the home %s by plan %s\n", machine.Root, *planned)
	for _, line := range plan.Listing() {
		fmt.Fprintln(stdout, line)
	}
	quoted := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	fmt.Fprintf(stdout, "undo: & %s cutover --undo %s\n", quoted(machine.Program), quoted(folder))
	return 0
}

// supervisorServing says why the cutover refuses while a supervisor serves
// the home whose state folder is state, or nothing.
func supervisorServing(state string) string {
	record, err := readBoardRecord(state)
	if err != nil {
		return ""
	}
	if boardAlive(context.Background(), record) == nil {
		return fmt.Sprintf("a supervisor serves this home at %s; pause the fleet and stop it with goblins stop first", record.URL)
	}
	return ""
}

// cutoverMachine is this machine as the cutover reads it, around the home
// the user's settings in env name: the old build's, then this build's, which
// a terminal opened before a cutover set them does not hold, and only then
// the home this build resolves.
func cutoverMachine(env installbase.EnvStore) (cutover.Machine, error) {
	root, projects, err := cutover.OldSettings(env)
	if err != nil {
		return cutover.Machine{}, err
	}
	if root == "" {
		if root, _, err = env.Get(installbase.HomeVariable); err != nil {
			return cutover.Machine{}, err
		}
	}
	if root == "" {
		h, err := home.Resolve()
		if err != nil {
			return cutover.Machine{}, err
		}
		root = h.Root
	}
	if projects == "" {
		if projects, _, err = env.Get(install.ProjectsRootVariable); err != nil {
			return cutover.Machine{}, err
		}
	}
	program, err := os.Executable()
	if err != nil {
		return cutover.Machine{}, err
	}
	return machineAt(env, root, projects, program)
}

// crossingMachine is this machine as the crossing of the home h reads it, by
// the build at program, and the projects root the user's settings name, the
// old build's or this build's.
func crossingMachine(env installbase.EnvStore, h home.Home, program string) (cutover.Machine, error) {
	_, projects, err := cutover.OldSettings(env)
	if err != nil {
		return cutover.Machine{}, err
	}
	if projects == "" {
		if projects, _, err = env.Get(install.ProjectsRootVariable); err != nil {
			return cutover.Machine{}, err
		}
	}
	return machineAt(env, h.Root, projects, program)
}

// machineAt is this machine around the home at root, moved by the build at
// program.
func machineAt(env installbase.EnvStore, root, projects, program string) (cutover.Machine, error) {
	commands := execx.OSRunner{}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return cutover.Machine{}, err
	}
	skills, err := iofs.Sub(codegoblins.Skills, ".agents/skills")
	if err != nil {
		return cutover.Machine{}, err
	}
	topics, err := iofs.ReadDir(codegoblins.Contract, "docs/commands")
	if err != nil {
		return cutover.Machine{}, err
	}
	var names []string
	for _, topic := range topics {
		names = append(names, strings.TrimSuffix(topic.Name(), ".md"))
	}
	return cutover.Machine{
		Root:         filepath.Clean(root),
		UserHome:     userHome,
		LocalAppData: os.Getenv("LOCALAPPDATA"),
		Harnesses:    harnessmap.Find(os.Getenv, userHome),
		ProjectsRoot: projects,
		Program:      program,
		Skills:       skills,
		Contract:     codegoblins.Contract,
		Commands:     names,
		Env:          env,
		Jobs:         cutover.TaskScheduler{Commands: commands},
		Vault:        cutover.CredentialManager{},
		Labels:       cutover.GitHubLabels{Client: githubapi.New(commands)},
		Programs:     cutover.RunningPrograms{},
		Link:         junction(commands),
		Git: func(dir string, args ...string) ([]byte, error) {
			result, err := commands.Run(context.Background(), execx.Request{Name: "git", Args: append([]string{"-C", dir}, args...)})
			if err != nil {
				return nil, err
			}
			if result.ExitCode != 0 {
				return nil, fmt.Errorf("git %s exited with code %d: %s", strings.Join(args, " "), result.ExitCode, strings.TrimSpace(string(result.Stderr)))
			}
			return result.Stdout, nil
		},
	}, nil
}
