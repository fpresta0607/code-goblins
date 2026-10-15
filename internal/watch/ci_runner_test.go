package watch

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/cirunner"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

// fakeLine is cfo gate turn --detach as the watch sees it: each run it starts
// writes the next pid beside its log, unless it exits with exit.
type fakeLine struct {
	ran  [][]string
	pid  int
	exit int
}

func (l *fakeLine) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	l.ran = append(l.ran, append([]string{request.Name}, request.Args...))
	if l.exit != 0 {
		return execx.Result{ExitCode: l.exit, Stderr: []byte("cfo gate turn: this run takes no turn: why")}, nil
	}
	l.pid++
	log := request.Args[slices.Index(request.Args, "--detach")+1]
	return execx.Result{}, os.WriteFile(log+".pid", []byte(strconv.Itoa(l.pid)), 0o600)
}

// watchedRunner is a ciRunnerWatch on fakes: a service that says pending and
// answers with err, the line, a clock, and processes that run while in
// running, with their start times.
type watchedRunner struct {
	watch   *ciRunnerWatch
	line    *fakeLine
	pending cirunner.Pending
	err     error
	asked   int
	running map[int]time.Time
	now     time.Time
	said    []string
}

func newWatchedRunner(t *testing.T) *watchedRunner {
	t.Helper()
	r := &watchedRunner{line: &fakeLine{}, running: map[int]time.Time{}, now: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	r.watch = &ciRunnerWatch{
		ask: func(ctx context.Context, request cirunner.Request, reply func(cirunner.Reply)) error {
			r.asked++
			if deadline, hasDeadline := ctx.Deadline(); !hasDeadline || time.Until(deadline) > ciRunnerAskLimit {
				t.Errorf("asked with deadline %v, %v; want the ask bounded at %v", deadline, hasDeadline, ciRunnerAskLimit)
			}
			if request.Kind != cirunner.AskPending {
				t.Errorf("asked %q; want pending", request.Kind)
			}
			if r.err != nil {
				return r.err
			}
			pending := r.pending
			reply(cirunner.Reply{Kind: cirunner.ReplyPending, Pending: &pending})
			return nil
		},
		commands: r.line,
		started: func(pid int) (time.Time, bool) {
			start, isRunning := r.running[pid]
			return start, isRunning
		},
		executable: `C:\fleet\cfo.exe`,
		logs:       t.TempDir(),
		now:        func() time.Time { return r.now },
		say:        func(line string) { r.said = append(r.said, line) },
	}
	return r
}

// A job that waits gets exactly one turn at the lowest priority, detached and
// named for the job, and no other while the run started for it lives.
func TestTheWatcherStartsOneLowPriorityTurnForAWaitingJob(t *testing.T) {
	// Arrange
	r := newWatchedRunner(t)
	r.pending = cirunner.Pending{IsWaiting: true, RunID: 7, Job: "desktop-backend-shard (1)"}
	r.running[1] = r.now

	// Act
	r.watch.look(context.Background())
	r.now = r.now.Add(30 * time.Second)
	r.watch.look(context.Background())

	// Assert
	log := filepath.Join(r.watch.logs, "job-7-20261014-120000.log")
	want := []string{`C:\fleet\cfo.exe`, "gate", "turn", "--low-priority", "--memory", "7GiB", "--as", "desktop CI: desktop-backend-shard (1) of run 7", "--detach", log, "--", `C:\fleet\cfo.exe`, "ci-runner", "job"}
	if len(r.line.ran) != 1 || !slices.Equal(r.line.ran[0], want) {
		t.Errorf("started %q; want one run, %q", r.line.ran, want)
	}
	if r.asked != 1 {
		t.Errorf("asked %d time(s); want the service left alone while the job's run lives", r.asked)
	}
	if len(r.said) != 1 || !strings.Contains(r.said[0], "as pid 1, logging to "+log) {
		t.Errorf("said %q; want the start named with its pid and log", r.said)
	}
}

// Once the run started for a job has ended, or its pid names another
// process, the next job that waits gets a turn of its own.
func TestTheWatcherStartsTheNextJobOnceTheLastRunEnded(t *testing.T) {
	for name, end := range map[string]func(r *watchedRunner){
		"ended":          func(r *watchedRunner) { delete(r.running, 1) },
		"its pid reused": func(r *watchedRunner) { r.running[1] = r.now },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			r := newWatchedRunner(t)
			r.pending = cirunner.Pending{IsWaiting: true, RunID: 7, Job: "desktop-backend-shard (1)"}
			r.running[1] = r.now
			r.watch.look(context.Background())
			r.now = r.now.Add(time.Minute)
			end(r)

			// Act
			r.watch.look(context.Background())

			// Assert
			if len(r.line.ran) != 2 {
				t.Errorf("started %d run(s); want a second once the first ended", len(r.line.ran))
			}
		})
	}
}

// Nothing starts when no job waits, nor where no service answers, and a
// machine with no runner says nothing about it.
func TestTheWatcherStartsNothingWhenNoJobWaits(t *testing.T) {
	for name, row := range map[string]struct {
		pending cirunner.Pending
		err     error
	}{
		"no job waits":          {pending: cirunner.Pending{}},
		"no service is running": {err: cirunner.ErrNotServing},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			r := newWatchedRunner(t)
			r.pending, r.err = row.pending, row.err

			// Act
			r.watch.look(context.Background())

			// Assert
			if len(r.line.ran) != 0 || len(r.said) != 0 {
				t.Errorf("started %q and said %q; want neither", r.line.ran, r.said)
			}
		})
	}
}

// A service that does not answer, and a turn that does not start, are each
// said once an hour, not at every look.
func TestTheWatcherSaysTroubleOnceAnHour(t *testing.T) {
	for name, row := range map[string]struct {
		err  error
		exit int
		want string
	}{
		"the service does not answer": {err: errors.New("cirunner: the service refused: GitHub answered HTTP 502"), want: "the desktop CI runner did not say whether a job waits: cirunner: the service refused: GitHub answered HTTP 502"},
		"the turn does not start":     {exit: 125, want: `the turn for desktop CI job "desktop-backend-shard (1)" of run 7 did not start: exit 125: cfo gate turn: this run takes no turn: why`},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			r := newWatchedRunner(t)
			r.pending = cirunner.Pending{IsWaiting: true, RunID: 7, Job: "desktop-backend-shard (1)"}
			r.err, r.line.exit = row.err, row.exit

			// Act
			for range 120 {
				r.watch.look(context.Background())
				r.now = r.now.Add(30 * time.Second)
			}
			r.watch.look(context.Background())

			// Assert
			if len(r.said) != 2 || r.said[0] != row.want || r.said[1] != row.want {
				t.Errorf("said %q; want %q once, and once more an hour later", r.said, row.want)
			}
		})
	}
}

// Each line said is added to the watch's log with the time it was said.
func TestAppendLineAddsEachLineWithItsTime(t *testing.T) {
	// Arrange
	path := filepath.Join(t.TempDir(), "watch.log")
	say := appendLine(path, func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) })

	// Act
	say("first")
	say("second")

	// Assert
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "2026-10-14T12:00:00Z first\n2026-10-14T12:00:00Z second\n" {
		t.Errorf("log = %q, %v; want both lines with their time", data, err)
	}
}
