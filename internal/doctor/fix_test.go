package doctor

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// npmStub answers the npm commands a fix runs: npm's global folder, a staged
// install that writes the harness's shim under its prefix, and the global
// install, which exits with globalExit.
type npmStub struct {
	root       string
	globalExit int
	calls      [][]string
}

func (n *npmStub) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	n.calls = append(n.calls, append([]string{request.Name}, request.Args...))
	switch {
	case slices.Equal(request.Args, []string{"root", "-g"}):
		return execx.Result{Stdout: []byte(n.root + "\r\n")}, nil
	case slices.Contains(request.Args, "--prefix"):
		prefix := request.Args[slices.Index(request.Args, "--prefix")+1]
		shim := filepath.Join(prefix, "node_modules", ".bin", "codex.cmd")
		if err := os.MkdirAll(filepath.Dir(shim), 0o700); err != nil {
			return execx.Result{}, err
		}
		return execx.Result{}, os.WriteFile(shim, []byte("@echo off\r\n"), 0o600)
	case slices.Contains(request.Args, "-g"):
		return execx.Result{ExitCode: n.globalExit, Stderr: []byte("npm error EBUSY")}, nil
	}
	return execx.Result{ExitCode: 1}, nil
}

// installsGlobally says whether a fix ran npm's global install.
func (n *npmStub) installsGlobally() bool {
	return slices.ContainsFunc(n.calls, func(call []string) bool { return slices.Contains(call, "-g") && slices.Contains(call, "install") })
}

func TestFixInstallsAProvedVersionOnlyWhileNothingRunsFromTheInstall(t *testing.T) {
	codex := HarnessVersion{Name: "codex", Installed: "0.160.0", Newest: "0.161.0", Source: "npm", Update: "npm install -g @openai/codex@0.161.0"}
	for _, test := range []struct {
		name              string
		running           []int
		proof             error
		globalExit        int
		probed            string
		wantOutcome       string
		isFailed          bool
		installsGlobally  bool
		provesStagedBuild bool
	}{
		{name: "proved and idle", probed: "codex-cli 0.161.0", wantOutcome: "0.161.0 installed after it reached its composer in a terminal of its own", installsGlobally: true, provesStagedBuild: true},
		{name: "something runs from it", running: []int{4242, 4243}, wantOutcome: "waits: 2 processes run from "},
		{name: "the new version does not start", proof: errors.New("its screen ends: Unknown dialog"), wantOutcome: "0.160.0 is kept: 0.161.0 did not reach its composer: its screen ends: Unknown dialog", isFailed: true, provesStagedBuild: true},
		{name: "npm fails", globalExit: 1, wantOutcome: "npm install -g @openai/codex@0.161.0 failed (exit 1): npm error EBUSY", isFailed: true, installsGlobally: true, provesStagedBuild: true},
		{name: "the old version still answers", probed: "codex-cli 0.160.0", wantOutcome: "installed, but codex --version answers codex-cli 0.160.0", isFailed: true, installsGlobally: true, provesStagedBuild: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			npm := &npmStub{root: filepath.Join(t.TempDir(), "npm", "node_modules"), globalExit: test.globalExit}
			stage := t.TempDir()
			var asked []string
			var proved string
			fixer := Fixer{
				Commands: npm,
				RunningFrom: func(folder string) ([]int, error) {
					asked = append(asked, folder)
					return test.running, nil
				},
				Prove: func(_ context.Context, name, executable string) error {
					proved = executable
					return test.proof
				},
				Probe: func(context.Context, string) HarnessProbe {
					return HarnessProbe{Name: "codex", Detail: test.probed, OK: true}
				},
				Stage: stage,
			}

			// Act
			fixed := fixer.Fix(context.Background(), HarnessReleases, codex)

			// Assert
			if !strings.Contains(fixed.Outcome, test.wantOutcome) || fixed.IsFailed != test.isFailed {
				t.Fatalf("fixed = %+v, want %q failed=%v", fixed, test.wantOutcome, test.isFailed)
			}
			if want := filepath.Join(npm.root, "@openai", "codex"); !slices.Equal(asked, []string{want}) {
				t.Fatalf("asked what runs from %v, want the npm install %s", asked, want)
			}
			if npm.installsGlobally() != test.installsGlobally {
				t.Fatalf("global install ran=%v, want %v: %v", npm.installsGlobally(), test.installsGlobally, npm.calls)
			}
			if test.provesStagedBuild && !strings.HasPrefix(proved, stage) {
				t.Fatalf("proved %q, want the build staged under %s", proved, stage)
			}
			if !test.provesStagedBuild && proved != "" {
				t.Fatalf("proved %q while something ran from the install", proved)
			}
			if entries, _ := os.ReadDir(stage); len(entries) != 0 {
				t.Fatalf("the staged build was left behind: %v", entries)
			}
		})
	}
}

// Claude Code updates itself on its own channel, so a fix runs nothing for it
// and names its own updater.
func TestFixLeavesClaudeCodeToItsOwnUpdater(t *testing.T) {
	// Arrange
	npm := &npmStub{}
	fixer := Fixer{Commands: npm, RunningFrom: func(string) ([]int, error) {
		t.Fatal("asked what runs from Claude Code")
		return nil, nil
	}}

	// Act
	fixed := fixer.Fix(context.Background(), HarnessReleases, HarnessVersion{Name: "claude", Installed: "2.1.293", Newest: "2.1.294", Update: "claude update"})

	// Assert
	if fixed.IsFailed || !strings.Contains(fixed.Outcome, "updates itself") || !strings.Contains(fixed.Outcome, "claude update") || len(npm.calls) != 0 {
		t.Fatalf("fixed = %+v, calls %v", fixed, npm.calls)
	}
}

// sleeperVariable makes this test binary wait a minute and exit, standing in
// for a harness that runs.
const sleeperVariable = "DOCTOR_TEST_SLEEPER"

// startSleeper runs program as a sleeper with args and keeps it until the
// test ends.
func startSleeper(t *testing.T, program string, args ...string) int {
	t.Helper()
	sleeper := exec.Command(program, args...)
	// The variable makes this test binary wait, and the rule the tests'
	// stand-in program, whichever of the two program is.
	sleeper.Env = append(append(os.Environ(), sleeperVariable+"=1"), standin.Env("", standin.Rule{Any: true, Hold: true})...)
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	standin.Hold(t, sleeper.Process.Pid)
	return sleeper.Process.Pid
}

// A process runs from a folder when its program is in it, or, as node runs a
// harness's script, when one of its arguments names a file in it.
func TestRunningFromFindsAProgramInTheFolderAndAScriptItRuns(t *testing.T) {
	// Arrange
	installed := filepath.Join(t.TempDir(), "node_modules", "@openai", "codex")
	scripted := filepath.Join(t.TempDir(), "node_modules", "@earendil-works", "pi-coding-agent")
	for _, folder := range []string{installed, scripted} {
		if err := os.MkdirAll(folder, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	standin.RemoveAtCleanup(t, installed)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program := filepath.Join(installed, "vendor", "codex.exe")
	if err := os.MkdirAll(filepath.Dir(program), 0o700); err != nil {
		t.Fatal(err)
	}
	standin.Put(t, program)
	fromFolder := startSleeper(t, program)
	withScript := startSleeper(t, self, filepath.Join(scripted, "dist", "cli.js"))

	// Act
	inInstalled, installedErr := RunningFrom(installed)
	inScripted, scriptedErr := RunningFrom(scripted)
	inNeither, neitherErr := RunningFrom(t.TempDir())

	// Assert
	if err := errors.Join(installedErr, scriptedErr, neitherErr); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(inInstalled, fromFolder) || slices.Contains(inInstalled, withScript) {
		t.Fatalf("running from %s = %v, want %d alone", installed, inInstalled, fromFolder)
	}
	if !slices.Contains(inScripted, withScript) || slices.Contains(inScripted, fromFolder) {
		t.Fatalf("running from %s = %v, want %d alone", scripted, inScripted, withScript)
	}
	if len(inNeither) != 0 {
		t.Fatalf("an empty folder has %v running from it", inNeither)
	}
}
