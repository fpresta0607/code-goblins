// Command leftovers proves an uninstall leaves the machine as it found it.
// It takes a snapshot of what a Windows user profile and the machine around
// it hold that an install could change: every file and folder of the
// profile, every key under HKCU\Software and the values where Windows keeps
// what starts at login, what is installed, the environment and what shows
// notifications, each PATH entry, every scheduled task, credential, running
// program, drive and attached virtual disk. Then it compares the snapshot
// taken before an install with the one taken after its uninstall and names
// every difference: a thing left behind, a thing taken that was not the
// install's, and a thing of the person's own that holds something else.
//
// Usage, on a clean Windows profile:
//
//	leftovers snapshot before.json [--also <folder>]... [--content <path>]...
//	... install, use, uninstall ...
//	leftovers snapshot after.json [--also <folder>]... [--content <path>]...
//	leftovers compare before.json after.json [--kept <path>]... [--report <file>]
//
// compare exits 1 while any difference is left that neither a path the
// uninstall named as kept nor one of the noise rules, the writes Windows and
// the runner make by themselves, passes over. What was passed over goes to
// the report, each with why.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// repeated is a flag given any number of times.
type repeated []string

func (r *repeated) String() string     { return fmt.Sprint(*r) }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "leftovers: snapshot <file> or compare <before> <after>")
		return 2
	}
	switch args[0] {
	case "snapshot":
		return runSnapshot(args[1:], stdout, stderr)
	case "compare":
		return runCompare(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "leftovers: unknown command %q\n", args[0])
	return 2
}

func runSnapshot(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var opts options
	flags.Var((*repeated)(&opts.also), "also", "another folder to list")
	flags.Var((*repeated)(&opts.content), "content", "a folder or file whose small files are hashed")
	if len(args) == 0 {
		fmt.Fprintln(stderr, "leftovers snapshot: the file to write is required")
		return 2
	}
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	snapshot, err := collect(opts)
	if err != nil {
		fmt.Fprintln(stderr, "leftovers snapshot:", err)
		return 1
	}
	if err := writeSnapshot(args[0], snapshot); err != nil {
		fmt.Fprintln(stderr, "leftovers snapshot:", err)
		return 1
	}
	fmt.Fprintf(stdout, "leftovers: %d files and folders, %d registry keys, %d registry values, %d PATH entries, %d scheduled tasks, %d credentials, %d programs running, %d drives, %d virtual disks in %s\n",
		snapshot.count(kindFile), snapshot.count(kindRegistryKey), snapshot.count(kindRegistryValue), snapshot.count(kindPathEntry),
		snapshot.count(kindTask), snapshot.count(kindCredential), snapshot.count(kindProcess), snapshot.count(kindVolume), snapshot.count(kindVirtualDisk), args[0])
	for _, unread := range snapshot.Unread {
		fmt.Fprintln(stdout, "leftovers: could not read", unread)
	}
	return 0
}

// least are the fewest things of each kind a profile holds, which a snapshot
// that reads nothing falls short of: a comparison of two empty snapshots
// would find nothing left behind.
var least = map[string]int{kindFile: 100, kindRegistryKey: 100, kindPathEntry: 1, kindProcess: 10, kindVolume: 1, kindTask: 1}

func runCompare(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("compare", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var kept repeated
	flags.Var(&kept, "kept", "a folder or file the uninstall named as kept")
	report := flags.String("report", "", "a file to write every difference to, with why each passed over was")
	if len(args) < 2 {
		fmt.Fprintln(stderr, "leftovers compare: the snapshots before and after are required")
		return 2
	}
	if err := flags.Parse(args[2:]); err != nil {
		return 2
	}
	before, err := readSnapshot(args[0])
	if err != nil {
		fmt.Fprintln(stderr, "leftovers compare:", err)
		return 1
	}
	after, err := readSnapshot(args[1])
	if err != nil {
		fmt.Fprintln(stderr, "leftovers compare:", err)
		return 1
	}
	for _, snapshot := range []struct {
		name string
		Snapshot
	}{{args[0], before}, {args[1], after}} {
		for kind, fewest := range least {
			if snapshot.count(kind) < fewest {
				fmt.Fprintf(stderr, "leftovers compare: %s holds %d of kind %s, fewer than any profile holds, so it read nothing there\n", snapshot.name, snapshot.count(kind), kind)
				return 1
			}
		}
	}
	remaining, over := compare(before, after, kept, noise())
	if *report != "" {
		data, err := json.MarshalIndent(struct {
			Remaining []difference `json:"remaining"`
			Passed    []passed     `json:"passed"`
		}{remaining, over}, "", " ")
		if err == nil {
			err = fsx.AtomicWriteFile(*report, data)
		}
		if err != nil {
			fmt.Fprintln(stderr, "leftovers compare: write the report:", err)
			return 1
		}
	}
	for _, d := range remaining {
		fmt.Fprintln(stdout, d)
	}
	fmt.Fprintf(stdout, "leftovers: %d differences left, %d passed over as kept or as noise\n", len(remaining), len(over))
	if len(remaining) > 0 {
		return 1
	}
	return 0
}
