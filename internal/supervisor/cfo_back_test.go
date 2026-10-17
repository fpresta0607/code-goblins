package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

// cfoBackRecorder stands in for bringing back a CFO whose terminal ended
// unasked: it records why each comeback was for and answers with err.
type cfoBackRecorder struct {
	mu   sync.Mutex
	whys []string
	err  error
}

func (r *cfoBackRecorder) back(_ context.Context, why string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.whys = append(r.whys, why)
	return r.err
}

func (r *cfoBackRecorder) told() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.whys)
}

// cfoBackBoard is a board of this sign-in that brings back a CFO whose
// terminal ended unasked through recorder, and whose harness was installed
// before anything here started.
func cfoBackBoard(t *testing.T) (*Service, home.Home, *cfoBackRecorder) {
	t.Helper()
	handler, h := startBoard(t, 8*gigabyte, &spawnRecorder{})
	service := handler.Service
	recorder := &cfoBackRecorder{}
	service.Options.Comeback = &Comeback{
		SignedIn: func() (time.Time, error) { return thisSignIn, nil },
		CFO: func(context.Context) error {
			t.Error("the comeback after a restart brought the CFO back")
			return nil
		},
		CFOEnded: recorder.back,
	}
	service.Options.ProgramInstalled = func(harness.Kind) (string, time.Time, error) {
		return `C:\fixture\claude.exe`, lastSignIn, nil
	}
	return service, h, recorder
}

// registerCFO records the home's CFO as registered in native terminal cfo as
// process pid, started at started.
func registerCFO(t *testing.T, h home.Home, pid int, started time.Time) {
	t.Helper()
	hostname, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(primaryRegistration{Host: NativeCFOTerminal, Agent: "claude", Process: lock.Info{PID: pid, OwnerPID: pid, Start: started, Hostname: hostname}})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "primary.json"), string(data))
}

// cfoEnded records how the program of native terminal cfo, pid started at
// started, ended at ended.
func cfoEnded(t *testing.T, h home.Home, pid int, started, ended time.Time, code uint32, isClosed bool) {
	t.Helper()
	data, err := json.Marshal(host.End{ID: NativeCFOTerminal, ChildPID: pid, ChildStart: started, Ended: ended, Code: code, Closed: isClosed})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(h.State, "hosts", NativeCFOTerminal+".end"), string(data))
}

// stoppedItems are the Command Center items that offer to reopen a CFO that
// kept ending, as the board has them.
func stoppedItems(s *Service) []Run {
	var kept []Run
	for _, r := range s.Store.Snapshot().Runs {
		if r.CFOStopped {
			kept = append(kept, r)
		}
	}
	return kept
}

// A CFO whose terminal ended in this sign-in while nobody asked it to stop
// comes back at the supervisor's next look, told why: a crash's exit code,
// the update of its harness that was installed while it ran, a host that
// ended with it without saying how, and a program that ended before it had
// registered, which the terminal names.
func TestACFOWhoseTerminalEndedUnaskedComesBackToldWhy(t *testing.T) {
	registered := thisSignIn.Add(time.Hour)
	for _, testCase := range []struct {
		name      string
		isEnded   bool
		pid       int
		started   time.Time
		code      uint32
		installed time.Time
		want      string
	}{
		{name: "a non-zero exit", isEnded: true, pid: 999999, started: registered, code: 1, want: "its terminal ended with exit code 1"},
		{name: "an exit after its harness was updated", isEnded: true, pid: 999999, started: registered, code: 1, installed: registered.Add(time.Minute), want: "its terminal ended with exit code 1, after an update of Claude Code was installed while it ran"},
		{name: "a host that ended with it", want: "its terminal ended with its host, which recorded nothing of how"},
		{name: "a program that ended before it registered", isEnded: true, pid: 999998, started: registered.Add(2 * time.Hour), code: 3, want: "its terminal ended with exit code 3"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			service, h, recorder := cfoBackBoard(t)
			if !testCase.installed.IsZero() {
				service.Options.ProgramInstalled = func(harness.Kind) (string, time.Time, error) {
					return `C:\fixture\claude.exe`, testCase.installed, nil
				}
			}
			registerCFO(t, h, 999999, registered)
			if testCase.isEnded {
				cfoEnded(t, h, testCase.pid, testCase.started, testCase.started.Add(time.Hour), testCase.code, false)
			}

			// Act
			err := service.bringEndedCFOBack(t.Context(), registered.Add(4*time.Hour))

			// Assert
			if err != nil {
				t.Fatalf("bringEndedCFOBack: %v", err)
			}
			if told := recorder.told(); !slices.Equal(told, []string{testCase.want}) {
				t.Errorf("the CFO came back told %q, want %q", told, testCase.want)
			}
		})
	}
}

// An end somebody asked for is never undone: a terminal a viewer closed, as
// Restart the CFO, a harness switch and the uninstall close it, and a harness
// that exited with code 0, as the Overlord's own exit does. A CFO that ended
// in the sign-in before is the comeback's to bring back after a restart.
func TestACFOThatWasStoppedStaysStopped(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		started  time.Time
		code     uint32
		isClosed bool
	}{
		{name: "closed by a viewer", started: thisSignIn.Add(time.Hour), code: 1, isClosed: true},
		{name: "its own exit", started: thisSignIn.Add(time.Hour)},
		{name: "of the sign-in before", started: lastSignIn.Add(time.Hour), code: 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			service, h, recorder := cfoBackBoard(t)
			registerCFO(t, h, 999999, testCase.started)
			cfoEnded(t, h, 999999, testCase.started, testCase.started.Add(time.Hour), testCase.code, testCase.isClosed)

			// Act
			err := service.bringEndedCFOBack(t.Context(), thisSignIn.Add(4*time.Hour))

			// Assert
			if err != nil {
				t.Fatalf("bringEndedCFOBack: %v", err)
			}
			if told := recorder.told(); len(told) != 0 {
				t.Errorf("the CFO came back told %q, want it left stopped", told)
			}
		})
	}
}

// Each end brings the CFO back once, however many looks find it ended, so a
// comeback that did not bring it back is not tried again in a loop; the
// reason it did not is kept.
func TestEachEndOfTheCFOBringsItBackOnce(t *testing.T) {
	// Arrange
	service, h, recorder := cfoBackBoard(t)
	recorder.err = errors.New("claude is not on PATH")
	started := thisSignIn.Add(time.Hour)
	registerCFO(t, h, 999999, started)
	cfoEnded(t, h, 999999, started, started.Add(time.Hour), 1, false)

	// Act
	first := service.bringEndedCFOBack(t.Context(), started.Add(2*time.Hour))
	second := service.bringEndedCFOBack(t.Context(), started.Add(2*time.Hour+cfoBackEvery))

	// Assert
	if first == nil || second != nil {
		t.Errorf("looks = %v, %v; want the failed comeback said once", first, second)
	}
	if told := recorder.told(); len(told) != 1 {
		t.Errorf("the CFO was brought back %d times, want once", len(told))
	}
	if record, err := readCFOBack(h.State); err != nil || record.Problem != "claude is not on PATH" {
		t.Errorf("record = %+v, %v; want why it did not come back", record, err)
	}
}

// A CFO that keeps ending soon after it started is brought back until it has
// ended three times in a row, and then left stopped: one wake for the next
// CFO and one Command Center item for the Overlord, made once, and taken back
// once a CFO runs again. The end of a CFO that ran for hours before them is
// not one of them.
func TestACFOThatEndsThreeTimesInARowIsNotBroughtBackAgain(t *testing.T) {
	// Arrange
	service, h, recorder := cfoBackBoard(t)
	registered := thisSignIn.Add(time.Hour)
	registerCFO(t, h, 999999, registered)
	// The first end is of a CFO that ran for hours; each after it, of the
	// one brought back a minute after the end before, ended within a minute
	// of its start.
	ends := []struct {
		pid     int
		ranFor  time.Duration
		started time.Time
	}{{999999, 4 * time.Hour, registered}, {999998, 30 * time.Second, registered.Add(4*time.Hour + time.Minute)}, {999997, 30 * time.Second, registered.Add(4*time.Hour + 2*time.Minute)}, {999996, 30 * time.Second, registered.Add(4*time.Hour + 3*time.Minute)}}

	// Act
	for _, end := range ends {
		cfoEnded(t, h, end.pid, end.started, end.started.Add(end.ranFor), 1, false)
		if err := service.bringEndedCFOBack(t.Context(), end.started.Add(end.ranFor+time.Second)); err != nil {
			t.Fatalf("bringEndedCFOBack: %v", err)
		}
	}
	if err := service.bringEndedCFOBack(t.Context(), registered.Add(6*time.Hour)); err != nil {
		t.Fatalf("bringEndedCFOBack: %v", err)
	}

	// Assert
	if told := recorder.told(); len(told) != 3 {
		t.Errorf("the CFO was brought back %d times, want three times before the cap", len(told))
	}
	pending, err := wake.Pending(h.State)
	if err != nil {
		t.Fatal(err)
	}
	if checks := slices.DeleteFunc(slices.Clone(pending), func(record wake.Record) bool { return record.Kind != "check" || record.Key != "cfo" }); len(checks) != 1 {
		t.Errorf("wakes %+v, want one check wake for the CFO", pending)
	}
	items := stoppedItems(service)
	if len(items) != 1 || items[0].State != "ready" {
		t.Fatalf("items %+v, want one waiting item that offers Reopen", items)
	}

	// Act: a CFO runs again.
	started, isAlive := proc.StartTime(os.Getpid())
	if !isAlive {
		t.Fatal("this test's own process has no start time")
	}
	registerCFO(t, h, os.Getpid(), started)
	if err := service.bringEndedCFOBack(t.Context(), registered.Add(7*time.Hour)); err != nil {
		t.Fatalf("bringEndedCFOBack: %v", err)
	}

	// Assert
	if items := stoppedItems(service); len(items) != 1 || items[0].State != "withdrawn" {
		t.Errorf("items %+v, want the item taken back once a CFO runs again", items)
	}
}

// The count of ends in a row starts again once the CFO held: the end of a CFO
// that ran cfoBackHeld is not in a row, nor does a short end join one from
// hours before, so the ends on either side never add up to the cap.
func TestAnEndAfterTheCFOHeldStartsTheCountAgain(t *testing.T) {
	registered := thisSignIn.Add(time.Hour)
	type end struct {
		ranFor  time.Duration
		started time.Time
	}
	for _, testCase := range []struct {
		name string
		ends []end
	}{
		{name: "an end of a CFO that held", ends: []end{{30 * time.Second, registered}, {30 * time.Second, registered.Add(time.Minute)}, {cfoBackHeld, registered.Add(2 * time.Minute)}, {30 * time.Second, registered.Add(13 * time.Minute)}, {30 * time.Second, registered.Add(14 * time.Minute)}}},
		{name: "two short ends hours apart", ends: []end{{30 * time.Second, registered}, {30 * time.Second, registered.Add(3 * time.Hour)}, {30 * time.Second, registered.Add(3*time.Hour + time.Minute)}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			service, h, recorder := cfoBackBoard(t)
			registerCFO(t, h, 999999, registered)

			// Act
			for i, end := range testCase.ends {
				cfoEnded(t, h, 999999-i, end.started, end.started.Add(end.ranFor), 1, false)
				if err := service.bringEndedCFOBack(t.Context(), end.started.Add(end.ranFor+time.Second)); err != nil {
					t.Fatalf("bringEndedCFOBack: %v", err)
				}
			}

			// Assert
			if told := recorder.told(); len(told) != len(testCase.ends) {
				t.Errorf("the CFO was brought back %d times, want after each of its %d ends", len(told), len(testCase.ends))
			}
			if items := stoppedItems(service); len(items) != 0 {
				t.Errorf("items %+v, want none", items)
			}
		})
	}
}

// A comeback that fails leaves the CFO stopped as the cap does, never closed
// with nothing lasting on the board: one check wake for the next CFO and one
// Command Center item for the Overlord that says why in plain words, whether
// its host recorded how it ended or not.
func TestACFOThatDidNotComeBackIsLeftForTheOverlordToReopen(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		isEnded bool
		want    string
	}{
		{name: "an end its host recorded", isEnded: true, want: "The CFO did not come back after its terminal ended with exit code 1: claude is not on PATH. Reopen the CFO once you have looked?"},
		{name: "a host that recorded nothing", want: "The CFO did not come back after its terminal ended with its host, which recorded nothing of how: claude is not on PATH. Reopen the CFO once you have looked?"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			// Arrange
			service, h, recorder := cfoBackBoard(t)
			recorder.err = errors.New("claude is not on PATH")
			started := thisSignIn.Add(time.Hour)
			registerCFO(t, h, 999999, started)
			if testCase.isEnded {
				cfoEnded(t, h, 999999, started, started.Add(time.Hour), 1, false)
			}

			// Act
			err := service.bringEndedCFOBack(t.Context(), started.Add(2*time.Hour))

			// Assert
			if err == nil {
				t.Error("the failed comeback was not said")
			}
			pending, err := wake.Pending(h.State)
			if err != nil {
				t.Fatal(err)
			}
			if checks := slices.DeleteFunc(slices.Clone(pending), func(record wake.Record) bool { return record.Kind != "check" || record.Key != "cfo" }); len(checks) != 1 {
				t.Errorf("wakes %+v, want one check wake for the CFO", pending)
			}
			if items := stoppedItems(service); len(items) != 1 || items[0].State != "ready" || items[0].Title != testCase.want {
				t.Errorf("items %+v, want one waiting item titled %q", items, testCase.want)
			}
		})
	}
}

// A comeback whose CFO ended while it started left a newer end, which is not
// a CFO left stopped but one more end in the row for the next look: one
// incident, a crash and then CFOs that keep ending while they start, raises
// one check wake and one item, the cap's, and none for each failed try.
func TestACFOThatEndsWhileItComesBackIsOneMoreEndInTheRow(t *testing.T) {
	// Arrange
	service, h, recorder := cfoBackBoard(t)
	recorder.err = errors.New("its native terminal ended during startup")
	registered := thisSignIn.Add(time.Hour)
	registerCFO(t, h, 999999, registered)
	cfoEnded(t, h, 999999, registered, registered.Add(time.Hour), 1, false)
	look := registered.Add(time.Hour + cfoBackEvery)
	pid := 999999
	service.Options.Comeback.CFOEnded = func(ctx context.Context, why string) error {
		pid--
		cfoEnded(t, h, pid, look, look.Add(3*time.Second), 1, false)
		return recorder.back(ctx, why)
	}
	checks := func() []wake.Record {
		pending, err := wake.Pending(h.State)
		if err != nil {
			t.Fatal(err)
		}
		return slices.DeleteFunc(pending, func(record wake.Record) bool { return record.Kind != "check" || record.Key != "cfo" })
	}

	// Act
	err := service.bringEndedCFOBack(t.Context(), look)

	// Assert
	if err == nil {
		t.Error("the failed comeback was not said")
	}
	if wakes, items := checks(), stoppedItems(service); len(wakes) != 0 || len(items) != 0 {
		t.Errorf("wakes %+v, items %+v; want none while a newer end waits for the next look", wakes, items)
	}

	// Act: each next look takes the newer end; the third short one is the cap.
	failed := 0
	for range 3 {
		look = look.Add(cfoBackEvery)
		if err := service.bringEndedCFOBack(t.Context(), look); err != nil {
			failed++
		}
	}

	// Assert
	if told := recorder.told(); len(told) != 3 || failed != 2 {
		t.Errorf("the CFO was brought back %d times, %d of them failed after the first; want three, two failed, then the cap", len(told), failed)
	}
	if wakes := checks(); len(wakes) != 1 {
		t.Errorf("wakes %+v, want one check wake for the incident", wakes)
	}
	if items := stoppedItems(service); len(items) != 1 || !strings.HasPrefix(items[0].Title, "The CFO's terminal ended 3 times in a row") {
		t.Errorf("items %+v, want the cap's one item", items)
	}
}
