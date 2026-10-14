package cirunner

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
)

// ErrBusy is a run asked for while a job runs. The service never queues a
// run, so the machine's heavy line stays the only queue.
var ErrBusy = errors.New("cirunner: a job already runs")

// Service runs one job at a time, as the fleet asks for each, and says each
// run and each refusal to Log when it answers on the pipe.
type Service struct {
	Deps   Deps
	Limits Limits
	Log    func(line string)

	mu      sync.Mutex
	started time.Time
	// isWatching says the VM is being looked at for a job's leftovers, which
	// holds the VM as a job does.
	isWatching bool
	last       string
	// refused tallies the requests refused, and idle the runs asked for that
	// started nothing, each logged at most once a minute.
	refused tally
	idle    tally

	// asking is held while GitHub is asked for a waiting job, so many asks at
	// once make one, and pending and pendingErr are its answer as of
	// pendingAt.
	asking     sync.Mutex
	pending    Pending
	pendingErr error
	pendingAt  time.Time
}

// tally counts what the log was not told of since at, when it last was, so
// no client's requests, however many, can push a job's lines out of it.
type tally struct {
	count int
	at    time.Time
}

const (
	// pendingFresh is how long an answer about a waiting job is given again
	// rather than asked for: the fleet looks every 30 seconds, and a client
	// asking without end must not spend the App's GitHub rate limit.
	pendingFresh = 15 * time.Second
	// pendingLimit bounds the GitHub calls that find a waiting job.
	pendingLimit = 30 * time.Second
	// WatchEvery is how often the service looks for a VM left running with
	// no job (Service.Watch).
	WatchEvery = 5 * time.Minute
)

// NewService is the service as installed in dir: its settings and the App's
// key read from there, GitHub's API, the VM through Hyper-V, each job's DVD
// written to dir's work folder, and log for what it says. It refuses a
// folder anyone but SYSTEM, Administrators and the service could have made
// or changed (checkFolder), and makes the work folder afresh, so nothing
// another user left there carries a job's runner configuration.
func NewService(dir string, log func(string)) (*Service, Config, error) {
	if err := checkFolder(dir); err != nil {
		return nil, Config{}, err
	}
	config, key, err := ReadConfig(dir)
	if err != nil {
		return nil, Config{}, err
	}
	work := filepath.Join(dir, "work")
	if err := fsx.RemoveAll(work); err != nil {
		return nil, Config{}, err
	}
	if err := os.MkdirAll(work, 0o700); err != nil {
		return nil, Config{}, err
	}
	github := &GitHub{
		Client:         &http.Client{Timeout: 30 * time.Second},
		Base:           "https://api.github.com",
		AppID:          config.AppID,
		InstallationID: config.InstallationID,
		Key:            key,
		Owner:          config.Owner,
		Repo:           config.Repo,
		Now:            time.Now,
	}
	deps := Deps{
		GitHub: github,
		VM:     HyperV{Commands: execx.OSRunner{}},
		DVD: func(runnerConfig string) (string, error) {
			iso := filepath.Join(work, "job-"+rand.Text()+".iso")
			return iso, BuildDVD(iso, runnerConfig)
		},
		Now:   time.Now,
		Sleep: sleep,
	}
	return &Service{Deps: deps, Limits: DefaultLimits, Log: log}, config, nil
}

// sleep waits for d, or until ctx ends.
func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

// Start puts the VM back to clean and deletes every runner the service
// registered, as a job's end would have, before the service takes any run:
// a service that stopped mid-job, as in a crash, left both behind.
func (s *Service) Start(ctx context.Context) error {
	if err := s.refusePublic(ctx); err != nil {
		return err
	}
	if err := putBack(ctx, s.Deps.VM); err != nil {
		return err
	}
	return s.Deps.GitHub.DeleteOwnRunners(ctx)
}

// Pending is the desktop job that has waited longest, when one waits and
// the repository is still private. An answer, or GitHub's failure to give
// one, is given again for pendingFresh, and asks made while GitHub is being
// asked wait for that answer, so asks and runs however many and fast make
// one set of GitHub calls at a time. The ask is the service's, within
// pendingLimit, never its client's, so a client that leaves mid-ask still
// has the answer kept, and leaving cannot make the next ask go to GitHub.
func (s *Service) Pending(ctx context.Context) (Pending, error) {
	if err := lockBefore(ctx, &s.asking); err != nil {
		return Pending{}, err
	}
	defer s.asking.Unlock()
	if !s.pendingAt.IsZero() && s.Deps.Now().Sub(s.pendingAt) < pendingFresh {
		return s.pending, s.pendingErr
	}
	asking, cancel := context.WithTimeout(context.WithoutCancel(ctx), pendingLimit)
	defer cancel()
	pending, err := Pending{}, s.refusePublic(asking)
	if err == nil {
		if pending, err = s.Deps.GitHub.QueuedJob(asking); err != nil {
			err = fmt.Errorf("cirunner: find a waiting job: %w", err)
		}
	}
	s.pending, s.pendingErr, s.pendingAt = pending, err, s.Deps.Now()
	return pending, err
}

// refusePublic refuses a repository that is not private, at the service's
// start and, through Pending, before each run, since a repository can be
// made public at any time and a self-hosted runner must never serve one.
func (s *Service) refusePublic(ctx context.Context) error {
	isPrivate, err := s.Deps.GitHub.IsPrivate(ctx)
	if err != nil {
		return fmt.Errorf("cirunner: ask GitHub whether the repository is private: %w", err)
	}
	if !isPrivate {
		return errors.New("cirunner: the repository is public, and a self-hosted runner must never serve a public repository")
	}
	return nil
}

// lockBefore locks mu, or gives up when ctx ends first.
func lockBefore(ctx context.Context, mu *sync.Mutex) error {
	for !mu.TryLock() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil
}

// Run runs one job, or refuses with ErrBusy while one runs. It first asks,
// as Pending does, whether the repository is still private and whether a
// job of the runner's waits, and starts nothing, neither the VM nor a
// runner, unless both are so: a client of the pipe can ask for a run
// whenever it likes, but only a waiting job of a private repository starts
// the VM.
func (s *Service) Run(ctx context.Context, progress func(string)) (Outcome, error) {
	s.mu.Lock()
	if !s.started.IsZero() {
		s.mu.Unlock()
		return Failed, ErrBusy
	}
	started := s.Deps.Now()
	s.started = started
	s.mu.Unlock()
	var outcome Outcome
	pending, err := s.Pending(ctx)
	switch {
	case err != nil:
		outcome, err = ended(ctx, err)
	case !pending.IsWaiting:
		outcome = NoJob
	default:
		progress(fmt.Sprintf("a job waits, %s of run %d, so the VM starts", pending.Job, pending.RunID))
		outcome, err = RunOneJob(ctx, s.Deps, s.Limits, progress)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.started = time.Time{}
	s.last = fmt.Sprintf("the last job, started at %s, %s", started.UTC().Format(time.RFC3339), describe(outcome, err))
	return outcome, err
}

// Watch looks at the VM every period while no job runs, until ctx ends, and
// puts it back to clean when it finds it running: a job's cleanup that
// failed every try leaves the VM running the job's code, which nothing else
// would end before the next job or the service's next start.
func (s *Service) Watch(ctx context.Context, every time.Duration) {
	for {
		s.Deps.Sleep(ctx, every)
		if ctx.Err() != nil {
			return
		}
		s.mu.Lock()
		if !s.started.IsZero() {
			s.mu.Unlock()
			continue
		}
		s.started, s.isWatching = s.Deps.Now(), true
		s.mu.Unlock()
		if isRunning, err := s.Deps.VM.IsRunning(ctx); err == nil && isRunning {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupLimit)
			if err := putBack(cleanup, s.Deps.VM); err != nil {
				s.Log("the VM ran with no job, and could not be put back: " + err.Error())
			} else {
				s.Log("the VM ran with no job, and was put back to clean")
			}
			cancel()
		}
		s.mu.Lock()
		s.started, s.isWatching = time.Time{}, false
		s.mu.Unlock()
	}
}

// refuse logs a refused request, why being the service's own words and
// never the request's, at most once a minute with how many were refused
// since, so a client refused without end cannot push a job's lines out of
// the log.
func (s *Service) refuse(why string) {
	s.note(&s.refused, func(count int) string { return fmt.Sprintf("refused %d request(s), the last for %s", count, why) })
}

// note counts one more on t, and logs line of the count unless the last
// line of t was logged under a minute ago.
func (s *Service) note(t *tally, line func(count int) string) {
	s.mu.Lock()
	t.count++
	now := s.Deps.Now()
	if !t.at.IsZero() && now.Sub(t.at) < time.Minute {
		s.mu.Unlock()
		return
	}
	count := t.count
	t.count, t.at = 0, now
	s.mu.Unlock()
	s.Log(line(count))
}

// Status says whether a job runs, and how the last one ended.
func (s *Service) Status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.isWatching:
		return "no job runs, and the service is looking at the VM for one's leftovers"
	case !s.started.IsZero():
		return "a job runs, started at " + s.started.UTC().Format(time.RFC3339)
	case s.last != "":
		return "no job runs, and " + s.last
	default:
		return "no job has run since the service started"
	}
}

func describe(outcome Outcome, err error) string {
	said := map[Outcome]string{
		Ran:       "ran to its end",
		NoJob:     "found no job to run",
		TimedOut:  "ran past its limit",
		Abandoned: "ended with its turn",
		Failed:    "failed",
	}[outcome]
	if err != nil {
		said += ", and " + err.Error()
	}
	return said
}
