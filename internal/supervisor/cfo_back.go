package supervisor

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/update"
)

// When the CFO's terminal ends in this sign-in while nobody asked it to stop,
// as when its harness crashes or ends itself after an update, the supervisor
// brings the CFO back by itself at its next look: on the harness it ran, on
// its conversation where that is small enough and on a new one where not, as
// Restart the CFO chooses, with a first instruction that says why it is back,
// so nobody has to type into it. An end somebody asked for stays: a terminal
// a viewer closed, as Restart, a harness switch and the uninstall close it,
// and a harness that exited with code 0, as its own exit does. A CFO that
// keeps ending soon after it started is not brought back once it has ended
// cfoBackCap times in a row, each soon after it started again: it stays
// stopped, with one wake for the next CFO and one Command Center item for the
// Overlord, whose one action is Reopen. A CFO the supervisor could not bring
// back is told of the same way.
// On 2026-10-16 and again on 2026-10-17 the CFO's Claude Code ended with exit
// code 1 right after a turn ended, and it stayed closed, the first time for
// 16 hours, until the Overlord reopened it and typed its first instruction.
const (
	// cfoBackEvery is how often the supervisor looks for a CFO whose terminal
	// ended unasked.
	cfoBackEvery = 10 * time.Second
	// cfoBackHeld is how long a CFO runs before its end no longer counts as
	// one more in a row, and how soon after the row's last end it starts for
	// its end to join the row.
	cfoBackHeld = 10 * time.Minute
	// cfoBackCap is how many ends in a row leave the CFO stopped.
	cfoBackCap = 3
)

// cfoBackFile records the CFO's end the supervisor last acted on, so each end
// is acted on once, and the ends in a row up to it.
const cfoBackFile = "cfo-back.json"

// cfoBack is cfoBackFile: the program whose end was acted on last, by pid and
// creation time, when each end in a row came, oldest first, and why the CFO
// did not come back when it did not.
type cfoBack struct {
	PID     int         `json:"pid"`
	Start   time.Time   `json:"start"`
	InARow  []time.Time `json:"in_a_row,omitempty"`
	Problem string      `json:"problem,omitempty"`
}

func readCFOBack(stateDir string) (cfoBack, error) {
	data, err := fsx.ReadFile(filepath.Join(stateDir, cfoBackFile))
	if err != nil {
		return cfoBack{}, err
	}
	var record cfoBack
	if err := json.Unmarshal(data, &record); err != nil {
		return cfoBack{}, fmt.Errorf("the record of the CFO's last end is unreadable: %w", err)
	}
	return record, nil
}

func writeCFOBack(stateDir string, record cfoBack) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, cfoBackFile), data)
}

// cfoEnd is the end of the CFO's program: the program by pid and creation
// time, when it ended, whether somebody asked for it, and why in plain words.
type cfoEnd struct {
	PID     int
	Start   time.Time
	At      time.Time
	isAsked bool
	Why     string
}

// lastCFOEnd is how the last program of native terminal cfo ended, for the
// registered CFO primary that no longer runs: as the terminal recorded it,
// for that program or one started after it, which ended before it
// registered; and otherwise the registered program's end, found at now,
// which a host that ended with it did not record.
func lastCFOEnd(stateDir string, primary primaryRegistration, now time.Time) cfoEnd {
	end, err := host.ReadEnd(stateDir, NativeCFOTerminal)
	if err != nil || end.ChildStart.Before(primary.Process.Start.Add(-time.Second)) {
		return cfoEnd{PID: primary.Process.PID, Start: primary.Process.Start, At: now, Why: "its terminal ended with its host, which recorded nothing of how"}
	}
	ended := cfoEnd{PID: end.ChildPID, Start: end.ChildStart, At: end.Ended}
	switch {
	case end.Closed:
		ended.isAsked, ended.Why = true, "its terminal was closed"
	case end.Code == 0:
		ended.isAsked, ended.Why = true, "it exited by itself with code 0"
	default:
		ended.Why = fmt.Sprintf("its terminal ended with exit code %d", end.Code)
	}
	return ended
}

// keepCFOBack looks every interval, until ctx ends, for a CFO whose terminal
// ended unasked, handing what went wrong to the next recovery cycle.
func (s *Service) keepCFOBack(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if err := s.bringEndedCFOBack(ctx, time.Now().UTC()); err != nil {
			s.mu.Lock()
			if s.fleetErr == nil || !strings.Contains(s.fleetErr.Error(), err.Error()) {
				s.fleetErr = errors.Join(s.fleetErr, err)
			}
			s.mu.Unlock()
		}
	}
}

// bringEndedCFOBack brings the home's CFO back when its terminal ended in this
// sign-in while nobody asked it to stop, once for each end, and leaves it
// stopped at the cap or when it could not bring it back. An end joins the row
// when its CFO ran under cfoBackHeld and started within cfoBackHeld of the
// row's last end; otherwise the row starts again, with that end when its CFO
// ran under cfoBackHeld. A CFO of the sign-in before is the comeback's, and a
// CFO that runs again takes back the item that left it stopped.
func (s *Service) bringEndedCFOBack(ctx context.Context, now time.Time) error {
	comeback := s.Options.Comeback
	if comeback == nil || comeback.CFOEnded == nil || s.Options.Example {
		return nil
	}
	stateDir := s.Store.Home.State
	cfo := readCFOState(stateDir)
	if cfo.registered {
		return s.Store.withdrawCFOStopped("The CFO runs again.")
	}
	// An update that installs stops this supervisor and swaps the programs a
	// start runs from, and the CFO started after it is the update's.
	if !cfo.closed || update.Installing(stateDir) {
		return nil
	}
	primary, _, err := readPrimary(stateDir)
	if err != nil || primary.Host != NativeCFOTerminal {
		return nil
	}
	s.comeback.Lock()
	signedIn, err := s.signInBegan()
	s.comeback.Unlock()
	if err != nil {
		return fmt.Errorf("a CFO whose terminal ended is not brought back: %w", err)
	}
	ended := lastCFOEnd(stateDir, primary, now)
	if ended.isAsked || ended.Start.Before(signedIn) {
		return nil
	}
	record, err := readCFOBack(stateDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if record.PID == ended.PID && record.Start.Equal(ended.Start) {
		return nil
	}
	if _, installed, err := s.programInstalled(harness.Kind(primary.Agent)); err == nil && installed.After(ended.Start) {
		name := primary.Agent
		if capability, known := CFOCapabilityFor(primary.Agent); known {
			name = capability.Name
		}
		ended.Why += ", after an update of " + name + " was installed while it ran"
	}
	inARow := record.InARow
	switch {
	case ended.At.Sub(ended.Start) >= cfoBackHeld:
		inARow = nil
	case len(inARow) > 0 && ended.Start.Sub(inARow[len(inARow)-1]) > cfoBackHeld:
		inARow = []time.Time{ended.At}
	default:
		inARow = append(inARow, ended.At)
	}
	record = cfoBack{PID: ended.PID, Start: ended.Start, InARow: inARow}
	if len(record.InARow) >= cfoBackCap {
		if err := writeCFOBack(stateDir, record); err != nil {
			return err
		}
		minutes := int(cfoBackHeld / time.Minute)
		what := fmt.Sprintf("The CFO's terminal ended %d times in a row, each within %d minutes of its start, with no more than %d minutes between one end and the next start, the last time because %s, so the supervisor stopped bringing it back", len(record.InARow), minutes, minutes, ended.Why)
		return s.leaveCFOStopped(what, "This CFO came back another way: find why it ends, from its last transcript and its terminal's end in state\\hosts\\cfo.end, before it ends again.", now)
	}
	s.starts.Lock()
	if s.isCFOComingBack {
		s.starts.Unlock()
		return nil
	}
	s.isCFOComingBack = true
	s.starts.Unlock()
	defer func() {
		s.starts.Lock()
		s.isCFOComingBack = false
		s.starts.Unlock()
		s.notify()
	}()
	// The end is spent before the CFO starts, so a supervisor that dies while
	// it starts never starts it a second time.
	if err := writeCFOBack(stateDir, record); err != nil {
		return err
	}
	s.notify()
	if err := comeback.CFOEnded(ctx, ended.Why); err != nil {
		record.Problem = err.Error()
		stopErr := s.leaveCFOStopped(fmt.Sprintf("The CFO did not come back after %s: %v", ended.Why, err), "Find why it did not, and why its terminal ended, from its last transcript and its terminal's end in state\\hosts\\cfo.end, before it ends again.", now)
		return errors.Join(fmt.Errorf("the CFO did not come back after %s: %w", ended.Why, err), writeCFOBack(stateDir, record), stopErr)
	}
	return nil
}

// signInBegan is when this Windows sign-in began, read once. The caller holds
// s.comeback.
func (s *Service) signInBegan() (time.Time, error) {
	if s.signedIn.IsZero() {
		signedIn, err := s.Options.Comeback.SignedIn()
		if err != nil {
			return time.Time{}, err
		}
		s.signedIn = signedIn
	}
	return s.signedIn, nil
}

// leaveCFOStopped tells why the supervisor left the CFO stopped, which what
// says in plain words: one check wake keyed cfo for the next CFO, with what it
// should find, and one Command Center item for the Overlord, whose one action
// is the board's Reopen the CFO, made while none waits already.
func (s *Service) leaveCFOStopped(what, find string, now time.Time) error {
	err := raiseFleetWake(s.Store.Home.State, "check", "cfo", what+". "+find)
	if s.Store.offersCFOReopen() {
		return err
	}
	var nonce [8]byte
	if _, nonceErr := rand.Read(nonce[:]); nonceErr != nil {
		return errors.Join(err, nonceErr)
	}
	id := "cfo-stopped-" + hex.EncodeToString(nonce[:])
	identity := sha256.Sum256([]byte("cfo-stopped\n" + id + "\n" + strconv.FormatInt(now.UnixNano(), 10)))
	command := "# This item reopens the CFO with the board's Reopen the CFO, never as a command.\n"
	made := time.Now().UTC()
	_, recordErr := s.recordBoardRun(Run{ID: id, Identity: hex.EncodeToString(identity[:]), Title: what + ". Reopen the CFO once you have looked?", Shell: "powershell", Command: command, Cwd: s.Store.Home.Root, State: "ready", CreatedAt: made, ExpiresAt: made.Add(runLifetime), CFOStopped: true})
	return errors.Join(err, recordErr)
}
