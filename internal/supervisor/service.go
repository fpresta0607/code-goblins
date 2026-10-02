package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fpresta0607/code-goblins/internal/axi"
	"github.com/fpresta0607/code-goblins/internal/connections"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/nativehook"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervise"
	"github.com/fpresta0607/code-goblins/internal/wake"
	"github.com/fpresta0607/code-goblins/internal/watch"
)

type ProgressReader interface {
	Progress(context.Context, string, string) (pipeline.Progress, error)
	CanSteer(context.Context, string, string, string) error
}

type Options struct {
	Example        bool
	CFO            *CFOConnection
	Gate           ProgressReader
	Reconcile      func(context.Context) error
	VerifyDelivery func(context.Context, state.TaskMeta, string, string, string) (string, error)
	// MergedPRs lists every pull request merged since a time.
	MergedPRs func(ctx context.Context, since time.Time) ([]MergedPR, error)
	// PullRequestState asks the forge whether a pull request is OPEN, CLOSED
	// or MERGED; without it a finished task whose merge no fleet history
	// shows reads Finished.
	PullRequestState func(ctx context.Context, url string) (PullRequestInfo, error)
	// Runs opens run items' windows; without it no item can run.
	Runs RunLauncher
	// PollPage waits up to a timeout for the Overlord's feedback on a Lavish
	// page; without it no page is polled.
	PollPage func(ctx context.Context, file string, timeout time.Duration) (axi.PagePoll, error)
	// FirstRun is what the first-run page reads and changes on this
	// machine; without it the board can start no CFO.
	FirstRun *FirstRun
	// Dispatch is what a queued task's Start reads and runs; without it the
	// board starts no goblin.
	Dispatch *Dispatch
}

type Service struct {
	Store                *Store
	Options              Options
	Git                  Git
	Instance             string
	Started              time.Time
	mu                   sync.Mutex
	lastError            string
	reconciled           time.Time
	presentationChecked  time.Time
	presentationIdentity string
	// registration is what the last recovery cycle found wrong with the
	// registration named registrationIdentity. It stands for that
	// registration alone: Snapshot shows it only while its own read finds the
	// same one.
	registration         string
	registrationIdentity string
	connectionChecks     *connections.Cache
	connectionInspector  *connections.Inspector
	history              []Task
	revision             uint64
	subscribers          map[chan struct{}]struct{}
	// pullRequests is what GitHub last said about each finished task's pull
	// request the history shows; only keepHistory touches it.
	pullRequests map[string]pullRequestState
	// historyErr is what the last history refresh met; the loop reports it
	// with its next recovery cycle.
	historyErr error
	// runRequests takes one run request at a time, so two with one ID never
	// both write a script.
	runRequests sync.Mutex
	// ordering saves one list order at a time.
	ordering sync.Mutex
	// starts guards starting, the task a Start is running cfo spawn for, and
	// startErrors, why each task's last Start failed.
	starts       sync.Mutex
	starting     string
	startErrors  map[string]string
	changing     map[string]string
	changeErrors map[string]taskChangeError
	// pages stops each open item's page poller; pageWork waits for them.
	pagesMu  sync.Mutex
	pages    map[string]context.CancelFunc
	pageWork sync.WaitGroup
	done     chan struct{}
	work     chan struct{}
	cancel   context.CancelFunc
}

// Start acquires the same singleton as legacy watch BEFORE opening recovery
// state. The browser, hook writers, and competing serve invocations cannot
// create a second controller or replay actions outside this lease.
func Start(ctx context.Context, h home.Home, options Options) (*Service, error) {
	if err := os.MkdirAll(h.State, 0700); err != nil {
		return nil, err
	}
	if err := AcquireWatchLock(h.State); err != nil {
		return nil, fmt.Errorf("supervisor: existing watch owner must finish before serve: %w", err)
	}
	clearStopRequest(h.State)
	store, err := Open(h)
	if err != nil {
		_ = lock.ReleaseExclusiveNamed(h.State, ".watch.lock")
		return nil, err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		_ = lock.ReleaseExclusiveNamed(h.State, ".watch.lock")
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	s := &Service{Store: store, Options: options, Instance: hex.EncodeToString(id[:]), Started: time.Now().UTC(), subscribers: map[chan struct{}]struct{}{}, done: make(chan struct{}), work: make(chan struct{}, 1), cancel: cancel}
	go s.run(ctx)
	return s, nil
}

func (s *Service) Close() {
	s.cancel()
	<-s.done
	s.mu.Lock()
	checks := s.connectionChecks
	s.mu.Unlock()
	if checks != nil {
		checks.Close()
	}
}

func (s *Service) Done() <-chan struct{} { return s.done }

// storageGrace is how long a failing store stays off the board. A failed
// save loses nothing: what it would have saved stays where it came from (an
// inbox file, an action not yet acknowledged), and the next cycle, at most
// two seconds away, saves it again. Only a store that keeps failing is the
// Overlord's business.
const storageGrace = 30 * time.Second

func (s *Service) publish(err error) {
	if errors.Is(err, ErrStorage) {
		if since := s.Store.failingSince.Load(); since == 0 || time.Since(time.Unix(0, since)) < storageGrace {
			err = withoutStorage(err)
		}
	}
	s.mu.Lock()
	if err != nil {
		s.lastError = bounded(err.Error(), 1000)
	} else {
		s.lastError = ""
	}
	s.mu.Unlock()
	s.notify()
}

// withoutStorage is err with every storage failure taken out of it, so the
// other errors a cycle met still reach the board; nil when only storage
// failed.
func withoutStorage(err error) error {
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var kept []error
		for _, part := range joined.Unwrap() {
			if part = withoutStorage(part); part != nil {
				kept = append(kept, part)
			}
		}
		return errors.Join(kept...)
	}
	if errors.Is(err, ErrStorage) {
		return nil
	}
	return err
}

// notify sends every board a fresh snapshot, keeping the last error.
func (s *Service) notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.revision++
	for ch := range s.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *Service) subscribe() (chan struct{}, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan struct{}, 1)
	s.subscribers[ch] = struct{}{}
	return ch, func() { s.mu.Lock(); delete(s.subscribers, ch); s.mu.Unlock() }
}

func (s *Service) run(ctx context.Context) {
	defer close(s.done)
	defer lock.ReleaseExclusiveNamed(s.Store.Home.State, ".watch.lock")
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.work:
				s.process(ctx)
			}
		}
	}()
	defer func() { s.cancel(); <-workerDone }()
	pipeDone := make(chan struct{})
	go func() {
		defer close(pipeDone)
		s.serveRunRequests(ctx)
	}()
	defer func() { s.cancel(); <-pipeDone }()
	defer func() { s.cancel(); s.pageWork.Wait() }()
	historyDone := make(chan struct{})
	go func() {
		defer close(historyDone)
		s.keepHistory(ctx, historyRefresh, historyWatch)
	}()
	defer func() { s.cancel(); <-historyDone }()
	// A single inbox watcher, independent of task count. A timeout also
	// recovers notifications lost during atomic renames or an AV filter fault.
	notified := make(chan struct{}, 1)
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		waiter, err := watch.NewNativeWaiter(nativehook.SpoolDir(s.Store.Home.State))
		if err == nil {
			defer waiter.Close()
		}
		for ctx.Err() == nil {
			before := time.Now()
			if waiter != nil {
				waiter.Wait(2 * time.Second)
			} else {
				time.Sleep(2 * time.Second)
			}
			if elapsed := time.Since(before); elapsed < 25*time.Millisecond {
				time.Sleep(25*time.Millisecond - elapsed)
			}
			select {
			case notified <- struct{}{}:
			default:
			}
		}
	}()
	defer func() { s.cancel(); <-waitDone }()
	reconcile := time.NewTicker(time.Minute)
	defer reconcile.Stop()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	s.cycle(ctx, true)
	for {
		select {
		case <-ctx.Done():
			return
		case <-reconcile.C:
			s.cycle(ctx, true)
		case <-notified:
			// The notification loop wakes at least every two seconds, so a
			// stop request is honoured within that.
			if stopRequested(s.Store.Home.State) {
				return
			}
			s.cycle(ctx, false)
		case <-s.Store.changed:
			s.cycle(ctx, false)
		case <-heartbeat.C:
			if !lock.HeldByNamed(s.Store.Home.State, ".watch.lock", os.Getpid()) {
				s.publish(errors.New("supervisor lost singleton ownership"))
				return
			}
			if err := monitor.TouchHeartbeat(s.Store.Home.State, time.Now()); err != nil {
				s.publish(err)
			}
		}
	}
}

func (s *Service) cycle(ctx context.Context, recover bool) {
	before := s.Store.Snapshot().Revision
	if err := s.Store.Ingest(); err != nil {
		s.publish(err)
		return
	}
	// Question failures cannot stop native events or independent progression.
	reconcileErr := s.Store.ingestQuestions()
	reconcileErr = errors.Join(reconcileErr, s.Store.ingestAnswers())
	reconcileErr = errors.Join(reconcileErr, s.Store.ingestActivity())
	reconcileErr = errors.Join(reconcileErr, s.Store.ingestReviews())
	reconcileErr = errors.Join(reconcileErr, s.Store.expireRuns(time.Now()))
	reconcileErr = errors.Join(reconcileErr, s.finishRuns(ctx))
	reconcileErr = errors.Join(reconcileErr, s.Store.retireItems())
	reconcileErr = errors.Join(reconcileErr, s.Store.supersedeQuestions())
	reconcileErr = errors.Join(reconcileErr, s.Store.settleDeliveries(time.Now().UTC(), s.lookAtTerminal))
	s.reconcilePresentations(ctx)
	s.watchPages(ctx)
	if recover {
		if s.Options.Reconcile != nil {
			reconcileErr = errors.Join(reconcileErr, s.Options.Reconcile(ctx))
		}
		s.checkRegistration(ctx)
		s.mu.Lock()
		reconcileErr = errors.Join(reconcileErr, s.historyErr)
		s.mu.Unlock()
		reconcileErr = errors.Join(reconcileErr, s.Store.pruneReviews(time.Now()))
		reconcileErr = errors.Join(reconcileErr, s.Store.pruneRuns(time.Now()))
		s.mu.Lock()
		s.reconciled = time.Now().UTC()
		s.mu.Unlock()
		reconcileErr = errors.Join(reconcileErr, s.reconcileTasks(time.Now()))
	}
	select {
	case s.work <- struct{}{}:
	default:
	}
	if recover || before != s.Store.Snapshot().Revision {
		s.publish(reconcileErr)
	}
}

// keepHistory rebuilds the Completed column away from the loop: at start,
// whenever historyMark changes (a task finished, or its gate saw it merge),
// which it checks every watch, and otherwise every interval. Its merge scan
// reads fleet repositories with git, and on the loop it took most of each
// minute while native events, the heartbeat and every snapshot waited. Each
// rebuild reaches the board at once.
func (s *Service) keepHistory(ctx context.Context, every, watch time.Duration) {
	ticker := time.NewTicker(watch)
	defer ticker.Stop()
	var mark string
	var rebuilt time.Time
	for {
		if next := s.historyMark(); next != mark || time.Since(rebuilt) >= every {
			mark, rebuilt = next, time.Now()
			err := s.refreshHistory(ctx, time.Now().UTC())
			s.mu.Lock()
			s.historyErr = err
			s.mu.Unlock()
			s.notify()
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// historyMark names what changes when Completed gains a card: the live task
// records, which cleanup removes as a task finishes, and the tasks whose gate
// saw their pull request merge.
func (s *Service) historyMark() string {
	var mark strings.Builder
	if entries, err := os.ReadDir(s.Store.Home.State); err == nil {
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".meta") {
				mark.WriteString(entry.Name() + ";")
			}
		}
	}
	for _, directory := range []string{"outcomes", "lifecycle"} {
		if entries, err := os.ReadDir(filepath.Join(s.Store.Home.State, directory)); err == nil {
			for _, entry := range entries {
				if info, err := entry.Info(); err == nil && !info.IsDir() {
					fmt.Fprintf(&mark, "%s/%s@%d;", directory, entry.Name(), info.ModTime().UnixNano())
				}
			}
		}
	}
	var merged []string
	for id, evaluation := range s.Store.Snapshot().Tasks {
		if evaluation.Phase == "merged" || evaluation.Phase == "done" {
			merged = append(merged, id+"@"+evaluation.Generation)
		}
	}
	slices.Sort(merged)
	mark.WriteString(strings.Join(merged, ";"))
	return mark.String()
}

// refreshHistory rebuilds the Completed column: finished tasks, the pull
// requests merged into fleet repositories, and what GitHub says of the
// finished tasks' other pull requests.
func (s *Service) refreshHistory(ctx context.Context, now time.Time) error {
	history := finishedTasks(s.Store.Home, now)
	for id, evaluation := range s.Store.Snapshot().Tasks {
		if evaluation.Phase != "done" && evaluation.Phase != "merged" {
			continue
		}
		meta, err := state.ReadTaskMeta(s.Store.Home.State, id)
		if err != nil || evaluation.Generation != meta.SpawnGen {
			continue
		}
		if evaluation.PR == "" {
			lines, _ := state.TailStatus(s.Store.Home.State, id, 200)
			_, evaluation.PR = statusActivity(lines, spawnTime(meta.SpawnGen))
		}
		if evaluation.PR != "" {
			history = append(history, Task{ID: id, Title: meta.Title, Project: filepath.Base(meta.Project), Evaluation: evaluation})
		}
	}
	var err error
	if s.Options.MergedPRs != nil {
		var merged []MergedPR
		merged, err = s.Options.MergedPRs(ctx, now.Add(-historyWindow))
		history = withMergedPRs(history, merged)
	}
	err = errors.Join(err, s.withPullRequestStates(ctx, history, now))
	s.mu.Lock()
	s.history = history
	s.mu.Unlock()
	return err
}

// checkRegistration runs on the once-a-minute recovery cycle and asks the
// terminal backend what Snapshot's own read of the registration cannot, so a
// CFO that moved shows as one state on the board before anyone tries to
// deliver to it.
func (s *Service) checkRegistration(ctx context.Context) {
	if s.Options.CFO == nil {
		return
	}
	check, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	problem := ""
	identity, err := s.Options.CFO.examine(check)
	if err != nil {
		problem = err.Error()
	}
	s.mu.Lock()
	s.registration, s.registrationIdentity = problem, identity
	s.mu.Unlock()
}

func (s *Service) reconcileTasks(now time.Time) error {
	d := s.Store.Snapshot()
	// Gate evidence outlives native hooks and retained session nodes. Only a
	// fresh monitor observation can suppress evaluation for a working agent.
	for task, id := range d.TaskSessions {
		node := d.Sessions[id]
		meta, err := state.ReadTaskMeta(s.Store.Home.State, task)
		if err != nil {
			continue // Retired task metadata is not reconstructed from old events.
		}
		prior := d.Tasks[task]
		if record, err := state.ReadLifecycle(s.Store.Home.State, task); err == nil && record.Generation == meta.SpawnGen && record.SuppressesMonitoring(s.Store.Home.State) {
			continue
		}
		if prior.Generation == meta.SpawnGen && prior.Phase == "done" && !node.UpdatedAt.After(prior.At) {
			continue
		}
		if node.Generation != "" && node.Generation != meta.SpawnGen || node.ID == "" && prior.Generation != meta.SpawnGen {
			continue
		}
		if (node.Phase == "active" || node.Phase == "started") && s.runtimeEvidence(meta, node, now).working() {
			continue
		}
		if evaluationPending(d.Actions, task) {
			continue
		}
		if err := s.Store.queueUnlessEvaluating(Action{ID: fmt.Sprintf("reconcile-%s-%d", task, now.Unix()/60), Kind: "evaluate", TaskID: task, Session: id, EventID: node.LastEventID, Generation: meta.SpawnGen}); err != nil {
			return err
		}
	}
	// A task no native hook has reported is evaluated from its worktree and
	// gate alone, so its status and pull request come from the fleet instead
	// of waiting on hooks that may never be installed.
	entries, err := os.ReadDir(s.Store.Home.State)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		task, ok := strings.CutSuffix(entry.Name(), ".meta")
		if !ok || entry.IsDir() || d.TaskSessions[task] != "" {
			continue
		}
		meta, err := state.ReadTaskMeta(s.Store.Home.State, task)
		if err != nil {
			continue
		}
		if prior := d.Tasks[task]; prior.Generation == meta.SpawnGen && prior.Phase == "done" || evaluationPending(d.Actions, task) {
			continue
		}
		if record, err := state.ReadLifecycle(s.Store.Home.State, task); err == nil && record.Generation == meta.SpawnGen && record.SuppressesMonitoring(s.Store.Home.State) {
			continue
		}
		if err := s.Store.queueUnlessEvaluating(Action{ID: fmt.Sprintf("reconcile-%s-%d", task, now.Unix()/60), Kind: "evaluate", TaskID: task, Generation: meta.SpawnGen}); err != nil {
			return err
		}
	}
	return nil
}

func evaluationPending(actions []Action, task string) bool {
	return slices.ContainsFunc(actions, func(a Action) bool {
		return a.TaskID == task && a.Kind == "evaluate" && (a.Status == "queued" || a.Status == "running")
	})
}

// actionTimeout bounds one action's run, so an evaluation or delivery that
// never returns cannot hold the queue.
const actionTimeout = 45 * time.Second

func (s *Service) process(ctx context.Context) {
	for i := 0; i < maxActions && ctx.Err() == nil; i++ {
		d := s.Store.Snapshot()
		pending := false
		for _, a := range d.Actions {
			if a.Status == "queued" {
				pending = true
				break
			}
		}
		if !pending {
			break
		}
		boundedCtx, cancel := context.WithTimeout(ctx, actionTimeout)
		err := s.Store.ProcessOne(boundedCtx, s.execute)
		cancel()
		if err != nil {
			s.publish(err)
			return
		}
		s.publish(nil)
	}
}

func (s *Service) execute(ctx context.Context, a Action) (Evaluation, error) {
	if a.Kind == "feedback" || a.Kind == "cfo_message" {
		return Evaluation{}, fmt.Errorf("%w: obsolete action kind %q is not accepted", ErrRejected, a.Kind)
	}
	if a.Kind != "evaluate" && a.Kind != "review" && a.Kind != "cfo_answer" && a.Kind != "goblin_answer" && a.Kind != "review_answer" && a.Kind != "review_clear" && a.Kind != "question_clear" && a.Kind != "run" {
		return Evaluation{}, fmt.Errorf("%w: unsupported action kind %q", ErrRejected, a.Kind)
	}
	if a.Kind == "run" {
		return s.startRun(ctx, a)
	}
	if a.Kind == "review_answer" {
		return s.answerReview(ctx, a)
	}
	if a.Kind == "review_clear" {
		return s.Store.clearReview(a.ReviewID, a.Generation, a.Text)
	}
	if a.Kind == "question_clear" {
		return s.Store.clearQuestion(a.QuestionID, a.Generation)
	}
	if a.Kind == "goblin_answer" {
		return s.answerGoblin(ctx, a)
	}
	if a.Kind == "cfo_answer" {
		if s.Options.CFO == nil {
			return Evaluation{}, fmt.Errorf("%w: CFO message transport is unavailable", ErrRejected)
		}
		text := a.Text
		found := false
		for _, q := range s.Store.Snapshot().Questions {
			if q.ID == a.QuestionID && q.Identity == a.Generation && q.AnswerID == a.ID {
				text = fmt.Sprintf("User answer to CFO question %s. Question: %s Answer: %s", q.ID, q.Text, a.Text)
				if a.AnswerKind == "other" {
					text = fmt.Sprintf("User answer to CFO question %s. Question: %s Answer (Other): %s", q.ID, q.Text, a.Text)
				}
				found = true
			}
		}
		if !found {
			return Evaluation{}, fmt.Errorf("%w: user question context changed", ErrRejected)
		}
		result, err := s.Options.CFO.Send(ctx, a.Generation, text)
		if errors.Is(err, fleet.ErrQueuedBehindTurn) {
			return Evaluation{Reason: "Submitted to the registered CFO while it was working; it takes the answer when its current turn ends."}, nil
		}
		return result, err
	}
	meta, err := state.ReadTaskMeta(s.Store.Home.State, a.TaskID)
	if err != nil {
		return Evaluation{}, err
	}
	if meta.SpawnGen != a.Generation {
		return Evaluation{}, fmt.Errorf("%w: task restarted or was replaced; refresh the board", ErrRejected)
	}
	if record, err := state.ReadLifecycle(s.Store.Home.State, meta.ID); err == nil && record.Generation == meta.SpawnGen && record.SuppressesMonitoring(s.Store.Home.State) {
		if a.Kind != "evaluate" {
			return Evaluation{}, fmt.Errorf("task is %s", record.Phase)
		}
		return Evaluation{Phase: record.Phase, Reason: record.Reason, Generation: meta.SpawnGen, At: record.Updated}, nil
	}
	if a.Kind == "review" {
		return s.deliverReview(ctx, meta, a)
	}
	head, err := s.Git.Head(ctx, meta.Worktree)
	if err != nil {
		return Evaluation{}, err
	}
	clean, err := s.Git.Clean(ctx, meta.Worktree)
	if err != nil {
		return Evaluation{}, err
	}
	prior := s.Store.Snapshot().Tasks[a.TaskID]
	base := prior.Base
	if prior.Generation != meta.SpawnGen || s.Git.validateRevision(ctx, meta.Worktree, base) != nil {
		base = ""
	}
	if base == "" {
		base, _ = s.Git.base(ctx, meta.Worktree)
	}
	result := Evaluation{Phase: "review", Reason: "Task awaits independent review and delivery evidence", Head: head, Base: base}
	if !clean {
		result.Phase = "working"
		result.Reason = "Worktree has uncommitted changes"
		return result, nil
	}
	if meta.Mode != "no-mistakes" {
		result.Reason = "Manual task mode requires its existing review and delivery evidence"
		return result, nil
	}
	branch, err := s.Git.Branch(ctx, meta.Worktree)
	if err != nil {
		return result, err
	}
	if s.Options.Gate == nil {
		return result, errors.New("no-mistakes evidence reader is unavailable")
	}
	p, err := s.Options.Gate.Progress(ctx, meta.Project, branch)
	if err != nil {
		result.Reason = err.Error()
		return result, nil
	}
	result.PR = p.PR
	if i := slices.IndexFunc(p.Steps, func(step pipeline.ProgressStep) bool {
		return step.Status != "completed" && step.Status != "skipped" && step.Status != "pending"
	}); i >= 0 {
		result.GateStep = p.Steps[i].Name
	}
	for _, step := range p.Steps {
		if step.Status == "awaiting_approval" || step.Status == "fix_review" || step.Status == "failed" {
			result.Phase = "blocked"
			result.Reason = "Pipeline decision required at " + step.Name + "; use cfo pipeline respond"
			return result, nil
		}
	}
	if p.Ready(head) {
		if strings.EqualFold(p.PRState, "merged") {
			result.Phase = "merged"
			result.Verified = false
			result.Reason = "PR merged; terminal and landed-content verification are pending"
			if p.TerminalVerified > 0 {
				result.Reason = "PR merged; landed content still requires verification"
			}
			if p.TerminalVerified > 0 && s.Options.VerifyDelivery != nil {
				mainHead, err := s.Options.VerifyDelivery(ctx, meta, base, head, p.PR)
				if err == nil {
					result.Phase = "done"
					result.Verified = true
					result.Reason = "Changed file content verified on main " + mainHead
				} else {
					result.Reason = "PR merged; " + err.Error()
				}
			}
		} else {
			result.Phase = "ready"
			result.Verified = true
			result.Reason = "Review, tests, lint, documentation, push and PR checks verified for this HEAD; merge requires operator authority"
		}
	} else {
		result.Reason = "Pipeline evidence is incomplete or belongs to a different HEAD"
	}
	return result, nil
}

func (s *Service) validateTerminalControl(ctx context.Context, meta state.TaskMeta) error {
	if meta.Mode == "no-mistakes" {
		branch, err := s.Git.Branch(ctx, meta.Worktree)
		if err != nil {
			return err
		}
		if s.Options.Gate == nil {
			return errors.New("cannot verify pipeline custody")
		}
		if err := s.Options.Gate.CanSteer(ctx, meta.Project, meta.Worktree, branch); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) previewGit(meta state.TaskMeta) Git {
	preview := s.Git
	prior := s.Store.Snapshot().Tasks[meta.ID]
	if prior.Generation == meta.SpawnGen {
		preview.Base = prior.Base
	}
	return preview
}

type Task struct {
	ID           string          `json:"id"`
	Title        string          `json:"title"`
	Branch       string          `json:"branch,omitempty"`
	Project      string          `json:"project"`
	Harness      string          `json:"harness"`
	Backend      string          `json:"backend"` // the terminal it runs in: native or herdr
	Model        string          `json:"model"`
	Effort       string          `json:"effort"`
	Mode         string          `json:"mode"`
	Generation   string          `json:"generation"`
	Session      string          `json:"session"`
	Dependencies []string        `json:"dependencies"`
	Runtime      RuntimeEvidence `json:"runtime"`
	// Activity is the task's own latest status line, and Report the kind of
	// its latest report.
	Activity   string    `json:"activity"`
	Report     string    `json:"report"`
	LastReport string    `json:"last_report"`
	Handoff    bool      `json:"handoff"`
	RetiredAt  time.Time `json:"retired_at"`
	// Archived marks completed history rather than a live task, Merged that
	// its pull request merged into its base, and Closed that GitHub closed it
	// without merging.
	Archived bool `json:"archived"`
	Merged   bool `json:"merged"`
	Closed   bool `json:"closed"`
	// Since is when a live task's session started, or when queued work's
	// brief was written; zero when neither is known.
	Since time.Time `json:"since"`
	// Brief says queued work has its brief, which a Start needs; Starting
	// that its Start runs cfo spawn now, and StartError why its last Start
	// failed.
	Brief         bool             `json:"brief"`
	Starting      bool             `json:"starting"`
	StartError    string           `json:"start_error"`
	Lifecycle     *LifecycleStatus `json:"lifecycle,omitempty"`
	Teardown      []string         `json:"teardown,omitempty"`
	ActionError   string           `json:"action_error,omitempty"`
	QueueRevision string           `json:"queue_revision,omitempty"`
	Detail        string           `json:"detail,omitempty"`
	Notes         []string         `json:"notes,omitempty"`
	Evaluation
}

type Snapshot struct {
	Example    bool            `json:"example"`
	Instance   string          `json:"instance"`
	Revision   uint64          `json:"revision"`
	Started    time.Time       `json:"started"`
	At         time.Time       `json:"at"`
	Reconciled time.Time       `json:"reconciled"`
	Healthy    bool            `json:"healthy"`
	Error      string          `json:"error"`
	Inbox      int             `json:"inbox"`
	Tasks      []Task          `json:"tasks"`
	Sessions   []Session       `json:"sessions"`
	Retired    []string        `json:"retired"`
	Actions    []Action        `json:"actions"`
	Decisions  []wake.Record   `json:"decisions"`
	Issues     []string        `json:"issues"`
	Questions  []Question      `json:"questions"`
	Activity   []BoardActivity `json:"activity"`
	Reviews    []Review        `json:"reviews"`
	Runs       []Run           `json:"runs"`

	// Attention is the Overlord's order of the live goblins, top first; a
	// goblin it does not name has not been placed.
	Attention []string `json:"attention"`
	// Registration says why the board cannot reach the primary CFO, with
	// the fix, and is empty while it can.
	Registration string `json:"registration"`
	// Build names the board bundle this supervisor serves, so a tab loaded
	// from an older one can tell the board was updated.
	Build string `json:"build,omitempty"`
	// CFORuns says a CFO is registered and running or starting in its native
	// terminal; without one the board shows its first-run page.
	CFORuns bool `json:"cfo_runs"`
	// CFOStarting says native terminal cfo is up for a CFO not registered
	// yet; the board opens that terminal for its sign-in, and no registration
	// problem is shown while it lasts.
	CFOStarting bool `json:"cfo_starting"`
	// CFOTerminal names the native terminal the board shows the CFO in (see
	// cfoState), and is empty while the CFO runs in Herdr or not at all.
	CFOTerminal string `json:"cfo_terminal"`
	// CFOHarness names the harness the registered CFO runs, such as claude or
	// codex, for the mark beside the CFO on the board; it is empty while no
	// CFO is registered.
	CFOHarness string `json:"cfo_harness"`
	// Memory is the machine's free memory for the Tasks meter, absent on a
	// board that cannot start goblins or cannot read it.
	Memory *Memory `json:"memory,omitempty"`
}

func (s *Service) Snapshot() (Snapshot, error) {
	d := s.Store.Snapshot()
	s.mu.Lock()
	out := Snapshot{Example: s.Options.Example, Instance: s.Instance, Revision: s.revision, Started: s.Started, At: time.Now().UTC(), Reconciled: s.reconciled, Error: s.lastError, Tasks: []Task{}, Attention: []string{}, Sessions: []Session{}, Retired: d.Retired, Actions: d.Actions, Issues: d.Issues}
	history := append([]Task(nil), s.history...)
	checked, checkedIdentity := s.registration, s.registrationIdentity
	for i := range d.Activity {
		if d.Activity[i].CFOIdentity != "" {
			d.Activity[i].Live = d.Activity[i].CFOIdentity == s.presentationIdentity && out.At.Sub(s.presentationChecked) < 2*time.Minute
		}
	}
	s.mu.Unlock()
	cfo := readCFOState(s.Store.Home.State)
	out.CFOTerminal, out.CFORuns, out.CFOStarting, out.CFOHarness = cfo.terminal, cfo.registered || cfo.starting, cfo.starting, cfo.harness
	// The registration problem comes from the same read as the rest, so the
	// board never shows a running CFO beside the problem of one it replaced.
	// What the recovery cycle found is added only for the registration it
	// examined.
	if s.Options.CFO != nil {
		out.Registration = cfo.problem
		if cfo.registered && cfo.problem == "" && cfo.identity == checkedIdentity {
			out.Registration = checked
		}
	}
	// The board sees how many images a question has, never where they are.
	out.Questions = make([]Question, len(d.Questions))
	for i, q := range d.Questions {
		q.ImageCount, q.Images = len(q.Images), nil
		out.Questions[i] = q
	}
	out.Activity = d.Activity
	// The board sees how many images a review has and what its document is,
	// never their digests.
	out.Reviews = make([]Review, len(d.Reviews))
	for i, r := range d.Reviews {
		r.ImageCount, r.ImageSums = len(r.ImageSums), nil
		if r.Document != nil {
			document := *r.Document
			document.Sum = ""
			r.Document = &document
		}
		out.Reviews[i] = r
	}
	// A goblin's question asked while its review page is open is that page's
	// item, so the Command Center shows one card: each names the other, the
	// page its newest pending question.
	for i := range out.Reviews {
		r := &out.Reviews[i]
		for j := range out.Questions {
			if q := &out.Questions[j]; carriesQuestion(*r, *q) {
				q.Page, r.Question = r.ID, q.ID
			}
		}
	}
	// The board sees what runs and how it went, never the process or digest.
	out.Runs = make([]Run, len(d.Runs))
	for i, r := range d.Runs {
		r.ScriptSum, r.RunAction, r.PID, r.Started = "", "", 0, nil
		out.Runs[i] = r
	}
	out.Healthy = supervise.WatcherHealthy(s.Store.Home.State, 30*time.Second)
	for _, node := range d.Sessions {
		if node.Role == "goblin" && d.TaskSessions[node.TaskID] == node.ID {
			if meta, err := state.ReadTaskMeta(s.Store.Home.State, node.TaskID); err == nil {
				node.Runtime = s.runtimeEvidence(meta, node, out.At)
				if record, err := state.ReadLifecycle(s.Store.Home.State, meta.ID); err == nil && record.Generation == meta.SpawnGen && record.SuppressesMonitoring(s.Store.Home.State) {
					node.Phase = record.Phase
				}
			}
		}
		out.Sessions = append(out.Sessions, node)
	}
	sort.Slice(out.Sessions, func(i, j int) bool { return out.Sessions[i].UpdatedAt.Before(out.Sessions[j].UpdatedAt) })
	var err error
	out.Decisions, err = wake.Pending(s.Store.Home.State)
	if err != nil {
		return out, err
	}
	entries, err := os.ReadDir(s.Store.Home.State)
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".meta") {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".meta")
		meta, err := state.ReadTaskMeta(s.Store.Home.State, id)
		if err != nil {
			continue
		}
		evaluation := d.Tasks[id]
		node, linked := d.Sessions[d.TaskSessions[id]]
		runtime := s.runtimeEvidence(meta, node, out.At)
		if evaluation.Generation != meta.SpawnGen {
			evaluation = Evaluation{}
		}
		lines, _ := state.TailStatus(s.Store.Home.State, id, 200)
		reportedAt, report := latestReport(lines, spawnTime(meta.SpawnGen))
		decisions := out.Decisions
		if supersedesQuestion(report) {
			decisions = slices.DeleteFunc(slices.Clone(out.Decisions), func(r wake.Record) bool {
				return r.Key == id && !r.Time.Truncate(time.Second).After(reportedAt)
			})
		}
		if !linked {
			evaluation = fleetEvaluation(evaluation, meta, runtime, decisions)
		} else if node.Generation != meta.SpawnGen {
			evaluation = Evaluation{Phase: "unknown", Reason: "Native session evidence has not been reported"}
		}
		if linked && (node.Phase == "active" || node.Phase == "started") && node.UpdatedAt.After(evaluation.At) {
			if runtime.working() {
				evaluation = Evaluation{Phase: "working", Reason: "Native activity and runtime evidence agree", At: node.UpdatedAt}
			} else {
				evaluation = Evaluation{Phase: "unavailable", Reason: runtime.Reason + "; independent task evaluation is pending", At: runtime.At}
			}
		}
		if evaluation.Phase == "" {
			evaluation = Evaluation{Phase: "review", Reason: "Session settled; evaluation is queued", At: node.UpdatedAt}
		}
		// A goblin's own newer report says what it is doing, unless a question
		// or the gate holds it or its work already merged. A question it asked
		// since replaces no such report: once answered, the goblin stands on
		// it again.
		standingAt, standing := standingReport(lines, spawnTime(meta.SpawnGen))
		if phase, reason, target, ok := reportedProgress(s.Store.Home.State, id, d.Reviews, standingAt, standing); ok && evaluation.Phase != "blocked" && evaluation.Phase != "failed" && evaluation.Phase != "merged" && evaluation.Phase != "done" {
			evaluation.Phase, evaluation.Reason, evaluation.WaitingOn = phase, reason, target
		}
		activity, pr := statusActivity(lines, spawnTime(meta.SpawnGen))
		lastReport, _ := taskSessionSummary(lines, spawnTime(meta.SpawnGen))
		if _, detail, ok := waitingQuestion(decisions, id); ok {
			activity = detail
		}
		if evaluation.PR == "" {
			evaluation.PR = pr
		}
		title := meta.Title
		if title == "" {
			title = id
		}
		out.Tasks = append(out.Tasks, Task{ID: id, Title: title, Project: filepath.Base(meta.Project), Harness: meta.Harness, Backend: meta.Backend, Model: meta.Model, Effort: meta.Effort, Mode: meta.Mode, Generation: meta.SpawnGen, Session: d.TaskSessions[id], Dependencies: []string{}, Runtime: runtime, Activity: activity, LastReport: lastReport, Since: sessionStarted(meta), Report: reportKind(report), Evaluation: evaluation})
		if len(out.Tasks) >= maxSessions {
			break
		}
	}
	// The goblins in progress run in the Overlord's attention order, and any
	// he has not placed follow it.
	if attention, err := fleet.ReadAttention(s.Store.Home); err != nil {
		out.Issues = append(slices.Clone(out.Issues), "The In progress order cannot be read: "+err.Error())
	} else {
		fleet.SortByAttention(out.Tasks, attention, func(task Task) string { return task.ID })
		out.Attention = slices.DeleteFunc(attention, func(id string) bool {
			return !slices.ContainsFunc(out.Tasks, func(task Task) bool { return task.ID == id })
		})
	}
	backlog, err := fleet.ReadBacklog(s.Store.Home)
	if err != nil {
		return out, err
	}
	for _, row := range backlog.Queued {
		if !row.Structured {
			continue
		}
		found := false
		for i := range out.Tasks {
			if out.Tasks[i].ID == row.ID {
				out.Tasks[i].Title = row.Title
				out.Tasks[i].Dependencies = row.BlockedByIDs
				found = true
				break
			}
		}
		if !found && len(out.Tasks) < maxSessions {
			out.Tasks = append(out.Tasks, Task{ID: row.ID, Title: row.Title, Project: row.Repo, Dependencies: row.BlockedByIDs, Since: briefWritten(s.Store.Home, row.ID), Evaluation: Evaluation{Phase: "queued", Reason: row.BlockedReason}})
		}
	}
	for _, brief := range queuedBriefs(s.Store.Home) {
		isParked := slices.ContainsFunc(backlog.Parked, func(row fleet.BacklogRow) bool { return row.Structured && row.ID == brief.ID })
		if !isParked && len(out.Tasks) < maxSessions && !slices.ContainsFunc(out.Tasks, func(t Task) bool { return t.ID == brief.ID }) {
			out.Tasks = append(out.Tasks, brief)
		}
	}
	s.starts.Lock()
	for i := range out.Tasks {
		task := &out.Tasks[i]
		task.Starting = task.ID == s.starting
		if task.Phase == "queued" {
			if queued, err := fleet.ReadQueuedTask(s.Store.Home, task.ID); err == nil {
				task.QueueRevision, task.Detail = queued.Revision, queued.Detail
				if queued.IsBriefOnly {
					task.Title = queued.Row.Title
				}
			}
			for _, record := range out.Decisions {
				if record.Key == task.ID && strings.HasPrefix(record.Detail, "task note: ") {
					task.Notes = append(task.Notes, strings.TrimPrefix(record.Detail, "task note: "))
				}
			}
			task.Brief = exists(filepath.Join(s.Store.Home.Data, task.ID, "brief.md"))
			task.StartError = s.startErrors[task.ID]
		}
		record, lifecycleErr := state.ReadLifecycle(s.Store.Home.State, task.ID)
		isCurrent := record.Generation == task.Generation || record.Generation == "queued" && task.Phase == "queued" && record.Phase == "stopping"
		if lifecycleErr == nil && !isCurrent && record.Action == "resume" && (record.Phase == "resuming" || record.Phase == "failed") {
			meta, err := state.ReadTaskMeta(s.Store.Home.State, task.ID)
			isCurrent = err == nil && meta.SpawnGen == task.Generation && meta.ResumeOperation == record.Operation
		}
		if lifecycleErr == nil {
			task.Teardown = record.TeardownLabels()
		}
		if lifecycleErr == nil && isCurrent && (record.Phase != "running" || len(record.Teardown) > 0) {
			task.Lifecycle = lifecycleStatus(record)
			if record.SuppressesMonitoring(s.Store.Home.State) {
				task.Phase, task.Reason, task.At = record.Phase, record.Reason, record.Updated
				task.Activity = record.Reason
			}
			if record.Phase == "stopped" {
				task.Archived = true
			}
		}
		if failure, ok := s.changeErrors[task.ID]; ok && failure.Generation == task.Generation && (lifecycleErr != nil || failure.Operation == record.Operation && failure.Updated.Equal(record.Updated)) {
			task.ActionError = failure.Message
		}
		if action := s.changing[task.ID]; action != "" {
			task.Phase = map[string]string{"pause": "pausing", "resume": "resuming", "stop": "stopping"}[action]
		}
	}
	s.starts.Unlock()
	if dispatch := s.Options.Dispatch; dispatch != nil {
		if memory, err := dispatch.Memory(); err == nil {
			memory.Floor, memory.Next = memoryFloor, memoryNext
			// Naming who holds commit reads every process, so it is done
			// only while commit is what the meter shows.
			if memory.CommitAvailable < memory.Available {
				if holders, err := dispatch.CommitHolders(); err == nil {
					memory.Holders = holders
				}
			}
			out.Memory = &memory
		}
	}
	for _, done := range history {
		if !done.Archived {
			for i := range out.Tasks {
				task := &out.Tasks[i]
				if task.ID == done.ID && task.PR == done.PR && (task.Phase == "done" || task.Phase == "merged") {
					task.Title, task.Project, task.Branch = done.Title, done.Project, done.Branch
					task.Merged, task.Closed = done.Merged, done.Closed
				}
			}
			continue
		}
		if strings.HasPrefix(done.ID, "merged:") && slices.ContainsFunc(out.Tasks, func(t Task) bool {
			return t.PR == done.PR && (t.Phase == "merged" || t.Phase == "done")
		}) {
			continue
		}
		out.Tasks = append(out.Tasks, done)
	}
	archived := archivedTasks(s.Store.Home)
	for i := range out.Tasks {
		task := &out.Tasks[i]
		id := strings.TrimPrefix(task.ID, "finished:")
		if state.ValidTaskID(id) == nil {
			if file, err := openTaskHandoff(s.Store.Home, id, archived); err == nil {
				task.Handoff = true
				file.Close()
			}
		}
	}
	if len(out.Decisions) > 100 {
		out.Decisions = out.Decisions[len(out.Decisions)-100:]
	}
	inbox, err := os.ReadDir(nativehook.SpoolDir(s.Store.Home.State))
	if err != nil {
		return out, err
	}
	for _, entry := range inbox {
		if strings.HasSuffix(entry.Name(), ".event.json") {
			out.Inbox++
		}
	}
	return out, nil
}
