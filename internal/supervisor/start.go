package supervisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
)

// Start is idempotent and confirms process identity plus a fresh observation.
// It never replaces a live but stale owner or a legacy hook watcher.
func Start(ctx context.Context, h home.Home) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return startProcess(ctx, h, exec.Command(exe, "supervisor", "run"))
}

func startProcess(ctx context.Context, h home.Home, cmd *exec.Cmd) error {
	if err := os.MkdirAll(h.State, 0700); err != nil {
		return err
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, ".supervisor-start.lock"); err != nil {
		return err
	}
	defer lock.ReleaseExclusiveNamed(h.State, ".supervisor-start.lock")
	if Status(h.State).Observing {
		return nil
	}
	if _, err := ReadPrimary(h.State); err != nil {
		return fmt.Errorf("supervisor: register an explicit primary endpoint before starting: %w", err)
	}
	for _, name := range []string{LockName, ".watch.lock"} {
		owner, err := lock.ReadNamed(h.State, name)
		if err == nil && owner.Alive() {
			return fmt.Errorf("supervisor: live owner of %s already exists; inspect it before recovery", name)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	cmd.Env = append(cleanHomeEnv(), "CFO_HOME="+h.Root, "CFO_STATE_OVERRIDE="+h.State)
	// A detached child has no console reader. Preserve its own diagnostic log;
	// wake delivery is the registered native agent path, independent of stdio.
	if err := preservePreviousLog(h.State); err != nil {
		return err
	}
	log, err := os.OpenFile(filepath.Join(h.State, "supervisor.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	configureProcess(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	defer cmd.Process.Release()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("supervisor: readiness not confirmed: %w; inspect supervisor status before retrying", ctx.Err())
		case <-ticker.C:
			status := Status(h.State)
			if status.Observing && status.PID == cmd.Process.Pid {
				return nil
			}
		}
	}
}

func preservePreviousLog(dir string) error {
	current, err := os.Open(filepath.Join(dir, "supervisor.log"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer current.Close()
	info, err := current.Stat()
	if err != nil {
		return err
	}
	const limit int64 = 1 << 20
	start := max(int64(0), info.Size()-limit)
	data, err := io.ReadAll(io.NewSectionReader(current, start, limit))
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(dir, "supervisor.previous.log"), data)
}

func cleanHomeEnv() []string {
	var result []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.EqualFold(name, "CFO_HOME") && !strings.EqualFold(name, "CFO_STATE_OVERRIDE") {
			result = append(result, entry)
		}
	}
	return result
}
