package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
)

// ProveLaunch starts executable, a build of the harness kind the fleet does
// not start goblins from yet, such as a newer version staged apart from the
// one on PATH, in a native terminal of its own named id, in dir, with the
// arguments and environment a goblin's launch of kind gets. It returns nil
// once the fleet's launch automation has brought it to its composer: every
// startup dialog it drew is one a spawn answers, and its composer waits for
// input. It types nothing into the composer, so no model is asked, and it
// closes the terminal whatever happened.
// nativeValidateWait bounds a staged build's validation, a --version or a
// --help that a working build answers at once.
var nativeValidateWait = time.Minute

func (s Service) ProveLaunch(ctx context.Context, id string, kind harness.Kind, executable, dir string) error {
	screens, ok := harness.NativeScreens(kind)
	if !ok {
		return fmt.Errorf("spawn: %s cannot run in a native terminal yet", kind)
	}
	adapter, err := s.Harness.Get(kind)
	if err != nil {
		return err
	}
	// An adapter that reads what its build can do, as pi's reads its --help,
	// reads the staged build's.
	validating, cancel := context.WithTimeout(ctx, nativeValidateWait)
	err = adapter.Validate(validating, stagedRunner{Runner: s.commands(), name: string(kind), executable: executable})
	cancel()
	if err != nil {
		return err
	}
	codexServers, err := codexMCPServers(kind)
	if err != nil {
		return fmt.Errorf("spawn: %w", err)
	}
	scratch := filepath.Join(dir, "scratch")
	// The launch's TMP names the folder beside its scratch folder, as a
	// goblin's does.
	if err := errors.Join(os.MkdirAll(scratch, 0o700), os.MkdirAll(home.SharedTempBeside(scratch), 0o700)); err != nil {
		return err
	}
	launch, err := adapter.Build(harness.LaunchSpec{BriefPath: filepath.Join(dir, "brief.md"), TaskTmp: scratch, Scratch: scratch, CodexMCPServers: codexServers})
	if err != nil {
		return fmt.Errorf("spawn: build harness launch: %w", err)
	}
	launch.Dir, launch.Executable = dir, executable
	program, err := nativeProgram(kind, launch)
	if err != nil {
		return err
	}
	userEnv, err := s.userEnvironment()
	if err != nil {
		return fmt.Errorf("spawn: read the user's environment: %w", err)
	}
	record, err := host.Launch(s.StateDir, s.HostCommand, s.nativeHostEnvironment(userEnv, launch, nil), host.Spec{ID: id, Args: program, Dir: dir, Cols: nativeCols, Rows: nativeRows})
	if err != nil {
		return fmt.Errorf("spawn: start native terminal %s: %w", id, err)
	}
	_, _, err = s.awaitNativeReady(ctx, record, screens, false)
	return errors.Join(err, host.Close(s.StateDir, record, nativeCloseWait))
}

// stagedRunner runs executable wherever a request names the harness name, so
// a harness's validation reaches the staged build rather than the one on PATH.
// A staged npm build runs through its .cmd shim, so a request given up on
// ends the shim's whole tree.
type stagedRunner struct {
	execx.Runner
	name, executable string
}

func (r stagedRunner) Run(ctx context.Context, request execx.Request) (execx.Result, error) {
	if request.Name == r.name {
		request.Name, request.KillTree = r.executable, true
	}
	return r.Runner.Run(ctx, request)
}
