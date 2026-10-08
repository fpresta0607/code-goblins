package spawn

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
)

// stagedBuild puts the fake harness at a path of its own, as a newer version
// staged apart from the one on PATH, and returns it.
func stagedBuild(t *testing.T, kind harness.Kind) string {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(t.TempDir(), "node_modules", ".bin", string(kind)+".exe")
	if err := os.MkdirAll(filepath.Dir(staged), 0o700); err != nil {
		t.Fatal(err)
	}
	copyFile(t, program, staged)
	return staged
}

// assertClosed fails unless the proof's terminal has ended.
func assertClosed(t *testing.T, stateDir, id string) {
	t.Helper()
	record, err := host.ReadRecord(stateDir, id)
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if host.Running(record) {
		t.Fatalf("the proof left its terminal running: %+v", record)
	}
}

// A staged build is proved before the fleet starts goblins from it: started
// in a native terminal of its own, as a goblin's launch starts it, it reaches
// its composer through the startup dialogs a spawn answers, nothing is typed
// into it, and its terminal is closed after.
func TestProveLaunchBringsAStagedBuildToItsComposerAndClosesIt(t *testing.T) {
	// Arrange
	f := newNativeFixture(t, harness.Codex, "")
	staged := stagedBuild(t, harness.Codex)
	// The build on PATH is gone, so only the staged one can answer.
	onPath, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(onPath); err != nil {
		t.Fatal(err)
	}

	// Act
	err = f.service.ProveLaunch(t.Context(), "harness-proof-codex", harness.Codex, staged, t.TempDir())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	assertClosed(t, f.stateDir, "harness-proof-codex")
	events := f.events(t)
	if !strings.Contains(events[0].Text, "--dangerously-bypass-approvals-and-sandbox") {
		t.Fatalf("the proof started the build without a goblin's arguments: %q", events[0].Text)
	}
	for _, event := range events {
		if event.Event == "submitted" {
			t.Fatalf("the proof typed into the composer: %+v", event)
		}
	}
}

// A build whose startup shows a screen the launch automation does not know is
// not proved, and the screen it stopped at is named.
func TestProveLaunchRefusesABuildWhoseStartupItDoesNotKnow(t *testing.T) {
	// Arrange
	previous := nativeStartup
	nativeStartup = 5 * time.Second
	t.Cleanup(func() { nativeStartup = previous })
	f := newNativeFixture(t, harness.Codex, "silent")
	staged := stagedBuild(t, harness.Codex)

	// Act
	err := f.service.ProveLaunch(t.Context(), "harness-proof-codex", harness.Codex, staged, t.TempDir())

	// Assert
	if err == nil || !strings.Contains(err.Error(), "showed neither a dialog it knows nor its harness's composer") {
		t.Fatalf("err = %v, want the startup it did not know", err)
	}
	assertClosed(t, f.stateDir, "harness-proof-codex")
}

// validatingAdapter validates the way the codex and pi adapters do, by
// running the harness by name, and keeps the program it reached.
type validatingAdapter struct {
	nativeAdapter
	reached *string
}

func (a validatingAdapter) Validate(ctx context.Context, runner execx.Runner) error {
	_, err := runner.Run(ctx, execx.Request{Name: string(a.kind), Args: []string{"--help"}})
	return err
}

// reachedRunner keeps the program a request names and answers it at once.
type reachedRunner struct{ reached *string }

func (r reachedRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	*r.reached = request.Name
	return execx.Result{}, nil
}

// A harness whose launch depends on what its build says it can do, as pi's
// does, is asked of the staged build, never of the one on PATH.
func TestProveLaunchValidatesTheStagedBuild(t *testing.T) {
	// Arrange
	f := newNativeFixture(t, harness.Codex, "ready")
	staged := stagedBuild(t, harness.Codex)
	var reached string
	f.service.Commands = reachedRunner{reached: &reached}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: validatingAdapter{nativeAdapter: nativeAdapter{kind: harness.Codex}, reached: &reached}}}

	// Act
	err := f.service.ProveLaunch(t.Context(), "harness-proof-codex", harness.Codex, staged, t.TempDir())

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if reached != staged {
		t.Fatalf("validated %q, want the staged build %s", reached, staged)
	}
}

// stuckRunner answers nothing until its request is given up on, as a build
// that hangs on --version does.
type stuckRunner struct{}

func (stuckRunner) Run(ctx context.Context, _ execx.Request) (execx.Result, error) {
	<-ctx.Done()
	return execx.Result{}, ctx.Err()
}

// A staged build that never answers its validation is given up on, and no
// terminal is started for it.
func TestProveLaunchGivesUpOnAValidationThatNeverEnds(t *testing.T) {
	// Arrange
	previous := nativeValidateWait
	nativeValidateWait = time.Second
	t.Cleanup(func() { nativeValidateWait = previous })
	f := newNativeFixture(t, harness.Codex, "ready")
	staged := stagedBuild(t, harness.Codex)
	var reached string
	f.service.Commands = stuckRunner{}
	f.service.Harness = harness.Registry{Adapters: map[harness.Kind]harness.Adapter{harness.Codex: validatingAdapter{nativeAdapter: nativeAdapter{kind: harness.Codex}, reached: &reached}}}

	// Act
	err := f.service.ProveLaunch(t.Context(), "harness-proof-codex", harness.Codex, staged, t.TempDir())

	// Assert
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the validation given up on", err)
	}
	if _, err := host.ReadRecord(f.stateDir, "harness-proof-codex"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a terminal was started for a build that never validated: %v", err)
	}
}
