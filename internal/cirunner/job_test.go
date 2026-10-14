package cirunner

import (
	"context"
	"errors"
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
	return nil
}

func (f *fakeJob) TurnOff(ctx context.Context) error {
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

func (f *fakeJob) QueuedJob(context.Context) (Pending, error) {
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
			if !slices.Equal(calls[len(calls)-3:], []string{"off", "restore", "delete"}) {
				t.Errorf("the job's last calls were %q; want the VM off, back at clean, and the runner deleted", calls)
			}
			if !slices.Equal(f.deleted, []int64{41}) {
				t.Errorf("deleted runners %v; want the job's own runner, 41", f.deleted)
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
