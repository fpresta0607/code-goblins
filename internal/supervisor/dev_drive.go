package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/gatehome"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// DevDrive is the board's Dev Drive setting and the Command Center items that
// set one up; without it the board shows neither. Every step that needs a
// person is one of those items, made here, in order, one at a time: create
// the drive, trust it, move the home's heavy folders onto it, and attach it
// again whenever its startup task did not. The last step, carrying the gate's
// home onto the drive, needs nobody, so the supervisor takes it itself.
type DevDrive struct {
	// Read reads what this machine says about Dev Drives.
	Read func(context.Context) (devdrive.Machine, error)
	// GateHome is where the gate's home is now (gatehome.Root); without it
	// the board has no step for the gate's home, and with it GateRuns and
	// CarryGateHome are set too.
	GateHome func() (string, error)
	// Boot is when the machine last started (devdrive.BootTime); without it
	// the attach item comes once, until the person asks again.
	Boot func() time.Time
	// StartTask starts the drive's startup task without elevation
	// (devdrive.StartTask); without it a missing drive gets the attach item
	// at once.
	StartTask func(context.Context) error
	// TaskCurrent says whether the startup task is the one this build
	// registers (devdrive.TaskCurrent); without it no item renews the task.
	TaskCurrent func(context.Context) (bool, error)
	// GateRuns counts the gate runs in flight in the gate's home at root, a
	// run parked on an answer among them.
	GateRuns func(ctx context.Context, root string) (int, error)
	// CarryGateHome carries the gate's home into the Dev Drive folder and
	// returns once that ended: nil when the home is there, an error that is
	// gatehome.ErrGateBusy when gate runs stayed in flight all the while it
	// waited, and otherwise what went wrong, in a line for the person.
	CarryGateHome func(ctx context.Context, folder string) error
}

// gateCarry is the supervisor's own carry of the gate's home onto the Dev
// Drive: whether one runs now, and why the last one failed for a reason
// other than a busy gate, which waits for the person to ask again.
type gateCarry struct {
	mu        sync.Mutex
	isRunning bool
	failed    string
	failedAt  time.Time
}

// DevDriveView is the Dev Drive as the board's Workspace panel shows it.
type DevDriveView struct {
	State string `json:"state"`
	Line  string `json:"line"`
	// Note is the one short note under the panel's Dev Drive row.
	Note    string `json:"note"`
	Explain string `json:"explain"`
	Choice  string `json:"choice,omitempty"`
	// Waiting is the step whose Command Center item waits to be run.
	Waiting string `json:"waiting,omitempty"`
	// Action is the button the panel offers: set-up, try-again or attach;
	// empty when a press would do nothing the supervisor does not do alone.
	Action string `json:"action,omitempty"`
	// Status is the panel's Dev Drive row in plain words: what is done, what
	// is left and what it waits on. Empty when nothing is left, and the
	// panel then shows no row.
	Status string `json:"status,omitempty"`
}

// The panel's buttons.
const (
	devDriveSetUp    = "set-up"
	devDriveTryAgain = "try-again"
	devDriveAttach   = "attach"
)

// watchDevDrive keeps the Dev Drive's view and items to the machine: now,
// every minute while a step is under way and every half hour otherwise,
// whenever the person asks, one of its items ends or its own carry of the
// gate's home ends, and at the next tick
// after config\dev-drive.json changed, as goblins dev-drive setup, goblins dev-drive
// move and the install change it. Only that file is read each tick; the
// machine is read when a look is due.
func (s *Service) watchDevDrive(ctx context.Context) {
	tick := s.devDriveTick
	if tick == 0 {
		tick = time.Minute
	}
	var due time.Time
	var seen devDriveMark
	for {
		if mark := s.devDriveMark(); mark != seen || !time.Now().Before(due) {
			due = time.Now().Add(s.keepDevDrive(ctx))
			seen = s.devDriveMark()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(tick):
		case <-s.devDriveNow:
			due = time.Time{}
		}
	}
}

// devDriveMark is when config\dev-drive.json last changed and its size, the
// zero mark while there is none.
type devDriveMark struct {
	modified time.Time
	size     int64
}

func (s *Service) devDriveMark() devDriveMark {
	info, err := os.Stat(home.DevDriveConfigPath(s.Store.Home.Root))
	if err != nil {
		return devDriveMark{}
	}
	return devDriveMark{modified: info.ModTime(), size: info.Size()}
}

// devDriveAgain asks the Dev Drive watch for another look now.
func (s *Service) devDriveAgain() {
	select {
	case s.devDriveNow <- struct{}{}:
	default:
	}
}

// keepDevDrive reads the machine and the home's config\dev-drive.json, makes
// the next step's item when one is due, or takes the gate step itself, and
// keeps the view the board shows.
// It reads the config each time rather than the home this supervisor started
// with, which a move changes.
func (s *Service) keepDevDrive(ctx context.Context) time.Duration {
	view, busy, err := s.devDriveStep(ctx)
	if err != nil {
		view = &DevDriveView{State: "unreadable", Line: "the Dev Drive could not be read: " + err.Error(), Note: "This machine's drives could not be read.", Explain: devdrive.Explain}
	}
	s.devDriveViewMu.Lock()
	changed := s.devDriveView == nil || *s.devDriveView != *view
	s.devDriveView = view
	s.devDriveViewMu.Unlock()
	if changed {
		s.notify()
	}
	if busy {
		return time.Minute
	}
	return 30 * time.Minute
}

func (s *Service) devDriveStep(ctx context.Context) (*DevDriveView, bool, error) {
	m, err := s.Options.DevDrive.Read(ctx)
	if err != nil {
		return nil, false, err
	}
	s.devDriveConfig.Lock()
	defer s.devDriveConfig.Unlock()
	root := s.Store.Home.Root
	config, err := home.ReadDevDriveConfig(root)
	if err != nil {
		return nil, false, err
	}
	h := s.Store.Home
	h.DevDrive = config.Root
	report := devdrive.Describe(m, h, config.VHD)
	view := &DevDriveView{State: string(report.State), Line: report.Line, Note: report.Note, Explain: devdrive.Explain, Choice: config.Choice}
	done := devDriveDone(report, m, h)
	waiting := s.devDriveWaiting()
	if report.State == devdrive.StateMissing {
		switch devdrive.Step(waiting) {
		case devdrive.StepAttach:
			view.Line = devdrive.MissingLine(config.Root, devdrive.AttachByItem)
		case devdrive.StepTask:
			view.Line = devdrive.MissingLine(config.Root, devdrive.AttachByRenew)
		}
	}
	if waiting != "" {
		view.Waiting = waiting
		view.Status = done + " " + devDriveLeft[devdrive.Step(waiting)] + " waits for you in the Command Center."
		return view, true, nil
	}
	var shouldMoveGateHome bool
	var gateRoot string
	if s.Options.DevDrive.GateHome != nil {
		if gateRoot, err = s.Options.DevDrive.GateHome(); err != nil {
			return nil, false, err
		}
		shouldMoveGateHome = gatehome.Read(gateRoot, config.Root).ShouldMove()
	}
	// A startup task registered before this build's is renewed by one item,
	// made once: from then on the task attaches the drive at boot and the
	// board may start it.
	var shouldRenewTask bool
	if _, isAttached := m.VolumeOf(config.Root); config.Root != "" && isAttached && config.VHD != "" && config.Offered[string(devdrive.StepTask)].IsZero() && s.Options.DevDrive.TaskCurrent != nil {
		isCurrent, err := s.Options.DevDrive.TaskCurrent(ctx)
		if err != nil {
			return nil, false, err
		}
		shouldRenewTask = !isCurrent
	}
	plan, needed := devdrive.Next(m, h, config, shouldMoveGateHome, shouldRenewTask)
	if !needed {
		return view, false, nil
	}
	if plan.Step == devdrive.StepGate {
		done += " The gate's home is not there yet."
		if config.Choice == home.DevDriveWanted {
			return view, s.keepGateCarry(ctx, view, done, plan.Folder, gateRoot, config.AskedAt), nil
		}
	}
	now := time.Now().UTC()
	offered := config.Offered[string(plan.Step)]
	due := config.Choice == home.DevDriveWanted && offered.Before(config.AskedAt)
	switch plan.Step {
	case devdrive.StepTask:
		due = true
	case devdrive.StepAttach:
		// A missing drive is looked after once for each boot and each ask:
		// its startup task first, which needs no click, then one item.
		since := s.devDriveBoot()
		if config.AskedAt.After(since) {
			since = config.AskedAt
		}
		due = offered.IsZero() || offered.Before(since)
		if due && s.attachingDevDrive(ctx, config.VHD, since, now) {
			view.Line = devdrive.MissingLine(config.Root, devdrive.AttachByTask)
			view.Status = done + " Its startup task is attaching it now, with no click."
			return view, true, nil
		}
	}
	if !due {
		switch {
		case plan.Step == devdrive.StepAttach:
			view.Action, view.Status = devDriveAttach, done
			view.Line = devdrive.MissingLine(config.Root, devdrive.AttachByPanel)
		case config.Choice == home.DevDriveWanted:
			view.Action, view.Status = devDriveTryAgain, done+" "+devDriveLeft[plan.Step]+" is left: its Command Center item did not finish."
		default:
			view.Action, view.Status = devDriveSetUp, done
		}
		return view, false, nil
	}
	if err := s.offerDevDriveStep(plan, now); err != nil {
		return nil, false, err
	}
	if config.Offered == nil {
		config.Offered = map[string]time.Time{}
	}
	config.Offered[string(plan.Step)] = now
	if plan.VHD != "" {
		config.VHD = plan.VHD
	}
	if err := home.WriteDevDriveConfig(root, config); err != nil {
		return nil, false, err
	}
	view.Waiting = string(plan.Step)
	if plan.Step == devdrive.StepAttach {
		view.Line = devdrive.MissingLine(config.Root, devdrive.AttachByItem)
	}
	view.Status = done + " " + devDriveLeft[plan.Step] + " waits for you in the Command Center."
	return view, true, nil
}

// devDriveBoot is when the machine last started, the zero time without Boot.
func (s *Service) devDriveBoot() time.Time {
	if s.Options.DevDrive.Boot == nil {
		return time.Time{}
	}
	return s.Options.DevDrive.Boot()
}

// attachingDevDrive starts the missing drive's startup task once since since,
// the boot or the person's last ask, and says whether the task is attaching
// it still: started, with no result written since and AttachBound not yet
// past. A task the board may not start, as one registered before this build
// is, attaches nothing, so its item comes at once. A result that cannot be
// read is waited for until the bound.
func (s *Service) attachingDevDrive(ctx context.Context, vhd string, since, now time.Time) bool {
	if s.Options.DevDrive.StartTask == nil {
		return false
	}
	if s.devDriveStarted.Before(since) {
		s.devDriveStarted = now
		err := s.Options.DevDrive.StartTask(ctx)
		s.isDevDriveStartRefused = err != nil
		return err == nil
	}
	if s.isDevDriveStartRefused || now.Sub(s.devDriveStarted) >= devdrive.AttachBound {
		return false
	}
	result, isWritten, err := devdrive.ReadAttachResult(vhd)
	return err != nil || !isWritten || result.Finished.Before(s.devDriveStarted)
}

// devDriveLeft names each step a person runs, as the row says what is left.
var devDriveLeft = map[devdrive.Step]string{
	devdrive.StepCreate: "Creating the drive",
	devdrive.StepAttach: "Attaching it again",
	devdrive.StepTask:   "Renewing its startup task, which attaches it at every boot,",
	devdrive.StepTrust:  "Trusting it, which turns on Defender's performance mode there,",
	devdrive.StepMove:   "Moving the worktrees and caches onto it",
}

// devDriveDone says in one sentence how far the Dev Drive is, for the row.
func devDriveDone(report devdrive.Report, m devdrive.Machine, h home.Home) string {
	switch report.State {
	case devdrive.StateOn:
		return "The worktrees and caches are on " + h.DevDrive + "."
	case devdrive.StateMissing:
		return filepath.VolumeName(h.DevDrive) + " is not attached, so no goblin starts."
	case devdrive.StatePresent:
		if v, found := m.Reusable(); found {
			return v.Letter() + " is a Dev Drive, not used yet."
		}
	}
	return "No Dev Drive is set up yet."
}

// keepGateCarry takes the gate step itself, once the person asked for a Dev
// Drive: it starts the carry of the gate's home at gateRoot into folder at
// the first look that finds no gate run in flight, with no Command Center
// item, and says in the row what it waits on. A carry that gave up because
// the gate stayed busy is started again the same way; one that failed for
// another reason waits for Try again, which only then is something to press.
// It says whether another look is due soon.
func (s *Service) keepGateCarry(ctx context.Context, view *DevDriveView, done, folder, gateRoot string, askedAt time.Time) bool {
	carry := &s.gateCarry
	carry.mu.Lock()
	defer carry.mu.Unlock()
	if carry.failed != "" && carry.failedAt.After(askedAt) {
		view.Action, view.Status = devDriveTryAgain, done+" It did not move: "+carry.failed
		return false
	}
	runs, err := s.Options.DevDrive.GateRuns(ctx, gateRoot)
	switch {
	case err != nil:
		view.Status = done + " It moves by itself once the gate's runs can be read: " + err.Error()
		return true
	case runs == 1:
		view.Status = done + " Waiting for 1 gate run to finish, then the gate's home moves by itself."
		return true
	case runs > 1:
		view.Status = fmt.Sprintf("%s Waiting for %d gate runs to finish, then the gate's home moves by itself.", done, runs)
		return true
	}
	view.Status = done + " It is moving there now, by itself."
	if carry.isRunning {
		return true
	}
	carry.isRunning, carry.failed = true, ""
	go func() {
		err := s.Options.DevDrive.CarryGateHome(context.WithoutCancel(ctx), folder)
		carry.mu.Lock()
		carry.isRunning = false
		if err != nil && !errors.Is(err, gatehome.ErrGateBusy) {
			carry.failed, carry.failedAt = err.Error(), time.Now().UTC()
		}
		carry.mu.Unlock()
		s.devDriveAgain()
	}()
	return true
}

// devDriveWaiting is the step of the Dev Drive item that waits or runs, or "".
// A gate item an earlier build made is none: the supervisor takes that step
// itself now, and a person who still runs the item moves the same home.
func (s *Service) devDriveWaiting() string {
	for _, r := range s.Store.Snapshot().Runs {
		if r.DevDrive != "" && r.DevDrive != string(devdrive.StepGate) && (r.State == "ready" || r.State == "running") {
			return r.DevDrive
		}
	}
	return ""
}

// offerDevDriveStep makes plan's Command Center item: the create, trust and
// attach steps run their script as administrator, and the move runs this
// build's goblins dev-drive move in the home.
func (s *Service) offerDevDriveStep(plan devdrive.Plan, now time.Time) error {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	id := "dev-drive-" + string(plan.Step) + "-" + hex.EncodeToString(nonce[:])
	identity := sha256.Sum256([]byte("dev-drive\n" + id + "\n" + strconv.FormatInt(now.UnixNano(), 10)))
	command := plan.Script
	if plan.Step == devdrive.StepMove {
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
		h := s.Store.Home
		command = "$ErrorActionPreference = 'Stop'\n$env:GOBLINS_HOME = " + quote(h.Root) + "\n$env:GOBLINS_STATE_OVERRIDE = " + quote(h.State) + "\n" +
			"& " + quote(executable) + " dev-drive move --to " + quote(plan.Folder) + "\nexit $LASTEXITCODE\n"
	}
	_, err := s.recordBoardRun(Run{ID: id, Identity: hex.EncodeToString(identity[:]), Title: plan.Title, Shell: "powershell", Admin: plan.Admin,
		Command: command, Cwd: s.Store.Home.Root, State: "ready", CreatedAt: now, ExpiresAt: now.Add(runLifetime), DevDrive: string(plan.Step)})
	return err
}

// devDriveViewNow is the view the board shows, or nil on a board without the
// setting or before the first look.
func (s *Service) devDriveViewNow() *DevDriveView {
	if s.Options.DevDrive == nil {
		return nil
	}
	s.devDriveViewMu.Lock()
	defer s.devDriveViewMu.Unlock()
	if s.devDriveView == nil {
		return nil
	}
	view := *s.devDriveView
	return &view
}

// askDevDrive records the person's answer to the Dev Drive offer: wanted,
// which asks for the next step now, or declined.
func (s *Service) askDevDrive(want bool) error {
	s.devDriveConfig.Lock()
	err := home.AnswerDevDrive(s.Store.Home.Root, want, time.Now().UTC())
	s.devDriveConfig.Unlock()
	if err == nil {
		s.devDriveAgain()
	}
	return err
}

// setDevDrive serves POST /api/dev-drive, the panel's Set up, Try again and
// Attach, and the first run's answer.
func (h *HTTP) setDevDrive(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Want *bool `json:"want"`
	}
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Want == nil {
		apiError(w, http.StatusBadRequest, "Say whether you want a Dev Drive")
		return
	}
	if h.Service.Options.DevDrive == nil {
		apiError(w, http.StatusConflict, "This board cannot set up a Dev Drive")
		return
	}
	if err := h.Service.askDevDrive(*input.Want); err != nil {
		apiError(w, http.StatusInternalServerError, "The Dev Drive answer was not kept: "+err.Error())
		return
	}
	respond(w, http.StatusOK, struct {
		Want bool `json:"want"`
	}{*input.Want})
}
