package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// DevDrive is the board's Dev Drive setting and the Command Center items that
// set one up; without it the board shows neither. Every step that needs a
// person is one of those items, made here, in order, one at a time: create
// the drive, trust it, move the home's heavy folders onto it, and attach it
// again whenever Windows did not.
type DevDrive struct {
	// Read reads what this machine says about Dev Drives.
	Read func(context.Context) (devdrive.Machine, error)
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
	// empty when there is nothing to press.
	Action string `json:"action,omitempty"`
}

// The panel's buttons.
const (
	devDriveSetUp    = "set-up"
	devDriveTryAgain = "try-again"
	devDriveAttach   = "attach"
)

// devDriveAttachEvery is how often the attach item comes back by itself while
// the drive stays missing and nobody ran the last one.
const devDriveAttachEvery = 12 * time.Hour

// watchDevDrive keeps the Dev Drive's view and items to the machine: now,
// every minute while a step is under way and every half hour otherwise,
// whenever the person asks or one of its items ends, and at the next tick
// after config\dev-drive.json changed, as cfo dev-drive setup, cfo dev-drive
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
// the next step's item when one is due, and keeps the view the board shows.
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
	report := devdrive.Describe(m, h)
	view := &DevDriveView{State: string(report.State), Line: report.Line, Note: report.Note, Explain: devdrive.Explain, Choice: config.Choice}
	if waiting := s.devDriveWaiting(); waiting != "" {
		view.Waiting = waiting
		return view, true, nil
	}
	plan, needed := devdrive.Next(m, h, config)
	if !needed {
		return view, false, nil
	}
	now := time.Now().UTC()
	offered := config.Offered[string(plan.Step)]
	due := config.Choice == home.DevDriveWanted && offered.Before(config.AskedAt)
	if plan.Step == devdrive.StepAttach {
		due = offered.Before(config.AskedAt) || offered.Before(now.Add(-devDriveAttachEvery))
	}
	if !due {
		switch {
		case plan.Step == devdrive.StepAttach:
			view.Action = devDriveAttach
		case config.Choice == home.DevDriveWanted:
			view.Action = devDriveTryAgain
		default:
			view.Action = devDriveSetUp
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
	return view, true, nil
}

// devDriveWaiting is the step of the Dev Drive item that waits or runs, or "".
func (s *Service) devDriveWaiting() string {
	for _, r := range s.Store.Snapshot().Runs {
		if r.DevDrive != "" && (r.State == "ready" || r.State == "running") {
			return r.DevDrive
		}
	}
	return ""
}

// offerDevDriveStep makes plan's Command Center item: the create, trust and
// attach steps run their script as administrator, and the move runs this
// build's cfo dev-drive move in the home.
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
		command = "$ErrorActionPreference = 'Stop'\n$env:CFO_HOME = " + quote(h.Root) + "\n$env:CFO_STATE_OVERRIDE = " + quote(h.State) + "\n" +
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
