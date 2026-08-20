package install

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// homeVariable is the variable that tells cfo where the fleet lives, and the
// one the installed hooks resolve the binary through.
const homeVariable = "CFO_HOME"

// pathVariable is the user PATH.
const pathVariable = "Path"

// Service installs and uninstalls the CFO on one machine. Every destination
// is a field so a test can point the whole thing at a temp directory and a
// fake environment: nothing here reads the real user's configuration by
// default, because the file it merges into is someone's personal Claude Code
// setup and losing their hooks to our installer is the worst failure here.
type Service struct {
	// Root is the resolved code-goblins checkout: the value CFO_HOME gets,
	// the directory added to PATH, and the directory holding cfo.exe.
	Root string
	// UserSettings is the Claude Code user settings file, normally
	// ~/.claude/settings.json.
	UserSettings string
	// RepoSettings is the checkout's own .claude/settings.json, whose CFO
	// hooks become duplicates once the user-scope ones are in place.
	RepoSettings string
	// Env is the user-scope environment.
	Env EnvStore
}

// Install wires the CFO into the machine and reports every change and every
// deliberate non-change. It is safe to run twice: a second run finds
// everything in place and writes nothing.
func (s Service) Install(out io.Writer) error {
	report := &reporter{out: out}

	// The settings files go first. They are the step that can refuse - a
	// hooks block this package cannot understand is not something to guess
	// at - and refusing before any environment variable has been written is
	// what keeps a failed install from leaving half of one behind.
	if err := s.writeUserHooks(report); err != nil {
		return err
	}
	if err := s.clearRepoHooks(report); err != nil {
		return err
	}
	if err := s.setHome(report); err != nil {
		return err
	}
	if err := s.addToPath(report); err != nil {
		return err
	}
	return s.finish(report, "cfo install: already installed - nothing changed")
}

// Uninstall reverses Install. An adopter who cannot cleanly back out will
// never try it in the first place, so this removes exactly what Install
// added and leaves the rest of their configuration alone.
func (s Service) Uninstall(out io.Writer) error {
	report := &reporter{out: out}

	if err := s.removeUserHooks(report); err != nil {
		return err
	}
	if err := s.unsetHome(report); err != nil {
		return err
	}
	if err := s.removeFromPath(report); err != nil {
		return err
	}
	return s.finish(report, "cfo install --uninstall: nothing to remove")
}

// finish publishes the environment change once, at the end, rather than
// after each variable, and prints the summary line.
func (s Service) finish(report *reporter, idleLine string) error {
	if report.envChanged {
		if err := s.Env.Broadcast(); err != nil {
			return err
		}
	}
	if !report.changed {
		fmt.Fprintln(report.out, idleLine)
	}
	return nil
}

func (s Service) setHome(report *reporter) error {
	current, set, err := s.Env.Get(homeVariable)
	if err != nil {
		return err
	}
	if set && sameDirectory(current, s.Root) {
		report.same("CFO_HOME", "already "+current)
		return nil
	}
	if err := s.Env.Set(homeVariable, s.Root); err != nil {
		return err
	}
	report.envChanged = true
	if set {
		report.change("CFO_HOME", fmt.Sprintf("changed from %s to %s (user scope)", current, s.Root))
		return nil
	}
	report.change("CFO_HOME", "set to "+s.Root+" (user scope)")
	return nil
}

func (s Service) unsetHome(report *reporter) error {
	current, set, err := s.Env.Get(homeVariable)
	if err != nil {
		return err
	}
	if !set {
		report.same("CFO_HOME", "not set")
		return nil
	}
	if err := s.Env.Unset(homeVariable); err != nil {
		return err
	}
	report.envChanged = true
	report.change("CFO_HOME", "removed (was "+current+")")
	return nil
}

func (s Service) addToPath(report *reporter) error {
	raw, _, err := s.Env.Get(pathVariable)
	if err != nil {
		return err
	}
	entries := pathEntries(raw)
	for _, entry := range entries {
		if samePathEntry(entry, s.Root) {
			report.same("PATH", "already contains "+s.Root)
			return nil
		}
	}
	if err := s.Env.Set(pathVariable, strings.Join(append(entries, s.Root), pathSeparator)); err != nil {
		return err
	}
	report.envChanged = true
	report.change("PATH", fmt.Sprintf("appended %s, keeping the %d entries already there", s.Root, len(entries)))
	return nil
}

func (s Service) removeFromPath(report *reporter) error {
	raw, _, err := s.Env.Get(pathVariable)
	if err != nil {
		return err
	}
	entries := pathEntries(raw)
	kept := make([]string, 0, len(entries))
	for _, entry := range entries {
		if samePathEntry(entry, s.Root) {
			continue
		}
		kept = append(kept, entry)
	}
	if len(kept) == len(entries) {
		report.same("PATH", "does not contain "+s.Root)
		return nil
	}
	if err := s.Env.Set(pathVariable, strings.Join(kept, pathSeparator)); err != nil {
		return err
	}
	report.envChanged = true
	report.change("PATH", fmt.Sprintf("removed %s, keeping the other %d entries", s.Root, len(kept)))
	return nil
}

func (s Service) writeUserHooks(report *reporter) error {
	file, err := loadSettings(s.UserSettings)
	if err != nil {
		return err
	}
	foreign := file.foreignHookCount()
	if err := file.pruneCFOHooks(); err != nil {
		return err
	}
	if err := file.addCFOHooks(); err != nil {
		return err
	}
	changed, backup, err := file.save()
	if err != nil {
		return err
	}
	if changed {
		report.change("user hooks", fmt.Sprintf("wrote %d CFO hook groups into %s", len(cfoHookGroups()), s.UserSettings))
		if backup != "" {
			report.detail("backed up the previous file to " + backup)
		}
	} else {
		report.same("user hooks", "already in "+s.UserSettings)
	}
	report.detail(fmt.Sprintf("left %d hook(s) that are not the CFO's exactly as they were", foreign))
	return nil
}

func (s Service) removeUserHooks(report *reporter) error {
	file, err := loadSettings(s.UserSettings)
	if err != nil {
		return err
	}
	if err := file.pruneCFOHooks(); err != nil {
		return err
	}
	changed, backup, err := file.save()
	if err != nil {
		return err
	}
	if !changed {
		report.same("user hooks", "none of the CFO's in "+s.UserSettings)
		return nil
	}
	report.change("user hooks", "removed the CFO hooks from "+s.UserSettings)
	if backup != "" {
		report.detail("backed up the previous file to " + backup)
	}
	report.detail(fmt.Sprintf("left %d hook(s) that are not the CFO's exactly as they were", file.foreignHookCount()))
	return nil
}

// clearRepoHooks drops the checkout's own hooks block. Once the user-scope
// hooks are in place both files match inside code-goblins and every hook
// fires twice: two session digests, two wake handlers. The permissions block
// and every other key in that file are left exactly as they are.
func (s Service) clearRepoHooks(report *reporter) error {
	if _, err := os.Stat(s.RepoSettings); err != nil {
		report.same("repo hooks", "no "+s.RepoSettings)
		return nil
	}
	file, err := loadSettings(s.RepoSettings)
	if err != nil {
		return err
	}
	if _, present := file.values["hooks"]; !present {
		report.same("repo hooks", "already absent from "+s.RepoSettings)
		return nil
	}
	delete(file.values, "hooks")
	changed, backup, err := file.save()
	if err != nil {
		return err
	}
	if !changed {
		report.same("repo hooks", "already absent from "+s.RepoSettings)
		return nil
	}
	report.change("repo hooks", "removed the duplicate hooks block from "+s.RepoSettings)
	if backup != "" {
		report.detail("backed up the previous file to " + backup)
	}
	return nil
}

// UserSettingsPath resolves the Claude Code user settings file the way
// Claude Code does: CLAUDE_CONFIG_DIR when it is set, ~/.claude otherwise.
func UserSettingsPath() (string, error) {
	if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("install: resolve the home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

// reporter prints one aligned line per step, so the output says what changed
// and what was deliberately left alone, and remembers whether anything
// changed at all.
type reporter struct {
	out        io.Writer
	changed    bool
	envChanged bool
}

func (r *reporter) change(step, detail string) {
	r.changed = true
	fmt.Fprintf(r.out, "  %-11s changed   %s\n", step, detail)
}

func (r *reporter) same(step, detail string) {
	fmt.Fprintf(r.out, "  %-11s unchanged %s\n", step, detail)
}

func (r *reporter) detail(detail string) {
	fmt.Fprintf(r.out, "  %-11s           %s\n", "", detail)
}
