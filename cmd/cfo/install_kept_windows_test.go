package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// keptHome is a code-goblins checkout an older build made the CFO home, as
// the Overlord's is: its fleet's state, data and worktrees in it, the
// previous build at its root and no bin, CFO_HOME and PATH naming it, and its
// board and the CFO's terminal host running from that root.
type keptHome struct {
	*updateHome
	tracked     map[string]string
	fleet       map[string]string
	environment string
	supervisor  *exec.Cmd
	cfoHost     *exec.Cmd
}

func newKeptHome(t *testing.T) *keptHome {
	t.Helper()
	root := filepath.Join(t.TempDir(), "code-goblins")
	k := &keptHome{
		updateHome: &updateHome{t: t, root: root, bin: filepath.Join(root, home.BinDir), state: filepath.Join(root, "state"), started: map[*exec.Cmd]time.Time{}},
		tracked:    map[string]string{"AGENTS.md": "the checkout's own contract", "cmd/cfo/main.go": "package main"},
		fleet: map[string]string{
			"state/tasks/demo-task/meta.json":     `{"id":"demo-task","status":"working"}`,
			"data/demo-task/brief.md":             "# Brief demo-task",
			".worktrees/gb-demo-task/unlanded.go": "package unlanded",
		},
	}
	for name, content := range k.files() {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cfo.exe", "goblins.exe"} {
		k.previous = writeBuild(t, filepath.Join(root, name), "previous")
	}
	standin.RemoveAtCleanup(t, root)
	t.Cleanup(k.endAll)

	k.environment = filepath.Join(t.TempDir(), "user-env.json")
	values, err := json.Marshal(map[string]string{"CFO_HOME": root, "Path": `C:\Windows;` + root})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(k.environment, values, 0o600); err != nil {
		t.Fatal(err)
	}
	k.supervisor = k.start(filepath.Join(root, "goblins.exe"), "serve", "--listen", "127.0.0.1:0")
	k.cfoHost = k.start(filepath.Join(root, "cfo.exe"), "host", "--id", "cfo")
	k.awaitBoard()
	return k
}

func (k *keptHome) files() map[string]string {
	all := map[string]string{}
	for _, files := range []map[string]string{k.tracked, k.fleet} {
		for name, content := range files {
			all[name] = content
		}
	}
	return all
}

// install runs build's cfo install on the machine, from a folder of its own,
// as the install script runs the release it downloaded: with no CFO_HOME in
// its environment, as on a machine where only the user scope names it, so a
// supervisor it starts finds its home as a real build does, from the
// environment it is given.
func (k *keptHome) install(build string) (int, string, []byte) {
	k.t.Helper()
	machine := k.t.TempDir()
	candidate := filepath.Join(machine, "release", "cfo.exe")
	if err := os.MkdirAll(filepath.Dir(candidate), 0o755); err != nil {
		k.t.Fatal(err)
	}
	data := writeBuild(k.t, candidate, build)
	cmd := exec.Command(candidate, "install")
	cmd.Dir = machine
	cmd.Env = append(os.Environ(),
		"CFO_TEST_UPDATE_RESOLVE=1", "CFO_TEST_UPDATE_SERVE_WAIT=8s", "CFO_TEST_HANDOVER_WAIT=2s",
		install.UserEnvFileVariable+"="+k.environment,
		"LOCALAPPDATA="+filepath.Join(machine, "Local"), "APPDATA="+filepath.Join(machine, "Roaming"),
		"USERPROFILE="+filepath.Join(machine, "profile"), "HOME="+filepath.Join(machine, "profile"))
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err := cmd.Run()
	code := 0
	if exit, ok := err.(*exec.ExitError); ok {
		code = exit.ExitCode()
	} else if err != nil {
		k.t.Fatal(err)
	}
	return code, output.String(), data
}

// The Overlord's machine: an install over a checkout made the home, while its
// board and the CFO run, keeps the home where it is and says so in one line,
// puts this build in bin and at the root, where his running sessions call it,
// restarts only the board on it, and leaves the CFO's terminal, the files git
// tracks and the fleet's state, data and worktrees exactly as they were.
func TestInstallOverACheckoutHomeInUseKeepsItAndItsFleetWorking(t *testing.T) {
	// Arrange
	k := newKeptHome(t)

	// Act
	code, output, candidate := k.install("candidate")

	// Assert
	if code != 0 {
		t.Fatalf("cfo install exited %d:\n%s", code, output)
	}
	if want := "Note: You already run Code Goblins from " + k.root + ", so it is updated there"; !strings.Contains(output, want) {
		t.Errorf("the install did not say %q:\n%s", want, output)
	}
	// Only this build answers /api/alive. Windows may give the restarted
	// board the number the earlier one gave up, so that one is known by its
	// start time too.
	record := k.awaitBoard()
	if err := boardAlive(context.Background(), record); err != nil {
		t.Errorf("the board (pid %d) does not serve this build (%v):\n%s", record.PID, err, output)
	}
	if k.running(k.supervisor) {
		t.Errorf("the earlier board still runs:\n%s", output)
	}
	if !k.running(k.cfoHost) {
		t.Errorf("the install ended the CFO's terminal host")
	}
	for _, program := range []string{filepath.Join(k.bin, "cfo.exe"), filepath.Join(k.bin, "goblins.exe"), filepath.Join(k.root, "cfo.exe"), filepath.Join(k.root, "goblins.exe")} {
		if got, err := os.ReadFile(program); err != nil || !bytes.Equal(got, candidate) {
			t.Errorf("%s is not this build (%v)", program, err)
		}
	}
	for name, content := range k.files() {
		if got, err := os.ReadFile(filepath.Join(k.root, filepath.FromSlash(name))); err != nil || string(got) != content {
			t.Errorf("%s = %q (%v), want it left as %q", name, got, err, content)
		}
	}
	if _, err := os.Stat(filepath.Join(k.root, home.InstalledMarker)); !os.IsNotExist(err) {
		t.Errorf("the install marked the checkout (%v)", err)
	}
	var values map[string]string
	data, err := os.ReadFile(k.environment)
	if err == nil {
		err = json.Unmarshal(data, &values)
	}
	if err != nil || values["CFO_HOME"] != k.root || values["Path"] != `C:\Windows;`+k.bin {
		t.Errorf("the user environment is %s (%v), want CFO_HOME kept and PATH naming %s", data, err, k.bin)
	}
}

// A build that does not serve gives the board back to the one that ran
// before, and the install says so in one line.
func TestAnInstalledBuildThatDoesNotServeGivesTheBoardBack(t *testing.T) {
	// Arrange
	k := newKeptHome(t)

	// Act
	code, output, _ := k.install("crash")

	// Assert
	if code != 0 {
		t.Fatalf("cfo install exited %d:\n%s", code, output)
	}
	if want := "so it runs the one before again"; !strings.Contains(output, want) {
		t.Errorf("the install did not say %q:\n%s", want, output)
	}
	record := k.awaitBoard()
	if boardAlive(context.Background(), record) == nil {
		t.Errorf("the board answers /api/alive, so not the build that ran before serves it:\n%s", output)
	}
	if !k.running(k.cfoHost) {
		t.Errorf("the install ended the CFO's terminal host")
	}
}
