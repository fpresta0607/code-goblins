package gatehome

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/installbase"
)

// Daemon is the gate's daemon for the home at root, as a move needs it.
type Daemon interface {
	// Stop stops root's daemon by its own consent. It is refused while a
	// gate run is in flight, and never forced.
	Stop(ctx context.Context, root string) error
	// Hold keeps any daemon of root from starting and proves no gate run is
	// in flight, until release.
	Hold(ctx context.Context, root string) (release func() error, err error)
	// Start installs root's scheduled task and starts its daemon, and
	// returns once it runs. A daemon that runs already is started.
	Start(ctx context.Context, root string) error
	// Forget removes the scheduled task that starts root's daemon at
	// sign-in, which no-mistakes names after the home and a stop leaves.
	Forget(ctx context.Context, root string) error
}

// ErrGateBusy is a move that changed nothing because the gate never came to
// rest while it waited: gate runs stayed in flight, or a daemon held the
// home. The same move carries the home once the gate rests.
var ErrGateBusy = errors.New("gate home: the gate stayed busy")

// Mover moves the gate's home onto the Dev Drive.
type Mover struct {
	Daemon Daemon
	// Env is the user's persistent environment, where NM_HOME is set.
	Env installbase.EnvStore
	// Git runs git, to point each gated checkout's no-mistakes remote at
	// its moved repository.
	Git execx.Runner
	// Tries is how many times the daemon is asked to stop and be held off
	// before the move gives up while gate runs stay in flight, Every apart;
	// at least one.
	Tries int
	Every time.Duration
	Now   func() time.Time
	// Waiting is told each time the daemon refused to stop or to be held
	// off.
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
// What either carries without a copy keeps its links, and each one that
// leads into the old home is made again to lead to the same place in the new
// one, as the copy makes its links. It returns what it did, in a line for the person, and whether the user's
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
	release, err := m.rest(ctx, old)
	if err != nil {
		return "", false, err
	}
	move := &moving{mover: m, old: old, target: target, plan: plan, release: release}
	if err := move.run(ctx); err != nil {
		return "", false, errors.Join(err, move.undo(context.WithoutCancel(ctx)))
	}
	return move.finish(context.WithoutCancel(ctx)), true, nil
}

// carryInPlace carries a home that is itself a junction to target without a
// copy: the daemon stops and is held off, the records in target are pointed
// at it, its links into the old home are made again to lead into target,
// NM_HOME and each gated checkout's remote name it, and the daemon
// starts there. Only then the junction goes, as a link alone, and target is
// read again for the database and every repository it held before.
func (m Mover) carryInPlace(ctx context.Context, old, target string) (string, bool, error) {
	repositories, err := filepath.Glob(filepath.Join(target, "repos", "*.git"))
	if err != nil {
		return "", false, err
	}
	release, err := m.rest(ctx, old)
	if err != nil {
		return "", false, err
	}
	move := &moving{mover: m, old: old, target: target, release: release}
	if err := move.run(ctx); err != nil {
		return "", false, errors.Join(err, move.undo(context.WithoutCancel(ctx)))
	}
	line, err := move.unlink(repositories)
	if err == nil {
		line += move.forget(context.WithoutCancel(ctx))
	}
	return line, true, err
}

// moving is one move after the daemon stopped and was held off, which release
// ends, with what it changed so far for its undo.
type moving struct {
	mover       Mover
	old, target string
	// plan is what the move copies, nil for a home carried in place.
	plan        *copyPlan
	release     func() error
	edits       []edit
	relinks     []relink
	previous    string
	hadPrevious bool
	isEnvSet    bool
	remotes     []remote
	isStarted   bool
}

func (m *moving) run(ctx context.Context) error {
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
	inPlace := []string{m.target}
	if m.plan != nil {
		inPlace = m.plan.kept
	}
	for _, folder := range inPlace {
		if err := walkLinks(folder, m.rebase); err != nil {
			return fmt.Errorf("gate home: the links in %s that lead through %s could not be pointed at it, so nothing moved: %w", folder, m.old, err)
		}
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
	release := m.release
	m.release = nil
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
		errs = append(errs, m.mover.Daemon.Stop(ctx, m.target), m.mover.Daemon.Forget(ctx, m.target))
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
	for _, relink := range slices.Backward(m.relinks) {
		errs = append(errs, relink.restore())
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

// relink is a link the move made again to lead where it carried what it led
// to, with where it led before, for the undo.
type relink struct {
	path, linked            string
	isJunction, isDirectory bool
}

// rebase makes the link at path, which leads to linked, again as its own
// kind to lead to the same place in target when linked is in the old home,
// and proves at once it leads there. Any other link stays as it is.
func (m *moving) rebase(path, linked string) error {
	want := carried(m.old, m.target, linked)
	if want == linked {
		return nil
	}
	isJunction, isDirectory, err := linkKind(path)
	if err != nil {
		return err
	}
	if err := removeLink(path); err != nil {
		return err
	}
	m.relinks = append(m.relinks, relink{path: path, linked: linked, isJunction: isJunction, isDirectory: isDirectory})
	if err := makeLink(path, want, isJunction, isDirectory); err != nil {
		return fmt.Errorf("the link %s could not be made again to lead to %s: %w", path, want, err)
	}
	if got, isLink := linkTarget(path); !isLink || !sameFolder(got, want) {
		return fmt.Errorf("%s does not lead to %s, where the move carries the link", path, want)
	}
	return nil
}

// restore makes the link again as its own kind to lead where it led before
// the move, removing the link the move made in its place.
func (r relink) restore() error {
	if _, err := os.Lstat(r.path); err == nil {
		if err := removeLink(r.path); err != nil {
			return err
		}
	}
	return makeLink(r.path, r.linked, r.isJunction, r.isDirectory)
}

// finish removes the old home's sign-in task, the old home, its links as
// links so what they lead to stays, and what a move by hand left aside, and
// says what the move did.
func (m *moving) finish(ctx context.Context) string {
	line := fmt.Sprintf("moved: the gate's home is %s on the Dev Drive, named by NM_HOME for every program started from now on, and its daemon runs there; %d gated checkout(s) push to it", m.target, len(m.remotes))
	line += m.forget(ctx)
	if err := removeTree(m.old); err != nil {
		line += fmt.Sprintf("; the old home %s could not be removed (%v) and stays as a backup", m.old, err)
	} else {
		line += "; the old home " + m.old + " is gone"
	}
	if err := m.plan.done(); err != nil {
		line += fmt.Sprintf("; what an earlier move left in the Dev Drive folder may stay in %s (%v)", m.plan.aside, err)
	}
	return line
}

// forget removes the old home's sign-in task once the daemon runs on the new
// one, and says so when it could not: the move is done either way.
func (m *moving) forget(ctx context.Context) string {
	if err := m.mover.Daemon.Forget(ctx, m.old); err != nil {
		return fmt.Sprintf("; the sign-in task that started the daemon on %s could not be removed (%v)", m.old, err)
	}
	return ""
}

// unlink removes the junction at the old home as a link alone, once the
// daemon runs on target, then proves target still holds its database and
// each repository it held before the move, and says what the move did.
func (m *moving) unlink(repositories []string) (string, error) {
	info, err := os.Lstat(m.old)
	if err != nil {
		return "", fmt.Errorf("gate home: the gate's home is %s, named by NM_HOME, but the junction %s could not be read: %w", m.target, m.old, err)
	}
	if !isLinkMode(info) {
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

func (m Mover) setEnv(target string) error {
	if err := m.Env.Set(Variable, target); err != nil {
		return err
	}
	return m.Env.Broadcast()
}

// rest brings the gate of the home at root to rest: it asks the daemon to
// stop, which it refuses while a gate run is in flight, and then holds it
// off, until both hold or the tries run out. A move that gives up, or is
// interrupted while it waits, starts a daemon it stopped again and says
// whether the daemon was stopped and whether it runs now.
func (m Mover) rest(ctx context.Context, root string) (func() error, error) {
	var err error
	var wasStopped bool
	for try := 0; try < max(1, m.Tries) && ctx.Err() == nil; try++ {
		if try > 0 {
			select {
			case <-ctx.Done():
				continue
			case <-time.After(m.Every):
			}
		}
		if err = m.Daemon.Stop(ctx, root); err == nil {
			wasStopped = true
			var release func() error
			if release, err = m.Daemon.Hold(ctx, root); err == nil {
				return release, nil
			}
		}
		if m.Waiting != nil {
			m.Waiting(err)
		}
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	} else {
		err = fmt.Errorf("%w: %w", ErrGateBusy, err)
	}
	daemon := "the daemon was not stopped and runs as before"
	if wasStopped {
		daemon = "the daemon was stopped and runs again on " + root
		if startErr := m.Daemon.Start(context.WithoutCancel(ctx), root); startErr != nil {
			daemon = "the daemon was stopped and did not start again on " + root
			err = errors.Join(err, startErr)
		}
	}
	return nil, fmt.Errorf("gate home: nothing moved, and %s; the home moves once no gate run is in flight: %w", daemon, err)
}
