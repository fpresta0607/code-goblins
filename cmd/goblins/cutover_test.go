package main

import (
	"bytes"
	"fmt"
	iofs "io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/cutover"
	"github.com/fpresta0607/code-goblins/internal/installbase"
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

// runningPrograms are the programs a test says run on the machine.
type runningPrograms []cutover.Process

func (r runningPrograms) Running() ([]cutover.Process, error) { return r, nil }

// cutoverHome is a home whose bin holds an older goblins.exe, and the
// machine the cutover reads around it, with nothing outside the folder and
// nothing running. The build and the home sit in folders whose names have a
// space, and the build's a quote, as a user profile's can.
func cutoverHome(t *testing.T) (cutover.Machine, string) {
	t.Helper()
	top := t.TempDir()
	root := filepath.Join(top, "the home")
	program := filepath.Join(top, "John's build", "goblins.exe")
	for path, content := range map[string]string{
		filepath.Join(root, "bin", "goblins.exe"): "the old build",
		program: "this build",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return cutover.Machine{
		Root: root, UserHome: filepath.Join(top, "user"), Program: program,
		Skills: fstest.MapFS{}, Contract: fstest.MapFS{}, Commands: []string{"spawn"},
		Env: cutoverEnv{}, Jobs: noJobs{}, Vault: noCredentials{}, Labels: noLabels{}, Programs: runningPrograms{},
	}, root
}

// undoCommand is the undo line an apply prints: a PowerShell call of a
// program with one argument, each in single quotes with a quote doubled.
var undoCommand = regexp.MustCompile(`(?m)^undo: & '((?:[^']|'')*)' cutover --undo '((?:[^']|'')*)'$`)

// printedUndo is the program and the folder of the undo line in stdout, as
// PowerShell reads them.
func printedUndo(stdout string) (program, folder string, isPrinted bool) {
	found := undoCommand.FindStringSubmatch(strings.ReplaceAll(stdout, "\r", ""))
	if found == nil {
		return "", "", false
	}
	return strings.ReplaceAll(found[1], "''", "'"), strings.ReplaceAll(found[2], "''", "'"), true
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
		if code != 2 || !strings.Contains(stderr, "usage: goblins cutover") {
			t.Errorf("%v: exit %d, %q; want the usage and exit 2", args, code, stderr)
		}
	}
}

func TestCutoverMakesOnlyThePlanItsDryRunPrintedAndUndoesIt(t *testing.T) {
	// Arrange
	machine, root := cutoverHome(t)
	code, stdout, stderr := runCutoverOn(machine, "--dry-run")
	digest := regexp.MustCompile(`(?m)^plan: ([0-9a-f]{64})$`).FindStringSubmatch(stdout)
	if code != 0 || digest == nil || !strings.Contains(stdout, filepath.Join(root, "bin", "goblins.exe")+": this build") {
		t.Fatalf("dry run: exit %d, %q, %q", code, stdout, stderr)
	}
	if content, err := os.ReadFile(filepath.Join(root, "bin", "goblins.exe")); err != nil || string(content) != "the old build" {
		t.Fatalf("the dry run changed the home: bin\\goblins.exe is %q, %v", content, err)
	}

	// Act
	refusedCode, _, refused := runCutoverOn(machine, "--plan", strings.Repeat("0", 64))
	appliedCode, applied, appliedErr := runCutoverOn(machine, "--plan", digest[1])

	// Assert
	if refusedCode != 1 || !strings.Contains(refused, "nothing was changed") {
		t.Fatalf("another digest: exit %d, %q; want a refusal", refusedCode, refused)
	}
	program, folder, isPrinted := printedUndo(applied)
	if appliedCode != 0 || !isPrinted {
		t.Fatalf("apply: exit %d, %q, %q", appliedCode, applied, appliedErr)
	}
	if program != machine.Program {
		t.Fatalf("the undo command runs %s; want this build, %s, since the undo writes the home's bin\\goblins.exe back", program, machine.Program)
	}
	if content, err := os.ReadFile(filepath.Join(root, "bin", "goblins.exe")); err != nil || string(content) != "this build" {
		t.Fatalf("bin\\goblins.exe is %q, %v; want this build", content, err)
	}
	undoneCode, _, undoneErr := runCutoverOn(machine, "--undo", folder)
	if undoneCode != 0 {
		t.Fatalf("undo: exit %d, %q", undoneCode, undoneErr)
	}
	content, err := os.ReadFile(filepath.Join(root, "bin", "goblins.exe"))
	if err != nil || string(content) != "the old build" {
		t.Fatalf("after the undo bin\\goblins.exe is %q, %v; want the old build", content, err)
	}
	if _, err := os.Stat(filepath.Join(root, "state")); !os.IsNotExist(err) {
		t.Errorf("after the undo the state folder is still there: %v", err)
	}
}

func dryRunDigest(t *testing.T, machine cutover.Machine) string {
	t.Helper()
	code, stdout, stderr := runCutoverOn(machine, "--dry-run")
	digest := regexp.MustCompile(`(?m)^plan: ([0-9a-f]{64})$`).FindStringSubmatch(stdout)
	if code != 0 || digest == nil {
		t.Fatalf("dry run: exit %d, %q, %q", code, stdout, stderr)
	}
	return digest[1]
}

// appliedCutover makes the cutover on machine and returns the folder its
// undo names.
func appliedCutover(t *testing.T, machine cutover.Machine) string {
	t.Helper()
	code, stdout, stderr := runCutoverOn(machine, "--plan", dryRunDigest(t, machine))
	_, folder, isPrinted := printedUndo(stdout)
	if code != 0 || !isPrinted {
		t.Fatalf("apply: exit %d, %q, %q", code, stdout, stderr)
	}
	return folder
}

// recordSupervisor writes the home's board record, as goblins serve does, naming
// a supervisor at board.
func recordSupervisor(t *testing.T, root, board string) {
	t.Helper()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(state, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeBoardRecord(state, boardRecord{PID: fakeBoardPID, URL: board}); err != nil {
		t.Fatal(err)
	}
}

// homeFiles is every file in the home and its content.
func homeFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry iofs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		files[path] = string(content)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

func TestCutoverRefusesEveryFormWhileASupervisorServesTheHome(t *testing.T) {
	for _, form := range []string{"--dry-run", "--plan", "--undo"} {
		t.Run(form, func(t *testing.T) {
			// Arrange
			machine, root := cutoverHome(t)
			args := []string{form}
			switch form {
			case "--plan":
				args = append(args, dryRunDigest(t, machine))
			case "--undo":
				args = append(args, appliedCutover(t, machine))
			}
			before := homeFiles(t, root)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/alive" {
					http.NotFound(w, r)
					return
				}
				fmt.Fprintf(w, `{"pid":%d}`, fakeBoardPID)
			}))
			defer server.Close()
			recordSupervisor(t, root, server.URL)

			// Act
			code, stdout, stderr := runCutoverOn(machine, args...)

			// Assert
			if code != 1 || stdout != "" || !strings.Contains(stderr, "a supervisor serves this home") {
				t.Fatalf("exit %d, %q, %q; want the refusal and exit 1", code, stdout, stderr)
			}
			after := homeFiles(t, root)
			delete(after, filepath.Join(root, "state", "board.json"))
			if !maps.Equal(before, after) {
				t.Fatalf("the refused cutover changed the home:\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

func TestCutoverRefusesEveryFormWhileAProgramRunsFromTheHomesBin(t *testing.T) {
	programs := []struct {
		name      string
		pid       int
		file      string
		want      string
		forbidden string
	}{
		{"the window", 1234, "goblins-window.exe", " (pid 1234) runs from this home; close it first", "goblins.exe"},
	}
	for _, program := range programs {
		for _, form := range []string{"--dry-run", "--plan", "--undo"} {
			t.Run(program.name+" "+form, func(t *testing.T) {
				// Arrange
				machine, root := cutoverHome(t)
				args := []string{form}
				switch form {
				case "--plan":
					args = append(args, dryRunDigest(t, machine))
				case "--undo":
					args = append(args, appliedCutover(t, machine))
				}
				before := homeFiles(t, root)
				machine.Programs = runningPrograms{{PID: program.pid, Image: filepath.Join(root, "bin", program.file)}}

				// Act
				code, stdout, stderr := runCutoverOn(machine, args...)

				// Assert
				if code != 1 || stdout != "" || !strings.Contains(stderr, filepath.Join("bin", program.file)+program.want) || strings.Contains(stderr, program.forbidden) {
					t.Fatalf("exit %d, %q, %q; want the refusal naming %s and exit 1", code, stdout, stderr, program.name)
				}
				if after := homeFiles(t, root); !maps.Equal(before, after) {
					t.Fatalf("the refused cutover changed the home:\nbefore %v\nafter  %v", before, after)
				}
			})
		}
	}
}

func TestCutoverProceedsWhileNothingRunsFromTheHomesBin(t *testing.T) {
	// Arrange
	machine, root := cutoverHome(t)
	machine.Programs = runningPrograms{
		{PID: 1, Image: machine.Program},
		{PID: 2, Image: filepath.Join(root, "binaries", "goblins-window.exe")},
		{PID: 3, Image: filepath.Join(root+" two", "bin", "goblins-window.exe")},
	}

	// Act
	folder := appliedCutover(t, machine)

	// Assert
	if content, err := os.ReadFile(filepath.Join(root, "bin", "goblins.exe")); err != nil || string(content) != "this build" {
		t.Fatalf("bin\\goblins.exe is %q, %v; want this build", content, err)
	}
	if code, _, stderr := runCutoverOn(machine, "--undo", folder); code != 0 {
		t.Fatalf("undo: exit %d, %q", code, stderr)
	}
}

// The undo of a crossing runs as the home's own goblins, from its bin: each
// program it writes there is set aside first, which Windows allows while it
// runs.
func TestCutoverProceedsWhileItRunsFromTheHomesBinItself(t *testing.T) {
	// Arrange
	machine, root := cutoverHome(t)
	machine.Programs = runningPrograms{{PID: os.Getpid(), Image: filepath.Join(root, "bin", "goblins.exe")}}

	// Act
	folder := appliedCutover(t, machine)
	code, _, stderr := runCutoverOn(machine, "--undo", folder)

	// Assert
	if code != 0 {
		t.Fatalf("undo: exit %d, %q", code, stderr)
	}
}

func TestCutoverProceedsWhenTheRecordedSupervisorDoesNotAnswer(t *testing.T) {
	// Arrange
	machine, root := cutoverHome(t)
	server := httptest.NewServer(http.NotFoundHandler())
	board := server.URL
	server.Close()
	recordSupervisor(t, root, board)

	// Act
	folder := appliedCutover(t, machine)

	// Assert
	if content, err := os.ReadFile(filepath.Join(root, "bin", "goblins.exe")); err != nil || string(content) != "this build" {
		t.Fatalf("bin\\goblins.exe is %q, %v; want this build", content, err)
	}
	if code, _, stderr := runCutoverOn(machine, "--undo", folder); code != 0 {
		t.Fatalf("undo: exit %d, %q", code, stderr)
	}
}

// TestCutoverUndoFromATerminalOpenedBeforeItGuardsTheHomeTheSettingsName
// runs the printed undo where the cutover ran: the user's settings name the
// home as this build does, and the terminal's own environment names none.
func TestCutoverUndoFromATerminalOpenedBeforeItGuardsTheHomeTheSettingsName(t *testing.T) {
	// Arrange
	_, root := cutoverHome(t)
	t.Setenv(installbase.HomeVariable, "")
	t.Setenv("LOCALAPPDATA", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"pid":%d}`, fakeBoardPID)
	}))
	defer server.Close()
	recordSupervisor(t, root, server.URL)
	before := homeFiles(t, root)
	// The folder holds no undo record, so a cutover that looked at another
	// home fails on reading it instead of undoing anything on this machine.
	folder := filepath.Join(root, "state", "backups", "cutover-20261109-120000")
	var stdout, stderr bytes.Buffer

	// Act
	code := runCutover([]string{"--undo", folder}, &stdout, &stderr, func() (cutover.Machine, error) {
		return cutoverMachine(cutoverEnv{installbase.HomeVariable: root})
	})

	// Assert
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "a supervisor serves this home") {
		t.Fatalf("exit %d, %q, %q; want the refusal for the home the settings name and exit 1", code, stdout.String(), stderr.String())
	}
	if after := homeFiles(t, root); !maps.Equal(before, after) {
		t.Fatalf("the refused undo changed the home:\nbefore %v\nafter  %v", before, after)
	}
}
