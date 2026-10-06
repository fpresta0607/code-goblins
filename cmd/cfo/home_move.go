package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/homemove"
	"github.com/fpresta0607/code-goblins/internal/host"
)

const homeMoveUsage = `usage: cfo home move [--to <dir>] [--apply --plan <digest>]

Move the CFO home CFO_HOME names, such as a code-goblins checkout an older
build made its home, to the per-user home every install now uses,
%LOCALAPPDATA%\CodeGoblins, or to --to. Its state, data, config and caches
move; a file the checkout's git tracks is copied so the checkout keeps its
source; the task records' paths into the moved folders follow them; worktrees
stay where their records say they are. Then this build is installed into the
new home, which points CFO_HOME, PATH and the hooks there, and the old home's
own binaries are removed.

Without --apply it is a dry run that changes nothing: it prints, for each
folder, the files and bytes before and after, the records it rewrites, the
proof that nothing is dropped (a digest over the SHA-256 of every file it
keeps as it is, before and after), and the plan's digest, and lists every
move with its SHA-256 in <new home>\home-move-plans\<digest>.txt.

--apply --plan <digest> makes that plan. It refuses while the supervisor or
any goblin's terminal runs from the home, and when the plan made again at the
dry run's time is not <digest>, changing nothing. Afterwards it reads every
moved file back from the new home and proves the digest unchanged.
`

func runHomeMove(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("home move", flag.ContinueOnError)
	flags.SetOutput(stderr)
	to := flags.String("to", "", "the folder to move the home to; the per-user home when not given")
	apply := flags.Bool("apply", false, "make the plan --plan names; without it, a dry run")
	planned := flags.String("plan", "", "the digest of the plan a dry run printed, which --apply makes")
	if err := flags.Parse(args); err != nil || flags.NArg() > 0 || *apply != (*planned != "") {
		fmt.Fprint(stderr, homeMoveUsage)
		return 2
	}
	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintf(stderr, "cfo home move: %v\n", err)
		return 1
	}
	if info, err := os.Stat(h.State); err != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "cfo home move: %s holds no state folder, so it is no home to move\n", h.Root)
		return 1
	}
	target := *to
	if target == "" {
		if target, err = home.DefaultRoot(); err != nil {
			fmt.Fprintf(stderr, "cfo home move: %v\n", err)
			return 1
		}
	}
	if target, err = fsx.AbsClean(target); err != nil {
		fmt.Fprintf(stderr, "cfo home move: %v\n", err)
		return 1
	}
	tracked, err := trackedFiles(h.Root)
	if err != nil {
		fmt.Fprintf(stderr, "cfo home move: list the files the checkout's git tracks: %v\n", err)
		return 1
	}
	if !*apply {
		plan, err := homemove.PlanMove(h.Root, target, tracked, time.Now())
		if err != nil {
			fmt.Fprintf(stderr, "cfo home move: %v\n", err)
			return 1
		}
		listing := moveListingPath(target, plan.Digest())
		if err := os.MkdirAll(filepath.Dir(listing), 0o755); err != nil {
			fmt.Fprintf(stderr, "cfo home move: %v\n", err)
			return 1
		}
		if err := fsx.AtomicWriteFile(listing, []byte(strings.Join(plan.Listing(), "\n")+"\n")); err != nil {
			fmt.Fprintf(stderr, "cfo home move: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "cfo home move: dry run from %s to %s; nothing was changed\n", h.Root, target)
		writeMovePlan(stdout, plan)
		fmt.Fprintf(stdout, "every move, with its SHA-256: %s\n", listing)
		fmt.Fprintf(stdout, "plan: %s\n", plan.Digest())
		fmt.Fprintf(stdout, "At a quiet point, with the supervisor stopped and no goblin running, run cfo home move --to \"%s\" --apply --plan %s with this build.\n", target, plan.Digest())
		return 0
	}

	if !planDigest.MatchString(*planned) {
		fmt.Fprintf(stderr, "cfo home move: --plan %s is not a plan digest\n", *planned)
		return 2
	}
	approved, err := fsx.ReadFile(moveListingPath(target, *planned))
	if err != nil {
		fmt.Fprintf(stderr, "cfo home move: no dry run printed plan %s for %s: %v\n", *planned, target, err)
		return 1
	}
	first, _, _ := strings.Cut(string(approved), "\n")
	stamp, _, _ := strings.Cut(strings.TrimPrefix(first, homemove.PlanTimePrefix), " ")
	at, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		fmt.Fprintf(stderr, "cfo home move: plan %s does not say when it was made: %v\n", *planned, err)
		return 1
	}
	if busy := homeBusy(h); busy != "" {
		fmt.Fprintf(stderr, "cfo home move: %s; a home moves only at a quiet point, so nothing was changed\n", busy)
		return 1
	}
	plan, err := homemove.PlanMove(h.Root, target, tracked, at)
	if err != nil {
		fmt.Fprintf(stderr, "cfo home move: %v\n", err)
		return 1
	}
	if plan.Digest() != *planned {
		fmt.Fprintf(stderr, "cfo home move: the home is not as plan %s found it any more; nothing was changed. Lines that differ:\n", *planned)
		now := plan.Listing()
		for _, line := range difference(strings.Split(strings.TrimRight(string(approved), "\n"), "\n"), now) {
			fmt.Fprintf(stderr, "only in plan %s: %s\n", *planned, line)
		}
		for _, line := range difference(now, strings.Split(strings.TrimRight(string(approved), "\n"), "\n")) {
			fmt.Fprintf(stderr, "only in the plan now: %s\n", line)
		}
		fmt.Fprintln(stderr, "Run cfo home move again for a dry run of the plan now.")
		return 1
	}
	fmt.Fprintf(stdout, "cfo home move: moving %s to %s by plan %s\n", h.Root, target, *planned)
	result, err := plan.Apply()
	if err != nil {
		fmt.Fprintf(stderr, "cfo home move: %v\n", err)
		return 1
	}
	proof := plan.Proof()
	fmt.Fprintf(stdout, "read back: %d of %d files, %d bytes, every one as planned; caches %d files, %d bytes\n", result.Files, proof.Files, result.Bytes, result.CacheFiles, result.CacheBytes)
	fmt.Fprintf(stdout, "  digest of every kept file's SHA-256, before: %s\n", proof.BeforeDigest)
	fmt.Fprintf(stdout, "  digest of every kept file's SHA-256, after:  %s\n", result.AfterDigest)
	if result.AfterDigest != proof.BeforeDigest {
		fmt.Fprintln(stderr, "cfo home move: the digests differ, so the move is not proven; the new home is left as it is for you to compare")
		return 1
	}
	finish := "run cfo install with this build"
	if len(plan.Programs) > 0 {
		finish += ", then remove " + strings.Join(plan.Programs, ", ") + " from " + h.Root
	}
	service, err := installService(target)
	if err != nil {
		fmt.Fprintf(stderr, "cfo home move: the home moved, but this build could not be installed into it: %v; %s\n", err, finish)
		return 1
	}
	fmt.Fprintf(stdout, "cfo home move: installing this build into %s\n", target)
	if err := service.Install(stdout); err != nil {
		fmt.Fprintf(stderr, "cfo home move: the home moved, but the install into it failed: %v; %s\n", err, finish)
		return 1
	}
	for _, left := range removeOldBinaries(h.Root, plan.Programs) {
		fmt.Fprintf(stdout, "left %s: something still runs it; remove it once nothing does\n", left)
	}
	fmt.Fprintf(stdout, "Moved. Claude Code keeps its own memory per folder, so a CFO started in %s finds the fleet's memory in its data\\memory; open a new terminal for CFO_HOME and PATH to take effect.\n", target)
	return 0
}

// trackedFiles names the files the checkout at root's git tracks, or none
// when root is no checkout of its own.
func trackedFiles(root string) (map[string]bool, error) {
	if _, err := os.Stat(filepath.Join(root, ".git")); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, err := execx.OSRunner{}.Run(ctx, execx.Request{Dir: root, Name: "git", Args: []string{"ls-files", "-z"}})
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("git ls-files exited with code %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	tracked := map[string]bool{}
	for _, name := range strings.Split(string(result.Stdout), "\x00") {
		if name != "" {
			tracked[name] = true
		}
	}
	return tracked, nil
}

func moveListingPath(target, digest string) string {
	return filepath.Join(target, "home-move-plans", digest+".txt")
}

// homeBusy says what still runs from the home, or "" when nothing does: its
// supervisor, or any goblin's or the CFO's native terminal.
func homeBusy(h home.Home) string {
	if running, ok := homeSupervisor(h.State); ok {
		return fmt.Sprintf("its supervisor runs (pid %d)", running.pid)
	}
	entries, _ := os.ReadDir(filepath.Join(h.State, "hosts"))
	var live []string
	for _, entry := range entries {
		id, ok := strings.CutSuffix(entry.Name(), ".json")
		if !ok {
			continue
		}
		if record, err := host.ReadRecord(h.State, id); err == nil && host.Running(record) {
			live = append(live, id)
		}
	}
	if len(live) > 0 {
		return "terminals still run: " + strings.Join(live, ", ")
	}
	return ""
}

// removeOldBinaries removes the older build's programs the plan listed at the
// old home's root, now that the new home's bin holds this build, and returns
// those something still runs.
func removeOldBinaries(root string, programs []string) []string {
	var left []string
	for _, name := range programs {
		if err := os.Remove(filepath.Join(root, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			left = append(left, filepath.Join(root, name))
		}
	}
	return left
}

func writeMovePlan(w io.Writer, plan homemove.Plan) {
	folders := map[string][2]int64{}
	for _, file := range plan.Files {
		folder, _, _ := strings.Cut(file.Rel, "/")
		count := folders[folder]
		folders[folder] = [2]int64{count[0] + 1, count[1] + file.Size}
	}
	for _, folder := range []string{"state", "data", "config"} {
		if count, ok := folders[folder]; ok {
			fmt.Fprintf(w, "move   %s: %d files, %d bytes, each with its SHA-256\n", folder, count[0], count[1])
		}
	}
	fmt.Fprintf(w, "move   caches: %d files, %d bytes, counted\n", plan.CacheFiles, plan.CacheBytes)
	for _, file := range plan.Files {
		if file.Copy {
			fmt.Fprintf(w, "copy   %s  %s (the checkout's git tracks it, so the checkout keeps it)\n", file.Rel, file.Hash)
		}
	}
	for _, rewrite := range plan.Rewrites {
		fmt.Fprintf(w, "change %s  %s -> %s (its paths into the moved folders follow them)\n", rewrite.Rel, rewrite.Before, rewrite.After)
	}
	if len(plan.Mentions) > 0 {
		fmt.Fprintf(w, "note   %d other files name the old home in their text and move as they are; the listing names each\n", len(plan.Mentions))
	}
	for _, left := range plan.Left {
		fmt.Fprintf(w, "stays  %s\n", left)
	}
	for _, program := range plan.Programs {
		fmt.Fprintf(w, "remove %s (an older build's program; this build is installed in the new home's bin)\n", program)
	}
	proof := plan.Proof()
	fmt.Fprintln(w, "proof:")
	fmt.Fprintf(w, "  before: %d files, %d bytes; caches %d files, %d bytes\n", proof.Files, proof.Bytes, proof.CacheFiles, proof.CacheBytes)
	fmt.Fprintf(w, "  after:  %d files at the new home (%d of them copied, %d rewritten); caches %d files, %d bytes\n", proof.Files, proof.Copied, proof.Rewritten, proof.CacheFiles, proof.CacheBytes)
	fmt.Fprintf(w, "  digest of every kept file's SHA-256, before: %s\n", proof.BeforeDigest)
	fmt.Fprintf(w, "  digest of every kept file's SHA-256, after:  %s\n", proof.AfterDigest)
}
