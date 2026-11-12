package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/crawler"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/installbase"
)

// Install wires in the per-user home wherever it runs, a checkout included,
// unless the user's GOBLINS_HOME names a home a fleet lives in, which it keeps;
// a GOBLINS_HOME naming a folder with no fleet does not decide it.
func TestInstallTargetIsThePerUserHomeWhereverItRunsUnlessAHomeIsInUse(t *testing.T) {
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	environment := filepath.Join(t.TempDir(), "user-env.json")
	t.Setenv(installbase.UserEnvFileVariable, environment)
	stale := t.TempDir()
	if err := os.WriteFile(environment, []byte(`{"GOBLINS_HOME": `+strconv.Quote(stale)+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	checkout := fakeSourceCheckout(t)
	want := filepath.Join(local, "CodeGoblins")
	for name, dir := range map[string]string{"a checkout": checkout, "a folder inside a checkout": filepath.Join(checkout, "cmd"), "any other folder": t.TempDir()} {
		t.Chdir(dir)
		target, err := installTarget()
		if err != nil || !strings.EqualFold(target.Root, want) || target.Kept {
			t.Errorf("%s: installTarget = %+v, %v; want the per-user home %q", name, target, err, want)
		}
	}

	if err := os.MkdirAll(filepath.Join(checkout, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(environment, []byte(`{"GOBLINS_HOME": `+strconv.Quote(checkout)+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if target, err := installTarget(); err != nil || !strings.EqualFold(target.Root, checkout) || !target.Kept || !target.Checkout {
		t.Errorf("with GOBLINS_HOME at a checkout holding a fleet: installTarget = %+v, %v; want that checkout kept", target, err)
	}

	t.Setenv("LOCALAPPDATA", "")
	if target, err := installTarget(); err == nil {
		t.Errorf("installTarget without LOCALAPPDATA = %+v, want a refusal", target)
	}
}

// fakeSourceCheckout is a code-goblins checkout with everything committed.
func fakeSourceCheckout(t *testing.T) string {
	t.Helper()
	checkout := t.TempDir()
	for _, file := range []string{"AGENTS.md", filepath.Join("cmd", "goblins", "main.go"), ".gitignore"} {
		path := filepath.Join(checkout, file)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("source\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "source"}} {
		if out, err := exec.Command("git", append([]string{"-C", checkout}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return checkout
}

// installedTree runs goblins install from dir on a machine of its own, a fresh
// profile with a file standing in for the user environment, and returns
// every file the install wrote under the per-user home, by its path there,
// with its content hash, and the user environment it recorded with the
// machine's own folders written as placeholders.
func installedTree(t *testing.T, dir string) (map[string]string, map[string]string) {
	t.Helper()
	root, profile, envFile := installOnAScratchMachine(t, dir)
	tree := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if filepath.ToSlash(rel) == "state/harnesses.json" {
			// The map names this machine's own folders and when it was
			// written; its shape is what must match.
			data = []byte(strings.ReplaceAll(strings.ReplaceAll(string(data), strings.ReplaceAll(profile, `\`, `\\`), "<profile>"), "\r", ""))
			data = []byte(strings.Join(dropLines(strings.Split(string(data), "\n"), `"written"`), "\n"))
		}
		sum := sha256.Sum256(data)
		tree[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, folder := range []string{"bin", "state", "data", "worktrees", "scratch", "caches"} {
		if info, err := os.Stat(filepath.Join(root, folder)); err != nil || !info.IsDir() {
			t.Errorf("the home has no %s folder: %v", folder, err)
		}
	}
	data, err := os.ReadFile(envFile)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(string(data), strings.ReplaceAll(profile, `\`, `\\`), "<profile>"), "\n") {
		if name, value, ok := strings.Cut(strings.TrimSpace(line), ": "); ok {
			env[strings.Trim(name, `"`)] = strings.TrimSuffix(value, ",")
		}
	}
	return tree, env
}

// installOnAScratchMachine runs goblins install with args from dir on a machine of
// its own, whose npm installs the crawler's packages. It returns the home it
// made, the profile and the environment file.
func installOnAScratchMachine(t *testing.T, dir string, args ...string) (root, profile, envFile string) {
	t.Helper()
	root, profile, envFile = scratchMachine(t, dir)
	var stdout, stderr bytes.Buffer
	if code := runInstall(args, &stdout, &stderr, commandRuntime{crawlerNpm: &crawlerPackages{}}); code != 0 {
		t.Fatalf("goblins install from %s exited %d:\n%s%s", dir, code, stdout.String(), stderr.String())
	}
	return root, profile, envFile
}

// scratchMachine makes dir the working folder of a machine of its own: a
// profile, a per-user folder and a user environment in temporary folders. It
// returns the home an install there makes, the profile and the environment
// file.
func scratchMachine(t *testing.T, dir string) (root, profile, envFile string) {
	t.Helper()
	machine := t.TempDir()
	profile = filepath.Join(machine, "profile")
	local := filepath.Join(profile, "AppData", "Local")
	for _, folder := range []string{local, filepath.Join(profile, ".claude")} {
		if err := os.MkdirAll(folder, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	envFile = filepath.Join(machine, "user-env.json")
	t.Setenv(installbase.UserEnvFileVariable, envFile)
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv("APPDATA", filepath.Join(profile, "AppData", "Roaming"))
	t.Setenv("USERPROFILE", profile)
	t.Setenv("HOME", profile)
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "PI_CODING_AGENT_DIR", "GOBLINS_HOME", "GOBLINS_STATE_OVERRIDE", "GOBLINS_PROJECTS_ROOT"} {
		t.Setenv(name, "")
	}
	t.Chdir(dir)
	return filepath.Join(local, "CodeGoblins"), profile, envFile
}

// Every install, an update's included, sets up the crawler connector every
// goblin carries. One whose npm fails still installs the rest, and says in a
// note the install script shows that goblins go without the crawler.
func TestInstallSetsUpTheCrawlerAndGoesOnWithoutIt(t *testing.T) {
	for _, test := range []struct {
		name     string
		exitCode int
		want     crawler.State
	}{
		{name: "npm installs the packages", want: crawler.Ready},
		{name: "npm fails", exitCode: 1, want: crawler.Missing},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root, _, _ := scratchMachine(t, t.TempDir())
			npm := &crawlerPackages{exitCode: test.exitCode}

			// Act
			var stdout, stderr bytes.Buffer
			code := runInstall(nil, &stdout, &stderr, commandRuntime{crawlerNpm: npm})

			// Assert
			if code != 0 {
				t.Fatalf("goblins install exited %d, want 0:\n%s%s", code, stdout.String(), stderr.String())
			}
			if _, err := os.Stat(filepath.Join(root, "bin", "goblins.exe")); err != nil {
				t.Errorf("the install left no bin\\goblins.exe: %v", err)
			}
			if state, err := crawler.Check(root, codegoblins.Crawler); state != test.want || err != nil || npm.runs != 1 {
				t.Errorf("the crawler after the install is %v, %v after %d npm runs, want %v after one", state, err, npm.runs, test.want)
			}
			isNoted := strings.Contains(stdout.String(), "Note: The crawler connector was not set up (") && strings.Contains(stdout.String(), "npm error network request failed")
			if isNoted != (test.exitCode != 0) {
				t.Errorf("the install noted a crawler it could not set up = %v, want %v:\n%s", isNoted, test.exitCode != 0, stdout.String())
			}
		})
	}
}

// stalledNpm stands in for an npm ci that waits on a registry it cannot
// reach: it returns once the setup's context ends, or, in a setup with no
// deadline, after a minute.
type stalledNpm struct{}

func (stalledNpm) Run(ctx context.Context, _ execx.Request) (execx.Result, error) {
	select {
	case <-ctx.Done():
		return execx.Result{}, ctx.Err()
	case <-time.After(time.Minute):
		return execx.Result{ExitCode: 1, Stderr: []byte("npm error network request timed out")}, nil
	}
}

// An npm ci that stalls is stopped at the crawler setup's deadline, so the
// install, which the board's Update ends past its own bound, finishes with
// the note that goblins go without the crawler.
func TestInstallStopsACrawlerSetupThatStallsAndGoesOnWithoutIt(t *testing.T) {
	// Arrange
	root, _, _ := scratchMachine(t, t.TempDir())
	wait := crawlerSetupWait
	crawlerSetupWait = 100 * time.Millisecond
	t.Cleanup(func() { crawlerSetupWait = wait })

	// Act
	var stdout, stderr bytes.Buffer
	code := runInstall(nil, &stdout, &stderr, commandRuntime{crawlerNpm: stalledNpm{}})

	// Assert
	if code != 0 {
		t.Fatalf("goblins install exited %d, want 0:\n%s%s", code, stdout.String(), stderr.String())
	}
	if want := "Note: The crawler connector was not set up (crawler: the setup took longer than 100ms, so it was stopped"; !strings.Contains(stdout.String(), want) {
		t.Errorf("the install did not say %q:\n%s", want, stdout.String())
	}
	if state, err := crawler.Check(root, codegoblins.Crawler); state != crawler.Missing || err != nil {
		t.Errorf("the crawler after a stopped setup is %v (%v), want Missing, with no goblin-crawl for a goblin to start", state, err)
	}
}

// An install from a build that carries no board would put a page saying the
// board was not built in place of the board, so it is refused before anything
// changes and says how to build one that carries it. An uninstall needs no
// board and goes on.
func TestInstallRefusesABuildWithoutItsBoard(t *testing.T) {
	// Arrange
	boardBuilt = func() bool { return false }
	t.Cleanup(func() { boardBuilt = func() bool { return true } })
	local := t.TempDir()
	t.Setenv("LOCALAPPDATA", local)
	t.Setenv(installbase.UserEnvFileVariable, filepath.Join(t.TempDir(), "user-env.json"))

	// Act
	var stdout, stderr bytes.Buffer
	code := runInstall(nil, &stdout, &stderr, commandRuntime{})

	// Assert
	if code != 1 {
		t.Fatalf("goblins install from a build without its board exited %d, want 1:\n%s%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"carries no board", "npm run build", "nothing was changed"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the refusal does not say %q:\n%s", want, stderr.String())
		}
	}
	if _, err := os.Stat(filepath.Join(local, "CodeGoblins")); !os.IsNotExist(err) {
		t.Errorf("the refused install made the home anyway: %v", err)
	}
	installOnAScratchMachine(t, t.TempDir(), "--uninstall", "--yes")
}

// --dev-drive keeps the person's answer to the setup's Dev Drive offer in the
// home: on asks the board for the first step's Command Center item, off keeps
// the offer away, and without it nothing is recorded.
func TestInstallKeepsTheAnswerToTheDevDriveOffer(t *testing.T) {
	for name, test := range map[string]struct {
		args   []string
		choice string
		asked  bool
	}{
		"ticked":    {[]string{"--dev-drive", "on"}, home.DevDriveWanted, true},
		"unticked":  {[]string{"--dev-drive", "off"}, home.DevDriveDeclined, false},
		"not shown": {nil, "", false},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			root, _, _ := installOnAScratchMachine(t, t.TempDir(), test.args...)

			// Assert
			config, err := home.ReadDevDriveConfig(root)
			if err != nil || config.Choice != test.choice || config.AskedAt.IsZero() == test.asked || config.Root != "" {
				t.Errorf("config = %+v, %v; want choice %q, asked %v, nothing moved", config, err, test.choice, test.asked)
			}
		})
	}
}

func dropLines(lines []string, containing string) []string {
	kept := lines[:0]
	for _, line := range lines {
		if !strings.Contains(line, containing) {
			kept = append(kept, line)
		}
	}
	return kept
}

// The Overlord's desktop install and a goblins install run from a code-goblins
// checkout set up the same home, in the same place, and the checkout gains
// nothing: a source checkout is never a home.
func TestInstallFromACheckoutAndFromTheDesktopInstallerMakeTheSameHome(t *testing.T) {
	// Arrange
	checkout := fakeSourceCheckout(t)

	// Act: the desktop installer runs goblins install from a neutral folder.
	fromCheckout, checkoutEnv := installedTree(t, checkout)
	fromInstaller, installerEnv := installedTree(t, t.TempDir())

	// Assert
	if len(fromCheckout) == 0 {
		t.Fatal("the install wrote no files")
	}
	var differ []string
	for path, sum := range fromCheckout {
		if fromInstaller[path] != sum {
			differ = append(differ, path)
		}
	}
	for path := range fromInstaller {
		if _, ok := fromCheckout[path]; !ok {
			differ = append(differ, path)
		}
	}
	sort.Strings(differ)
	if len(differ) > 0 {
		t.Errorf("the two homes differ in %v", differ)
	}
	for _, want := range []string{"bin/goblins.exe", "AGENTS.md", ".ceo-home", "state/harnesses.json", "config/pipeline.json", "data/routing.json"} {
		if _, ok := fromCheckout[want]; !ok {
			t.Errorf("the home has no %s", want)
		}
	}
	for path := range fromCheckout {
		if strings.HasPrefix(path, ".agents/") || strings.HasPrefix(path, ".claude/") || path == "goblins.exe" {
			t.Errorf("the home holds %s, which belongs in bin or the shared skills folder", path)
		}
	}
	if checkoutEnv["Path"] != installerEnv["Path"] || checkoutEnv["GOBLINS_HOME"] != installerEnv["GOBLINS_HOME"] || !strings.HasSuffix(checkoutEnv["Path"], `CodeGoblins\\bin"`) {
		t.Errorf("user environment from a checkout %v, from the installer %v; want both the same with the home's bin on PATH", checkoutEnv, installerEnv)
	}
	status, err := exec.Command("git", "-C", checkout, "status", "--porcelain", "--untracked-files=all", "--ignored").CombinedOutput()
	if err != nil || len(bytes.TrimSpace(status)) != 0 {
		t.Errorf("the checkout's git status after install = %q, %v; want nothing new", status, err)
	}
}

// --start-at-login takes on or off, and is no part of an uninstall; either
// mistake is refused before anything is changed.
func TestInstallRefusesAStartAtLoginItCannotTake(t *testing.T) {
	for name, args := range map[string][]string{
		"neither on nor off": {"--start-at-login", "maybe"},
		"with --uninstall":   {"--uninstall", "--start-at-login", "off"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			var stdout, stderr bytes.Buffer

			// Act
			code := runInstall(args, &stdout, &stderr, commandRuntime{})

			// Assert
			if code != 2 || !strings.Contains(stderr.String(), "--start-at-login") || stdout.Len() != 0 {
				t.Errorf("goblins install %v exited %d, said %q and %q; want 2 naming --start-at-login and nothing done", args, code, stdout.String(), stderr.String())
			}
		})
	}
}
