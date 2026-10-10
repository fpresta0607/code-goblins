package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/release"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
	"github.com/fpresta0607/code-goblins/internal/update"
)

// Bounds on reading and downloading a release, and on the new build's
// install, which brings the home's contract, skills and hooks up to date and
// fetches skills over the network: past releaseRefreshWait it is ended, so
// the card's last step never runs without end, and the update ends as one
// whose home refresh is still to finish.
var (
	releaseCheckWait    = 30 * time.Second
	releaseDownloadWait = 10 * time.Minute
	releaseRefreshWait  = 5 * time.Minute
)

// Seams a test replaces: who may update, and how a download's signature is
// read.
var (
	updaterRefusal = func(h home.Home) error {
		ancestry, err := proc.Ancestry(os.Getpid(), 32)
		if err != nil {
			ancestry = nil
		}
		return supervisor.UpdaterRefusal(h.State, ancestry, os.Environ())
	}
	readSignature release.SignatureReader = release.WindowsSignature
)

// releaseSteps are the steps an update from a release says as it begins
// each, the way the install says its own.
var releaseSteps = []string{"Download Code Goblins %s", "Check the download", "Install Code Goblins %s", "Bring the home up to date"}

func releaseStep(out io.Writer, n int, tag string) {
	title := releaseSteps[n-1]
	if strings.Contains(title, "%s") {
		title = fmt.Sprintf(title, tag)
	}
	fmt.Fprintf(out, "[%d/%d] %s\n", n, len(releaseSteps), title)
}

// installedBuild reports whether program is one of the home's installed
// programs: cfo.exe or goblins.exe in its bin, or at its root, where an older
// install kept them and every install keeps them current.
func installedBuild(h home.Home, program string) bool {
	folder := filepath.Clean(filepath.Dir(program))
	if !sameHomePath(folder, h.Bin()) && !sameHomePath(folder, h.Root) {
		return false
	}
	for _, name := range update.Aliases {
		if strings.EqualFold(filepath.Base(program), name) {
			return true
		}
	}
	return false
}

// releaseUpdate updates the home from the newest published release: with
// check it only says how this build stands against it. Otherwise it downloads
// the release's programs, keeps them only once they match the release's
// SHA256SUMS and, for a signed release, its publisher's signature, then runs
// the downloaded build's own update, which swaps it in, restarts only the
// supervisor and rolls back a build that does not serve, and finally brings
// the home's contract, skills and hooks up to date with the new build's
// install. to, when given, names the release the Overlord chose, and a newer
// one published since is refused rather than installed in its place. pressed
// names the Update item he pressed in the Command Center, which ran this
// command: the grant the board wrote for his click stands for him there, in
// place of a terminal of his own.
func releaseUpdate(h home.Home, check bool, to, pressed string, stdout, stderr io.Writer) int {
	source, err := release.SourceFromEnvironment()
	if err != nil {
		fmt.Fprintf(stderr, "cfo update: %v\n", err)
		return 1
	}
	switch {
	case pressed != "":
		if err := supervisor.TakeUpdateGrant(h.State, pressed, to); err != nil {
			fmt.Fprintf(stderr, "Failed: %v. Nothing was changed.\n", err)
			return 1
		}
	case !check:
		if err := updaterRefusal(h); err != nil {
			fmt.Fprintf(stderr, "cfo update: %v\n", err)
			return 1
		}
	}
	// One update from a release downloads into the home at a time; the
	// build it installs takes the update's own lock.
	if _, err := lock.AcquireExclusiveNamed(update.Dir(h.State), ".release.lock"); err != nil {
		fmt.Fprintf(stderr, "cfo update: another update of this home from a release is running: %v\n", err)
		return 1
	}
	defer lock.ReleaseExclusiveNamed(update.Dir(h.State), ".release.lock")

	ctx, cancel := context.WithTimeout(context.Background(), releaseCheckWait)
	latest, err := release.Latest(ctx, http.DefaultClient, source, h.State)
	cancel()
	if err != nil {
		fmt.Fprintf(stderr, "Failed: the newest release of Code Goblins could not be read (%v). Nothing was changed.\n", err)
		return 1
	}
	if to != "" && to != latest.Tag {
		fmt.Fprintf(stderr, "Failed: the newest release is now %s, not %s, so nothing was changed. Press Update on the newer one, or run goblins update.\n", latest.Tag, to)
		return 1
	}
	standing := release.StandingOf(version, latest.Tag)
	if standing == release.FromSource {
		fmt.Fprintf(stdout, "Code Goblins %s is published (%s).\n", latest.Tag, latest.Page)
		fmt.Fprintln(stdout, "This Code Goblins was built from a clone, so it updates from that clone: in PowerShell, in the clone, run git pull, then .\\install.cmd -Dev.")
		if check {
			return 0
		}
		return 1
	}
	switch standing {
	case release.UpToDate:
		fmt.Fprintf(stdout, "Code Goblins %s runs here, the newest release, so there is nothing to update.\n", version)
		return 0
	case release.Ahead:
		fmt.Fprintf(stdout, "Code Goblins %s runs here, newer than the newest release, %s, so there is nothing to update.\n", version, latest.Tag)
		return 0
	}
	fmt.Fprintf(stdout, "Code Goblins %s runs here, and %s was published %s (%s).\n", version, latest.Tag, latest.Published.Local().Format("2006-01-02"), latest.Page)
	if check {
		if lines := release.WhatsNew(latest.Notes, 5); len(lines) > 0 {
			fmt.Fprintln(stdout, "What's new:")
			for _, line := range lines {
				fmt.Fprintf(stdout, "  - %s\n", line)
			}
		}
		fmt.Fprintln(stdout, "To update, run goblins update in a terminal of your own, or press Update in the Command Center.")
		return 0
	}
	releaseStep(stdout, 1, latest.Tag)
	dir := filepath.Join(update.Dir(h.State), "release", latest.Tag)
	ctx, cancel = context.WithTimeout(context.Background(), releaseDownloadWait)
	download, err := release.Fetch(ctx, http.DefaultClient, source, latest, dir, readSignature, func() {
		releaseStep(stdout, 2, latest.Tag)
	})
	cancel()
	if err != nil {
		fmt.Fprintf(stderr, "Failed: %v.\n", strings.TrimSuffix(err.Error(), "."))
		return 1
	}
	// The download leaves once the build it holds has run its update; what
	// that update keeps for its way back is its own copy.
	defer os.RemoveAll(dir)
	for _, name := range []string{release.Program, release.Window} {
		if sum, ok := download.Sums[name]; ok {
			fmt.Fprintf(stdout, "      %s matches the release's SHA256SUMS: %s\n", name, sum)
		}
	}
	if download.Publisher == "" {
		fmt.Fprintln(stdout, "      The release is unsigned, so its SHA-256 sums are what is checked.")
	} else {
		fmt.Fprintf(stdout, "      Each is validly signed by %s.\n", download.Publisher)
	}

	releaseStep(stdout, 3, latest.Tag)
	// The build's own update bounds itself, so it gets no bound here.
	switch code := runProgram(h, download.Program, []string{"update"}, 0, stdout, stderr); code {
	case updateInstalled:
	default:
		// The line the card ends on: how it went in one sentence, with
		// nothing to paste and nothing to look up. What the build printed
		// above it says why, for whoever opens the output.
		fmt.Fprintln(stderr, notInstalled(code, latest.Tag))
		return code
	}

	// The install brings up to date the home the machine names, so it runs
	// only where that is this home.
	releaseStep(stdout, 4, latest.Tag)
	installCode := 0
	switch target, err := installTarget(); {
	case err != nil:
		fmt.Fprintf(stderr, "cfo update: which home this machine's install names could not be read (%v).\n", err)
		installCode = 1
	case !sameHomePath(target.Root, h.Root):
		fmt.Fprintf(stdout, "Note: Code Goblins %s runs, but this machine's install names the home %s, not this one, so this home's contract, skills and hooks were left as they were.\n", latest.Tag, target.Root)
	default:
		installCode = runProgram(h, filepath.Join(h.Programs(), "cfo.exe"), []string{"install"}, releaseRefreshWait, stdout, stderr)
	}
	if installCode != 0 {
		fmt.Fprintf(stdout, "Updated: Code Goblins %s runs, but its install did not bring the home's contract, skills and hooks up to date (exit code %d), and goblins install finishes that.\n", latest.Tag, installCode)
		return updateHomeIncomplete
	}
	fmt.Fprintf(stdout, "Updated: Code Goblins %s runs.\n", latest.Tag)
	return updateInstalled
}

// notInstalled is the line an update from a release ends on when the build it
// ran did not install: which build serves now, by that build's exit code.
// Every one of them leaves the next update, which Try again on the card
// runs, to put right by itself whatever this one left.
func notInstalled(code int, tag string) string {
	switch code {
	case updateRolledBack:
		return fmt.Sprintf("Rolled back: Code Goblins %s was not installed, and %s serves again.", tag, version)
	case updateDegraded:
		return fmt.Sprintf("Rolled back: Code Goblins %s was not installed, and %s serves again from a kept copy until the next update repairs its files.", tag, version)
	case updateBoardDown:
		return fmt.Sprintf("Failed: Code Goblins %s was not installed, and the board is down until Code Goblins is opened again.", tag)
	case 1:
		return fmt.Sprintf("Failed: Code Goblins %s was not installed, and nothing was changed.", tag)
	}
	return fmt.Sprintf("Failed: Code Goblins %s was not installed.", tag)
}

// runProgram runs program with arguments for the home, its output with this
// command's, and returns its exit code. With a wait it ends a program that
// runs past it, says so, and returns 1.
func runProgram(h home.Home, program string, arguments []string, wait time.Duration, stdout, stderr io.Writer) int {
	ctx := context.Background()
	if wait > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, wait)
		defer cancel()
	}
	command := execx.CommandContext(ctx, program, arguments...)
	command.Dir = h.Root
	command.Stdout, command.Stderr = stdout, stderr
	command.WaitDelay = 2 * time.Second
	err := command.Run()
	var exit *exec.ExitError
	switch {
	case ctx.Err() != nil:
		fmt.Fprintf(stderr, "cfo update: %s %s took longer than %s, so it was stopped\n", filepath.Base(program), strings.Join(arguments, " "), wait)
		return 1
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	}
	fmt.Fprintf(stderr, "cfo update: run %s: %v\n", program, err)
	return 1
}
