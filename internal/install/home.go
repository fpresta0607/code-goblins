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
	"github.com/fpresta0607/code-goblins/internal/harnessmap"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// markerText explains home.InstalledMarker to whoever finds it. After it,
// following a blank line, the marker lists the contract files the install
// wrote, one slash-separated path per line, so the next install can remove
// those its binary no longer ships.
const markerText = "This folder is a Code Goblins CFO home that cfo install set up.\r\n" +
	"The CFO hooks act here only while this file exists, so leave it in place.\r\n" +
	"Below are the contract files it wrote; the next install removes any its binary no longer ships.\r\n" +
	"\r\n"

// formerHome is the folder CFO_HOME names when it is not this home: the home
// this install takes the machine over from, or "" when there is none. It
// refuses while that folder holds a fleet's state, whether an install set it
// up or an older build made a checkout its home without the marker: moving
// CFO_HOME would start every new session in an empty home, leave that home
// first on PATH, and leave its fleet's state unreachable. Once cfo home move
// has carried the state away, the install takes over.
func (s Service) formerHome() (string, error) {
	current, set, err := s.Env.Get(homeVariable)
	if err != nil {
		return "", err
	}
	if !set || sameDirectory(current, s.Root) {
		return "", nil
	}
	if info, err := os.Stat(filepath.Join(current, "state")); err != nil || !info.IsDir() {
		return current, nil
	}
	return "", fmt.Errorf("install: CFO_HOME is %s, a home in use; run cfo home move to bring its fleet to %s, or run goblins uninstall there first, then run this again to start anew in %s", current, s.Root, s.Root)
}

// homeFolders are the folders every home holds beside data\, which the
// install lays out separately; AGENTS.md's CFO home section describes each.
var homeFolders = []string{home.BinDir, "state", home.WorktreesDir, home.ScratchDir, home.CachesDir}

// writeHome sets up the home: its folders, the contract, the policy files
// where they are missing, the binary under both its names in bin, the skills
// in the shared skills folder, the map of where each harness keeps its
// configuration, and last the marker, so a home that failed partway is never
// taken for a primary one. Its data folder is laid out after it.
func (s Service) writeHome(report *reporter) error {
	markerPath := filepath.Join(s.Root, home.InstalledMarker)
	previous, hadMarker, err := readManifest(markerPath)
	if err != nil {
		return fmt.Errorf("install: read %s: %w", markerPath, err)
	}
	var created []string
	for _, folder := range homeFolders {
		path := filepath.Join(s.Root, folder)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			continue
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("install: create %s: %w", path, err)
		}
		created = append(created, folder)
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
	if err := s.retireRootBinaries(report); err != nil {
		return err
	}
	if err := s.installSkills(report); err != nil {
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
	case len(created) > 0:
		report.change("home", "set up "+s.Root+" with "+strings.Join(created, ", "))
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
		changed, err := writeIfDifferent(filepath.Join(s.Root, filepath.FromSlash(name)), data)
		if err != nil {
			return err
		}
		manifest = append(manifest, name)
		if changed {
			written++
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
// bin\cfo.exe, and beside it as goblins.exe, its second name. An old copy
// still running, such as the supervisor, cannot be overwritten but can be
// renamed, so it moves aside under a name of its own and goes once nothing
// runs it, on this install or a later one, or by the janitor's sweep.
func (s Service) copyBinary(report *reporter) error {
	data, err := fsx.ReadFile(s.Binary)
	if err != nil {
		return fmt.Errorf("install: read the running binary %s: %w", s.Binary, err)
	}
	var copied []string
	stillRunning := false
	for _, name := range []string{"cfo.exe", "goblins.exe"} {
		changed, running, err := replaceProgram(filepath.Join(s.bin(), name), data)
		if err != nil {
			return err
		}
		if changed {
			copied = append(copied, name)
		}
		stillRunning = stillRunning || running
	}
	if len(copied) == 0 {
		report.same("binary", "cfo.exe and goblins.exe in "+s.bin()+" are already this build")
		return nil
	}
	report.change("binary", fmt.Sprintf("copied %s to %s in %s", s.Binary, strings.Join(copied, " and "), s.bin()))
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
	target := filepath.Join(s.bin(), windowName)
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
		report.same("window", windowName+" in "+s.bin()+" is already this build")
		return nil
	}
	report.change("window", fmt.Sprintf("copied %s to %s in %s", source, windowName, s.bin()))
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
	if _, err := os.Stat(filepath.Join(s.bin(), windowName)); err != nil {
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

// rootPrograms are the files an older install put at the home's root, which
// now live in bin.
var rootPrograms = []string{"cfo.exe", "goblins.exe", windowName, windowPicture}

// retireRootBinaries removes the binaries an older install put at the home's
// root, now that bin holds them. One a process still runs cannot be removed
// but can be renamed, so it moves into bin as an aside copy, which the
// janitor removes once nothing runs it.
func (s Service) retireRootBinaries(report *reporter) error {
	var removed, moved []string
	for _, name := range rootPrograms {
		paths, _ := filepath.Glob(filepath.Join(s.Root, name+".*.old"))
		paths, _ = appendGlob(paths, filepath.Join(s.Root, name+".*.update-old"))
		paths, _ = appendGlob(paths, filepath.Join(s.Root, name+".held-*"))
		for _, path := range append([]string{filepath.Join(s.Root, name)}, paths...) {
			err := os.Remove(path)
			switch {
			case err == nil:
				removed = append(removed, filepath.Base(path))
			case errors.Is(err, fs.ErrNotExist):
			default:
				aside := filepath.Join(s.bin(), filepath.Base(path)+"."+rand.Text()+".old")
				if err := os.Rename(path, aside); err != nil {
					return fmt.Errorf("install: retire %s, which an older install put at the home's root: %w", path, err)
				}
				moved = append(moved, filepath.Base(path))
			}
		}
	}
	if len(removed) > 0 {
		report.change("binary", "removed "+strings.Join(removed, ", ")+" from "+s.Root+", where an older install put them")
	}
	if len(moved) > 0 {
		report.change("binary", "moved "+strings.Join(moved, ", ")+" from "+s.Root+" into "+s.bin()+": something still runs them, and the janitor removes them once nothing does")
	}
	if len(removed)+len(moved) == 0 {
		return nil
	}
	// A Start at login entry that ran the window from the root now runs it
	// from bin.
	return s.adoptStartAtLogin(report)
}

func appendGlob(paths []string, pattern string) ([]string, error) {
	more, err := filepath.Glob(pattern)
	return append(paths, more...), err
}

// installSkills installs the skills the binary ships the way every harness
// reads them: one real copy in the shared skills folder and a junction to it
// in Claude Code's, never a second copy and never over a skill the user put
// there. It records where each harness keeps its configuration in the home.
func (s Service) installSkills(report *reporter) error {
	result, err := harnessmap.InstallSkills(s.Skills, s.Harnesses, s.Link)
	if err != nil {
		return fmt.Errorf("install: %w", err)
	}
	if len(result.Changed) > 0 {
		report.change("skills", "wrote "+strings.Join(result.Changed, ", ")+" into "+s.Harnesses.SharedSkills+", each with a junction from Claude Code's skills folder")
	} else if len(result.Names) > 0 {
		report.same("skills", strings.Join(result.Names, ", ")+" in "+s.Harnesses.SharedSkills+", each with a junction from Claude Code's skills folder")
	}
	for _, note := range result.Kept {
		report.same("skills", "kept "+note)
	}
	m := s.Harnesses
	m.Installed = result.Names
	if err := harnessmap.Write(filepath.Join(s.Root, "state"), m); err != nil {
		return fmt.Errorf("install: record where each harness keeps its configuration: %w", err)
	}
	return nil
}

// removeSkills removes the skills an install put in the shared skills folder
// and their junctions, as the harness map recorded them, and nothing else.
func (s Service) removeSkills(report *reporter) error {
	m, err := harnessmap.Read(filepath.Join(s.Root, "state"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	removed, err := harnessmap.RemoveSkills(m, m.Installed)
	if err != nil {
		return fmt.Errorf("install: remove the skills Code Goblins installed: %w", err)
	}
	if len(removed) > 0 {
		report.change("skills", "removed "+strings.Join(removed, ", ")+" from "+m.SharedSkills+" and their junctions")
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
