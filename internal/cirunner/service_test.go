package cirunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// A second run while one runs is refused at once, never queued, so the heavy
// line stays the only queue, and the run after the first ends is taken.
func TestServiceRefusesASecondRunWhileOneRuns(t *testing.T) {
	// Arrange
	f := &fakeJob{busyAt: 2, offAt: 5}
	var config string
	s := &Service{Deps: testDeps(f, &config), Limits: DefaultLimits}
	sleep := s.Deps.Sleep
	isSleeping, release := make(chan struct{}), make(chan struct{})
	s.Deps.Sleep = func(ctx context.Context, step time.Duration) {
		select {
		case isSleeping <- struct{}{}:
			<-release
		default:
		}
		sleep(ctx, step)
	}
	first := make(chan error, 1)
	go func() { _, err := s.Run(context.Background(), func(string) {}); first <- err }()
	<-isSleeping

	// Act
	outcome, err := s.Run(context.Background(), func(string) {})
	close(release)
	firstErr := <-first
	f.offAt = 0
	f.polls, f.busyAt = 0, 0
	nextOutcome, nextErr := s.Run(context.Background(), func(string) {})

	// Assert
	if !errors.Is(err, ErrBusy) || outcome != Failed {
		t.Errorf("a second Run = %q, %v; want it refused with ErrBusy", outcome, err)
	}
	if firstErr != nil || nextErr != nil || nextOutcome != NoJob {
		t.Errorf("the first run ended with %v, and the next = %q, %v; want the next run taken once the first ended", firstErr, nextOutcome, nextErr)
	}
}

// A service that starts with its VM running, as after a crash, turns it off,
// puts it back to clean and deletes every runner it registered, before
// anything else.
func TestServiceStartPutsTheVMBackFirst(t *testing.T) {
	// Arrange
	f := &fakeJob{isRunning: true}
	var config string
	s := &Service{Deps: testDeps(f, &config), Limits: DefaultLimits}

	// Act
	err := s.Start(context.Background())

	// Assert
	if calls := f.callsMade(); err != nil || !slices.Equal(calls, []string{"off", "restore", "delete own"}) {
		t.Errorf("Start = %v with calls %q; want off, restore, then the service's runners deleted", err, calls)
	}
	if f.isRunning {
		t.Error("the VM still runs after the service started")
	}
}

// A service whose VM cannot be put back takes no run: it does not start.
func TestServiceStartFailsOnAVMNotPutBack(t *testing.T) {
	// Arrange
	f := &fakeJob{isRunning: true, failing: "restore"}
	var config string
	s := &Service{Deps: testDeps(f, &config), Limits: DefaultLimits}

	// Act
	err := s.Start(context.Background())

	// Assert
	if err == nil || slices.Contains(f.callsMade(), "delete own") {
		t.Errorf("Start = %v with calls %q; want it failed before anything more", err, f.callsMade())
	}
}

// The service's status says whether a job runs, and how the last one ended.
func TestServiceStatusSaysWhatRuns(t *testing.T) {
	// Arrange
	f := &fakeJob{failing: "start"}
	var config string
	s := &Service{Deps: testDeps(f, &config), Limits: DefaultLimits}
	before := s.Status()
	s.started = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	during := s.Status()
	s.isWatching = true
	watching := s.Status()
	s.started, s.isWatching = time.Time{}, false

	// Act
	_, _ = s.Run(context.Background(), func(string) {})
	after := s.Status()

	// Assert
	for _, row := range []struct{ got, want string }{
		{before, "no job has run since the service started"},
		{during, "a job runs, started at 2026-10-14T12:00:00Z"},
		{watching, "no job runs, and the service is looking at the VM for one's leftovers"},
		{after, "no job runs, and the last job, started at 2026-10-14T12:00:00Z, failed, and start failed"},
	} {
		if row.got != row.want {
			t.Errorf("Status = %q; want %q", row.got, row.want)
		}
		if strings.ContainsAny(row.got, ";\u2014") {
			t.Errorf("Status = %q; want no semicolon or em dash", row.got)
		}
	}
}

// The installed service reads its folder's settings, calls GitHub's API for
// its one repository, and writes each job's DVD to its work folder.
func TestNewServiceRunsFromItsFolder(t *testing.T) {
	// Arrange
	dir, key := serviceFolder(t, goodSettings, "pkcs1")
	protectFolder(t, dir)

	// Act
	s, config, err := NewService(dir, func(string) {})

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	github, isGitHub := s.Deps.GitHub.(*GitHub)
	if !isGitHub || github.Base != "https://api.github.com" || github.Owner != "fpresta0607" || github.Repo != "PrecisionDocs-AI" || github.AppID != 1 || github.InstallationID != 2 || !github.Key.Equal(key) || github.Client.Timeout == 0 {
		t.Errorf("GitHub = %+v; want the settings' App and repository at GitHub's API, with a timeout", s.Deps.GitHub)
	}
	if _, isHyperV := s.Deps.VM.(HyperV); !isHyperV || s.Limits != DefaultLimits || config.Allowed != "S-1-5-21-1-2-3-1001" {
		t.Errorf("VM %T, limits %+v, allowed %q; want Hyper-V, the default limits and the settings' user", s.Deps.VM, s.Limits, config.Allowed)
	}
	iso, err := s.Deps.DVD("abc")
	next, nextErr := s.Deps.DVD("def")
	if image, readErr := os.ReadFile(iso); err != nil || readErr != nil || filepath.Dir(iso) != filepath.Join(dir, "work") || !strings.HasPrefix(filepath.Base(iso), "job-") || len(image) == 0 {
		t.Errorf("DVD = %q, %v, %v; want it written to the work folder", iso, err, readErr)
	}
	if nextErr != nil || next == iso {
		t.Errorf("the next DVD = %q, %v; want a file of its own beside %q", next, nextErr, iso)
	}
}

// A run asked for when no job of the runner's waits, or when GitHub cannot
// say, starts nothing: no VM, and no runner registered. Only a waiting job
// starts the VM, whoever asks.
func TestRunStartsNothingWhenNoJobWaits(t *testing.T) {
	for name, row := range map[string]struct {
		queued    *Pending
		queuedErr error
		want      Outcome
		wantErr   string
	}{
		"no job waits":          {queued: &Pending{}, want: NoJob},
		"GitHub cannot be read": {queuedErr: errors.New("GitHub answered 502"), want: Failed, wantErr: "find a waiting job: GitHub answered 502"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := &fakeJob{queued: row.queued, queuedErr: row.queuedErr}
			var config string
			s := &Service{Deps: testDeps(f, &config), Limits: DefaultLimits}

			// Act
			outcome, err := s.Run(context.Background(), func(string) {})

			// Assert
			if outcome != row.want || row.wantErr == "" && err != nil || row.wantErr != "" && (err == nil || !strings.Contains(err.Error(), row.wantErr)) {
				t.Errorf("Run = %q, %v; want %q with error %q", outcome, err, row.want, row.wantErr)
			}
			if calls := f.callsMade(); len(calls) != 0 || f.asks != 1 {
				t.Errorf("the run made %q after %d ask(s); want one ask of GitHub and nothing else", calls, f.asks)
			}
		})
	}
}

// A public repository is refused at the service's start and before each
// run, since a self-hosted runner must never serve one, and the refusal
// starts nothing: no VM, and no runner registered.
func TestServiceRefusesAPublicRepository(t *testing.T) {
	for name, act := range map[string]func(s *Service) error{
		"starting": func(s *Service) error { return s.Start(context.Background()) },
		"running": func(s *Service) error {
			outcome, err := s.Run(context.Background(), func(string) {})
			if outcome != Failed {
				return fmt.Errorf("the run ended %q", outcome)
			}
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := &fakeJob{isPublic: true}
			var config string
			s := &Service{Deps: testDeps(f, &config), Limits: DefaultLimits}

			// Act
			err := act(s)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "the repository is public") {
				t.Errorf("%s = %v; want the public repository refused", name, err)
			}
			if calls := f.callsMade(); len(calls) != 0 || f.asks != 0 {
				t.Errorf("%s made %q and %d ask(s) for a job; want nothing", name, calls, f.asks)
			}
		})
	}
}

// Many asks at once for the waiting job make one ask of GitHub, and an
// answer is given again until it is pendingFresh old, so a client asking
// without end cannot spend the App's rate limit.
func TestPendingAsksGitHubOnceForManyAsks(t *testing.T) {
	// Arrange: GitHub answers slowly enough that the asks overlap.
	f := &fakeJob{asking: 50 * time.Millisecond}
	var config string
	s := &Service{Deps: testDeps(f, &config), Limits: DefaultLimits}
	var asked sync.WaitGroup

	// Act
	for range 50 {
		asked.Add(1)
		go func() {
			defer asked.Done()
			if pending, err := s.Pending(context.Background()); err != nil || !pending.IsWaiting {
				t.Errorf("Pending = %+v, %v; want the waiting job", pending, err)
			}
		}()
	}
	asked.Wait()
	once := f.asks
	s.Deps.Sleep(context.Background(), pendingFresh)
	_, _ = s.Pending(context.Background())

	// Assert
	if once != 1 || f.asks != 2 {
		t.Errorf("GitHub was asked %d time(s) for 50 asks, and %d in all once the answer was %v old; want 1 and 2", once, f.asks, pendingFresh)
	}
}

// The watch puts back a VM it finds running with no job, as a cleanup that
// failed every try leaves it, and leaves alone the VM of a job that runs.
func TestWatchPutsBackAVMRunningWithNoJob(t *testing.T) {
	for name, row := range map[string]struct {
		isJobRunning bool
		want         []string
	}{
		"no job runs": {want: []string{"off", "restore"}},
		"a job runs":  {isJobRunning: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := &fakeJob{isRunning: true}
			var config string
			s := &Service{Deps: testDeps(f, &config), Limits: DefaultLimits, Log: func(string) {}}
			if row.isJobRunning {
				s.started = time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
			}
			ctx, cancel := context.WithCancel(context.Background())
			sleeps := 0
			sleep := s.Deps.Sleep
			s.Deps.Sleep = func(ctx context.Context, step time.Duration) {
				if sleeps++; sleeps > 1 {
					cancel()
				}
				sleep(ctx, step)
			}

			// Act
			s.Watch(ctx, WatchEvery)

			// Assert
			if calls := f.callsMade(); !slices.Equal(calls, row.want) {
				t.Errorf("the watch made %q; want %q", calls, row.want)
			}
			if s.isWatching || row.isJobRunning == s.started.IsZero() {
				t.Errorf("after the watch, watching = %v and started = %v; want the service as it was", s.isWatching, s.started)
			}
		})
	}
}

// Sleep ends with its context.
func TestSleepEndsWithItsContext(t *testing.T) {
	// Arrange
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := time.Now()

	// Act
	Sleep(ctx, time.Hour)

	// Assert
	if waited := time.Since(started); waited > time.Second {
		t.Errorf("Sleep waited %v on an ended context; want it to end at once", waited)
	}
}
