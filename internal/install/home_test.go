package install

import (
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// installedFixture is a machine with no checkout: install sets up the home at
// f.root from contract and policy and copies a stand-in binary into it. The
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

// makePrimaryCheckout makes root a primary home the way a clone is one: a git
// checkout holding the contract and a state folder.
func makePrimaryCheckout(t *testing.T, root string) {
	t.Helper()
	if out, err := exec.Command("git", "-C", root, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(root, "AGENTS.md"), "contract")
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
			targets := []string{name}
			if rest, ok := strings.CutPrefix(name, agentSkills); ok {
				targets = append(targets, claudeSkills+rest)
			}
			for _, target := range targets {
				if got := readFile(t, filepath.Join(f.root, filepath.FromSlash(target))); got != string(want) {
					t.Errorf("%s in the home differs from the binary's copy", target)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"cfo.exe", "goblins.exe"} {
		if got := readFile(t, filepath.Join(f.root, name)); got != "build 1" {
			t.Errorf("%s = %q, want the running binary", name, got)
		}
	}
	if got := f.env.values["CFO_HOME"]; got != f.root {
		t.Errorf("CFO_HOME = %q, want the home %q", got, f.root)
	}
	if got := f.env.values["Path"]; got != `C:\Windows;`+f.root {
		t.Errorf("PATH = %q, want the home appended", got)
	}
	commands := hookCommands(t, f.user)
	for _, hook := range Hooks() {
		if count(commands, hook.Command) != 1 {
			t.Errorf("CFO hook %q appears %d times, want 1", hook.Command, count(commands, hook.Command))
		}
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
func TestReinstallOutsideACheckoutUpdatesTheContractAndKeepsTunedPolicy(t *testing.T) {
	f := installedFixture(t, nil,
		fstest.MapFS{"AGENTS.md": {Data: []byte("contract 1")}, ".agents/skills/stow/SKILL.md": {Data: []byte("skill 1")}},
		fstest.MapFS{"config/pipeline.json": {Data: []byte("policy 1")}})
	f.install()
	writeFile(t, filepath.Join(f.root, "config", "pipeline.json"), "tuned")
	writeFile(t, f.service.Binary, "build 2")
	f.service.Contract = fstest.MapFS{"AGENTS.md": {Data: []byte("contract 2")}, ".agents/skills/stow/SKILL.md": {Data: []byte("skill 2")}}
	f.service.Policy = fstest.MapFS{"config/pipeline.json": {Data: []byte("policy 2")}, "data/routing.json": {Data: []byte("lanes 2")}}

	output := f.install()

	for path, want := range map[string]string{
		"AGENTS.md":                    "contract 2",
		".agents/skills/stow/SKILL.md": "skill 2",
		".claude/skills/stow/SKILL.md": "skill 2",
		"config/pipeline.json":         "tuned",
		"data/routing.json":            "lanes 2",
		"cfo.exe":                      "build 2",
		"goblins.exe":                  "build 2",
	} {
		if got := readFile(t, filepath.Join(f.root, filepath.FromSlash(path))); got != want {
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
			makePrimaryCheckout(t, inUse)
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

// A checkout install, which is what install.cmd -Dev runs, refuses while
// CFO_HOME names another home in use, as an install outside a checkout does:
// moving CFO_HOME would leave that home's binaries first on PATH, running
// against the checkout, and its fleet's state unreachable. A re-run from the
// home CFO_HOME names goes through.
func TestInstallFromACheckoutRefusesWhileAnotherHomeIsInUse(t *testing.T) {
	inUse := t.TempDir()
	makePrimaryCheckout(t, inUse)
	f := newFixture(t, adopterSettings, map[string]string{"CFO_HOME": inUse, "Path": `C:\Windows;` + inUse})

	var out strings.Builder
	err := f.service.Install(&out)

	if err == nil || !strings.Contains(err.Error(), inUse) || !strings.Contains(err.Error(), "goblins uninstall") {
		t.Fatalf("Install = %v, want a refusal naming the home in use and goblins uninstall\n%s", err, out.String())
	}
	if len(f.env.setCalls) != 0 || f.env.values["CFO_HOME"] != inUse || f.env.values["Path"] != `C:\Windows;`+inUse {
		t.Errorf("the refused install changed the environment: %v", f.env.setCalls)
	}
	if got := readFile(t, f.user); got != adopterSettings {
		t.Errorf("the refused install rewrote the user settings:\n%s", got)
	}
}

// A clone install.cmd -Dev has just made the home, before any fleet has run
// there, is already a home in use: the one-liner run next must refuse rather
// than move CFO_HOME and leave the clone's binaries first on PATH.
func TestAFreshCheckoutInstallIsAHomeInUse(t *testing.T) {
	checkout := newFixture(t, adopterSettings, map[string]string{"Path": `C:\Windows`})
	if out, err := exec.Command("git", "-C", checkout.root, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(checkout.root, "AGENTS.md"), "contract")
	checkout.install()
	f := installedFixture(t, map[string]string{"CFO_HOME": checkout.root}, codegoblins.Contract, codegoblins.Policy)

	var out strings.Builder
	err := f.service.Install(&out)

	if err == nil || !strings.Contains(err.Error(), checkout.root) || !strings.Contains(err.Error(), "goblins uninstall") {
		t.Fatalf("Install = %v, want a refusal naming the fresh checkout and goblins uninstall\n%s", err, out.String())
	}
	if len(f.env.setCalls) != 0 {
		t.Errorf("the refused install changed the environment: %v", f.env.setCalls)
	}
}

func TestInstallFromACheckoutRerunFromTheSameHomeSucceeds(t *testing.T) {
	f := newFixture(t, adopterSettings, map[string]string{"Path": `C:\Windows`})
	makePrimaryCheckout(t, f.root)
	f.install()

	output := f.install()

	if got := f.env.values["CFO_HOME"]; got != f.root {
		t.Errorf("CFO_HOME = %q, want the checkout %q", got, f.root)
	}
	if !strings.Contains(output, "already installed - nothing changed") {
		t.Errorf("a re-run from the same home reported a change:\n%s", output)
	}
}

// The marker is written last, so an install that fails partway leaves no home
// the hooks would act in, and the environment as it was.
func TestInstallOutsideACheckoutThatFailsPartwayLeavesNoPrimaryHome(t *testing.T) {
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	if err := os.MkdirAll(filepath.Join(f.root, "goblins.exe"), 0o755); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	err := f.service.Install(&out)

	if err == nil || !strings.Contains(err.Error(), "run cfo install again") {
		t.Fatalf("Install = %v, want the copy's failure\n%s", err, out.String())
	}
	if primary(f.root) {
		t.Error("a home whose install failed partway is primary")
	}
	if len(f.env.setCalls) != 0 {
		t.Errorf("a failed install wrote the environment: %v", f.env.setCalls)
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
		if strings.HasPrefix(command, rootPrefix) {
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

// A newer binary that no longer ships a skill removes both copies of the
// files an older one wrote, and leaves a skill the operator added.
func TestReinstallOutsideACheckoutRemovesFilesTheBinaryNoLongerShips(t *testing.T) {
	f := installedFixture(t, nil,
		fstest.MapFS{"AGENTS.md": {Data: []byte("contract")}, ".agents/skills/stow/SKILL.md": {Data: []byte("stow")}, ".agents/skills/stash/SKILL.md": {Data: []byte("stash")}},
		fstest.MapFS{})
	f.install()
	writeFile(t, filepath.Join(f.root, ".agents", "skills", "mine", "SKILL.md"), "operator")
	f.service.Contract = fstest.MapFS{"AGENTS.md": {Data: []byte("contract")}, ".agents/skills/stash/SKILL.md": {Data: []byte("stash")}}

	output := f.install()

	for _, gone := range []string{".agents/skills/stow/SKILL.md", ".claude/skills/stow/SKILL.md"} {
		if _, err := os.Stat(filepath.Join(f.root, filepath.FromSlash(gone))); !os.IsNotExist(err) {
			t.Errorf("%s survived an install whose binary no longer ships it", gone)
		}
	}
	for path, want := range map[string]string{
		".agents/skills/stash/SKILL.md": "stash",
		".claude/skills/stash/SKILL.md": "stash",
		".agents/skills/mine/SKILL.md":  "operator",
	} {
		if got := readFile(t, filepath.Join(f.root, filepath.FromSlash(path))); got != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
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
