package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/state"
)

// The Overlord, 2026-10-18: "is there a way to avoid pooling or when kernel
// page runs into issue we just saw that the header shows the restart button
// in terminal cfo for safe pc restart??" Four days after its boot this PC's
// kernel pools held about 11 GB, and with every goblin paused it had 3.6 to
// 6 GB free, so each goblin resumed was paused again at the memory floor
// within minutes and the fleet ran one goblin at a time until he restarted
// the PC.
//
// Past poolLine the CFO panel's header offers Restart PC, on a board on this
// PC only, proven his as AFK mode's on is. His press tells the CFO first, has
// every working goblin write its handoff and pause until the PC has
// restarted, ends the runs in the machine's heavy line, waits for the CFO's
// turn to end, then restarts Windows. After he signs in, the comeback brings
// the CFO back and the scheduler resumes each goblin paused for the restart,
// one at a time as memory allows. A goblin that does not pause stops the
// restart short, so nothing is lost: the goblins already paused for it stay
// paused until the PC restarts.

// poolLine is how much the kernel's paged and nonpaged pools hold together
// past which free memory on this PC can no longer hold the fleet, measured on
// it on 2026-10-18. It has 31.7 GiB of memory. With every goblin paused and
// the pools at 10.96 GiB it had 3.6 to 6 GiB free, 4.8 at the middle. A
// goblin's terminal holds 0.8 GiB (six measured, 0.70 to 1.06) and a heavy
// run up to 2.5 GiB (the line's own record). The fleet is more than one
// goblin at a time: two at work, one of them in a heavy run, need 2 × 0.8 +
// 2.5 = 4.1 GiB above the 4 GiB floor, 8.1 GiB free with no goblin, 3.3 GiB
// more than those pools left. So the line is 10.96 - 3.3 = 7.66 GiB, kept at
// 7.5 so Restart PC shows before the fleet falls to one goblin at a time.
const poolLine = 15 << 29

// How long the restart waits for a start or a change under way to end, and
// for the CFO's turn to end, and how often it looks.
const (
	restartQuietWait = 2 * time.Minute
	restartCFOWait   = 3 * time.Minute
	restartLook      = 2 * time.Second
)

// PCRestart is the restart of the PC his Restart PC began, for the board:
// State is stopping while the fleet stops, restarting once Windows was asked
// to restart, and stopped when the restart stopped short, which Problem says
// why.
type PCRestart struct {
	State   string    `json:"state"`
	Problem string    `json:"problem,omitempty"`
	At      time.Time `json:"at"`
}

// restartingBoard asks for Restart PC: a restart ends every program on the
// PC, his own among them, so it is his alone, from a board on it.
var restartingBoard = boardAsker("Restarting the PC is the Supreme Overlord's alone", ": he presses Restart PC on a board of his own on this PC", "Press Restart PC in the Code Goblins window on this PC.", reopenTheWindow+"press Restart PC there.")

// restartPCFromBoard serves POST /api/pc/restart, the CFO panel's Restart
// PC, whose body is an empty object. Only a board of the Overlord's own on
// this PC restarts it, proven as AFK mode's on is, so a press through his
// private board, through a proxy or from a browser an agent started is
// refused. The body is read before anything is proven, so a body the
// endpoint does not take is refused as one whoever sent it.
func (h *HTTP) restartPCFromBoard(w http.ResponseWriter, r *http.Request) {
	if err := decodeBody(w, r, &struct{}{}, 1<<10); err != nil {
		apiError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := h.Service.overlordsBoard(r, h.Host, time.Now(), restartingBoard); err != nil {
		h.refuse(w, http.StatusForbidden, "restart", "A press of Restart PC on a board was refused", "", err)
		return
	}
	if err := h.Service.beginPCRestart(); err != nil {
		apiError(w, http.StatusConflict, err.Error())
		return
	}
	respond(w, http.StatusAccepted, struct {
		State string `json:"state"`
	}{"stopping"})
}

// beginPCRestart starts the restart in the background once the pools are
// past the line, and refuses a second while one runs.
func (s *Service) beginPCRestart() error {
	if s.Options.RestartWindows == nil || s.Options.Example || s.Options.Dispatch == nil || s.Options.Dispatch.Memory == nil || s.Options.Dispatch.Spawn == nil {
		return errors.New("This board cannot restart the PC")
	}
	memory, err := s.Options.Dispatch.Memory()
	if err != nil {
		return fmt.Errorf("The kernel's pools cannot be read: %w", err)
	}
	if pools := memory.PagedPool + memory.NonpagedPool; pools < poolLine {
		return fmt.Errorf("The kernel's pools hold %.1f GB, under the %.1f GB line, so the PC needs no restart", gigabytes(pools), gigabytes(poolLine))
	}
	signedIn, err := s.currentSignIn()
	if err != nil {
		return fmt.Errorf("The PC cannot restart, since this sign-in's start cannot be read: %w", err)
	}
	s.starts.Lock()
	if s.pcRestart != nil && s.pcRestart.State != "stopped" {
		s.starts.Unlock()
		return errors.New("The PC is restarting already")
	}
	s.pcRestart = &PCRestart{State: "stopping", At: time.Now().UTC()}
	s.starts.Unlock()
	s.notify()
	go s.restartPC(context.Background(), memory, signedIn)
	return nil
}

// isRestartingPC says the fleet stops for the PC's restart, while which
// nothing starts, resumes or comes back by itself. The caller holds s.starts.
func (s *Service) isRestartingPC() bool {
	return s.pcRestart != nil && s.pcRestart.State != "stopped"
}

// pcRestartView is the restart of the PC for the board, or nil when none
// began.
func (s *Service) pcRestartView() *PCRestart {
	s.starts.Lock()
	defer s.starts.Unlock()
	if s.pcRestart == nil {
		return nil
	}
	view := *s.pcRestart
	return &view
}

// restartPC stops the fleet for the restart and restarts Windows, or says on
// the board and to the CFO why it stopped short. memory is the reading the
// press was taken on, and signedIn when this sign-in began, which each
// goblin's pause names.
func (s *Service) restartPC(ctx context.Context, memory Memory, signedIn time.Time) {
	stateDir := s.Store.Home.State
	summary, problem := s.stopForRestart(ctx, memory, signedIn)
	if problem == "" {
		// What the restart did reaches the CFO when the comeback brings it
		// back, and a CFO that cannot be told it holds up no restart.
		if err := raiseFleetWake(stateDir, "memory", "restart", "The PC restarts now. "+summary); err != nil {
			s.publish(err)
		}
		if err := s.Options.RestartWindows(); err != nil {
			problem = "Windows did not restart: " + err.Error()
		}
	}
	restart := PCRestart{State: "restarting", At: time.Now().UTC()}
	if problem != "" {
		restart.State, restart.Problem = "stopped", problem
		if err := raiseFleetWake(stateDir, "memory", "restart", "The PC did not restart: "+problem+". "+summary+" A goblin paused for the restart stays paused until the PC restarts, or until it is resumed: tell the Overlord, who can press Restart PC again."); err != nil {
			s.publish(err)
		}
	}
	s.starts.Lock()
	s.pcRestart = &restart
	s.starts.Unlock()
	s.notify()
	if problem != "" {
		s.runAsked()
	}
}

// stopForRestart stops the fleet for the restart, in order: the CFO is told,
// a start or change under way ends, every working goblin writes its handoff
// and pauses until the PC has restarted, the runs in the machine's heavy line
// end, and the CFO's turn ends. It says what it did, and why it stopped
// short, empty when it did not.
func (s *Service) stopForRestart(ctx context.Context, memory Memory, signedIn time.Time) (summary, problem string) {
	stateDir := s.Store.Home.State
	told := fmt.Sprintf("The Overlord pressed Restart PC on his board: the kernel's pools hold %.1f GB (%.1f GB paged, %.1f GB nonpaged), past the %.1f GB line past which free memory cannot hold the fleet. The supervisor now pauses every working goblin until the PC has restarted, each writing its handoff first, ends the runs in the machine's heavy line, waits for your turn to end, then restarts Windows. Start, resume and merge nothing meanwhile, and end your turn. After he signs in the comeback brings you back, and the scheduler resumes each goblin paused for the restart, one at a time as memory allows.",
		gigabytes(memory.PagedPool+memory.NonpagedPool), gigabytes(memory.PagedPool), gigabytes(memory.NonpagedPool), gigabytes(poolLine))
	if err := raiseFleetWake(stateDir, "memory", "restart", told); err != nil {
		return "", "the CFO could not be told: " + err.Error()
	}
	if busy := s.awaitQuiet(ctx); busy != "" {
		return "", busy + " did not end within " + restartQuietWait.String()
	}
	goblins, err := s.workingGoblins()
	if err != nil {
		return "", "the working goblins cannot all be read: " + err.Error()
	}
	paused, problem := s.pauseForRestart(goblins, signedIn)
	if problem != "" {
		return paused, problem
	}
	summary = paused
	if s.Options.EndHeavyRuns != nil {
		ended, err := s.Options.EndHeavyRuns()
		switch {
		case err != nil:
			summary += " Not every run in the machine's heavy line ended: " + err.Error() + "."
		case len(ended) > 0:
			summary += " Ended in the machine's heavy line: " + strings.Join(ended, ", ") + "."
		}
	}
	if !s.awaitCFOStoppingPoint(ctx) {
		summary += " Your turn had not ended after " + restartCFOWait.String() + "."
	}
	return summary, ""
}

// awaitQuiet waits up to restartQuietWait for a start or a change of a goblin
// under way to end, and names what still runs, empty once nothing does.
func (s *Service) awaitQuiet(ctx context.Context) string {
	deadline := time.Now().Add(restartQuietWait)
	for {
		s.starts.Lock()
		busy := ""
		if s.starting != "" {
			busy = "starting " + s.starting
		}
		for id, action := range s.changing {
			busy = changingVerbs[action] + " " + id
		}
		if s.isCFOComingBack {
			busy = "the CFO's comeback"
		}
		s.starts.Unlock()
		if busy == "" || !time.Now().Before(deadline) {
			return busy
		}
		select {
		case <-time.After(restartLook):
		case <-ctx.Done():
			return busy
		}
	}
}

// workingGoblins are the goblins the restart pauses: each native goblin
// whose terminal host runs and that is not paused, pausing or stopped, its
// helpers among them, except one whose latest report delivered a pull
// request and that took no prompt since, which waits on its merge. A goblin
// whose records cannot be read may be working, so it is named rather than
// passed over.
func (s *Service) workingGoblins() ([]state.TaskMeta, error) {
	stateDir := s.Store.Home.State
	var working []state.TaskMeta
	var problems error
	for _, meta := range liveTasks(stateDir) {
		if meta.Backend != "native" || meta.SpawnGen == "" || s.hasDelivered(meta) {
			continue
		}
		record, err := host.ReadRecord(stateDir, meta.ID)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		if !host.Running(record) {
			continue
		}
		lifecycle, err := state.ReadLifecycle(stateDir, meta.ID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			problems = errors.Join(problems, err)
			continue
		}
		if err == nil && lifecycle.Generation == meta.SpawnGen && lifecycle.SuppressesMonitoring(stateDir) {
			continue
		}
		working = append(working, meta)
	}
	return working, problems
}

// pauseForRestart pauses goblins until a sign-in after signedIn, all at
// once, each through cfo pause, which asks it for its handoff before it
// stops its terminal, and reads back that each is paused. It says which
// paused and which saved no handoff, and names each that did not pause.
func (s *Service) pauseForRestart(goblins []state.TaskMeta, signedIn time.Time) (summary, problem string) {
	isPausing := map[string]bool{}
	for _, meta := range goblins {
		isPausing[meta.ID] = true
	}
	// A helper's parent pauses it with the parent's own condition.
	parents := slices.DeleteFunc(slices.Clone(goblins), func(meta state.TaskMeta) bool { return isPausing[meta.Parent] })
	operation := fmt.Sprintf("restart-pause-%d", time.Now().UnixNano())
	until := signedIn.UTC().Format(time.RFC3339Nano)
	s.starts.Lock()
	if s.changing == nil {
		s.changing = map[string]string{}
		s.changeErrors = map[string]taskChangeError{}
	}
	for _, meta := range parents {
		s.changing[meta.ID] = "pause"
		delete(s.changeErrors, meta.ID)
	}
	s.starts.Unlock()
	s.notify()
	failures := make([]string, len(parents))
	var pauses sync.WaitGroup
	for index, meta := range parents {
		pauses.Go(func() {
			output, err := s.Options.Dispatch.Spawn(context.Background(), []string{"pause", meta.ID, "--generation", meta.SpawnGen, "--operation", operation, "--reason", "restart", "--until", until})
			if err != nil {
				failures[index] = spawnFailure(output, err)
			}
		})
	}
	pauses.Wait()
	s.starts.Lock()
	for _, meta := range parents {
		delete(s.changing, meta.ID)
	}
	s.starts.Unlock()
	s.notify()
	var paused, withoutHandoff, unpaused []string
	for _, meta := range goblins {
		record, err := state.ReadLifecycle(s.Store.Home.State, meta.ID)
		switch {
		case err != nil:
			unpaused = append(unpaused, meta.ID+" ("+err.Error()+")")
		case record.Generation != meta.SpawnGen || record.Phase != "paused" || record.Pause == nil || record.Pause.Reason != "restart":
			why := record.Phase
			if at := slices.IndexFunc(parents, func(parent state.TaskMeta) bool { return parent.ID == meta.ID }); at >= 0 && failures[at] != "" {
				why = failures[at]
			}
			unpaused = append(unpaused, meta.ID+" ("+why+")")
		default:
			paused = append(paused, meta.ID)
			if !record.HandoffSaved {
				withoutHandoff = append(withoutHandoff, meta.ID)
			}
		}
	}
	summary = "No goblin was working, so none paused for the restart."
	if len(paused) > 0 {
		summary = "Paused for the restart: " + strings.Join(paused, ", ") + "."
	}
	if len(withoutHandoff) > 0 {
		summary += " Saved no handoff, so each resumes on its own session and worktree: " + strings.Join(withoutHandoff, ", ") + "."
	}
	if len(unpaused) > 0 {
		return summary, "not every working goblin paused: " + strings.Join(unpaused, ", ")
	}
	return summary, ""
}

// awaitCFOStoppingPoint waits up to restartCFOWait for the registered CFO's
// turn to end, and says whether it did; with no CFO in its native terminal
// there is no turn to wait for.
func (s *Service) awaitCFOStoppingPoint(ctx context.Context) bool {
	cfo := readCFOState(s.Store.Home.State)
	if !cfo.registered || cfo.terminal != NativeCFOTerminal {
		return true
	}
	deadline := time.Now().Add(restartCFOWait)
	for !s.cfoAtStoppingPoint(ctx, cfo.harness) {
		if !time.Now().Before(deadline) {
			return false
		}
		select {
		case <-time.After(restartLook):
		case <-ctx.Done():
			return false
		}
	}
	return true
}

// restartedSince says whether this sign-in began after until, the sign-in a
// restart pause was taken in: a restart ends that sign-in, so the pause has
// cleared.
func (s *Service) restartedSince(until string) (bool, error) {
	paused, err := time.Parse(time.RFC3339, until)
	if err != nil {
		return false, err
	}
	signedIn, err := s.currentSignIn()
	return err == nil && signedIn.After(paused), err
}
