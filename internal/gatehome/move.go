package gatehome

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/installbase"
)

// Daemon is the gate's daemon for the home at root, as a move needs it.
type Daemon interface {
	// Uninstall stops root's daemon and removes the scheduled task that
	// starts it at sign-in, which no-mistakes names after the home. It is
	// refused while a gate run is in flight, and never forced.
	Uninstall(ctx context.Context, root string) error
	// Hold keeps any daemon of root from starting and proves no gate run is
	// in flight, until release.
	Hold(ctx context.Context, root string) (release func() error, err error)
	// Start installs root's scheduled task and starts its daemon, and
	// returns once it runs.
	Start(ctx context.Context, root string) error
}

// Mover moves the gate's home onto the Dev Drive.
type Mover struct {
	Daemon Daemon
	// Env is the user's persistent environment, where NM_HOME is set.
	Env installbase.EnvStore
	// Git runs git, to point each gated checkout's no-mistakes remote at
	// its moved repository.
	Git execx.Runner
	// Tries is how many times the daemon is asked to stop before the move
	// gives up while gate runs stay in flight, Every apart; at least one.
	Tries int
	Every time.Duration
	Now   func() time.Time
	// Waiting is told each time the daemon refused to stop.
	Waiting func(err error)
}

// Move moves the gate's home at old into the Dev Drive folder devDrive
// through one path. It waits until no gate run is in flight and the daemon
// stops by its own consent, never a forced stop, and holds it off; copies the
// home and proves every copied file holds the same bytes; points the copy's
// own records at its new place; then switches: the user's NM_HOME names the
// copy, each gated checkout's no-mistakes remote names its moved repository,
// and the daemon starts on the copy. Only once it runs there does the old home
// go. A step that fails undoes every step before it and starts the daemon on
// the old home again, which the move never changed. No gate run is in flight
// while it moves, so the daemon has none to recover on the copy.
//
// A home already in the Dev Drive folder changes nothing, and a home not made
// yet is named on the Dev Drive from the start. A home whose folders a move by
// hand put in the Dev Drive folder behind junctions moves around them: they
// stay where they are, and what else that move left in the Dev Drive folder
// is set aside and goes once the move is done. A home that is itself a
// junction to the gate's home on the Dev Drive is carried there in place.
// It returns what it did, in a line for the person, and whether the user's
// NM_HOME names the Dev Drive by this call, which an error after the switch
// does not undo.
func (m Mover) Move(ctx context.Context, old, devDrive string) (string, bool, error) {
	if devDrive == "" {
		return "", false, errors.New("gate home: the fleet has no Dev Drive folder to move it to")
	}
	target := Target(devDrive)
	if isUnder(old, devDrive) {
		return "already on the Dev Drive: the gate's home is " + old + ", named by NM_HOME; nothing changed", false, nil
	}
	if _, err := os.Lstat(old); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(target, 0o700); err != nil {
			return "", false, err
		}
		if err := m.setEnv(target); err != nil {
			return "", false, err
		}
		return fmt.Sprintf("placed: the gate's home is %s on the Dev Drive from the start, named by NM_HOME", target), true, nil
	}
	if linked, isLink := linkTarget(old); isLink && sameFolder(linked, target) {
		return m.carryInPlace(ctx, old, target)
	}
	plan, err := planCopy(old, target, devDrive, m.Now())
	if err != nil {
		return "", false, err
	}
	if err := m.stop(ctx, old); err != nil {
		return "", false, err
	}
	move := &moving{mover: m, old: old, target: target, plan: plan}
	if err := move.run(ctx); err != nil {
		return "", false, errors.Join(err, move.undo(context.WithoutCancel(ctx)))
	}
	return move.finish(), true, nil
}

// carryInPlace carries a home that is itself a junction to target without a
// copy: the daemon stops and is held off, the records in target are pointed
// at it, NM_HOME and each gated checkout's remote name it, and the daemon
// starts there. Only then the junction goes, as a link alone, and target is
// read again for the database and every repository it held before.
func (m Mover) carryInPlace(ctx context.Context, old, target string) (string, bool, error) {
	repositories, err := filepath.Glob(filepath.Join(target, "repos", "*.git"))
	if err != nil {
		return "", false, err
	}
	if err := m.stop(ctx, old); err != nil {
		return "", false, err
	}
	move := &moving{mover: m, old: old, target: target}
	if err := move.run(ctx); err != nil {
		return "", false, errors.Join(err, move.undo(context.WithoutCancel(ctx)))
	}
	line, err := move.unlink(repositories)
	return line, true, err
}

// moving is one move after the daemon stopped, with what it changed so far
// for its undo.
type moving struct {
	mover       Mover
	old, target string
	// plan is what the move copies, nil for a home carried in place.
	plan        *copyPlan
	release     func() error
	edits       []edit
	previous    string
	hadPrevious bool
	isEnvSet    bool
	remotes     []remote
	isStarted   bool
}

func (m *moving) run(ctx context.Context) error {
	release, err := m.mover.Daemon.Hold(ctx, m.old)
	if err != nil {
		return fmt.Errorf("gate home: the daemon stopped but could not be held off, so nothing moved: %w", err)
	}
	m.release = release
	if m.plan != nil {
		if err := m.plan.setAside(); err != nil {
			return fmt.Errorf("gate home: what a move by hand left in %s could not be set aside, so nothing moved: %w", m.target, err)
		}
		if err := m.plan.copy(ctx); err != nil {
			return fmt.Errorf("gate home: the copy to %s failed, so nothing moved: %w", m.target, err)
		}
	}
	edits, err := pointRecords(ctx, m.old, m.target)
	m.edits = edits
	if err != nil {
		return fmt.Errorf("gate home: the records in %s could not be pointed at it, so nothing moved: %w", m.target, err)
	}
	if m.previous, m.hadPrevious, err = m.mover.Env.Get(Variable); err != nil {
		return err
	}
	m.isEnvSet = true
	if err := m.mover.setEnv(m.target); err != nil {
		return err
	}
	m.remotes, err = repointRemotes(ctx, m.mover.Git, m.old, m.target)
	if err != nil {
		return err
	}
	release, m.release = m.release, nil
	if err := release(); err != nil {
		return err
	}
	m.isStarted = true
	if err := m.mover.Daemon.Start(ctx, m.target); err != nil {
		return fmt.Errorf("gate home: the daemon did not start on %s, so the move was undone: %w", m.target, err)
	}
	return nil
}

// undo reverses what run did, last step first, and starts the daemon on the
// old home again.
func (m *moving) undo(ctx context.Context) error {
	var errs []error
	if m.isStarted {
		errs = append(errs, m.mover.Daemon.Uninstall(ctx, m.target))
	}
	errs = append(errs, restoreRemotes(ctx, m.mover.Git, m.remotes))
	if m.isEnvSet {
		if m.hadPrevious {
			errs = append(errs, m.mover.Env.Set(Variable, m.previous))
		} else {
			errs = append(errs, m.mover.Env.Unset(Variable))
		}
		errs = append(errs, m.mover.Env.Broadcast())
	}
	errs = append(errs, restoreEdits(m.edits))
	if m.plan != nil {
		errs = append(errs, m.plan.discard(), m.plan.restore())
	}
	if m.release != nil {
		errs = append(errs, m.release())
	}
	if err := m.mover.Daemon.Start(ctx, m.old); err != nil {
		errs = append(errs, fmt.Errorf("gate home: the daemon did not start again on %s: %w", m.old, err))
	}
	return errors.Join(errs...)
}

// finish removes the old home, its links as links so what they lead to on
// the Dev Drive stays, and what a move by hand left aside, and says what the
// move did.
func (m *moving) finish() string {
	line := fmt.Sprintf("moved: the gate's home is %s on the Dev Drive, named by NM_HOME for every program started from now on, and its daemon runs there; %d gated checkout(s) push to it", m.target, len(m.remotes))
	if err := removeHome(m.old); err != nil {
		line += fmt.Sprintf("; the old home %s could not be removed (%v) and stays as a backup", m.old, err)
	} else {
		line += "; the old home " + m.old + " is gone"
	}
	if err := m.plan.done(); err != nil {
		line += fmt.Sprintf("; what an earlier move left in the Dev Drive folder may stay in %s (%v)", m.plan.aside, err)
	}
	return line
}

// unlink removes the junction at the old home as a link alone, once the
// daemon runs on target, then proves target still holds its database and
// each repository it held before the move, and says what the move did.
func (m *moving) unlink(repositories []string) (string, error) {
	info, err := os.Lstat(m.old)
	if err != nil {
		return "", fmt.Errorf("gate home: the gate's home is %s, named by NM_HOME, but the junction %s could not be read: %w", m.target, m.old, err)
	}
	if info.Mode().Type()&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
		return "", fmt.Errorf("gate home: the gate's home is %s, named by NM_HOME, but %s is no longer a junction, so it stays", m.target, m.old)
	}
	if err := os.Remove(m.old); err != nil {
		return "", fmt.Errorf("gate home: the gate's home is %s, named by NM_HOME, but the junction %s could not be removed: %w", m.target, m.old, err)
	}
	var missing []string
	for _, path := range append([]string{filepath.Join(m.target, "state.sqlite")}, repositories...) {
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, path)
		}
	}
	if len(missing) > 0 {
		return "", fmt.Errorf("gate home: the junction %s is gone, but the gate's home %s lacks %s", m.old, m.target, strings.Join(missing, ", "))
	}
	return fmt.Sprintf("moved: the gate's home is %s on the Dev Drive, named by NM_HOME for every program started from now on, and its daemon runs there; %d gated checkout(s) push to it; the junction %s is gone, removed as a link", m.target, len(m.remotes), m.old), nil
}

// removeHome removes each link in the old home, which leads to what the
// moved home now holds, then the rest.
func removeHome(old string) error {
	err := walkLinks(old, func(path, _ string) error { return os.Remove(path) })
	if err != nil {
		return err
	}
	return fsx.RemoveAll(old)
}

func (m Mover) setEnv(target string) error {
	if err := m.Env.Set(Variable, target); err != nil {
		return err
	}
	return m.Env.Broadcast()
}

// stop asks the daemon to stop until it does or the tries run out.
func (m Mover) stop(ctx context.Context, root string) error {
	var err error
	for try := 0; try < max(1, m.Tries); try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(m.Every):
			}
		}
		if err = m.Daemon.Uninstall(ctx, root); err == nil {
			return nil
		}
		if m.Waiting != nil {
			m.Waiting(err)
		}
	}
	return fmt.Errorf("gate home: the daemon did not stop while gate runs stay in flight, so nothing moved; it moves once no gate run is in flight: %w", err)
}
