package install

import (
	"bytes"
	"errors"
	"fmt"
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

// writeHome lays out a home outside a checkout: state and data, the
// contract, the policy files where they are missing, the binary under both
// its names, and last the marker, so a home that failed partway is never
// taken for a primary one.
func (s Service) writeHome(report *reporter) error {
	markerPath := filepath.Join(s.Root, home.InstalledMarker)
	previous, hadMarker, err := readManifest(markerPath)
	if err != nil {
		return fmt.Errorf("install: read %s: %w", markerPath, err)
	}
	var created []string
	for _, dir := range []string{"state", "data"} {
		path := filepath.Join(s.Root, dir)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			continue
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return fmt.Errorf("install: create %s: %w", path, err)
		}
		created = append(created, dir)
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
		report.change("home", fmt.Sprintf("set up %s with %s", s.Root, strings.Join(created, " and ")))
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
	data, err := os.ReadFile(path)
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
// cfo.exe, and beside it as goblins.exe, its second name.
func (s Service) copyBinary(report *reporter) error {
	data, err := os.ReadFile(s.Binary)
	if err != nil {
		return fmt.Errorf("install: read the running binary %s: %w", s.Binary, err)
	}
	var copied []string
	for _, name := range []string{"cfo.exe", "goblins.exe"} {
		target := filepath.Join(s.Root, name)
		changed, err := writeIfDifferent(target, data)
		if err != nil {
			return fmt.Errorf("install: replace %s: %w; if cfo serve is running from it, stop it and run cfo install again", target, err)
		}
		if changed {
			copied = append(copied, name)
		}
	}
	if len(copied) == 0 {
		report.same("binary", "cfo.exe and goblins.exe in "+s.Root+" are already this build")
		return nil
	}
	report.change("binary", fmt.Sprintf("copied %s to %s in %s", s.Binary, strings.Join(copied, " and "), s.Root))
	return nil
}

// writeIfDifferent writes data to path unless path already holds exactly
// that, and reports whether it wrote.
func writeIfDifferent(path string, data []byte) (bool, error) {
	current, err := os.ReadFile(path)
	if err == nil && bytes.Equal(current, data) {
		return false, nil
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := fsx.AtomicWriteFile(path, data); err != nil {
		return false, err
	}
	return true, nil
}
