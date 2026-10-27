package supervisor

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// signedInAt is when the test's Windows sign-in began.
var signedInAt = time.Date(2026, 10, 18, 11, 13, 27, 205663700, time.UTC)

// restartLog is what a restart did, in order, as its fakes saw it, and what
// the fake restart of Windows found on disk when it was called. While
// heavyHeld is open, the end of the heavy line's runs waits on it.
type restartLog struct {
	mu        sync.Mutex
	events    []string
	atRestart map[string]string
	restarted chan struct{}
	heavyHeld chan struct{}
}

func (l *restartLog) add(event string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *restartLog) recorded() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.events)
}

// restartBoard is a board on the Overlord's own PC whose kernel pools hold
// pools bytes together. Its cfo pause writes a goblin's handoff, then its
// pause, and pauses its helpers with it, as the real one does, and a goblin
// named in failing fails to pause. Windows' restart is a fake that records
// each goblin's pause and handoff as it finds them on disk.
func restartBoard(t *testing.T, pools uint64, failing ...string) (*Service, home.Home, *restartLog, *spawnRecorder) {
	t.Helper()
	spawner := &spawnRecorder{}
	handler, h := startBoardWith(t, Memory{Available: 8 * gigabyte, Total: 32 * gigabyte, CommitAvailable: 20 * gigabyte, CommitLimit: 48 * gigabyte, PagedPool: pools / 3 * 2, NonpagedPool: pools - pools/3*2}, spawner)
	s := handler.Service
	s.Instance = "test-instance"
	asOverlordsBoard(s)
	s.Options.Comeback = &Comeback{SignedIn: func() (time.Time, error) { return signedInAt, nil }}
	log := &restartLog{atRestart: map[string]string{}, restarted: make(chan struct{})}
	spawner.during = func(args []string) {
		if args[0] != "pause" {
			log.add(strings.Join(args[:2], " "))
			return
		}
		if pending, err := wake.Pending(h.State); err == nil && len(pending) > 0 && pending[0].Key == "restart" && strings.Contains(pending[0].Detail, "pressed Restart PC") {
			log.add("the CFO was told before pausing " + args[1])
		}
		log.add("pause " + args[1])
		if slices.Contains(failing, args[1]) {
			writeRestartPause(t, h, args[1], "failed", args[slices.Index(args, "--until")+1])
			return
		}
		writeRestartPause(t, h, args[1], "paused", args[slices.Index(args, "--until")+1])
		for _, helper := range []string{"helper-goblin"} {
			if meta, err := state.ReadTaskMeta(h.State, helper); err == nil && meta.Parent == args[1] {
				writeRestartPause(t, h, helper, "paused", args[slices.Index(args, "--until")+1])
			}
		}
	}
	s.Options.EndHeavyRuns = func() ([]string, error) {
		log.add("end the heavy line's runs")
		if log.heavyHeld != nil {
			<-log.heavyHeld
		}
		return []string{"go test ./... in C:\\work, task first-goblin (pid 4242)"}, nil
	}
	s.Options.RestartWindows = func() error {
		log.add("restart Windows")
		for _, id := range []string{"first-goblin", "second-goblin", "helper-goblin"} {
			record, err := state.ReadLifecycle(h.State, id)
			_, handoffErr := os.Stat(record.Handoff)
			if err == nil && record.Pause != nil {
				log.atRestart[id] = record.Phase + " for " + record.Pause.Reason + " until " + record.Pause.Until + ", handoff saved " + map[bool]string{true: "yes", false: "no"}[record.HandoffSaved && handoffErr == nil]
			}
		}
		close(log.restarted)
		return nil
	}
	return s, h, log, spawner
}

// writeRestartPause writes what cfo pause leaves of id's pause until the PC
// restarts: the handoff its goblin wrote first, then its lifecycle record,
// in phase.
func writeRestartPause(t *testing.T, h home.Home, id, phase, until string) {
	t.Helper()
	meta, err := state.ReadTaskMeta(h.State, id)
	if err != nil {
		t.Error(err)
		return
	}
	condition, err := state.NewPauseCondition("restart", until, time.Now().UTC())
	if err != nil {
		t.Error(err)
		return
	}
	record := state.Lifecycle{ID: id, Generation: meta.SpawnGen, RequestGeneration: meta.SpawnGen, Operation: "restart-pause", Action: "pause", Phase: phase, Pause: &condition, Reason: "restart", Started: time.Now().UTC(), Updated: time.Now().UTC()}
	if phase == "paused" {
		record.Handoff = filepath.Join(meta.TaskTmp, "pause-restart-pause.md")
		writeFile(t, record.Handoff, "# Handoff of "+id+"\n")
		record.HandoffSaved = true
	}
	if err := state.WriteLifecycle(h.State, record); err != nil {
		t.Error(err)
	}
}

// awaitRestartState waits for the restart to reach want on the board.
func awaitRestartState(t *testing.T, s *Service, want string) PCRestart {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if view := s.pcRestartView(); view != nil && view.State == want {
			return *view
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the restart = %+v, want it %s", s.pcRestartView(), want)
	return PCRestart{}
}

// The Overlord, 2026-10-18: "the header shows the restart button in terminal
// cfo for safe pc restart". His press tells the CFO first, has every working
// goblin write its handoff and pause until the PC has restarted, ends the
// runs in the machine's heavy line, then restarts Windows: here a fake,
// which finds each handoff and each pause already written. A helper pauses
// with its parent, a goblin already paused is left as it is, and nothing
// starts or resumes by itself while the fleet stops.
func TestRestartPCWritesEveryHandoffAndPauseBeforeWindowsRestarts(t *testing.T) {
	// Arrange
	s, h, log, spawner := restartBoard(t, 8*gigabyte)
	now := time.Now().UTC()
	first := hostedGoblin(t, h, "first-goblin", now)
	second := hostedGoblin(t, h, "second-goblin", now)
	helper := hostedGoblin(t, h, "helper-goblin", now)
	helper.Parent = first.ID
	if err := state.WriteTaskMeta(h.State, helper); err != nil {
		t.Fatal(err)
	}
	pausedGoblin(t, h, "paused-goblin", "overlord", "", now.Add(-time.Hour))
	pausedGoblin(t, h, "cleared-goblin", "dependency", "date:"+now.Add(-time.Minute).Format(time.RFC3339), now.Add(-time.Hour))
	log.heavyHeld = make(chan struct{})

	// Act
	code, body := askTheBoard(t, s, "POST", "/api/pc/restart", "{}", nil)
	for deadline := time.Now().Add(30 * time.Second); !slices.Contains(log.recorded(), "end the heavy line's runs") && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}
	readFleet(t, s, now, 0, 1, 2)
	close(log.heavyHeld)
	select {
	case <-log.restarted:
	case <-time.After(30 * time.Second):
		t.Fatalf("Windows was never restarted: %v", log.recorded())
	}
	restart := awaitRestartState(t, s, "restarting")

	// Assert
	if code != http.StatusAccepted || strings.TrimSpace(body) != `{"state":"stopping"}` {
		t.Fatalf("POST /api/pc/restart = %d %s, want it accepted", code, body)
	}
	until := signedInAt.Format(time.RFC3339Nano)
	calls := spawner.recorded()
	if len(calls) != 2 {
		t.Fatalf("cfo ran %v, want a pause of each working goblin and nothing else", calls)
	}
	for _, meta := range []state.TaskMeta{first, second} {
		want := []string{"pause", meta.ID, "--generation", meta.SpawnGen, "--operation", calls[0][5], "--reason", "restart", "--until", until}
		if !slices.ContainsFunc(calls, func(call []string) bool { return slices.Equal(call, want) }) {
			t.Errorf("cfo ran %v, want %v", calls, want)
		}
	}
	events := log.recorded()
	if !strings.HasPrefix(events[0], "the CFO was told before pausing ") || !slices.Equal(events[len(events)-2:], []string{"end the heavy line's runs", "restart Windows"}) {
		t.Errorf("the restart did %v, want the CFO told, then the pauses, then the heavy line's runs ended, then Windows restarted", events)
	}
	for _, id := range []string{first.ID, second.ID, helper.ID} {
		if got, want := log.atRestart[id], "paused for restart until "+until+", handoff saved yes"; got != want {
			t.Errorf("as Windows restarted %s was %q, want %q", id, got, want)
		}
	}
	if record, err := state.ReadLifecycle(h.State, "paused-goblin"); err != nil || record.Pause.Reason != "overlord" {
		t.Errorf("the goblin already paused = %+v, %v, want it left paused by the Overlord", record, err)
	}
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 2 || pending[1].Key != "restart" || pending[1].Detail != "The PC restarts now. Paused for the restart: first-goblin, helper-goblin, second-goblin. Ended in the machine's heavy line: go test ./... in C:\\work, task first-goblin (pid 4242)." {
		t.Errorf("the CFO's queue = %+v, %v, want it told first, then what the restart did", pending, err)
	}
	if restart.Problem != "" {
		t.Errorf("the restart = %+v, want it restarting with no problem", restart)
	}
}

// A goblin whose latest report delivered a pull request waits on its merge,
// not on a session. A restart that paused it would have the scheduler resume
// it after the sign-in, only to report the same pull request done again, as
// three goblins did after a restart on 2026-10-27, so the restart leaves it
// unpaused. One the CFO steered back to work after its done report took a
// prompt since, and is paused with its handoff as one still working is.
func TestRestartPCLeavesAGoblinThatDeliveredItsPullRequestUnpaused(t *testing.T) {
	// Arrange
	s, h, log, spawner := restartBoard(t, 8*gigabyte)
	now := time.Now().UTC()
	delivered := hostedGoblin(t, h, "first-goblin", now)
	working := hostedGoblin(t, h, "second-goblin", now)
	if err := state.AppendStatus(h.State, delivered.ID, "done: PR https://github.com/acme/api/pull/7"); err != nil {
		t.Fatal(err)
	}
	if err := state.AppendStatus(h.State, working.ID, "done: PR https://github.com/acme/api/pull/8"); err != nil {
		t.Fatal(err)
	}
	promptedAfterItsReports(t, s.Store, working)

	// Act
	code, _ := askTheBoard(t, s, "POST", "/api/pc/restart", "{}", nil)
	select {
	case <-log.restarted:
	case <-time.After(30 * time.Second):
		t.Fatalf("Windows was never restarted: %v", log.recorded())
	}
	awaitRestartState(t, s, "restarting")

	// Assert
	if code != http.StatusAccepted {
		t.Fatalf("POST /api/pc/restart = %d, want it accepted", code)
	}
	if calls := spawner.recorded(); len(calls) != 1 || calls[0][0] != "pause" || calls[0][1] != working.ID {
		t.Errorf("cfo ran %v, want a pause of %s alone", calls, working.ID)
	}
	if got, isPaused := log.atRestart[delivered.ID]; isPaused {
		t.Errorf("as Windows restarted %s was %q, want it left unpaused", delivered.ID, got)
	}
	if record, err := state.ReadLifecycle(h.State, delivered.ID); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("%s has lifecycle %+v, %v; want none, so no pause clears into a resume", delivered.ID, record, err)
	}
}

// A goblin that does not pause stops the restart short, so its work is not
// cut off: Windows is not restarted, the board says why, the CFO is told,
// and the goblins already paused stay paused until the PC restarts.
func TestARestartStopsShortWhenAGoblinDoesNotPause(t *testing.T) {
	// Arrange
	s, h, log, _ := restartBoard(t, 8*gigabyte, "second-goblin")
	now := time.Now().UTC()
	hostedGoblin(t, h, "first-goblin", now)
	hostedGoblin(t, h, "second-goblin", now)

	// Act
	code, _ := askTheBoard(t, s, "POST", "/api/pc/restart", "{}", nil)
	restart := awaitRestartState(t, s, "stopped")

	// Assert
	if code != http.StatusAccepted {
		t.Fatalf("POST /api/pc/restart = %d, want it accepted", code)
	}
	if slices.Contains(log.recorded(), "restart Windows") {
		t.Fatalf("Windows was restarted though second-goblin did not pause: %v", log.recorded())
	}
	if restart.Problem != "not every working goblin paused: second-goblin (failed)" {
		t.Errorf("the restart's problem = %q, want it to name the goblin that did not pause", restart.Problem)
	}
	if record, err := state.ReadLifecycle(h.State, "first-goblin"); err != nil || record.Phase != "paused" || record.Pause.Reason != "restart" {
		t.Errorf("first-goblin = %+v, %v, want it left paused until the PC restarts", record, err)
	}
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 2 || !strings.HasPrefix(pending[1].Detail, "The PC did not restart: not every working goblin paused: second-goblin (failed). Paused for the restart: first-goblin.") {
		t.Errorf("the CFO's queue = %+v, %v, want it told why the PC did not restart", pending, err)
	}
	if code, body := askTheBoard(t, s, "POST", "/api/pc/restart", "{}", nil); code != http.StatusAccepted {
		t.Errorf("a second press after the restart stopped short = %d %s, want it to try again", code, body)
	}
	awaitRestartState(t, s, "stopped")
}

// Restart PC is for pools past the line: under it the press is refused with
// what they hold, and nothing pauses.
func TestRestartPCIsRefusedWhileThePoolsAreUnderTheLine(t *testing.T) {
	// Arrange
	s, h, log, spawner := restartBoard(t, 7*gigabyte)
	hostedGoblin(t, h, "first-goblin", time.Now().UTC())

	// Act
	code, body := askTheBoard(t, s, "POST", "/api/pc/restart", "{}", nil)

	// Assert
	if code != http.StatusConflict || !strings.Contains(body, "The kernel's pools hold 7.0 GB, under the 7.5 GB line, so the PC needs no restart") {
		t.Errorf("POST /api/pc/restart = %d %s, want it refused under the line", code, body)
	}
	if calls := spawner.recorded(); len(calls) != 0 || len(log.recorded()) != 0 || s.pcRestartView() != nil {
		t.Errorf("a refused press ran %v and did %v, want nothing", calls, log.recorded())
	}
}

// Only the PC's own board restarts it, as only it turns AFK mode on: a press
// through his private board, through a proxy or from a browser an agent
// started is refused, the CFO is told, and nothing pauses.
func TestOnlyThePCsOwnBoardRestartsThePC(t *testing.T) {
	for name, press := range map[string]func(*testing.T, *Service) int{
		"his private board": func(t *testing.T, s *Service) int {
			code, _ := pressOnHisPhone(t, throughTheGate(t, s), true, func(r *http.Request) {
				r.URL.Path, r.Body, r.ContentLength = "/api/pc/restart", io.NopCloser(strings.NewReader("{}")), 2
			})
			return code
		},
		"a proxy": func(t *testing.T, s *Service) int {
			code, _ := askTheBoard(t, s, "POST", "/api/pc/restart", "{}", func(r *http.Request) { r.Header.Set("X-Forwarded-For", "100.64.0.7") })
			return code
		},
		"a browser Claude Code started": func(t *testing.T, s *Service) int {
			s.inspectCaller = func(pid int) ([]proc.Entry, []string, error) {
				started := time.Now().Add(-time.Hour)
				return []proc.Entry{
					{PID: pid, ParentPID: 4242, ExeBase: "msedge.exe", Start: started},
					{PID: 4242, ParentPID: 900, ExeBase: "claude.exe", Start: started.Add(-time.Minute)},
					{PID: 900, ExeBase: "explorer.exe", Start: started.Add(-time.Hour)},
				}, nil, nil
			}
			code, _ := askTheBoard(t, s, "POST", "/api/pc/restart", "{}", nil)
			return code
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			s, h, log, spawner := restartBoard(t, 8*gigabyte)
			hostedGoblin(t, h, "first-goblin", time.Now().UTC())

			// Act
			code := press(t, s)

			// Assert
			if code != http.StatusForbidden {
				t.Errorf("a press from %s = %d, want it refused", name, code)
			}
			if calls := spawner.recorded(); len(calls) != 0 || len(log.recorded()) != 0 || s.pcRestartView() != nil {
				t.Errorf("a refused press ran %v and did %v, want nothing", calls, log.recorded())
			}
			pending, err := wake.Pending(h.State)
			if err != nil || len(pending) != 1 || pending[0].Kind != "review" || pending[0].Key != "restart" {
				t.Errorf("the CFO's queue = %+v, %v, want the one refusal", pending, err)
			}
		})
	}
}

// A pause for the PC's restart clears once a later Windows sign-in runs: the
// restart ends the sign-in it was taken in, and nothing else clears it.
func TestARestartPauseClearsOnlyInALaterSignIn(t *testing.T) {
	for name, test := range map[string]struct {
		signedIn    time.Time
		wantResumed bool
	}{
		"the sign-in it was taken in": {signedIn: signedInAt},
		"the sign-in after a restart": {signedIn: signedInAt.Add(4 * 24 * time.Hour), wantResumed: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			spawner := &spawnRecorder{}
			handler, h := startBoard(t, 8*gigabyte, spawner)
			s := handler.Service
			s.Options.Comeback = &Comeback{SignedIn: func() (time.Time, error) { return test.signedIn, nil }}
			meta := pausedGoblin(t, h, "restart-goblin", "restart", signedInAt.Format(time.RFC3339Nano), signedInAt.Add(time.Hour))
			now := time.Now().UTC()

			// Act
			readFleet(t, s, now, 0, 1, 2)

			// Assert
			calls := spawner.recorded()
			if test.wantResumed {
				calls = awaitDispatch(t, s, spawner, 1)
			}
			isResumed := slices.ContainsFunc(calls, func(call []string) bool { return call[0] == "resume" && call[1] == meta.ID })
			if isResumed != test.wantResumed {
				t.Errorf("cfo ran %v in %s, want a resume %v", calls, name, test.wantResumed)
			}
		})
	}
}

// A restart whose last step fails says so on the board and to the CFO, and
// the goblins stay paused until the PC restarts.
func TestARestartThatWindowsRefusesSaysWhy(t *testing.T) {
	// Arrange
	s, h, _, _ := restartBoard(t, 8*gigabyte)
	hostedGoblin(t, h, "first-goblin", time.Now().UTC())
	s.Options.RestartWindows = func() error { return errors.New("A required privilege is not held by the client.") }

	// Act
	askTheBoard(t, s, "POST", "/api/pc/restart", "{}", nil)
	restart := awaitRestartState(t, s, "stopped")

	// Assert
	if restart.Problem != "Windows did not restart: A required privilege is not held by the client." {
		t.Errorf("the restart = %+v, want Windows' refusal", restart)
	}
	pending, err := wake.Pending(h.State)
	if err != nil || len(pending) != 3 || !strings.HasPrefix(pending[2].Detail, "The PC did not restart: Windows did not restart") {
		t.Errorf("the CFO's queue = %+v, %v, want it told the PC did not restart", pending, err)
	}
}
