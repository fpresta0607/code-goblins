package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/cutover"
)

type cutoverEnv map[string]string

func (e cutoverEnv) Get(name string) (string, bool, error) {
	value, isSet := e[name]
	return value, isSet, nil
}
func (e cutoverEnv) Set(name, value string) error { e[name] = value; return nil }
func (e cutoverEnv) Unset(name string) error      { delete(e, name); return nil }
func (e cutoverEnv) Names() ([]string, error)     { return nil, nil }
func (cutoverEnv) Broadcast() error               { return nil }

type noJobs struct{}

func (noJobs) Jobs() ([]cutover.Job, error) { return nil, nil }
func (noJobs) Register(cutover.Job) error   { return nil }
func (noJobs) Delete(string) error          { return nil }

type noCredentials struct{}

func (noCredentials) Entries(string) ([]auth.VaultEntry, error) { return nil, nil }
func (noCredentials) Write(auth.VaultEntry) error               { return nil }
func (noCredentials) Delete(string) error                       { return nil }

type noLabels struct{}

func (noLabels) Labels(string) ([]string, error)     { return nil, nil }
func (noLabels) Rename(string, string, string) error { return nil }

// cutoverHome is a home whose bin holds an older goblins.exe, and the
// machine the cutover reads around it, with nothing outside the folder.
func cutoverHome(t *testing.T) (cutover.Machine, string) {
	t.Helper()
	top := t.TempDir()
	root := filepath.Join(top, "home")
	for path, content := range map[string]string{
		filepath.Join(root, "bin", "goblins.exe"): "the old build",
		filepath.Join(top, "build", "ceo.exe"):    "this build",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return cutover.Machine{
		Root: root, UserHome: filepath.Join(top, "user"), Program: filepath.Join(top, "build", "ceo.exe"),
		Skills: fstest.MapFS{}, Commands: []string{"spawn"},
		Env: cutoverEnv{}, Jobs: noJobs{}, Vault: noCredentials{}, Labels: noLabels{},
	}, root
}

func runCutoverOn(machine cutover.Machine, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := runCutover(args, &stdout, &stderr, func() (cutover.Machine, error) { return machine, nil })
	return code, stdout.String(), stderr.String()
}

func TestCutoverRefusesAnythingButOneOfItsForms(t *testing.T) {
	machine, _ := cutoverHome(t)
	for _, args := range [][]string{nil, {"--dry-run", "--undo", "x"}, {"--dry-run", "extra"}, {"--unknown"}} {
		// Act
		code, _, stderr := runCutoverOn(machine, args...)

		// Assert
		if code != 2 || !strings.Contains(stderr, "usage: ceo cutover") {
			t.Errorf("%v: exit %d, %q; want the usage and exit 2", args, code, stderr)
		}
	}
}

func TestCutoverMakesOnlyThePlanItsDryRunPrintedAndUndoesIt(t *testing.T) {
	// Arrange
	machine, root := cutoverHome(t)
	code, stdout, stderr := runCutoverOn(machine, "--dry-run")
	digest := regexp.MustCompile(`(?m)^plan: ([0-9a-f]{64})$`).FindStringSubmatch(stdout)
	if code != 0 || digest == nil || !strings.Contains(stdout, filepath.Join(root, "bin", "ceo.exe")+": this build") {
		t.Fatalf("dry run: exit %d, %q, %q", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "bin", "ceo.exe")); !os.IsNotExist(err) {
		t.Fatalf("the dry run changed the home: %v", err)
	}

	// Act
	refusedCode, _, refused := runCutoverOn(machine, "--plan", strings.Repeat("0", 64))
	appliedCode, applied, appliedErr := runCutoverOn(machine, "--plan", digest[1])

	// Assert
	if refusedCode != 1 || !strings.Contains(refused, "nothing was changed") {
		t.Fatalf("another digest: exit %d, %q; want a refusal", refusedCode, refused)
	}
	undo := regexp.MustCompile(`(?m)^undo: ceo cutover --undo (.+)$`).FindStringSubmatch(strings.ReplaceAll(applied, "\r", ""))
	if appliedCode != 0 || undo == nil {
		t.Fatalf("apply: exit %d, %q, %q", appliedCode, applied, appliedErr)
	}
	if content, err := os.ReadFile(filepath.Join(root, "bin", "goblins.exe")); err != nil || string(content) != "this build" {
		t.Fatalf("bin\\goblins.exe is %q, %v; want this build", content, err)
	}
	undoneCode, _, undoneErr := runCutoverOn(machine, "--undo", undo[1])
	if undoneCode != 0 {
		t.Fatalf("undo: exit %d, %q", undoneCode, undoneErr)
	}
	content, err := os.ReadFile(filepath.Join(root, "bin", "goblins.exe"))
	if err != nil || string(content) != "the old build" {
		t.Fatalf("after the undo bin\\goblins.exe is %q, %v; want the old build", content, err)
	}
	for _, gone := range []string{filepath.Join(root, "bin", "ceo.exe"), filepath.Join(root, "state")} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("after the undo %s is still there: %v", gone, err)
		}
	}
}
