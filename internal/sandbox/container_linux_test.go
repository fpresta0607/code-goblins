package sandbox

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/services"
)

// realDockerVariable set to 1 has these tests run their commands in real
// containers of the machine's Docker engine, as the sandbox workflow does on
// its hosted runner. Unset, they skip, since no other test runs a container.
const realDockerVariable = "GOBLINS_REAL_DOCKER"

// proofImage is the small public image the tests run, pinned to one build.
// Its /etc/alpine-release is in no runner's own file system.
const proofImage = "alpine:3.22@sha256:49340b346798875b5592e5d420241f2f9cfba5f47e62ebb1446a1b34734e898f"

// hostOnlyVariable is set in the test's own environment, which no container
// of a sandbox task may see.
const hostOnlyVariable = "GOBLINS_PROBE_HOST_ONLY"

// containerProbe says, one line each, what the command found in the
// container: the image's release, where it ran, a file of the worktree,
// the host's variable, and whether it could write the worktree's .git and
// the scratch folder.
const containerProbe = `echo "release=$(cat /etc/alpine-release 2>/dev/null || echo none)"
echo "pwd=$(pwd)"
echo "planted=$(cat /work/planted.txt 2>/dev/null || echo none)"
echo "host_variable=${` + hostOnlyVariable + `:-unset}"
if echo probe >> /work/.git 2>/dev/null; then echo git=written; else echo git=refused; fi
if echo probe > /scratch/probe.txt 2>/dev/null; then echo scratch=written; else echo scratch=refused; fi`

// requireRealDocker skips the test unless realDockerVariable asks for real
// containers, and then fails it unless the engine answers and runs Linux
// containers, so the workflow's run never passes with nothing proved.
func requireRealDocker(t *testing.T) {
	t.Helper()
	if os.Getenv(realDockerVariable) != "1" {
		t.Skipf("runs real containers only with %s=1, as the sandbox workflow sets it", realDockerVariable)
	}
	output, err := exec.Command("docker", "info", "--format", "{{.OSType}}").Output()
	if err != nil || strings.TrimSpace(string(output)) != "linux" {
		t.Fatalf("docker info = %q (%v), want an engine that runs Linux containers", output, err)
	}
}

// containedRun is a run of command in proofImage for task cg-proof, of a
// worktree that holds a .git link and planted.txt, with a scratch folder of
// its own, whose container is removed once the test ends if it is left.
func containedRun(t *testing.T, command ...string) Run {
	t.Helper()
	root := t.TempDir()
	worktree, scratch := filepath.Join(root, "worktree"), filepath.Join(root, "scratch")
	for _, dir := range []string{worktree, scratch} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(worktree, ".git"), []byte("gitdir: /project/.git/worktrees/cg-proof\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "planted.txt"), []byte("from-the-worktree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	name := "goblins-sandbox-cg-proof-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", name).Run() })
	return Run{Task: "cg-proof", Name: name, Image: proofImage, Checkout: worktree, Scratch: scratch, Cwd: worktree, Command: command}
}

// dockerRun is the docker run goblins starts for r.
func dockerRun(t *testing.T, r Run) *exec.Cmd {
	t.Helper()
	arguments, err := DockerArguments(r)
	if err != nil {
		t.Fatal(err)
	}
	return exec.Command("docker", arguments...)
}

// containers are the ids of every container, running or not, named name.
func containers(t *testing.T, name string) []string {
	t.Helper()
	output, err := exec.Command("docker", "ps", "--all", "--quiet", "--filter", "name=^/"+name+"$").Output()
	if err != nil {
		t.Fatalf("docker ps --filter name=%s: %v", name, err)
	}
	return strings.Fields(string(output))
}

// isRunning reports whether the container name runs, looked for every
// 200 ms for at most two minutes, which an image's first pull fits in. It
// returns as soon as it sees the container, so the bound only keeps the look
// from waiting forever.
func isRunning(t *testing.T, name string) bool {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Minute); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		output, err := exec.Command("docker", "ps", "--quiet", "--filter", "name=^/"+name+"$", "--filter", "status=running").Output()
		if err != nil {
			t.Fatalf("docker ps --filter name=%s: %v", name, err)
		}
		if len(strings.Fields(string(output))) == 1 {
			return true
		}
	}
	return false
}

// The docker run goblins starts for a sandbox task's command runs it in a
// real container of the task's image: the command sees the worktree at
// /work, where it runs, and the scratch folder at /scratch, sees none of the
// host's variables, and cannot write the worktree's .git, and the container
// is gone once the command ended.
func TestDockerArgumentsContainTheCommandInARealContainer(t *testing.T) {
	// Arrange
	requireRealDocker(t)
	run := containedRun(t, "sh", "-c", containerProbe)
	t.Setenv(hostOnlyVariable, "from-the-host")
	link, err := os.ReadFile(filepath.Join(run.Checkout, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	command := dockerRun(t, run)
	var complaint bytes.Buffer
	command.Stderr = &complaint

	// Act
	said, err := command.Output()

	// Assert
	if err != nil {
		t.Fatalf("docker run: %v; stdout: %s; stderr: %s", err, said, complaint.String())
	}
	lines := strings.Split(strings.TrimSpace(string(said)), "\n")
	if !strings.HasPrefix(lines[0], "release=3.22") {
		t.Fatalf("the probe said %q, want release=3.22 first: it ran outside the image", said)
	}
	for _, want := range []string{"pwd=/work", "planted=from-the-worktree", "host_variable=unset", "git=refused", "scratch=written"} {
		if !slices.Contains(lines, want) {
			t.Fatalf("the probe said %q, want the line %s", said, want)
		}
	}
	if after, err := os.ReadFile(filepath.Join(run.Checkout, ".git")); err != nil || string(after) != string(link) {
		t.Fatalf("the worktree's .git = %q (%v) after the run, want it as it was, %q", after, err, link)
	}
	if wrote, err := os.ReadFile(filepath.Join(run.Scratch, "probe.txt")); err != nil || string(wrote) != "probe\n" {
		t.Fatalf("the scratch folder's probe.txt = %q (%v), want what the container wrote at /scratch", wrote, err)
	}
	if left := containers(t, run.Name); len(left) != 0 {
		t.Fatalf("containers named %s after the run = %q, want none", run.Name, left)
	}
}

// A gate's Test step, which the gate gives as cmd.exe /c, runs as sh -c in a
// real container of the task's image, at the gate's checkout.
func TestShellCommandRunsAGateStepInARealContainer(t *testing.T) {
	// Arrange
	requireRealDocker(t)
	run := containedRun(t, ShellCommand([]string{"cmd.exe", "/c", `echo "step=$(cat /etc/alpine-release 2>/dev/null || echo none) $(pwd)"`})...)
	command := dockerRun(t, run)
	var complaint bytes.Buffer
	command.Stderr = &complaint

	// Act
	said, err := command.Output()

	// Assert
	if err != nil {
		t.Fatalf("docker run: %v; stdout: %s; stderr: %s", err, said, complaint.String())
	}
	if !strings.HasPrefix(string(said), "step=3.22") || !strings.HasSuffix(strings.TrimSpace(string(said)), " /work") {
		t.Fatalf("the step said %q, want step=3.22 at /work: it ran outside the image", said)
	}
}

// A docker run that is itself ended, as an interrupt ends it, leaves its
// container running, and the engine goblins removes a run's container with
// removes it, so nothing of the task runs on.
func TestRemoveContainerRemovesTheRealContainerOfAnInterruptedDockerRun(t *testing.T) {
	// Arrange
	requireRealDocker(t)
	run := containedRun(t, "sleep", "300")
	client := dockerRun(t, run)
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	if !isRunning(t, run.Name) {
		t.Fatalf("the container %s did not run within two minutes", run.Name)
	}
	if err := client.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = client.Wait()
	if !isRunning(t, run.Name) {
		t.Fatalf("the premise failed: the docker run ended by a kill took its container %s with it", run.Name)
	}
	engine := services.CLI{Commands: execx.OSRunner{}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Act
	err := engine.RemoveContainer(ctx, run.Name)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if left := containers(t, run.Name); len(left) != 0 {
		t.Fatalf("containers named %s after the removal = %q, want none", run.Name, left)
	}
}
