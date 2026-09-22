package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	Send           func(context.Context, string, string) error
	Peek           func(context.Context, string, int) (string, error)
	Reconcile      func(context.Context) error
	VerifyDelivery func(context.Context, state.TaskMeta, string, string, string) (string, error)
}

type Service struct {
	Store       *Store
	Options     Options
	Git         Git
	Instance    string
	Started     time.Time
	mu          sync.Mutex
	lastError   string
	reconciled  time.Time
	revision    uint64
	subscribers map[chan struct{}]struct{}
	done        chan struct{}
	work        chan struct{}
	cancel      context.CancelFunc
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
	var reconcileErr error
	if recover {
		if s.Options.Reconcile != nil {
			reconcileErr = s.Options.Reconcile(ctx)
		}
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
		pending := false
		for _, a := range d.Actions {
			if a.TaskID == task && a.Kind == "evaluate" && (a.Status == "queued" || a.Status == "running") {
				pending = true
				break
			}
		}
		if pending {
			continue
		}
		if _, err := s.Store.Queue(Action{ID: fmt.Sprintf("reconcile-%s-%d", task, now.Unix()/60), Kind: "evaluate", TaskID: task, Session: id, EventID: node.LastEventID, Generation: meta.SpawnGen}); err != nil {
			return err
		}
	}
	return nil
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
	if a.Kind == "cfo_message" {
		if s.Options.CFO == nil {
			return Evaluation{}, fmt.Errorf("%w: CFO message transport is unavailable", ErrRejected)
		}
		return s.Options.CFO.Send(ctx, a.Generation, a.Text)
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
	if a.Kind == "feedback" {
		if err := s.validateFeedback(ctx, meta, a); err != nil {
			return Evaluation{}, fmt.Errorf("%w: %v", ErrRejected, err)
		}
		if s.Options.Send == nil {
			return Evaluation{}, fmt.Errorf("%w: Herdr steering is unavailable", ErrRejected)
		}
		message := a.Text
		if a.File != "" {
			message = fmt.Sprintf("Review feedback for %s:%d (%s, HEAD %s)\n> %s", a.File, a.Line, a.Side, a.Head, strings.ReplaceAll(a.Text, "\n", "\n> "))
		}
		if err := s.Options.Send(ctx, a.TaskID, message); err != nil {
			return Evaluation{}, err
		}
		return Evaluation{Reason: "Feedback accepted by the task agent"}, nil
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
		result.Phase = "ready"
		result.Verified = true
		result.Reason = "Review, tests, lint, documentation, push and PR checks verified for this HEAD; merge requires operator authority"
		if strings.EqualFold(p.PRState, "merged") && p.TerminalVerified > 0 {
			result.Phase = "merged"
			result.Verified = false
			result.Reason = "PR merged; landed content still requires verification"
			if s.Options.VerifyDelivery != nil {
				mainHead, err := s.Options.VerifyDelivery(ctx, meta, base, head, p.PR)
				if err == nil {
					result.Phase = "done"
					result.Verified = true
					result.Reason = "Changed file content verified on main " + mainHead
				} else {
					result.Reason = "PR merged; " + err.Error()
				}
			}
		}
	} else {
		result.Reason = "Pipeline evidence is incomplete or belongs to a different HEAD"
	}
	return result, nil
}

func (s *Service) validateFeedback(ctx context.Context, meta state.TaskMeta, a Action) error {
	if strings.HasPrefix(strings.TrimSpace(a.Text), "/") {
		return errors.New("feedback cannot execute harness slash commands")
	}
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
	if a.File != "" {
		if a.Line < 1 || (a.Side != "added" && a.Side != "removed" && a.Side != "context") {
			return errors.New("feedback requires a valid diff line")
		}
		diff, err := s.previewGit(meta).Diff(ctx, meta.Worktree, a.Revision, a.File)
		if err != nil {
			return err
		}
		if a.Head == "" || diff.Head != a.Head {
			return errors.New("HEAD changed; refresh the diff before submitting feedback")
		}
		if a.DiffID == "" || a.DiffID != diff.Fingerprint {
			return errors.New("diff changed; refresh before submitting feedback")
		}
		if !diffHasLine(diff.Patch, a.Line, a.Side) {
			return errors.New("feedback line is no longer present; refresh the diff")
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

func diffHasLine(patch string, wanted int, side string) bool {
	old, newLine := 0, 0
	for _, line := range strings.Split(patch, "\n") {
		if strings.HasPrefix(line, "@@ ") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				fmt.Sscanf(strings.Split(fields[1], ",")[0], "-%d", &old)
				fmt.Sscanf(strings.Split(fields[2], ",")[0], "+%d", &newLine)
			}
			continue
		}
		if old == 0 && newLine == 0 || len(line) == 0 {
			continue
		}
		switch line[0] {
		case '+':
			if side == "added" && newLine == wanted {
				return true
			}
			newLine++
		case '-':
			if side == "removed" && old == wanted {
				return true
			}
			old++
		case ' ':
			if side == "context" && newLine == wanted {
				return true
			}
			old++
			newLine++
		}
	}
	return false
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
	Evaluation
}

type Snapshot struct {
	Example    bool          `json:"example"`
	Instance   string        `json:"instance"`
	Revision   uint64        `json:"revision"`
	Started    time.Time     `json:"started"`
	At         time.Time     `json:"at"`
	Reconciled time.Time     `json:"reconciled"`
	Healthy    bool          `json:"healthy"`
	Error      string        `json:"error"`
	Inbox      int           `json:"inbox"`
	Tasks      []Task        `json:"tasks"`
	Sessions   []Session     `json:"sessions"`
	Retired    []string      `json:"retired"`
	Actions    []Action      `json:"actions"`
	Decisions  []wake.Record `json:"decisions"`
	Issues     []string      `json:"issues"`
}

func (s *Service) Snapshot() (Snapshot, error) {
	d := s.Store.Snapshot()
	s.mu.Lock()
	out := Snapshot{Example: s.Options.Example, Instance: s.Instance, Revision: s.revision, Started: s.Started, At: time.Now().UTC(), Reconciled: s.reconciled, Error: s.lastError, Tasks: []Task{}, Sessions: []Session{}, Retired: d.Retired, Actions: d.Actions, Issues: d.Issues}
	s.mu.Unlock()
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
		if (linked && node.Generation != meta.SpawnGen) || (!linked && evaluation.Generation != meta.SpawnGen) {
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
		out.Tasks = append(out.Tasks, Task{ID: id, Title: id, Project: filepath.Base(meta.Project), Harness: meta.Harness, Model: meta.Model, Effort: meta.Effort, Mode: meta.Mode, Generation: meta.SpawnGen, Session: d.TaskSessions[id], Dependencies: []string{}, Runtime: runtime, Evaluation: evaluation})
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
	out.Decisions, err = wake.Pending(s.Store.Home.State)
	if err != nil {
		return out, err
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
