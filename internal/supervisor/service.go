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
	// MergedPRs lists, newest first, at most limit pull requests merged since
	// a time.
	MergedPRs func(ctx context.Context, since time.Time, limit int) ([]MergedPR, error)
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
	registration         string
	history              []Task
	revision             uint64
	subscribers          map[chan struct{}]struct{}
	done                 chan struct{}
	work                 chan struct{}
	cancel               context.CancelFunc
}

// Start acquires the same singleton as legacy watch BEFORE opening recovery
// state. The browser, hook writers, and competing serve invocations cannot
// create a second controller or replay actions outside this lease.
func Start(ctx context.Context, h home.Home, options Options) (*Service, error) {
	if err := os.MkdirAll(h.State, 0700); err != nil {
		return nil, err
	}
	if _, err := lock.AcquireExclusiveNamed(h.State, ".watch.lock"); err != nil {
		return nil, fmt.Errorf("supervisor: existing watch owner must finish before serve: %w", err)
	}
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

func (s *Service) Close() { s.cancel(); <-s.done }

func (s *Service) Done() <-chan struct{} { return s.done }

func (s *Service) publish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.lastError = bounded(err.Error(), 1000)
	} else {
		s.lastError = ""
	}
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
	reconcileErr = errors.Join(reconcileErr, s.Store.ingestActivity())
	reconcileErr = errors.Join(reconcileErr, s.Store.supersedeQuestions())
	s.reconcilePresentations(ctx)
	if recover {
		if s.Options.Reconcile != nil {
			reconcileErr = errors.Join(reconcileErr, s.Options.Reconcile(ctx))
		}
		s.checkRegistration(ctx)
		reconcileErr = errors.Join(reconcileErr, s.refreshHistory(ctx))
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

// refreshHistory rebuilds the Completed column on the once-a-minute recovery
// cycle: finished tasks, and the pull requests merged into fleet repositories.
func (s *Service) refreshHistory(ctx context.Context) error {
	now := time.Now().UTC()
	history := finishedTasks(s.Store.Home.State, now)
	var err error
	if s.Options.MergedPRs != nil {
		var merged []MergedPR
		merged, err = s.Options.MergedPRs(ctx, now.Add(-historyWindow), historyLimit)
		history = withMergedPRs(history, merged)
	}
	s.mu.Lock()
	s.history = history
	s.mu.Unlock()
	return err
}

// checkRegistration runs on the once-a-minute recovery cycle, so a CFO that
// exited or moved shows as one state on the board before anyone tries to
// deliver to it.
func (s *Service) checkRegistration(ctx context.Context) {
	if s.Options.CFO == nil {
		return
	}
	check, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	problem := ""
	if err := s.Options.CFO.check(check); err != nil {
		problem = err.Error()
	}
	s.mu.Lock()
	s.registration = problem
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
		if _, err := s.Store.Queue(Action{ID: fmt.Sprintf("reconcile-%s-%d", task, now.Unix()/60), Kind: "evaluate", TaskID: task, Session: id, EventID: node.LastEventID, Generation: meta.SpawnGen}); err != nil {
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
		if _, err := s.Store.Queue(Action{ID: fmt.Sprintf("reconcile-%s-%d", task, now.Unix()/60), Kind: "evaluate", TaskID: task, Generation: meta.SpawnGen}); err != nil {
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
		boundedCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
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
	if a.Kind != "evaluate" && a.Kind != "review" && a.Kind != "cfo_answer" {
		return Evaluation{}, fmt.Errorf("%w: unsupported action kind %q", ErrRejected, a.Kind)
	}
	if a.Kind == "cfo_answer" {
		if s.Options.CFO == nil {
			return Evaluation{}, fmt.Errorf("%w: CFO message transport is unavailable", ErrRejected)
		}
		text := a.Text
		found := false
		for _, q := range s.Store.Snapshot().Questions {
			if q.ID == a.QuestionID && q.Identity == a.Generation && q.AnswerID == a.ID {
				text = fmt.Sprintf("User answer to CFO question %s\nQuestion: %s\nAnswer: %s", q.ID, q.Text, a.Text)
				if a.AnswerKind == "other" {
					text = fmt.Sprintf("User answer to CFO question %s\nQuestion: %s\nAnswer (Other): %s", q.ID, q.Text, a.Text)
				}
				found = true
			}
		}
		if !found {
			return Evaluation{}, fmt.Errorf("%w: user question context changed", ErrRejected)
		}
		return s.Options.CFO.Send(ctx, a.Generation, text)
	}
	meta, err := state.ReadTaskMeta(s.Store.Home.State, a.TaskID)
	if err != nil {
		return Evaluation{}, err
	}
	if meta.SpawnGen != a.Generation {
		return Evaluation{}, fmt.Errorf("%w: task restarted or was replaced; refresh the board", ErrRejected)
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
	Project      string          `json:"project"`
	Harness      string          `json:"harness"`
	Model        string          `json:"model"`
	Effort       string          `json:"effort"`
	Mode         string          `json:"mode"`
	Generation   string          `json:"generation"`
	Session      string          `json:"session"`
	Dependencies []string        `json:"dependencies"`
	Runtime      RuntimeEvidence `json:"runtime"`
	// Activity is the task's own latest status line.
	Activity string `json:"activity"`
	// Archived marks completed history rather than a live task, and Merged
	// that its pull request merged into a fleet repository.
	Archived bool `json:"archived"`
	Merged   bool `json:"merged"`
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

	// Registration says why the board cannot reach the primary CFO, with
	// the fix, and is empty while it can.
	Registration string `json:"registration"`
}

func (s *Service) Snapshot() (Snapshot, error) {
	d := s.Store.Snapshot()
	s.mu.Lock()
	out := Snapshot{Example: s.Options.Example, Instance: s.Instance, Revision: s.revision, Started: s.Started, At: time.Now().UTC(), Reconciled: s.reconciled, Error: s.lastError, Registration: s.registration, Tasks: []Task{}, Sessions: []Session{}, Retired: d.Retired, Actions: d.Actions, Issues: d.Issues}
	history := append([]Task(nil), s.history...)
	for i := range d.Activity {
		if d.Activity[i].CFOIdentity != "" {
			d.Activity[i].Live = d.Activity[i].CFOIdentity == s.presentationIdentity && out.At.Sub(s.presentationChecked) < 2*time.Minute
		}
	}
	s.mu.Unlock()
	out.Questions = d.Questions
	out.Activity = d.Activity
	out.Healthy = supervise.WatcherHealthy(s.Store.Home.State, 30*time.Second)
	for _, node := range d.Sessions {
		if node.Role == "goblin" && d.TaskSessions[node.TaskID] == node.ID {
			if meta, err := state.ReadTaskMeta(s.Store.Home.State, node.TaskID); err == nil {
				node.Runtime = s.runtimeEvidence(meta, node, out.At)
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
		if !linked {
			evaluation = fleetEvaluation(evaluation, meta, runtime, out.Decisions)
		} else if node.Generation != meta.SpawnGen {
			evaluation = Evaluation{Phase: "unknown", Reason: "Native session evidence has not been reported"}
		}
		if linked && (node.Phase == "active" || node.Phase == "started") && node.UpdatedAt.After(evaluation.At) {
			if runtime.working() {
				evaluation = Evaluation{Phase: "working", Reason: "Native activity and Herdr runtime evidence agree", At: node.UpdatedAt}
			} else {
				evaluation = Evaluation{Phase: "unavailable", Reason: runtime.Reason + "; independent task evaluation is pending", At: runtime.At}
			}
		}
		if evaluation.Phase == "" {
			evaluation = Evaluation{Phase: "review", Reason: "Session settled; evaluation is queued", At: node.UpdatedAt}
		}
		lines, _ := state.TailStatus(s.Store.Home.State, id, 200)
		activity, pr := statusActivity(lines, spawnTime(meta.SpawnGen))
		if _, detail, ok := waitingQuestion(out.Decisions, id); ok {
			activity = detail
		}
		if evaluation.PR == "" {
			evaluation.PR = pr
		}
		out.Tasks = append(out.Tasks, Task{ID: id, Title: id, Project: filepath.Base(meta.Project), Harness: meta.Harness, Model: meta.Model, Effort: meta.Effort, Mode: meta.Mode, Generation: meta.SpawnGen, Session: d.TaskSessions[id], Dependencies: []string{}, Runtime: runtime, Activity: activity, Evaluation: evaluation})
		if len(out.Tasks) >= maxSessions {
			break
		}
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
			out.Tasks = append(out.Tasks, Task{ID: row.ID, Title: row.Title, Project: row.Repo, Dependencies: row.BlockedByIDs, Evaluation: Evaluation{Phase: "queued", Reason: row.BlockedReason}})
		}
	}
	for _, brief := range queuedBriefs(s.Store.Home) {
		if len(out.Tasks) < maxSessions && !slices.ContainsFunc(out.Tasks, func(t Task) bool { return t.ID == brief.ID }) {
			out.Tasks = append(out.Tasks, brief)
		}
	}
	for _, done := range history {
		if strings.HasPrefix(done.ID, "merged:") && slices.ContainsFunc(out.Tasks, func(t Task) bool {
			return t.PR == done.PR && (t.Phase == "merged" || t.Phase == "done")
		}) {
			continue
		}
		out.Tasks = append(out.Tasks, done)
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
