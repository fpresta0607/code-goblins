package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/layout"
)

const homeUsage = `usage: cfo home migrate [--apply --plan <digest>] [--memory-from <dir>]

Lay out a home whose data folder predates the layout AGENTS.md describes
under "The CFO home": create the layout's folders, file every finished task
folder into data\archive\finished and every stale brief into
data\archive\parked exactly as the watcher does, give the backlog its Parked
section, import a harness memory folder into data\memory, and mark the data
as laid out so the watcher files it from then on.

Without --apply it is a dry run and changes nothing in the data folder: it
lists every file it would move, create or change, every task folder it leaves
where it is and why, and the proof that no file is dropped: the file counts
before and after, and a digest over the SHA-256 of every file whose content it
keeps, taken before and after, which must match. It ends with the plan's
digest, "plan: <digest>", and keeps the plan's listing in
state\home-migrate-plans\<digest>.txt.

--apply --plan <digest> makes the plan that dry run printed. It plans again,
from the data folder as it is now and at the time of the dry run, and when
that plan's digest is not <digest> it refuses, changing nothing and backing
nothing up, and prints every line in one plan but not the other. Otherwise it
first copies the whole data folder to state\backups\home-migrate-<time>\data,
verifying every copy, then makes exactly the plan and reads the data folder
back to prove it. It refuses, changing nothing, when anything it would move or
write changed since it planned again.

--memory-from names the harness memory folder to import, which is only
read. By default it is Claude Code's memory folder for the home,
~\.claude\projects\<home path>\memory, when that exists. A fact
the home already has with other content is left as the home has it.
`

var planDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

func runHome(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "move" {
		return runHomeMove(args[1:], stdout, stderr)
	}
	if len(args) == 0 || args[0] != "migrate" {
		fmt.Fprint(stderr, homeUsage+"\n"+homeMoveUsage)
		return 2
	}
	flags := flag.NewFlagSet("home migrate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	apply := flags.Bool("apply", false, "make the plan --plan names after a full backup; without it, a dry run")
	planned := flags.String("plan", "", "the digest of the plan a dry run printed, which --apply makes")
	memoryFrom := flags.String("memory-from", "", "the harness memory folder to import, read only")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() > 0 || *apply != (*planned != "") {
		fmt.Fprint(stderr, homeUsage)
		return 2
	}
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintf(stderr, "cfo home migrate: %v\n", err)
		return 1
	}
	from, err := memoryFolder(h, *memoryFrom)
	if err != nil {
		fmt.Fprintf(stderr, "cfo home migrate: %v\n", err)
		return 1
	}
	if !*apply {
		return dryRun(h, from, *memoryFrom, stdout, stderr)
	}
	approved, err := approvedPlan(h, *planned)
	if err != nil {
		fmt.Fprintf(stderr, "cfo home migrate: %v\n", err)
		return 1
	}
	at, err := time.Parse(time.RFC3339, strings.TrimPrefix(approved[0], layout.PlanTimePrefix))
	if err != nil {
		fmt.Fprintf(stderr, "cfo home migrate: plan %s does not say when it was made: %v\n", *planned, err)
		return 1
	}
	m, err := layout.PlanMigration(h, from, at)
	if err != nil {
		fmt.Fprintf(stderr, "cfo home migrate: %v\n", err)
		return 1
	}
	if m.PlanDigest() != *planned {
		fmt.Fprintf(stderr, "cfo home migrate: the plan for %s is not plan %s any more; nothing was changed or backed up\n", h.Data, *planned)
		onlyApproved, onlyNow := difference(approved, m.Listing()), difference(m.Listing(), approved)
		for _, line := range onlyApproved {
			fmt.Fprintf(stderr, "only in plan %s: %s\n", *planned, line)
		}
		for _, line := range onlyNow {
			fmt.Fprintf(stderr, "only in the plan now: %s\n", line)
		}
		fmt.Fprintln(stderr, "Run cfo home migrate again for a dry run of the plan now.")
		return 1
	}
	fmt.Fprintf(stdout, "cfo home migrate: migrating %s by plan %s\n", h.Data, *planned)
	writeMigration(stdout, m)
	backupDir := filepath.Join(h.State, "backups", "home-migrate-"+time.Now().UTC().Format("20060102T150405Z"))
	result, err := m.Apply(backupDir)
	if err != nil {
		fmt.Fprintf(stderr, "cfo home migrate: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "backup: %d files copied to %s, each verified against what was read\n", len(result.BackedUp), filepath.Join(backupDir, "data"))
	fmt.Fprintf(stdout, "migrated: every file moved or written matches the plan\n")
	for _, rel := range result.Unexpected {
		fmt.Fprintf(stdout, "changed by something else meanwhile: data/%s\n", rel)
	}
	return 0
}

// dryRun prints the migration it would make now and its digest, and keeps
// its listing so --apply can show what differs from it.
func dryRun(h home.Home, from, memoryFrom string, stdout, stderr io.Writer) int {
	m, err := layout.PlanMigration(h, from, time.Now().UTC().Truncate(time.Second))
	if err != nil {
		fmt.Fprintf(stderr, "cfo home migrate: %v\n", err)
		return 1
	}
	digest := m.PlanDigest()
	listing := planListingPath(h, digest)
	if err := os.MkdirAll(filepath.Dir(listing), 0o755); err != nil {
		fmt.Fprintf(stderr, "cfo home migrate: %v\n", err)
		return 1
	}
	if err := fsx.AtomicWriteFile(listing, []byte(strings.Join(m.Listing(), "\n")+"\n")); err != nil {
		fmt.Fprintf(stderr, "cfo home migrate: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "cfo home migrate: dry run for %s; nothing was changed\n", h.Data)
	writeMigration(stdout, m)
	fmt.Fprintf(stdout, "plan: %s\n", digest)
	command := "cfo home migrate --apply --plan " + digest
	if memoryFrom != "" {
		command += ` --memory-from "` + memoryFrom + `"`
	}
	fmt.Fprintf(stdout, "Run %s to make exactly this change after a full backup.\n", command)
	return 0
}

func planListingPath(h home.Home, digest string) string {
	return filepath.Join(h.State, "home-migrate-plans", digest+".txt")
}

// approvedPlan is the listing a dry run kept for the plan digest names.
func approvedPlan(h home.Home, digest string) ([]string, error) {
	if !planDigest.MatchString(digest) {
		return nil, fmt.Errorf("--plan %s is not a plan digest", digest)
	}
	data, err := fsx.ReadFile(planListingPath(h, digest))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("no dry run printed plan %s; run cfo home migrate first", digest)
	}
	if err != nil {
		return nil, err
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n"), nil
}

// difference is every line of these that others does not have.
func difference(these, others []string) []string {
	has := map[string]bool{}
	for _, line := range others {
		has[line] = true
	}
	var only []string
	for _, line := range these {
		if !has[line] {
			only = append(only, line)
		}
	}
	return only
}

// memoryFolder is the harness memory folder to import: the one named, which
// must exist, or else Claude Code's folder for the home when it exists.
func memoryFolder(h home.Home, named string) (string, error) {
	if named != "" {
		if info, err := os.Stat(named); err != nil || !info.IsDir() {
			return "", fmt.Errorf("--memory-from %s is not a folder", named)
		}
		return filepath.Abs(named)
	}
	folder := layout.ClaudeMemoryFolder(h.Root)
	if folder == "" {
		return "", nil
	}
	if info, err := os.Stat(folder); err == nil && info.IsDir() {
		return folder, nil
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	return "", nil
}

func writeMigration(w io.Writer, m layout.Migration) {
	if m.MemoryFrom != "" {
		fmt.Fprintf(w, "memory: imports %s, which is only read\n", m.MemoryFrom)
	} else {
		fmt.Fprintln(w, "memory: no harness memory folder to import")
	}
	for _, folder := range m.Folders {
		fmt.Fprintf(w, "create folder data/%s\n", folder)
	}
	var moved []string
	for from := range m.Moved {
		moved = append(moved, from)
	}
	sort.Strings(moved)
	for _, move := range m.Moves {
		fmt.Fprintf(w, "file   %s (%s)\n", move.ID, move.Reason)
	}
	for _, from := range moved {
		fmt.Fprintf(w, "move   data/%s -> data/%s  %s\n", from, m.Moved[from], m.Before[from])
	}
	for _, write := range m.Writes {
		after := m.After[write.Path]
		if write.Before == "" {
			fmt.Fprintf(w, "create data/%s  %s  %s\n", write.Path, after, write.Why)
		} else {
			fmt.Fprintf(w, "change data/%s  %s -> %s  %s; keeps every line it had\n", write.Path, write.Before, after, write.Why)
		}
	}
	for _, kept := range m.Kept {
		fmt.Fprintf(w, "stays  data/%s (%s)\n", kept.ID, kept.Reason)
	}
	for _, note := range m.NotImported {
		fmt.Fprintf(w, "not imported: %s\n", note)
	}
	proof := m.Proof()
	fmt.Fprintln(w, "proof:")
	fmt.Fprintf(w, "  before: %d files\n", proof.Before)
	fmt.Fprintf(w, "  after:  %d files: %d moved, %d unchanged, %d changed, %d created\n", proof.After, proof.Moved, proof.Unchanged, proof.Changed, proof.Created)
	fmt.Fprintf(w, "  digest of every kept file's SHA-256, before: %s\n", proof.BeforeDigest)
	fmt.Fprintf(w, "  digest of every kept file's SHA-256, after:  %s\n", proof.AfterDigest)
	fmt.Fprintf(w, "  dropped: %d\n", len(proof.Dropped))
}
