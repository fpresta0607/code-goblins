package install

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// installedFixture is a machine where install sets up the home at f.root
// from contract and policy and copies a stand-in binary into its bin. The
// home is kept out of any git repository, as the per-user home is.
func installedFixture(t *testing.T, env map[string]string, contract, policy fs.FS) *fixture {
	t.Helper()
	f := newFixture(t, adopterSettings, env)
	t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(f.root))
	f.service.Contract, f.service.Policy = contract, policy
	f.service.Binary = filepath.Join(t.TempDir(), "cfo.exe")
	writeFile(t, f.service.Binary, "build 1")
	return f
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// makePrimaryHome makes root a primary home the way an install does: the
// contract, a state folder and the marker.
func makePrimaryHome(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "AGENTS.md"), "contract")
	writeFile(t, filepath.Join(root, home.InstalledMarker), "marker")
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func primary(root string) bool {
	return home.IsPrimary(home.Home{Root: root, State: filepath.Join(root, "state")})
}

func TestInstallOutsideACheckoutSetsUpAPrimaryHomeFromTheBinary(t *testing.T) {
	f := installedFixture(t, map[string]string{"Path": `C:\Windows`}, codegoblins.Contract, codegoblins.Policy)
	f.install()

	if !primary(f.root) {
		t.Fatal("the home install set up is not primary, so every hook would stay inert there")
	}
	for _, embedded := range []fs.FS{codegoblins.Contract, codegoblins.Policy} {
		err := fs.WalkDir(embedded, ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			want, err := fs.ReadFile(embedded, name)
			if err != nil {
				return err
			}
			if got := readFile(t, filepath.Join(f.root, filepath.FromSlash(name))); got != string(want) {
				t.Errorf("%s in the home differs from the binary's copy", name)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, folder := range []string{"bin", "state", "worktrees", "scratch", "caches", "data"} {
		if info, err := os.Stat(filepath.Join(f.root, folder)); err != nil || !info.IsDir() {
			t.Errorf("the home has no %s folder: %v", folder, err)
		}
	}
	for _, name := range []string{"cfo.exe", "goblins.exe"} {
		if got := readFile(t, filepath.Join(f.bin, name)); got != "build 1" {
			t.Errorf("bin\\%s = %q, want the running binary", name, got)
		}
		if _, err := os.Stat(filepath.Join(f.root, name)); !os.IsNotExist(err) {
			t.Errorf("%s sits at the home's root: %v", name, err)
		}
	}
	if got := readFile(t, filepath.Join(f.profile, ".agents", "skills", "stow", "SKILL.md")); got != "stow" {
		t.Errorf("the shipped skill in the shared skills folder = %q", got)
	}
	if _, err := os.Stat(filepath.Join(f.root, ".agents")); !os.IsNotExist(err) {
		t.Errorf("the home holds a second copy of the skills: %v", err)
	}
	if got := f.env.values["CFO_HOME"]; got != f.root {
		t.Errorf("CFO_HOME = %q, want the home %q", got, f.root)
	}
	if got := f.env.values["Path"]; got != `C:\Windows;`+f.bin {
		t.Errorf("PATH = %q, want the home's bin appended", got)
	}
	commands := hookCommands(t, f.user)
	for _, hook := range Hooks(f.root) {
		line := hook.Command + " " + strings.Join(hook.Args, " ")
		if count(commands, line) != 1 {
			t.Errorf("CFO hook %q appears %d times, want 1", line, count(commands, line))
		}
	}
}

// The desktop window ships beside the binary in a release, and install puts
// it in the home beside goblins.exe, where goblins looks for it.
func TestInstallPutsTheDesktopWindowBesideGoblins(t *testing.T) {
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	writeFile(t, filepath.Join(filepath.Dir(f.service.Binary), "goblins-window.exe"), "window 1")

	output := f.install()

	if got := readFile(t, filepath.Join(f.bin, "goblins-window.exe")); got != "window 1" {
		t.Errorf("goblins-window.exe in the home = %q, want the window beside the binary", got)
	}
	if !strings.Contains(output, "goblins-window.exe") {
		t.Errorf("the install does not name the window it copied:\n%s", output)
	}

	writeFile(t, filepath.Join(filepath.Dir(f.service.Binary), "goblins-window.exe"), "window 2")
	f.install()

	if got := readFile(t, filepath.Join(f.bin, "goblins-window.exe")); got != "window 2" {
		t.Errorf("after an update goblins-window.exe = %q, want the new window", got)
	}
}

// A build with no window beside it, such as one from source, installs no
// window, and goblins shows the board in the browser.
func TestInstallWithoutAWindowLeavesTheBrowser(t *testing.T) {
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)

	output := f.install()

	if _, err := os.Stat(filepath.Join(f.bin, "goblins-window.exe")); !os.IsNotExist(err) {
		t.Errorf("the home holds a window no release shipped: %v", err)
	}
	if !strings.Contains(output, "no desktop window beside") {
		t.Errorf("the install does not say the board opens in the browser:\n%s", output)
	}
}

// A build with no window beside it leaves a window an earlier install put in
// the home, and says goblins keeps using it rather than the browser.
func TestInstallWithoutAWindowKeepsTheHomesWindow(t *testing.T) {
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	window := filepath.Join(f.bin, "goblins-window.exe")
	writeFile(t, window, "window 1")

	output := f.install()

	if got := readFile(t, window); got != "window 1" {
		t.Errorf("goblins-window.exe in the home = %q, want the window it already held", got)
	}
	if !strings.Contains(output, "keeps its existing "+window) {
		t.Errorf("the install does not name the window the home keeps:\n%s", output)
	}
	if strings.Contains(output, "in the browser") {
		t.Errorf("the install says the board opens in the browser though the home has a window:\n%s", output)
	}
}

func TestInstallOutsideACheckoutTwiceChangesNothingTheSecondTime(t *testing.T) {
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	f.install()
	sets := len(f.env.setCalls)

	output := f.install()

	if len(f.env.setCalls) != sets {
		t.Errorf("a second install wrote the environment again: %v", f.env.setCalls[sets:])
	}
	for _, want := range []string{"already installed - nothing changed", "already current in " + f.root, "already this build", f.root + " is set up"} {
		if !strings.Contains(output, want) {
			t.Errorf("a second install's output is missing %q:\n%s", want, output)
		}
	}
}

// A newer binary brings the contract, the skills and itself up to date, and
// adds a policy file the home lacks, but a policy file the operator has tuned
// is theirs.
func TestReinstallUpdatesTheContractAndTheSkillsAndKeepsTunedPolicy(t *testing.T) {
	f := installedFixture(t, nil,
		fstest.MapFS{"AGENTS.md": {Data: []byte("contract 1")}},
		fstest.MapFS{"config/pipeline.json": {Data: []byte("policy 1")}})
	f.service.Skills = fstest.MapFS{"stow/SKILL.md": {Data: []byte("skill 1")}}
	f.install()
	writeFile(t, filepath.Join(f.root, "config", "pipeline.json"), "tuned")
	writeFile(t, f.service.Binary, "build 2")
	f.service.Contract = fstest.MapFS{"AGENTS.md": {Data: []byte("contract 2")}}
	f.service.Skills = fstest.MapFS{"stow/SKILL.md": {Data: []byte("skill 2")}}
	f.service.Policy = fstest.MapFS{"config/pipeline.json": {Data: []byte("policy 2")}, "data/routing.json": {Data: []byte("lanes 2")}}

	output := f.install()

	for path, want := range map[string]string{
		filepath.Join(f.root, "AGENTS.md"):                                "contract 2",
		filepath.Join(f.profile, ".agents", "skills", "stow", "SKILL.md"): "skill 2",
		filepath.Join(f.profile, ".claude", "skills", "stow", "SKILL.md"): "skill 2",
		filepath.Join(f.root, "config", "pipeline.json"):                  "tuned",
		filepath.Join(f.root, "data", "routing.json"):                     "lanes 2",
		filepath.Join(f.bin, "cfo.exe"):                                   "build 2",
		filepath.Join(f.bin, "goblins.exe"):                               "build 2",
	} {
		if got := readFile(t, path); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}
	if !strings.Contains(output, "kept config/pipeline.json") {
		t.Errorf("the output does not say the tuned policy was kept:\n%s", output)
	}
}

// Outside a checkout, install and uninstall act only on the per-user home: a
// CFO_HOME naming another home in use is refused before anything is written,
// while one naming no home at all is simply replaced.
func TestOutsideACheckoutAHomeInUseIsLeftAlone(t *testing.T) {
	for name, act := range map[string]func(Service, io.Writer) error{"install": Service.Install, "uninstall": Service.Uninstall} {
		t.Run(name, func(t *testing.T) {
			inUse := t.TempDir()
			makePrimaryHome(t, inUse)
			f := installedFixture(t, map[string]string{"CFO_HOME": inUse}, codegoblins.Contract, codegoblins.Policy)

			var out strings.Builder
			err := act(f.service, &out)

			if err == nil || !strings.Contains(err.Error(), inUse) || !strings.Contains(err.Error(), "goblins uninstall") {
				t.Fatalf("%s = %v, want a refusal naming the home in use and goblins uninstall\n%s", name, err, out.String())
			}
			if got := f.env.values["CFO_HOME"]; got != inUse || len(f.env.setCalls) != 0 {
				t.Errorf("CFO_HOME = %q after %v, want it left at the home in use", got, f.env.setCalls)
			}
			if got := readFile(t, f.user); got != adopterSettings {
				t.Errorf("the refused %s rewrote the user settings:\n%s", name, got)
			}
			if _, err := os.Stat(filepath.Join(f.root, "AGENTS.md")); !os.IsNotExist(err) {
				t.Errorf("the refused %s wrote the contract into %s", name, f.root)
			}
		})
	}
	t.Run("a CFO_HOME naming no home", func(t *testing.T) {
		gone := filepath.Join(t.TempDir(), "deleted-checkout")
		f := installedFixture(t, map[string]string{"CFO_HOME": gone}, codegoblins.Contract, codegoblins.Policy)
		output := f.install()
		if got := f.env.values["CFO_HOME"]; got != f.root || !strings.Contains(output, "changed from "+gone) {
			t.Errorf("CFO_HOME = %q, want it moved from %q to %q:\n%s", got, gone, f.root, output)
		}
	})
}

// An install refuses while CFO_HOME names another home in use: moving
// CFO_HOME would leave that home's binaries first on PATH and its fleet's
// state unreachable. A folder holding a fleet's state is in use whether an
// install set it up or an older build made a checkout its home without the
// marker, and the refusal names the move that brings the fleet along. A
// CFO_HOME left naming a folder with no state is no home, and the install
// takes over.
func TestInstallRefusesWhileAnotherHomeIsInUse(t *testing.T) {
	tests := []struct {
		name    string
		arrange func(t *testing.T, root string)
		refused bool
	}{
		{name: "a home an install set up", refused: true, arrange: makePrimaryHome},
		{name: "a checkout an older build made its home", refused: true, arrange: func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "AGENTS.md"), "contract")
			writeFile(t, filepath.Join(root, "state", "g1.meta"), "id=g1\n")
			if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "a folder with no state", arrange: func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "AGENTS.md"), "contract")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			inUse := t.TempDir()
			test.arrange(t, inUse)
			f := newFixture(t, adopterSettings, map[string]string{"CFO_HOME": inUse, "Path": `C:\Windows;` + inUse})

			// Act
			var out strings.Builder
			err := f.service.Install(&out)

			// Assert
			if !test.refused {
				if err != nil {
					t.Fatalf("Install = %v, want the install to take over from a folder that holds no fleet\n%s", err, out.String())
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), inUse) || !strings.Contains(err.Error(), "cfo home move") || !strings.Contains(err.Error(), "goblins uninstall") {
				t.Fatalf("Install = %v, want a refusal naming the home in use, cfo home move and goblins uninstall\n%s", err, out.String())
			}
			if len(f.env.setCalls) != 0 || f.env.values["CFO_HOME"] != inUse || f.env.values["Path"] != `C:\Windows;`+inUse {
				t.Errorf("the refused install changed the environment: %v", f.env.setCalls)
			}
			if got := readFile(t, f.user); got != adopterSettings {
				t.Errorf("the refused install rewrote the user settings:\n%s", got)
			}
		})
	}
}

// A home an install has just made, before any fleet has run there, is already
// a home in use: an install into another folder must refuse rather than move
// CFO_HOME away from it.
func TestAFreshInstallIsAHomeInUse(t *testing.T) {
	first := installedFixture(t, map[string]string{"Path": `C:\Windows`}, codegoblins.Contract, codegoblins.Policy)
	first.install()
	f := installedFixture(t, map[string]string{"CFO_HOME": first.root}, codegoblins.Contract, codegoblins.Policy)

	var out strings.Builder
	err := f.service.Install(&out)

	if err == nil || !strings.Contains(err.Error(), first.root) || !strings.Contains(err.Error(), "goblins uninstall") {
		t.Fatalf("Install = %v, want a refusal naming the fresh home and goblins uninstall\n%s", err, out.String())
	}
	if len(f.env.setCalls) != 0 {
		t.Errorf("the refused install changed the environment: %v", f.env.setCalls)
	}
}

// A home an older install set up kept its binaries at its root, on PATH
// there; a reinstall moves them into bin and PATH with them. The desktop
// window, which this build does not supply, stays the home's own in bin.
func TestReinstallMovesAnOlderInstallsRootBinariesIntoBin(t *testing.T) {
	// Arrange
	f := installedFixture(t, map[string]string{"Path": `C:\Windows`}, codegoblins.Contract, codegoblins.Policy)
	makePrimaryHome(t, f.root)
	f.env.values["CFO_HOME"] = f.root
	f.env.values["Path"] = `C:\Windows;` + f.root
	older := []string{"cfo.exe", "goblins.exe", "goblins-window.exe", "goblins-window.png", "cfo.exe.1790989608292912900.update-old", "goblins.exe.abc.old", "cfo.exe.held-66714dea"}
	for _, name := range older {
		writeFile(t, filepath.Join(f.root, name), "older build")
	}

	// Act
	output := f.install()

	// Assert
	for _, name := range older {
		if _, err := os.Stat(filepath.Join(f.root, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived at the home's root: %v", name, err)
		}
	}
	if got := readFile(t, filepath.Join(f.bin, "cfo.exe")); got != "build 1" {
		t.Errorf("bin\\cfo.exe = %q, want this build", got)
	}
	for _, name := range []string{"goblins-window.exe", "goblins-window.png"} {
		if got, err := os.ReadFile(filepath.Join(f.bin, name)); err != nil || string(got) != "older build" {
			t.Errorf("bin\\%s = %q (%v), want the home's own kept:\n%s", name, got, err, output)
		}
	}
	if got := f.env.values["Path"]; got != `C:\Windows;`+f.bin {
		t.Errorf("PATH = %q, want the root swapped for bin:\n%s", got, output)
	}
}

func TestInstallOutsideACheckoutThatFailsPartwayLeavesNoPrimaryHome(t *testing.T) {
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	if err := os.MkdirAll(filepath.Join(f.bin, "goblins.exe"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	err := f.service.Install(&out)

	if err == nil || !strings.Contains(err.Error(), "replace "+filepath.Join(f.bin, "goblins.exe")) {
		t.Fatalf("Install = %v, want the copy's failure\n%s", err, out.String())
	}
	if primary(f.root) {
		t.Error("a home whose install failed partway is primary")
	}
	if len(f.env.setCalls) != 0 {
		t.Errorf("a failed install wrote the environment: %v", f.env.setCalls)
	}
}

// An update whose new build cannot be written after the old one moved aside
// puts the old one back, so the hooks and the Start-menu shortcut still find
// cfo.exe and goblins.exe.
func TestUpdateOutsideACheckoutThatCannotWriteTheNewBuildKeepsTheOldOne(t *testing.T) {
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	f.install()
	writeFile(t, f.service.Binary, "build 2")
	goblins := filepath.Join(f.bin, "goblins.exe")
	original := atomicWriteFile
	t.Cleanup(func() { atomicWriteFile = original })
	atomicWriteFile = func(path string, data []byte) error {
		if path == goblins {
			return errors.New("disk full")
		}
		return original(path, data)
	}

	var out strings.Builder
	err := f.service.Install(&out)

	if err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("Install = %v, want the write's failure\n%s", err, out.String())
	}
	if got := readFile(t, goblins); got != "build 1" {
		t.Errorf("goblins.exe = %q, want the old build back", got)
	}
	if aside, _ := filepath.Glob(filepath.Join(f.bin, "*.old")); len(aside) != 0 {
		t.Errorf("copies left aside: %v", aside)
	}
}

func TestUninstallOutsideACheckoutUnwiresTheHomeAndKeepsIt(t *testing.T) {
	f := installedFixture(t, map[string]string{"Path": `C:\Windows`}, codegoblins.Contract, codegoblins.Policy)
	f.install()
	writeFile(t, filepath.Join(f.root, "state", "wake.log"), "fleet state")

	output := f.uninstall()

	if _, set := f.env.values["CFO_HOME"]; set {
		t.Error("CFO_HOME survived the uninstall")
	}
	if got := f.env.values["Path"]; got != `C:\Windows` {
		t.Errorf("PATH = %q, want the home removed", got)
	}
	for _, command := range hookCommands(t, f.user) {
		if isCFOCommand(command) {
			t.Errorf("CFO hook %q survived the uninstall", command)
		}
	}
	if got := readFile(t, filepath.Join(f.root, "state", "wake.log")); got != "fleet state" {
		t.Errorf("the home's state = %q, want it kept", got)
	}
	if !strings.Contains(output, "kept "+f.root) {
		t.Errorf("the output does not say the home was kept:\n%s", output)
	}
}

// A newer binary that no longer ships a file removes the copy an older one
// wrote, and leaves a file the operator added. That is also how a home an
// older install set up loses the second copy of the skills it kept in the
// home, now that they live once in the shared skills folder.
func TestReinstallRemovesFilesTheBinaryNoLongerShips(t *testing.T) {
	f := installedFixture(t, nil,
		fstest.MapFS{"AGENTS.md": {Data: []byte("contract")}, ".agents/skills/stow/SKILL.md": {Data: []byte("stow")}, ".claude/skills/stow/SKILL.md": {Data: []byte("stow")}},
		fstest.MapFS{})
	f.install()
	writeFile(t, filepath.Join(f.root, ".agents", "skills", "mine", "SKILL.md"), "operator")
	f.service.Contract = fstest.MapFS{"AGENTS.md": {Data: []byte("contract")}}

	output := f.install()

	for _, gone := range []string{".agents/skills/stow/SKILL.md", ".claude/skills/stow/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(gone))); !os.IsNotExist(err) {
			t.Errorf("%s survived an install whose binary no longer ships it", gone)
		}
	}
	if got := readFile(t, filepath.Join(f.root, ".agents", "skills", "mine", "SKILL.md")); got != "operator" {
		t.Errorf("the operator's own file = %q, want it kept", got)
	}
	if !strings.Contains(output, "removed 2 files the binary no longer ships") {
		t.Errorf("the output does not report the removals:\n%s", output)
	}
	if !primary(f.root) {
		t.Error("the home is not primary after the upgrade")
	}

	output = f.install()

	if !strings.Contains(output, "already installed - nothing changed") {
		t.Errorf("a re-run with nothing stale reported a change:\n%s", output)
	}
}

// The marker is read from disk, so a line naming a path outside the home is
// never acted on.
func TestReinstallOutsideACheckoutNeverRemovesAPathOutsideTheHome(t *testing.T) {
	f := installedFixture(t, nil, fstest.MapFS{"AGENTS.md": {Data: []byte("contract")}}, fstest.MapFS{})
	f.install()
	outside := filepath.Join(filepath.Dir(f.root), "outside.txt")
	writeFile(t, outside, "not the home's")
	marker := filepath.Join(f.root, home.InstalledMarker)
	writeFile(t, marker, readFile(t, marker)+"../outside.txt\r\n"+outside+"\r\n")

	f.install()

	if got := readFile(t, outside); got != "not the home's" {
		t.Errorf("the file beside the home = %q, want it untouched", got)
	}
}

// An update carries the desktop window beside its build into the home as an
// install puts it there, and says what it did; a build with no window beside
// it leaves the home's window as it is.
func TestCarryWindowBringsTheWindowBesideTheBuildIntoTheHome(t *testing.T) {
	for name, test := range map[string]struct {
		beside string
		want   string
		says   string
	}{
		"a newer window beside the build":  {"window 2", "window 2", "copied "},
		"the same window beside the build": {"window 1", "window 1", "is already this build"},
		"no window beside the build":       {"", "window 1", "keeps its existing "},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			root, release := t.TempDir(), t.TempDir()
			window := filepath.Join(root, "bin", "goblins-window.exe")
			writeFile(t, window, "window 1")
			if test.beside != "" {
				writeFile(t, filepath.Join(release, "goblins-window.exe"), test.beside)
			}
			var out strings.Builder

			// Act
			err := CarryWindow(root, filepath.Join(release, "cfo.exe"), &out)

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if got := readFile(t, window); got != test.want {
				t.Errorf("goblins-window.exe in the home = %q, want %q", got, test.want)
			}
			if !strings.Contains(out.String(), test.says) {
				t.Errorf("the report does not say %q:\n%s", test.says, out.String())
			}
		})
	}
}

// A window that cannot be put in the home is an error the update can report,
// and the home's window is left as it was.
func TestCarryWindowReportsAWindowItCannotReplace(t *testing.T) {
	// Arrange: a folder where the window goes cannot be replaced by a file.
	root, release := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(root, "bin", "goblins-window.exe", "held"), "not a program")
	writeFile(t, filepath.Join(release, "goblins-window.exe"), "window 2")

	// Act
	err := CarryWindow(root, filepath.Join(release, "cfo.exe"), io.Discard)

	// Assert
	if err == nil {
		t.Fatal("CarryWindow reports no error for a window it could not put in the home")
	}
	if got := readFile(t, filepath.Join(root, "bin", "goblins-window.exe", "held")); got != "not a program" {
		t.Errorf("what the home held in the window's place is now %q", got)
	}
}

// earlierWindow gives the fixture a copy of the desktop window in a folder of
// its own, as an earlier install left one, and a window beside the binary for
// this install to put in the home. It returns the earlier copy's folder.
func earlierWindow(t *testing.T, f *fixture) string {
	t.Helper()
	f.service.EarlierWindow = filepath.Join(t.TempDir(), "CodeGoblinsWindow")
	writeFile(t, filepath.Join(f.service.EarlierWindow, "goblins-window.exe"), "window 0")
	writeFile(t, filepath.Join(f.service.EarlierWindow, "goblins-window.png"), "picture")
	writeFile(t, filepath.Join(filepath.Dir(f.service.Binary), "goblins-window.exe"), "window 1")
	return f.service.EarlierWindow
}

// An install whose home holds the desktop window takes the place of a copy an
// earlier install kept in a folder of its own: the copy and its folder go,
// and a second install finds nothing left to do.
func TestInstallRemovesTheEarlierCopyOfTheDesktopWindow(t *testing.T) {
	// Arrange
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	earlier := earlierWindow(t, f)

	// Act
	output := f.install()
	again := f.install()

	// Assert
	if _, err := os.Stat(earlier); !os.IsNotExist(err) {
		t.Errorf("the earlier copy's folder %s is still there: %v", earlier, err)
	}
	if got := readFile(t, filepath.Join(f.bin, "goblins-window.exe")); got != "window 1" {
		t.Errorf("goblins-window.exe in the home = %q, want the window beside the binary", got)
	}
	if !strings.Contains(output, "removed the earlier desktop window in "+earlier) {
		t.Errorf("the install does not say it removed the earlier copy:\n%s", output)
	}
	if !strings.Contains(again, "nothing changed") {
		t.Errorf("a second install changes something:\n%s", again)
	}
}

// A home that holds no desktop window leaves the earlier copy alone: it is
// the only window there is.
func TestInstallWithNoWindowKeepsTheEarlierCopy(t *testing.T) {
	// Arrange
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	earlier := earlierWindow(t, f)
	if err := os.Remove(filepath.Join(filepath.Dir(f.service.Binary), "goblins-window.exe")); err != nil {
		t.Fatal(err)
	}

	// Act
	output := f.install()

	// Assert
	for name, want := range map[string]string{"goblins-window.exe": "window 0", "goblins-window.png": "picture"} {
		if got := readFile(t, filepath.Join(earlier, name)); got != want {
			t.Errorf("%s of the earlier copy = %q, want it as it was", name, got)
		}
	}
	if !strings.Contains(output, "kept the earlier desktop window in "+earlier) {
		t.Errorf("the install does not say it kept the earlier copy:\n%s", output)
	}
}

// Only what an earlier install put in the folder is removed: a folder that
// holds anything else stays, with that in it, and the install says so once.
func TestInstallLeavesWhatElseTheEarlierWindowsFolderHolds(t *testing.T) {
	// Arrange
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	earlier := earlierWindow(t, f)
	writeFile(t, filepath.Join(earlier, "notes.txt"), "the user's own")

	// Act
	output := f.install()
	again := f.install()

	// Assert
	if got := readFile(t, filepath.Join(earlier, "notes.txt")); got != "the user's own" {
		t.Errorf("notes.txt = %q, want the user's file untouched", got)
	}
	for _, name := range []string{"goblins-window.exe", "goblins-window.png"} {
		if _, err := os.Stat(filepath.Join(earlier, name)); !os.IsNotExist(err) {
			t.Errorf("%s of the earlier copy is still there: %v", name, err)
		}
	}
	if !strings.Contains(output, "left the folder, which holds other files") {
		t.Errorf("the install does not say it left the folder:\n%s", output)
	}
	if !strings.Contains(again, "nothing changed") {
		t.Errorf("a second install changes something:\n%s", again)
	}
}
