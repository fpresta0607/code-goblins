package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"

	codegoblins "github.com/fpresta0607/code-goblins"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// copyProgram writes a copy of the program at from, a Go program with its
// build information, to to.
func copyProgram(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, to, string(data))
}

// hookPrograms are the programs each hook in a goblin's settings runs.
func hookPrograms(t *testing.T, settings []byte) map[string]bool {
	t.Helper()
	var document struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(settings, &document); err != nil {
		t.Fatal(err)
	}
	commands := map[string]bool{}
	for _, groups := range document.Hooks {
		for _, group := range groups {
			for _, hook := range group.Hooks {
				commands[hook.Command] = true
			}
		}
	}
	return commands
}

// A goblin's hooks run the home's hook program, which Windows starts with no
// console host, only while it is the same build as the home's cfo.exe: with
// none, or one another build left, they run cfo.exe, so no hook ever runs
// another build's code.
func TestAGoblinsHooksRunTheHookProgramOnlyWhileItIsCfosBuild(t *testing.T) {
	this := os.Args[0]
	tests := []struct {
		name     string
		hook     func(t *testing.T, path string)
		wantHook bool
	}{
		{"no hook program", func(*testing.T, string) {}, false},
		{"the same build", func(t *testing.T, path string) { copyProgram(t, this, path) }, true},
		{"not a program", func(t *testing.T, path string) { writeFile(t, path, "not a program") }, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			bin := filepath.Join(root, home.BinDir)
			copyProgram(t, this, filepath.Join(bin, "cfo.exe"))
			test.hook(t, filepath.Join(bin, HookProgram))

			// Act
			settings, err := GoblinSettings(root, filepath.Join(root, "state"))

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			want := filepath.Join(bin, "cfo.exe")
			if test.wantHook {
				want = filepath.Join(bin, HookProgram)
			}
			if commands := hookPrograms(t, settings); len(commands) != 1 || !commands[want] {
				t.Fatalf("the hooks run %v, want %s alone", commands, want)
			}
		})
	}
}

// The hook program is cfo built with -H windowsgui, which its build settings
// record: that is the one difference two builds may have and still be the
// same build.
func TestTheSameBuildDiffersOnlyByTheWindowedFlag(t *testing.T) {
	build := func(ldflags, revision string) *debug.BuildInfo {
		info := &debug.BuildInfo{GoVersion: "go1.26.5", Main: debug.Module{Path: "github.com/fpresta0607/code-goblins", Version: "(devel)"}}
		if ldflags != "" {
			info.Settings = append(info.Settings, debug.BuildSetting{Key: "-ldflags", Value: ldflags})
		}
		info.Settings = append(info.Settings, debug.BuildSetting{Key: "vcs.revision", Value: revision})
		return info
	}
	tests := []struct {
		name        string
		hook, cfo   *debug.BuildInfo
		isSameBuild bool
	}{
		{"a release", build("-H windowsgui -X main.version=v0.6.0", "a1"), build("-X main.version=v0.6.0", "a1"), true},
		{"a build from source", build("-H windowsgui", "a1"), build("", "a1"), true},
		{"the flag written with =", build("-H=windowsgui", "a1"), build("", "a1"), true},
		{"another release", build("-H windowsgui -X main.version=v0.6.0", "a1"), build("-X main.version=v0.6.1", "a1"), false},
		{"another commit", build("-H windowsgui", "a1"), build("", "b2"), false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Act
			isSameBuild := buildIdentity(test.hook) == buildIdentity(test.cfo)

			// Assert
			if isSameBuild != test.isSameBuild {
				t.Fatalf("same build = %t, want %t", isSameBuild, test.isSameBuild)
			}
		})
	}
}

// An install puts the hook program shipped beside its binary in the home's
// bin, and one with none beside it, such as a build from source, leaves the
// home's own.
func TestInstallPutsTheHookProgramBesideCfo(t *testing.T) {
	// Arrange
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	writeFile(t, filepath.Join(filepath.Dir(f.service.Binary), HookProgram), "hook program 1")

	// Act
	output := f.install()
	if err := os.Remove(filepath.Join(filepath.Dir(f.service.Binary), HookProgram)); err != nil {
		t.Fatal(err)
	}
	again := f.install()

	// Assert
	if got := readFile(t, filepath.Join(f.bin, HookProgram)); got != "hook program 1" {
		t.Errorf("%s in the home = %q, want the hook program beside the binary", HookProgram, got)
	}
	if !strings.Contains(output, "copied") || !strings.Contains(output, HookProgram) {
		t.Errorf("the install does not name the hook program it copied:\n%s", output)
	}
	if !strings.Contains(again, "no hook program beside") {
		t.Errorf("an install with no hook program beside it does not say so:\n%s", again)
	}
}

// cfo update carries the hook program beside the candidate into the home,
// as it carries the window.
func TestCarryHookProgramBringsTheHookProgramIntoTheHome(t *testing.T) {
	// Arrange
	candidate := filepath.Join(t.TempDir(), "cfo.exe")
	writeFile(t, candidate, "candidate")
	writeFile(t, filepath.Join(filepath.Dir(candidate), HookProgram), "hook program 2")
	programs := t.TempDir()
	writeFile(t, filepath.Join(programs, HookProgram), "hook program 1")
	var out strings.Builder

	// Act
	err := CarryHookProgram(programs, candidate, &out)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(programs, HookProgram)); got != "hook program 2" {
		t.Fatalf("%s in the home = %q, want the candidate's", HookProgram, got)
	}
}
