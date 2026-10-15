package cirunner

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeJob is the VM and GitHub as a job sees them. Each look at the VM is a
// poll: the runner turns busy at busyAt polls, and the VM powers itself off at
// offAt polls, or never when zero. A call named in failing fails, and every
// call fails once its context has ended, as a command's does.
type fakeJob struct {
	mu        sync.Mutex
	calls     []string
	isRunning bool
	polls     int
	busyAt    int
	offAt     int
	failing   string
	attached  string
	deleted   []int64
	// deleting is how long deleting a runner takes.
	deleting time.Duration
	// queued, when set, is the job GitHub says waits, and queuedErr what
	// asking for it fails with. asks counts the asks, and asking is how long
	// GitHub takes to answer one.
	queued    *Pending
	queuedErr error
	asks      int
	asking    time.Duration
	// failFirst is how many of each call's first tries fail.
	failFirst map[string]int
	// offHeld, when set, holds each turn of the VM off until it is closed.
	offHeld chan struct{}
	// isPublic says the repository is public, and privacyAsks counts the
	// asks whether it is private.
	isPublic    bool
	privacyAsks int
}

func (f *fakeJob) IsPrivate(context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.privacyAsks++
	return !f.isPublic, nil
}

func (f *fakeJob) note(ctx context.Context, call string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
	if err := ctx.Err(); err != nil {
		return err
	}
	if f.failing == call {
		return errors.New(call + " failed")
	}
	if f.failFirst[call] > 0 {
		f.failFirst[call]--
		return errors.New(call + " failed this time")
	}
	return nil
}

func (f *fakeJob) TurnOff(ctx context.Context) error {
	if f.offHeld != nil {
		<-f.offHeld
	}
	err := f.note(ctx, "off")
	if err == nil {
		f.mu.Lock()
		f.isRunning = false
		f.mu.Unlock()
	}
	return err
}

func (f *fakeJob) Restore(ctx context.Context) error { return f.note(ctx, "restore") }

func (f *fakeJob) AttachDVD(ctx context.Context, iso string) error {
	f.mu.Lock()
	f.attached = iso
	f.mu.Unlock()
	return f.note(ctx, "dvd")
}

func (f *fakeJob) Start(ctx context.Context) error {
	err := f.note(ctx, "start")
	if err == nil {
		f.mu.Lock()
		f.isRunning = true
		f.mu.Unlock()
	}
	return err
}

func (f *fakeJob) IsRunning(ctx context.Context) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.polls++
	if f.offAt != 0 && f.polls >= f.offAt {
		f.isRunning = false
	}
	return f.isRunning, ctx.Err()
}

func (f *fakeJob) QueuedJob(ctx context.Context) (Pending, error) {
	f.mu.Lock()
	f.asks++
	f.mu.Unlock()
	time.Sleep(f.asking)
	if err := ctx.Err(); err != nil {
		return Pending{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.queuedErr != nil {
		return Pending{}, f.queuedErr
	}
	if f.queued != nil {
		return *f.queued, nil
	}
	return Pending{IsWaiting: true, RunID: 7, Job: "desktop-backend-shard (1)"}, nil
}

func (f *fakeJob) RegisterRunner(ctx context.Context, name string) (Runner, error) {
	return Runner{ID: 41, Name: name, Config: "abc"}, f.note(ctx, "register")
}

func (f *fakeJob) RunnerState(ctx context.Context, _ int64) (bool, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.busyAt != 0 && f.polls >= f.busyAt, true, ctx.Err()
}

func (f *fakeJob) DeleteRunner(ctx context.Context, id int64) error {
	time.Sleep(f.deleting)
	err := f.note(ctx, "delete")
	f.mu.Lock()
	f.deleted = append(f.deleted, id)
	f.mu.Unlock()
	return err
}

func (f *fakeJob) DeleteOwnRunners(ctx context.Context) error { return f.note(ctx, "delete own") }

func (f *fakeJob) callsMade() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

// testDeps runs a job against f on a clock that each sleep moves on, and
// hands the runner's configuration to a DVD it records in config.
func testDeps(f *fakeJob, config *string) Deps {
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	return Deps{
		GitHub: f,
		VM:     f,
		DVD: func(given string) (string, error) {
			*config = given
			if f.failing == "build dvd" {
				return "", errors.New("build dvd failed")
			}
			return `C:\work\job.iso`, nil
		},
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			return now
		},
		Sleep: func(_ context.Context, step time.Duration) {
			mu.Lock()
			defer mu.Unlock()
			now = now.Add(step)
		},
	}
}

// However a job ends, the VM is off and back at clean, and the job's runner
// is deleted.
func TestRunOneJobEndsEachWayItCan(t *testing.T) {
	for name, row := range map[string]struct {
		busyAt, offAt, cancelAt int
		failing                 string
		want                    Outcome
		wantErr                 string
	}{
		"the job runs and the VM powers off":   {busyAt: 2, offAt: 5, want: Ran},
		"no job comes within the pickup":       {want: NoJob},
		"the VM powers off before a job comes": {offAt: 3, want: NoJob},
		"the job runs past its limit":          {busyAt: 2, want: TimedOut},
		"the turn ends during the job":         {busyAt: 2, cancelAt: 4, want: Abandoned},
		"the DVD cannot be built":              {failing: "build dvd", want: Failed, wantErr: "build dvd failed"},
		"the DVD cannot be attached":           {failing: "dvd", want: Failed, wantErr: "dvd failed"},
		"the VM does not start":                {failing: "start", want: Failed, wantErr: "start failed"},
		"the runner cannot be deleted":         {busyAt: 2, offAt: 5, failing: "delete", want: Ran, wantErr: "delete failed"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := &fakeJob{busyAt: row.busyAt, offAt: row.offAt, failing: row.failing}
			var config string
			d := testDeps(f, &config)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if row.cancelAt != 0 {
				sleep, sleeps := d.Sleep, 0
				d.Sleep = func(ctx context.Context, step time.Duration) {
					if sleeps++; sleeps == row.cancelAt {
						cancel()
					}
					sleep(ctx, step)
				}
			}

			// Act
			outcome, err := RunOneJob(ctx, d, DefaultLimits, func(string) {})

			// Assert
			if outcome != row.want || row.wantErr == "" && err != nil || row.wantErr != "" && (err == nil || !strings.Contains(err.Error(), row.wantErr)) {
				t.Errorf("RunOneJob = %q, %v; want %q with error %q", outcome, err, row.want, row.wantErr)
			}
			if isRunning, _ := f.IsRunning(context.Background()); isRunning {
				t.Error("the VM still runs after the job ended")
			}
			calls := f.callsMade()
			deletes := 1
			if row.failing == "delete" {
				deletes = cleanupTries
			}
			want := append([]string{"off", "restore"}, slices.Repeat([]string{"delete"}, deletes)...)
			if len(calls) < len(want) || !slices.Equal(calls[len(calls)-len(want):], want) {
				t.Errorf("the job's last calls were %q; want the VM off, back at clean, and the runner deleted, %d time(s)", calls, deletes)
			}
			if !slices.Equal(f.deleted, slices.Repeat([]int64{41}, deletes)) {
				t.Errorf("deleted runners %v; want the job's own runner, 41", f.deleted)
			}
		})
	}
}

// A cleanup step that fails is tried again, a pause apart, up to
// cleanupTries in all: a VM still running the job's code, or a runner GitHub
// still counts busy, is put back or deleted at a later try.
func TestRunOneJobTriesItsCleanupAgain(t *testing.T) {
	for name, row := range map[string]struct {
		failFirst map[string]int
		want      []string
	}{
		"turning the VM off fails once":     {failFirst: map[string]int{"off": 1}, want: []string{"off", "off", "restore", "delete"}},
		"putting it back fails twice":       {failFirst: map[string]int{"restore": 2}, want: []string{"off", "restore", "off", "restore", "off", "restore", "delete"}},
		"deleting the runner fails twice":   {failFirst: map[string]int{"delete": 2}, want: []string{"off", "restore", "delete", "delete", "delete"}},
		"every step works at its first try": {want: []string{"off", "restore", "delete"}},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := &fakeJob{busyAt: 2, offAt: 5}
			var config string
			d := testDeps(f, &config)
			// The failures begin once the job runs, so the job's own first
			// putting back is not one of them.
			sleep, isSet := d.Sleep, false
			d.Sleep = func(ctx context.Context, step time.Duration) {
				if !isSet {
					f.mu.Lock()
					f.failFirst, isSet = maps.Clone(row.failFirst), true
					f.mu.Unlock()
				}
				sleep(ctx, step)
			}

			// Act
			outcome, err := RunOneJob(context.Background(), d, DefaultLimits, func(string) {})

			// Assert
			calls := f.callsMade()
			cleanup := calls[slices.Index(calls, "start")+1:]
			if outcome != Ran || err != nil || !slices.Equal(cleanup, row.want) {
				t.Errorf("RunOneJob = %q, %v, cleaning up with %q; want ran with no error, cleaning up with %q", outcome, err, cleanup, row.want)
			}
		})
	}
}

// The job's DVD holds its runner's configuration, and goes once the VM is
// back at clean. A VM that could not be put back may still hold it, so it
// stays.
func TestRunOneJobRemovesItsDVD(t *testing.T) {
	for name, row := range map[string]struct {
		failing   string
		isRemoved bool
	}{
		"the VM put back":              {isRemoved: true},
		"the VM could not be put back": {failing: "restore", isRemoved: false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := &fakeJob{busyAt: 2, offAt: 5}
			var config string
			d := testDeps(f, &config)
			iso := filepath.Join(t.TempDir(), "job.iso")
			d.DVD = func(given string) (string, error) { return iso, BuildDVD(iso, given) }
			sleep := d.Sleep
			d.Sleep = func(ctx context.Context, step time.Duration) {
				f.mu.Lock()
				f.failing = row.failing
				f.mu.Unlock()
				sleep(ctx, step)
			}

			// Act
			_, _ = RunOneJob(context.Background(), d, DefaultLimits, func(string) {})

			// Assert
			if _, err := os.Stat(iso); os.IsNotExist(err) != row.isRemoved {
				t.Errorf("the DVD after the job: %v; want it removed = %v", err, row.isRemoved)
			}
		})
	}
}

// A job starts from a VM turned off and put back to clean, registers its
// runner, and hands the runner's configuration to the VM on its DVD.
func TestRunOneJobStartsFromClean(t *testing.T) {
	// Arrange
	f := &fakeJob{isRunning: true, busyAt: 2, offAt: 5}
	var config string
	d := testDeps(f, &config)
	var said []string

	// Act
	outcome, err := RunOneJob(context.Background(), d, DefaultLimits, func(line string) { said = append(said, line) })

	// Assert
	if outcome != Ran || err != nil {
		t.Fatalf("RunOneJob = %q, %v; want ran", outcome, err)
	}
	if calls := f.callsMade(); !slices.Equal(calls[:5], []string{"off", "restore", "register", "dvd", "start"}) {
		t.Errorf("the job's first calls were %q; want off, restore, register, dvd, start", calls)
	}
	if config != "abc" || f.attached != `C:\work\job.iso` {
		t.Errorf("the DVD was built from %q and %q attached; want the runner's configuration on the built DVD", config, f.attached)
	}
	want := []string{"the VM started with runner cg-desktop-20261014-120000", "the runner is online", "the runner took a job"}
	if !slices.Equal(said, want) {
		t.Errorf("the job said %q; want %q", said, want)
	}
}

// A VM that cannot be put back to clean runs no job, and no runner is
// registered for one.
func TestRunOneJobRegistersNoRunnerOnAVMNotPutBack(t *testing.T) {
	for _, failing := range []string{"off", "restore"} {
		t.Run(failing, func(t *testing.T) {
			// Arrange
			f := &fakeJob{failing: failing}
			var config string

			// Act
			outcome, err := RunOneJob(context.Background(), testDeps(f, &config), DefaultLimits, func(string) {})

			// Assert
			if outcome != Failed || err == nil {
				t.Errorf("RunOneJob = %q, %v; want failed", outcome, err)
			}
			if calls := f.callsMade(); slices.Contains(calls, "register") || slices.Contains(calls, "start") {
				t.Errorf("the job called %q; want no runner and no VM started", calls)
			}
		})
	}
}

// The pickup limit ends a VM whose runner never took a job at the first look
// past it, and the job limit one whose job runs on, at the first look past
// that.
func TestRunOneJobEndsAtItsLimits(t *testing.T) {
	for name, row := range map[string]struct {
		busyAt    int
		want      Outcome
		wantPolls int
	}{
		"the pickup": {want: NoJob, wantPolls: 61},
		"the job":    {busyAt: 2, want: TimedOut, wantPolls: 361},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := &fakeJob{busyAt: row.busyAt}
			var config string

			// Act
			outcome, err := RunOneJob(context.Background(), testDeps(f, &config), DefaultLimits, func(string) {})

			// Assert
			if outcome != row.want || err != nil || f.polls != row.wantPolls {
				t.Errorf("RunOneJob = %q, %v after %d looks; want %q at look %d", outcome, err, f.polls, row.want, row.wantPolls)
			}
		})
	}
}
