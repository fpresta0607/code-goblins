package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// maxHelperBrief bounds a helper's brief: it says what the helper does, not
// everything it reads.
const maxHelperBrief = 32 << 10

// HelperRequest is a goblin's ask for a helper: the goblin asking, the
// helper's brief as text, and its short title.
type HelperRequest struct {
	Parent string `json:"parent"`
	Brief  string `json:"brief"`
	Title  string `json:"title,omitempty"`
}

// HelperStart is the helper the supervisor is starting for a request: its
// task id and the branch it works on.
type HelperStart struct {
	ID     string `json:"id"`
	Branch string `json:"branch"`
}

// RequestHelper asks this home's supervisor for a helper of request.Parent,
// from inside that goblin's own terminal, the proof every goblin's question
// gives: no other process can ask in a goblin's name. The supervisor starts
// the helper in the background and says which, or says why not and when to
// ask again.
func RequestHelper(h home.Home, request HelperRequest) (HelperStart, error) {
	if _, err := goblinAsker(h.State, request.Parent); err != nil {
		return HelperStart{}, err
	}
	reply, err := askPipe(h.State, runPipeRequest{Kind: "helper", Helper: &request})
	if err != nil {
		return HelperStart{}, err
	}
	if reply.Helper == nil {
		return HelperStart{}, errors.New("the supervisor accepted the helper but did not say which it starts")
	}
	return *reply.Helper, nil
}

// helperStart is the cfo spawn a helper's start runs, and whom it tells.
type helperStart struct {
	HelperStart
	parent state.TaskMeta
	brief  string
	title  string
}

// acceptHelper admits a helper of request.Parent and starts it through cfo
// spawn in the background, or says why not and when to ask again. It takes
// the start slot the board's Start and Resume take, so one goblin starts at
// a time, and holds the memory, commit, disk and live-goblin marks every
// start does. The caps are spawn's own: a helper of a helper is refused for
// good, and a second helper until the first is merged or stopped.
func (s *Service) acceptHelper(request HelperRequest) (HelperStart, error) {
	dispatch := s.Options.Dispatch
	switch {
	case dispatch == nil || s.Options.Progress == nil:
		return HelperStart{}, errors.New("this supervisor cannot start goblins")
	case state.ValidTaskID(request.Parent) != nil:
		return HelperStart{}, fmt.Errorf("%q is not a task ID", request.Parent)
	case strings.TrimSpace(request.Brief) == "":
		return HelperStart{}, errors.New("a helper needs a brief that says what it does")
	case len(request.Brief) > maxHelperBrief:
		return HelperStart{}, fmt.Errorf("the brief is over %d KiB; a helper's brief says what it does, not everything it reads", maxHelperBrief>>10)
	case strings.ContainsAny(request.Title, "\r\n"):
		return HelperStart{}, errors.New("a helper's title is one line")
	}
	plan, err := s.planHelper(request)
	if err != nil {
		return HelperStart{}, err
	}
	s.starts.Lock()
	busy := s.starting
	for task, action := range s.changing {
		if action == "resume" {
			busy = task
		}
	}
	isChanging := s.changing[request.Parent] != ""
	if busy == "" && !isChanging {
		s.starting = plan.ID
	}
	s.starts.Unlock()
	switch {
	case isChanging:
		return HelperStart{}, fmt.Errorf("%s is being paused, resumed or stopped; ask again once it is back at work", request.Parent)
	case busy != "":
		return HelperStart{}, fmt.Errorf("%s is starting, and one goblin starts at a time; ask again in a minute", busy)
	}
	isStarting := false
	defer func() {
		if !isStarting {
			s.starts.Lock()
			s.starting = ""
			s.starts.Unlock()
		}
	}()
	memory, err := dispatch.Memory()
	if err != nil {
		return HelperStart{}, fmt.Errorf("free memory cannot be read, so nothing starts: %w", err)
	}
	disk, err := s.machineDisk()
	if err != nil {
		return HelperStart{}, fmt.Errorf("free disk cannot be read, so nothing starts: %w", err)
	}
	if err := CheckLaunch(s.Store.Home, memory, disk); err != nil {
		return HelperStart{}, fmt.Errorf("%w; ask again in ten minutes, or once the board's memory meter reads 5 GB", err)
	}
	// The helper's status log is written before its brief, so the brief
	// never shows on the board as queued work, and its id is never given
	// twice whatever becomes of the start.
	if err := state.AppendStatus(s.Store.Home.State, plan.ID, "starting: a helper of "+plan.parent.ID+", which asked the supervisor for it, on branch "+plan.Branch); err != nil {
		return HelperStart{}, err
	}
	if err := os.MkdirAll(filepath.Dir(plan.brief), 0o700); err != nil {
		return HelperStart{}, err
	}
	if err := fsx.AtomicWriteFile(plan.brief, []byte(request.Brief)); err != nil {
		return HelperStart{}, err
	}
	isStarting = true
	go s.runHelperStart(dispatch, plan)
	return plan.HelperStart, nil
}

// planHelper is the start a helper of request.Parent runs, or why it cannot
// start: the parent is a live native goblin at work on a branch, with no
// helper of its own and none of its own parent.
func (s *Service) planHelper(request HelperRequest) (helperStart, error) {
	stateDir := s.Store.Home.State
	parent, err := state.HelperParent(stateDir, request.Parent)
	if err != nil {
		return helperStart{}, err
	}
	if parent.Backend != "native" {
		return helperStart{}, fmt.Errorf("%s runs in no native terminal, and only a goblin in one can have a helper", parent.ID)
	}
	if record, err := state.ReadLifecycle(stateDir, parent.ID); err == nil && record.Generation == parent.SpawnGen && record.Phase != "running" && record.Phase != "failed" {
		return helperStart{}, fmt.Errorf("%s is %s; ask again once it is back at work", parent.ID, record.Phase)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return helperStart{}, err
	}
	branch, err := runOutput(context.Background(), s.Options.Progress, parent.Worktree, "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || branch == "" {
		return helperStart{}, fmt.Errorf("%s's worktree is on no branch, and a helper's branch is cut from its parent's; commit your work on a branch, then ask again", parent.ID)
	}
	id, err := state.NextHelperID(stateDir, parent.ID)
	if err != nil {
		return helperStart{}, err
	}
	return helperStart{
		HelperStart: HelperStart{ID: id, Branch: branch + id[len(parent.ID):]},
		parent:      parent,
		brief:       filepath.Join(s.Store.Home.Data, id, "brief.md"),
		title:       strings.TrimSpace(request.Title),
	}, nil
}

func (p helperStart) args() []string {
	args := []string{"spawn", p.ID, "--project", p.parent.Project, "--brief", p.brief, "--harness", p.parent.Harness}
	// A record written by spawn names a harness's own default "default".
	for _, flag := range [][2]string{{"--model", p.parent.Model}, {"--effort", p.parent.Effort}} {
		if flag[1] != "" && flag[1] != "default" {
			args = append(args, flag[0], flag[1])
		}
	}
	args = append(args, "--mode", "local-only", "--parent", p.parent.ID)
	if p.title != "" {
		args = append(args, "--title", p.title)
	}
	return args
}

// runHelperStart runs a helper's cfo spawn to its end, which is never cut
// short, as a Start's is not, then tells its parent in its terminal and the
// CFO through the wake queue how it went, before the start slot frees.
func (s *Service) runHelperStart(dispatch *Dispatch, plan helperStart) {
	output, err := dispatch.Spawn(context.Background(), plan.args())
	told := "Your helper " + plan.ID + " is up on branch " + plan.Branch + ", working on its brief; it reports to you here."
	detail := "started: helper of " + plan.parent.ID + ", which asked the supervisor for it, on branch " + plan.Branch + "; it reports to " + plan.parent.ID
	if err != nil {
		failure := spawnFailure(output, err)
		told = "Your helper " + plan.ID + " could not start: " + failure + ". Ask again with cfo helper start once that is fixed."
		detail = "start failed: helper of " + plan.parent.ID + ": " + failure
	}
	if output, err := dispatch.Spawn(context.Background(), []string{"send", plan.parent.ID, told}); err != nil {
		detail += "; " + plan.parent.ID + " could not be told: " + spawnFailure(output, err)
	}
	h := s.Store.Home
	if _, err := wake.Append(h.State, "notify", plan.ID, detail); err != nil {
		s.publish(err)
	} else if _, err := wake.PublishEpisode(h.State); err != nil {
		s.publish(err)
	}
	s.starts.Lock()
	s.starting = ""
	s.starts.Unlock()
	s.notify()
}
