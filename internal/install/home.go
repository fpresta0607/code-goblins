package install

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// Claude Code reads a project's skills from .claude/skills and the other
// harnesses from .agents/skills. A checkout makes the one a junction to the
// other; a home set up here gets the files in both.
const (
	agentSkills  = ".agents/skills/"
	claudeSkills = ".claude/skills/"
)

// markerText explains home.InstalledMarker to whoever finds it. After it,
// following a blank line, the marker lists the contract files the install
// wrote, one slash-separated path per line, so the next install can remove
// those its binary no longer ships.
const markerText = "This folder is a Code Goblins CFO home that cfo install set up.\r\n" +
	"The CFO hooks act here only while this file exists, so leave it in place.\r\n" +
	"Below are the contract files it wrote; the next install removes any its binary no longer ships.\r\n" +
	"\r\n"

// refuseAnotherHome keeps an install from taking the machine from a home
// still in use. CFO_HOME naming another primary home means a fleet lives
// there: moving CFO_HOME would start every new session in an empty home,
// leave that home first on PATH, and leave its fleet's state unreachable.
func (s Service) refuseAnotherHome() error {
	current, set, err := s.Env.Get(homeVariable)
	if err != nil {
		return err
	}
	if !set || sameDirectory(current, s.Root) || !home.IsPrimary(home.Home{Root: current, State: filepath.Join(current, "state")}) {
		return nil
	}
	return fmt.Errorf("install: CFO_HOME is %s, a home in use; run this from that home to keep it, or run goblins uninstall there first, then run this again to move to %s", current, s.Root)
}

// writeHome sets up a home outside a checkout: state, the contract, the
// policy files where they are missing, the binary under both its names, and
// last the marker, so a home that failed partway is never taken for a
// primary one. Its data folder is laid out after it, as a checkout's is.
func (s Service) writeHome(report *reporter) error {
	markerPath := filepath.Join(s.Root, home.InstalledMarker)
	previous, hadMarker, err := readManifest(markerPath)
	if err != nil {
		return fmt.Errorf("install: read %s: %w", markerPath, err)
	}
	stateDir := filepath.Join(s.Root, "state")
	createdState := false
	if info, err := os.Stat(stateDir); err != nil || !info.IsDir() {
		if err := os.MkdirAll(stateDir, 0o755); err != nil {
			return fmt.Errorf("install: create %s: %w", stateDir, err)
		}
		createdState = true
	}
	manifest, written, err := s.writeContract()
	if err != nil {
		return err
	}
	if err := s.seedPolicy(report); err != nil {
		return err
	}
	if err := s.copyBinary(report); err != nil {
		return err
	}
	if err := s.copyWindow(report); err != nil {
		return err
	}
	removed, err := s.removeUnshipped(previous, manifest)
	if err != nil {
		return err
	}
	var done []string
	if written > 0 {
		done = append(done, fmt.Sprintf("wrote %d of %d files into %s", written, len(manifest), s.Root))
	}
	if removed > 0 {
		done = append(done, fmt.Sprintf("removed %d files the binary no longer ships", removed))
	}
	if len(done) > 0 {
		report.change("contract", strings.Join(done, "; "))
	} else {
		report.same("contract", fmt.Sprintf("all %d files already current in %s", len(manifest), s.Root))
	}
	if _, err := writeIfDifferent(markerPath, []byte(markerText+strings.Join(manifest, "\r\n")+"\r\n")); err != nil {
		return fmt.Errorf("install: mark %s as a CFO home: %w", s.Root, err)
	}
	switch {
	case createdState:
		report.change("home", "set up "+s.Root+" with its state folder")
	case !hadMarker:
		report.change("home", "marked "+s.Root+" as a CFO home again")
	default:
		report.same("home", s.Root+" is set up")
	}
	return nil
}

// writeContract writes the contract into the home and returns every file it
// covers, slash-separated, and how many of them it had to write.
func (s Service) writeContract() ([]string, int, error) {
	var manifest []string
	written := 0
	err := fs.WalkDir(s.Contract, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := fs.ReadFile(s.Contract, name)
		if err != nil {
			return err
		}
		targets := []string{name}
		if rest, ok := strings.CutPrefix(name, agentSkills); ok {
			targets = append(targets, claudeSkills+rest)
		}
		for _, target := range targets {
			changed, err := writeIfDifferent(filepath.Join(s.Root, filepath.FromSlash(target)), data)
			if err != nil {
				return err
			}
			manifest = append(manifest, target)
			if changed {
				written++
			}
		}
		return nil
	})
	if err != nil {
		return nil, 0, fmt.Errorf("install: write the CFO contract into %s: %w", s.Root, err)
	}
	return manifest, written, nil
}

// readManifest reads the contract files a previous install listed in its
// marker, and whether there was a marker at all.
func readManifest(path string) ([]string, bool, error) {
	data, err := fsx.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	_, list, _ := strings.Cut(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n\n")
	var names []string
	for _, line := range strings.Split(list, "\n") {
		if line != "" {
			names = append(names, line)
		}
	}
	return names, true, nil
}

// removeUnshipped removes each file a previous install listed that the
// running binary no longer ships. Only listed paths are touched, so a file the
// operator added stays, and only those inside the home, since the list is read
// from disk. Paths are compared without case, as Windows resolves them, so a
// skill renamed only in case is not removed from under its new name.
func (s Service) removeUnshipped(previous, manifest []string) (int, error) {
	shipped := make(map[string]bool, len(manifest))
	for _, name := range manifest {
		shipped[strings.ToLower(name)] = true
	}
	removed := 0
	for _, name := range previous {
		local := filepath.FromSlash(name)
		if shipped[strings.ToLower(name)] || !filepath.IsLocal(local) {
			continue
		}
		err := os.Remove(filepath.Join(s.Root, local))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return removed, fmt.Errorf("install: remove %s, which the binary no longer ships: %w", name, err)
		}
		removed++
	}
	return removed, nil
}

func (s Service) seedPolicy(report *reporter) error {
	var written, kept []string
	err := fs.WalkDir(s.Policy, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		target := filepath.Join(s.Root, filepath.FromSlash(name))
		if _, err := os.Stat(target); err == nil {
			kept = append(kept, name)
			return nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		data, err := fs.ReadFile(s.Policy, name)
		if err != nil {
			return err
		}
		if _, err := writeIfDifferent(target, data); err != nil {
			return err
		}
		written = append(written, name)
		return nil
	})
	if err != nil {
		return fmt.Errorf("install: write the default policy into %s: %w", s.Root, err)
	}
	if len(written) > 0 {
		report.change("policy", "wrote the defaults "+strings.Join(written, ", "))
	}
	if len(kept) > 0 {
		report.same("policy", "kept "+strings.Join(kept, ", ")+" as they are: they are yours to tune")
	}
	return nil
}

// copyBinary puts the running binary where the hooks look for it, as
// cfo.exe, and beside it as goblins.exe, its second name. An old copy still
// running, such as the supervisor, cannot be overwritten but can be renamed,
// so it moves aside under a name of its own and goes once nothing runs it,
// on this install or a later one.
func (s Service) copyBinary(report *reporter) error {
	data, err := fsx.ReadFile(s.Binary)
	if err != nil {
		return fmt.Errorf("install: read the running binary %s: %w", s.Binary, err)
	}
	var copied []string
	stillRunning := false
	for _, name := range []string{"cfo.exe", "goblins.exe"} {
		changed, running, err := replaceProgram(filepath.Join(s.Root, name), data)
		if err != nil {
			return err
		}
		if changed {
			copied = append(copied, name)
		}
		stillRunning = stillRunning || running
	}
	if len(copied) == 0 {
		report.same("binary", "cfo.exe and goblins.exe in "+s.Root+" are already this build")
		return nil
	}
	report.change("binary", fmt.Sprintf("copied %s to %s in %s", s.Binary, strings.Join(copied, " and "), s.Root))
	if stillRunning {
		report.detail("the previous build still runs, such as the supervisor; goblins stop, then goblins --board, restarts it on this one")
	}
	return nil
}

// windowName is the desktop window a release ships beside cfo.exe, which
// goblins starts where it sits beside goblins.exe.
const windowName = "goblins-window.exe"

// copyWindow puts the desktop window shipped beside the running binary in the
// home beside goblins.exe. A window that is open is moved aside as the binary
// is. A build with no window beside it, such as one built from source, leaves
// the home as it is: goblins keeps a window the home already holds, and
// otherwise shows the board in the browser.
func (s Service) copyWindow(report *reporter) error {
	source := filepath.Join(filepath.Dir(s.Binary), windowName)
	target := filepath.Join(s.Root, windowName)
	data, err := fsx.ReadFile(source)
	if errors.Is(err, fs.ErrNotExist) {
		if _, err := os.Stat(target); err == nil {
			report.same("window", "no desktop window beside "+s.Binary+"; the home keeps its existing "+target+", and goblins keeps using it")
			return nil
		}
		report.same("window", "no desktop window beside "+s.Binary+"; goblins shows the board in the browser")
		return nil
	}
	if err != nil {
		return fmt.Errorf("install: read the desktop window %s: %w", source, err)
	}
	changed, running, err := replaceProgram(target, data)
	if err != nil {
		return err
	}
	if !changed {
		report.same("window", windowName+" in "+s.Root+" is already this build")
		return nil
	}
	report.change("window", fmt.Sprintf("copied %s to %s in %s", source, windowName, s.Root))
	if running {
		report.detail("the previous window still runs; quit it from its tray icon and goblins opens this one")
	}
	return nil
}

// CarryWindow puts the desktop window shipped beside binary in the home at
// root, as an install does, and writes what it did to out. cfo update carries
// it once the candidate serves: the window is a program of its own that shows
// any build's board, so it follows an update and takes no part in it.
func CarryWindow(root, binary string, out io.Writer) error {
	return Service{Root: root, Binary: binary}.copyWindow(&reporter{out: out})
}

// windowPicture is the notifications' picture the desktop window writes
// beside itself.
const windowPicture = "goblins-window.png"

// adoptEarlierWindow makes this home's desktop window the one the user has,
// where an earlier install kept a copy in a folder of its own: Start at login
// starts this home in that copy's place, and the copy goes once no window
// runs from it. Its Start menu entry is the install script's to replace. A
// home that holds no window leaves the earlier copy as the one there is.
func (s Service) adoptEarlierWindow(report *reporter) error {
	if s.EarlierWindow == "" || sameDirectory(s.EarlierWindow, s.Root) {
		return nil
	}
	if _, err := os.Stat(s.EarlierWindow); err != nil {
		return nil
	}
	if _, err := os.Stat(filepath.Join(s.Root, windowName)); err != nil {
		report.same("window", "kept the earlier desktop window in "+s.EarlierWindow+", since this home holds none")
		return nil
	}
	if err := s.adoptStartAtLogin(report); err != nil {
		return err
	}
	// An open window holds its program, which Windows cannot delete, so it is
	// named rather than ended: quitting it is the user's.
	program := filepath.Join(s.EarlierWindow, windowName)
	err := os.Remove(program)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		report.same("window", fmt.Sprintf("the earlier desktop window %s is still open (%v); quit it from its tray icon, and the next install removes it", program, err))
		return nil
	}
	removed := err == nil
	if os.Remove(filepath.Join(s.EarlierWindow, windowPicture)) == nil {
		removed = true
	}
	// Only an empty folder goes: what else it holds is not this install's.
	switch err := os.Remove(s.EarlierWindow); {
	case err == nil:
		report.change("window", "removed the earlier desktop window in "+s.EarlierWindow+"; this home's window takes its place")
	case removed:
		report.change("window", "removed the earlier desktop window from "+s.EarlierWindow+" and left the folder, which holds other files")
	}
	return nil
}

// replaceProgram writes data to target unless target already holds it, and
// reports whether it wrote and whether an older copy still runs. A running
// program cannot be overwritten but can be renamed, so the old copy moves
// aside under a name of its own and goes once nothing runs it, on this
// install or a later one.
func replaceProgram(target string, data []byte) (bool, bool, error) {
	aside := ""
	if current, err := fsx.ReadFile(target); err == nil && !bytes.Equal(current, data) {
		aside = target + "." + rand.Text() + ".old"
		if err := os.Rename(target, aside); err != nil {
			return false, false, fmt.Errorf("install: move %s aside: %w", target, err)
		}
	}
	changed, err := writeIfDifferent(target, data)
	if err != nil {
		if aside != "" {
			err = errors.Join(err, os.Rename(aside, target))
		}
		return false, false, fmt.Errorf("install: replace %s: %w", target, err)
	}
	return changed, removeAsideCopies(target) > 0, nil
}

// removeAsideCopies removes the copies of target that an install moved aside
// and nothing runs any more, and returns how many are left because something
// still runs them.
func removeAsideCopies(target string) int {
	aside, _ := filepath.Glob(target + ".*.old")
	left := 0
	for _, path := range aside {
		if err := os.Remove(path); err != nil {
			left++
		}
	}
	return left
}

// atomicWriteFile is a variable so a test can make a write fail.
var atomicWriteFile = fsx.AtomicWriteFile

// writeIfDifferent writes data to path unless path already holds exactly
// that, and reports whether it wrote.
func writeIfDifferent(path string, data []byte) (bool, error) {
	current, err := fsx.ReadFile(path)
	if err == nil && bytes.Equal(current, data) {
		return false, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := atomicWriteFile(path, data); err != nil {
		return false, err
	}
	return true, nil
}
