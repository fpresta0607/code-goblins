package home

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// SharedTempDir is the one folder in a scratch root that is no task's: every
// goblin's TMP names it, beside the scratch folders its TEMP, TMPDIR and
// GOTMPDIR name.
//
// It exists because of how Git Bash decides /tmp. Its msys runtime mounts
// /tmp once for every msys process of a user: the first process to start
// while no other runs asks Windows for the temporary folder, which is TMP,
// else TEMP, else the user's profile, and every later process shares that
// mount, whatever its own TMP says, until the last one ends. A goblin whose
// TMP named its scratch folder therefore made that folder the machine's /tmp
// whenever its shell came first, and cleaning the task up took /tmp from
// every Git Bash on the machine (2026-10-09). TMP is the one variable the
// runtime reads, so it is the one that cannot be a task's.
//
// The name begins with a dot, which no task id may, so no task's scratch
// folder can be this one.
const SharedTempDir = ".tmp"

// SharedTemp is the folder new goblins' TMP names.
func (h Home) SharedTemp() string { return filepath.Join(h.Scratch(), SharedTempDir) }

// SharedTempBeside is the shared temporary folder that goes with a task's
// scratch folder: the one beside it, in the same scratch root, so a task keeps
// both on one drive wherever its record put its scratch folder.
func SharedTempBeside(scratch string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(scratch)), SharedTempDir)
}

// msysRuntime is the library every msys program loads from its own folder.
const msysRuntime = "msys-2.0.dll"

// liveTempWait bounds asking one runtime what its /tmp is.
const liveTempWait = 20 * time.Second

// LiveTemp is the folder one running msys runtime, such as Git Bash's, has
// mounted as /tmp for every shell of the user.
type LiveTemp struct {
	// Runtime is the folder holding the runtime's msys-2.0.dll.
	Runtime string
	// Folder is its /tmp as a Windows path.
	Folder string
}

// LiveTemps asks every msys runtime that has a running process what its /tmp
// is. Only a runtime with a running process holds a mount: the mount goes
// with its last process, and the next first process decides again.
//
// Nothing a running shell sees changes: each runtime is asked by its own
// cygpath, which reads the mount the running processes share. Should the
// last of them end before cygpath starts, cygpath is the runtime's first
// process and decides /tmp itself for as long as it runs, so it is given
// safeTemp, a folder that is never removed, as its TMP and TEMP.
//
// An error means some runtime could not be asked, so which folders are a live
// /tmp is unknown, and a caller about to remove a folder leaves it.
//
// A test binary asks nothing and is told no runtime runs. Its safeTemp is a
// folder of the test's own, which goes when the test ends, and a cygpath of
// the machine's Git Bash started with it could be that runtime's first
// process: the test would then have made a folder it removes the machine's
// /tmp, which is the defect this exists to end. A test that needs an answer
// gives its own in place of this function.
func LiveTemps(ctx context.Context, safeTemp string) ([]LiveTemp, error) {
	if isTestBinary() {
		return nil, nil
	}
	images, err := processImages()
	if err != nil {
		return nil, fmt.Errorf("home: list the running processes: %w", err)
	}
	return liveTemps(ctx, images, safeTemp)
}

// liveTemps is LiveTemps over a list of running programs' paths.
func liveTemps(ctx context.Context, images []string, safeTemp string) ([]LiveTemp, error) {
	if !filepath.IsAbs(safeTemp) {
		return nil, fmt.Errorf("home: %q is no absolute folder to ask a msys runtime from", safeTemp)
	}
	if err := os.MkdirAll(safeTemp, 0o755); err != nil {
		return nil, err
	}
	var temps []LiveTemp
	asked := map[string]bool{}
	for _, image := range images {
		runtime := filepath.Dir(image)
		key := strings.ToLower(runtime)
		if asked[key] {
			continue
		}
		asked[key] = true
		if _, err := os.Stat(filepath.Join(runtime, msysRuntime)); err != nil {
			continue
		}
		folder, err := askLiveTemp(ctx, runtime, safeTemp)
		if err != nil {
			return nil, fmt.Errorf("home: the msys runtime in %s is running and could not be asked what its /tmp is: %w", runtime, err)
		}
		temps = append(temps, LiveTemp{Runtime: runtime, Folder: folder})
	}
	return temps, nil
}

// askLiveTemp runs runtime's cygpath for the Windows path of /tmp.
func askLiveTemp(ctx context.Context, runtime, safeTemp string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, liveTempWait)
	defer cancel()
	environment := []string{"TMP=" + safeTemp, "TEMP=" + safeTemp}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "TMP") && !strings.EqualFold(name, "TEMP") {
			environment = append(environment, entry)
		}
	}
	result, err := execx.OSRunner{}.Run(ctx, execx.Request{Name: filepath.Join(runtime, "cygpath.exe"), Args: []string{"-w", "/tmp"}, Env: environment, KillTree: true})
	if err != nil {
		return "", err
	}
	folder := strings.TrimSpace(string(result.Stdout))
	if result.ExitCode != 0 || !filepath.IsAbs(folder) {
		return "", fmt.Errorf("cygpath -w /tmp exited %d and printed %q: %s", result.ExitCode, folder, strings.TrimSpace(string(result.Stderr)))
	}
	return filepath.Clean(folder), nil
}

// LiveTempIn names the runtime whose /tmp is folder or a folder inside it,
// so removing folder would take /tmp from every shell of that runtime.
func LiveTempIn(folder string, temps []LiveTemp) (LiveTemp, bool) {
	folder = physical(folder)
	for _, temp := range temps {
		if rel, err := filepath.Rel(folder, physical(temp.Folder)); err == nil && filepath.IsLocal(rel) {
			return temp, true
		}
	}
	return LiveTemp{}, false
}

// physical is path with its links and short names resolved where it exists,
// so two spellings of one folder compare equal, and cleaned where it does not.
func physical(path string) string {
	if resolved, err := fsx.Canonical(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

// ErrLiveTempUnknown is why RemoveScratch left a folder it could not prove is
// no running msys runtime's /tmp.
var ErrLiveTempUnknown = errors.New("whether it is a running Git Bash's /tmp could not be read")

// RemoveScratch removes a task's scratch folder unless a running msys runtime
// has it, or a folder inside it, mounted as /tmp, and names that runtime's
// folder when it leaves the folder in place for that. Whoever removes a
// scratch folder removes it here: cleanup, a failed spawn's teardown and the
// janitor. A goblin started since TMP became the shared folder cannot make
// its scratch folder /tmp, but one started before can, as can anything a
// goblin starts with a TMP of its own, so the mount is read every time.
//
// liveTemps is LiveTemps, or a test's answer in its place. When it cannot say,
// the folder stays and the error is an ErrLiveTempUnknown: a folder kept a
// day longer costs disk, and a /tmp removed costs every Git Bash on the
// machine.
func RemoveScratch(ctx context.Context, folder string, liveTemps func(ctx context.Context, safeTemp string) ([]LiveTemp, error)) (heldBy string, err error) {
	if _, statErr := os.Stat(folder); errors.Is(statErr, os.ErrNotExist) {
		return "", nil
	}
	temps, err := liveTemps(ctx, SharedTempBeside(folder))
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrLiveTempUnknown, err)
	}
	if temp, held := LiveTempIn(folder, temps); held {
		return temp.Runtime, nil
	}
	return "", os.RemoveAll(folder)
}
