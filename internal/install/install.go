package install

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harnessmap"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
)

// homeVariable is the variable that tells cfo where the fleet lives, and the
// one the installed hooks resolve the binary through.
const homeVariable = "CFO_HOME"

// pathVariable is the user PATH.
const pathVariable = "Path"

// ProjectsRootVariable names the folder that holds the operator's checkouts,
// which is what lets `--project <name>` stand for `<root>/<name>`. It is a
// fact about the machine, so it is kept where CFO_HOME is and nowhere in the
// repository: every adopter's folder is somewhere else.
const ProjectsRootVariable = "CFO_PROJECTS_ROOT"

// Service installs and uninstalls the CFO on one machine. Every destination
// is a field so a test can point the whole thing at a temp directory and a
// fake environment: nothing here reads the real user's configuration by
// default, because the file it merges into is someone's personal Claude Code
// setup and losing their hooks to our installer is the worst failure here.
type Service struct {
	// Root is the CFO home, the per-user home every install uses unless
	// CFO_HOME names another. It is the value CFO_HOME gets; its bin folder is
	// the directory added to PATH and the one holding cfo.exe.
	Root string
	// UserSettings is the Claude Code user settings file, normally
	// ~/.claude/settings.json.
	UserSettings string
	// RepoSettings is the checkout's own .claude/settings.json, whose CFO
	// hooks become duplicates once the user-scope ones are in place.
	RepoSettings string
	// Env is the user-scope environment.
	Env EnvStore
	// ProjectsRoot is the folder to record as the projects root. Empty leaves
	// whatever is recorded alone, so a plain re-install never forgets it.
	ProjectsRoot string
	// Contract and Policy are what a home gets from the binary, and Binary
	// is the running executable, copied into its bin folder.
	Contract fs.FS
	Policy   fs.FS
	Binary   string
	// Skills are the skills the binary ships, one folder each, which go to
	// the shared skills folder Harnesses names with a junction from Claude
	// Code's, never into the home.
	Skills fs.FS
	// Harnesses is where each harness keeps its configuration on this
	// machine, recorded in the home for the CFO and goblins to read.
	Harnesses harnessmap.Map
	// Link makes the directory junction Claude Code reads a shared skill
	// through.
	Link harnessmap.Linker
	// HarnessDirs are the configuration folders, by harness name, whose
	// board native hooks (written by `cfo hooks install`) an uninstall
	// removes.
	HarnessDirs map[string]string
	// StartMenuShortcut is the Code Goblins shortcut the install script puts
	// in the Start menu, which an uninstall removes.
	StartMenuShortcut string
	// StartAtLoginKey is the key under HKEY_CURRENT_USER holding the
	// programs Windows starts at login. An uninstall clears it of the desktop
	// window's entry for this home, and an install makes that entry start
	// this home where it started an earlier copy of the window.
	StartAtLoginKey string
	// EarlierWindow is the folder an earlier install kept the desktop window
	// in, on its own. An install whose home holds the window takes its place.
	EarlierWindow string
}

// Install wires the CFO into the machine and reports every change and every
// deliberate non-change. It is safe to run twice: a second run finds
// everything in place and writes nothing.
func (s Service) Install(out io.Writer) error {
	report := &reporter{out: out}

	// The environment store is probed before anything is written, so an
	// unsupported platform or an unreadable HKCU\Environment refuses here
	// with the machine untouched. The settings files then go before the
	// environment writes: they are the step that can refuse on content - a
	// hooks block this package cannot understand is not something to guess
	// at - and refusing there still leaves the environment as it was. A home
	// outside a checkout is written after them, for the same reason.
	if _, _, err := s.Env.Get(homeVariable); err != nil {
		return err
	}
	if s.ProjectsRoot != "" {
		if info, err := os.Stat(s.ProjectsRoot); err != nil || !info.IsDir() {
			return fmt.Errorf("install: --projects-root %s is not a directory; name the folder that holds your checkouts", s.ProjectsRoot)
		}
	}
	if s.Contract == nil || s.Policy == nil || s.Skills == nil || s.Binary == "" {
		return errors.New("install: the contract, the policy, the skills and the running binary are all required to set up a home")
	}
	former, err := s.formerHome()
	if err != nil {
		return err
	}
	if err := s.writeUserHooks(report); err != nil {
		return err
	}
	if err := s.clearRepoHooks(report); err != nil {
		return err
	}
	if err := s.writeHome(report); err != nil {
		return err
	}
	if err := s.layOutData(report); err != nil {
		return err
	}
	if err := s.setHome(report); err != nil {
		return err
	}
	if err := s.addToPath(report, former); err != nil {
		return err
	}
	if err := s.setProjectsRoot(report); err != nil {
		return err
	}
	if err := s.adoptEarlierWindow(report); err != nil {
		return err
	}
	return s.finish(report, "cfo install: already installed - nothing changed")
}

// Uninstall reverses Install. An adopter who cannot cleanly back out will
// never try it in the first place, so this removes exactly what Install
// added and leaves the rest of their configuration alone.
func (s Service) Uninstall(out io.Writer) error {
	report := &reporter{out: out}

	if _, _, err := s.Env.Get(homeVariable); err != nil {
		return err
	}
	if _, err := s.formerHome(); err != nil {
		return err
	}
	if err := s.removeUserHooks(report); err != nil {
		return err
	}
	if err := s.removeNativeHooks(report); err != nil {
		return err
	}
	if err := s.unsetHome(report); err != nil {
		return err
	}
	if err := s.removeFromPath(report); err != nil {
		return err
	}
	if err := s.unsetProjectsRoot(report); err != nil {
		return err
	}
	if err := s.removeStartMenuShortcut(report); err != nil {
		return err
	}
	if err := s.removeStartAtLogin(report); err != nil {
		return err
	}
	if err := s.removeSkills(report); err != nil {
		return err
	}
	report.same("home", "kept "+s.Root+" with its state and data; delete the folder to remove them")
	return s.finish(report, "cfo install --uninstall: nothing to remove")
}

// removeNativeHooks removes the board's native lifecycle hooks from each
// harness's own configuration, leaving everything else there as it is.
func (s Service) removeNativeHooks(report *reporter) error {
	for _, harness := range []string{"claude", "codex", "pi"} {
		dir, ok := s.HarnessDirs[harness]
		if !ok {
			continue
		}
		removed, err := nativehook.Uninstall(harness, dir)
		if err != nil {
			return fmt.Errorf("install: remove the %s native hooks from %s: %w", harness, dir, err)
		}
		if removed {
			report.change("native", "removed the board's "+harness+" hooks from "+dir)
			continue
		}
		report.same("native", "no board hooks for "+harness+" in "+dir)
	}
	return nil
}

// windowShortcutName is the shortcut the desktop window had in the Start menu
// while it was installed on its own; Code Goblins opens the window now.
const windowShortcutName = "Code Goblins Window.lnk"

// removeStartMenuShortcut removes the Start-menu shortcuts an install made,
// when there are any: Code Goblins, and, where the home holds the desktop
// window, the one the window had while installed on its own. A home with no
// window leaves that shortcut, since the earlier copy is then the only window.
func (s Service) removeStartMenuShortcut(report *reporter) error {
	if s.StartMenuShortcut == "" {
		return nil
	}
	window := filepath.Join(filepath.Dir(s.StartMenuShortcut), windowShortcutName)
	shortcuts := []string{s.StartMenuShortcut}
	if _, err := os.Stat(filepath.Join(s.bin(), windowName)); err == nil {
		shortcuts = append(shortcuts, window)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("install: inspect the desktop window in %s: %w", s.bin(), err)
	}
	for _, shortcut := range shortcuts {
		err := os.Remove(shortcut)
		if errors.Is(err, fs.ErrNotExist) {
			// Only an earlier install made the window's own shortcut.
			if shortcut == s.StartMenuShortcut {
				report.same("start menu", "no shortcut at "+shortcut)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("install: remove the Start-menu shortcut %s: %w", shortcut, err)
		}
		report.change("start menu", "removed "+shortcut)
	}
	return nil
}

// StartMenuShortcutPath is where the install script puts the Code Goblins
// shortcut: the user's Start-menu programs, under APPDATA, or nothing when
// APPDATA is not set.
func StartMenuShortcutPath() string {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return ""
	}
	return filepath.Join(appData, "Microsoft", "Windows", "Start Menu", "Programs", "Code Goblins.lnk")
}

// EarlierWindowDir is the folder an earlier install kept the desktop window
// in: CodeGoblinsWindow under LOCALAPPDATA, or nothing when LOCALAPPDATA is
// not set.
func EarlierWindowDir() string {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return ""
	}
	return filepath.Join(local, "CodeGoblinsWindow")
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

func (s Service) setProjectsRoot(report *reporter) error {
	current, set, err := s.Env.Get(ProjectsRootVariable)
	if err != nil {
		return err
	}
	switch {
	case s.ProjectsRoot == "" && set:
		report.same("projects", "root is "+current)
		return nil
	case s.ProjectsRoot == "":
		report.same("projects", "root not set; `cfo install --projects-root <dir>` lets --project take a bare name")
		return nil
	case set && sameDirectory(current, s.ProjectsRoot):
		report.same("projects", "root already "+current)
		return nil
	}
	if err := s.Env.Set(ProjectsRootVariable, s.ProjectsRoot); err != nil {
		return err
	}
	report.envChanged = true
	if set {
		report.change("projects", fmt.Sprintf("root changed from %s to %s (user scope)", current, s.ProjectsRoot))
		return nil
	}
	report.change("projects", "root set to "+s.ProjectsRoot+" (user scope)")
	return nil
}

func (s Service) unsetProjectsRoot(report *reporter) error {
	current, set, err := s.Env.Get(ProjectsRootVariable)
	if err != nil {
		return err
	}
	if !set {
		report.same("projects", "root not set")
		return nil
	}
	if err := s.Env.Unset(ProjectsRootVariable); err != nil {
		return err
	}
	report.envChanged = true
	report.change("projects", "root removed (was "+current+")")
	return nil
}

// projectsRoot reads the machine's projects root, or "" when none is recorded.
// The process environment answers first, so an operator can point one command
// somewhere else. The user scope answers second, because a session that was
// already open when `cfo install --projects-root` ran keeps its old
// environment for as long as it lives, and the CFO's own session is exactly
// that.
func projectsRoot(env EnvStore) (string, error) {
	if root := strings.TrimSpace(os.Getenv(ProjectsRootVariable)); root != "" {
		return root, nil
	}
	root, _, err := env.Get(ProjectsRootVariable)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(root), nil
}

// MachineProjectsRoot is projectsRoot against this machine's own user scope.
func MachineProjectsRoot() (string, error) {
	return projectsRoot(NewEnvStore(execx.OSRunner{}))
}

// SetMachineProjectsRoot records root as this machine's projects root in the
// user scope, as cfo install --projects-root does.
func SetMachineProjectsRoot(root string) error {
	return NewEnvStore(execx.OSRunner{}).Set(ProjectsRootVariable, root)
}

// AddToUserPath appends dir to this machine's user PATH unless the PATH holds
// it already, keeping every entry there as it is written.
func AddToUserPath(dir string) error {
	env := NewEnvStore(execx.OSRunner{})
	raw, _, err := env.Get(pathVariable)
	if err != nil {
		return err
	}
	entries := pathEntries(raw)
	for _, entry := range entries {
		if samePathEntry(entry, dir) {
			return nil
		}
	}
	if err := env.Set(pathVariable, strings.Join(append(entries, dir), pathSeparator)); err != nil {
		return err
	}
	return env.Broadcast()
}

// addToPath puts the home's bin folder on the user PATH, where the hooks and
// every terminal find cfo and goblins, and takes off the home's root, where an
// older install put the binaries, and the root of the former home it takes
// over from, so neither an older build left there nor a cfo.exe built in that
// checkout ever runs before this home's bin.
func (s Service) addToPath(report *reporter, former string) error {
	raw, _, err := s.Env.Get(pathVariable)
	if err != nil {
		return err
	}
	entries := pathEntries(raw)
	kept := make([]string, 0, len(entries)+1)
	present := false
	var dropped []string
	for _, entry := range entries {
		switch {
		case samePathEntry(entry, s.Root) || former != "" && samePathEntry(entry, former):
			dropped = append(dropped, entry)
		case samePathEntry(entry, s.bin()):
			present = true
			kept = append(kept, entry)
		default:
			kept = append(kept, entry)
		}
	}
	if present && len(dropped) == 0 {
		report.same("PATH", "already contains "+s.bin())
		return nil
	}
	if !present {
		kept = append(kept, s.bin())
	}
	if err := s.Env.Set(pathVariable, strings.Join(kept, pathSeparator)); err != nil {
		return err
	}
	report.envChanged = true
	if !present {
		report.change("PATH", fmt.Sprintf("appended %s, keeping the %d other entries already there", s.bin(), len(kept)-1))
	}
	for _, entry := range dropped {
		report.change("PATH", "removed "+entry+", where an older build's binaries were")
	}
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
		if samePathEntry(entry, s.bin()) || samePathEntry(entry, s.Root) {
			continue
		}
		kept = append(kept, entry)
	}
	if len(kept) == len(entries) {
		report.same("PATH", "does not contain "+s.bin())
		return nil
	}
	if err := s.Env.Set(pathVariable, strings.Join(kept, pathSeparator)); err != nil {
		return err
	}
	report.envChanged = true
	report.change("PATH", fmt.Sprintf("removed %s, keeping the other %d entries", s.bin(), len(kept)))
	return nil
}

// bin is the home's folder for the installed binaries.
func (s Service) bin() string {
	return filepath.Join(s.Root, home.BinDir)
}

func (s Service) writeUserHooks(report *reporter) error {
	file, err := loadSettings(s.UserSettings)
	if err != nil {
		return err
	}
	foreign := file.foreignHookCount()
	stood, err := file.pruneCFOHooks()
	if err != nil {
		return err
	}
	if err := file.addCFOHooks(s.Root, stood); err != nil {
		return err
	}
	changed, backup, err := file.save()
	if err != nil {
		return err
	}
	if changed {
		report.change("user hooks", fmt.Sprintf("wrote %d CFO hook groups into %s", len(cfoHookGroups(s.Root)), s.UserSettings))
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
	if _, err := file.pruneCFOHooks(); err != nil {
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
