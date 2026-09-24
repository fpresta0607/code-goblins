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

// markerText explains home.InstalledMarker to whoever finds it.
const markerText = "This folder is a Code Goblins CFO home that cfo install set up.\r\n" +
	"The CFO hooks act here only while this file exists, so leave it in place.\r\n"

// refuseAnotherHome keeps an install outside a checkout from taking the
// machine from a home still in use. CFO_HOME naming another primary home
// means a fleet lives there: moving CFO_HOME would start every new session in
// an empty home and leave that fleet's state unreachable.
func (s Service) refuseAnotherHome() error {
	current, set, err := s.Env.Get(homeVariable)
	if err != nil {
		return err
	}
	if !set || sameDirectory(current, s.Root) || !home.IsPrimary(home.Home{Root: current, State: filepath.Join(current, "state")}) {
		return nil
	}
	return fmt.Errorf("install: CFO_HOME is %s, a home in use; run cfo install from that checkout, or run cfo install --uninstall there first to move to %s", current, s.Root)
}

// writeHome lays out a home outside a checkout: state and data, the
// contract, the policy files where they are missing, the binary under both
// its names, and last the marker, so a home that failed partway is never
// taken for a primary one.
func (s Service) writeHome(report *reporter) error {
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
	if err := s.writeContract(report); err != nil {
		return err
	}
	if err := s.seedPolicy(report); err != nil {
		return err
	}
	if err := s.copyBinary(report); err != nil {
		return err
	}
	marked, err := writeIfDifferent(filepath.Join(s.Root, home.InstalledMarker), []byte(markerText))
	if err != nil {
		return fmt.Errorf("install: mark %s as a CFO home: %w", s.Root, err)
	}
	switch {
	case len(created) > 0:
		report.change("home", fmt.Sprintf("set up %s with %s", s.Root, strings.Join(created, " and ")))
	case marked:
		report.change("home", "marked "+s.Root+" as a CFO home again")
	default:
		report.same("home", s.Root+" is set up")
	}
	return nil
}

func (s Service) writeContract(report *reporter) error {
	written, total := 0, 0
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
			total++
			if changed {
				written++
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("install: write the CFO contract into %s: %w", s.Root, err)
	}
	if written == 0 {
		report.same("contract", fmt.Sprintf("all %d files already current in %s", total, s.Root))
		return nil
	}
	report.change("contract", fmt.Sprintf("wrote %d of %d files into %s", written, total, s.Root))
	return nil
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
