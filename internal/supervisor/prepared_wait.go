package supervisor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/host"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

var errPreparedChanged = errors.New("prepared authorization invalidated")

func preparedHandoff(h home.Home, record state.PreparedWait) ([]byte, error) {
	if !fsx.SamePath(record.Handoff, filepath.Join(h.Data, record.ID, "handoff.md")) {
		return nil, errors.New("prepared handoff must be the task's saved data handoff.md")
	}
	file, err := openHandoff(handoffPlace{h.Data, filepath.Join(record.ID, "handoff.md")})
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxHandoff+1))
	if err != nil || len(data) == 0 || len(data) > maxHandoff {
		return nil, errors.New("prepared handoff is empty or unreadable within its size limit")
	}
	digest := sha256.Sum256(data)
	if record.Digest != "" && record.Digest != hex.EncodeToString(digest[:]) {
		return nil, fmt.Errorf("%w: prepared handoff changed", errPreparedChanged)
	}
	return data, nil
}

func preparedSource(ctx context.Context, runner execx.Runner, meta state.TaskMeta) (head, branch string, err error) {
	if runner == nil {
		return "", "", errors.New("prepared source reader unavailable")
	}
	head, err = runOutput(ctx, runner, meta.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	branch, err = runOutput(ctx, runner, meta.Worktree, "git", "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return "", "", err
	}
	if branch == "" {
		return "", "", errors.New("prepared source requires a branch")
	}
	dirty, err := runOutput(ctx, runner, meta.Worktree, "git", "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", "", err
	}
	if dirty != "" {
		return "", "", fmt.Errorf("%w: prepared source has pending worker-owned changes", errPreparedChanged)
	}
	return head, branch, nil
}

// PublishPreparedWait authenticates the reporting terminal, rather than
// trusting an inherited task or generation variable as permission to stop it.
func PublishPreparedWait(ctx context.Context, h home.Home, id, generation, until, handoff string, runner execx.Runner) error {
	meta, err := goblinAsker(h.State, id)
	if err != nil {
		return err
	}
	if generation == "" || meta.SpawnGen != generation {
		return errors.New("prepared wait generation is not the reporting terminal's current generation")
	}
	return publishPreparedWait(ctx, h, meta, until, handoff, runner)
}

func publishPreparedWait(ctx context.Context, h home.Home, meta state.TaskMeta, until, handoff string, runner execx.Runner) error {
	id, generation := meta.ID, meta.SpawnGen
	name := ".prepared-wait-" + id + ".lock"
	if _, err := lock.AcquireExclusiveNamed(h.State, name); err != nil {
		return err
	}
	defer lock.ReleaseExclusiveNamed(h.State, name)
	at := time.Now().UTC()
	condition, err := state.NewPauseCondition("dependency", until, at)
	if err != nil {
		return err
	}
	conversation, err := state.ReadConversation(h.State, id)
	if err != nil || conversation.Generation != generation || conversation.Session == "" {
		return errors.New("prepared wait requires the saved conversation of this generation")
	}
	record := state.PreparedWait{ID: id, Generation: generation, Project: meta.Project, Worktree: meta.Worktree, Session: conversation.Session, Condition: condition, PreparedAt: at, Handoff: handoff, Phase: "prepared"}
	data, err := preparedHandoff(h, record)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	record.Digest = hex.EncodeToString(digest[:])
	record.Head, record.Branch, err = preparedSource(ctx, runner, meta)
	if err != nil {
		return err
	}
	record.Operation = record.OperationID()
	prior, err := state.ReadPreparedWait(h.State, id)
	if err == nil && prior.Operation == record.Operation {
		return nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if current, err := state.ReadTaskMeta(h.State, id); err != nil || current.SpawnGen != generation || current.Worktree != meta.Worktree || current.Project != meta.Project {
		return errors.New("prepared wait source changed while reporting")
	}
	if err := state.WritePreparedWait(h.State, record); err != nil {
		return err
	}
	if err := state.AppendStatus(h.State, id, record.Report()); err != nil {
		return err
	}
	Reported(h.State)
	return nil
}

func (s *Service) preparedDependency(record state.PreparedWait) error {
	kind, id, _ := strings.Cut(record.Condition.Until, ":")
	seen := map[string]bool{record.ID: true}
	for kind == "task" {
		if seen[id] {
			return errors.New("prepared dependency is cyclic")
		}
		seen[id] = true
		_, metaErr := state.ReadTaskMeta(s.Store.Home.State, id)
		_, outcomeErr := state.ReadOutcome(s.Store.Home.State, id)
		if metaErr != nil && !errors.Is(metaErr, os.ErrNotExist) {
			return metaErr
		}
		if outcomeErr != nil && !errors.Is(outcomeErr, os.ErrNotExist) {
			return outcomeErr
		}
		if metaErr != nil && outcomeErr != nil {
			return fmt.Errorf("prepared dependency task %s is unknown", id)
		}
		waiting, err := state.ReadPreparedWait(s.Store.Home.State, id)
		if err == nil && waiting.Phase != "invalid" && waiting.Phase != "cleared" && waiting.Phase != "failed" {
			kind, id, _ = strings.Cut(waiting.Condition.Until, ":")
			continue
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		paused, err := state.ReadLifecycle(s.Store.Home.State, id)
		if err == nil && paused.Pause != nil && paused.Pause.Reason == "dependency" && paused.Phase == "paused" {
			kind, id, _ = strings.Cut(paused.Pause.Until, ":")
			continue
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		break
	}
	return nil
}

func (s *Service) preparedEvidence(ctx context.Context, record state.PreparedWait, now time.Time) ([]byte, error) {
	meta, err := state.ReadTaskMeta(s.Store.Home.State, record.ID)
	if err != nil || meta.SpawnGen != record.Generation || meta.Worktree != record.Worktree || meta.Project != record.Project {
		return nil, fmt.Errorf("%w: prepared generation or source changed", errPreparedChanged)
	}
	conversation, err := state.ReadConversation(s.Store.Home.State, record.ID)
	if err != nil || conversation.Generation != record.Generation || conversation.Session != record.Session {
		return nil, fmt.Errorf("%w: prepared saved session changed or is unreadable", errPreparedChanged)
	}
	lines, err := state.TailStatus(s.Store.Home.State, record.ID, 200)
	if err != nil {
		return nil, err
	}
	_, report := ownReport(lines, spawnTime(record.Generation))
	if report != record.Report() {
		return nil, fmt.Errorf("%w: prepared signal superseded by newer worker activity", errPreparedChanged)
	}
	isPrompted, err := NativePromptSince(s.Store.Home.State, record.ID, record.Generation, record.PreparedAt.Add(time.Nanosecond))
	if err != nil {
		return nil, fmt.Errorf("prepared prompt evidence is unreadable: %w", err)
	}
	if isPrompted {
		return nil, fmt.Errorf("%w: worker took a newer prompt", errPreparedChanged)
	}
	for _, action := range s.Store.Snapshot().Actions {
		if action.TaskID == record.ID && (action.Status == "queued" || action.Status == "running") && (action.Kind == "message" || action.Kind == "feedback") {
			return nil, errors.New("prepared worker has pending input")
		}
	}
	head, branch, err := preparedSource(ctx, s.Options.Progress, meta)
	if err != nil {
		return nil, err
	}
	if head != record.Head || branch != record.Branch {
		return nil, fmt.Errorf("%w: prepared branch or commit changed", errPreparedChanged)
	}
	data, err := preparedHandoff(s.Store.Home, record)
	if err != nil {
		return nil, err
	}
	if s.Options.Gate == nil {
		return nil, errors.New("prepared gate ownership is unreadable")
	}
	progress, err := s.Options.Gate.Progress(ctx, meta.Project, branch)
	if err != nil && !errors.Is(err, pipeline.ErrNoProgress) {
		return nil, fmt.Errorf("prepared gate ownership: %w", err)
	}
	if err == nil && !progress.IsTerminal() {
		return nil, fmt.Errorf("%w: active or parked gate owns the task", errPreparedChanged)
	}
	if err := s.Options.Gate.CanSteer(ctx, meta.Project, meta.Worktree, branch); err != nil {
		if errors.Is(err, pipeline.ErrCustodyHeld) || errors.Is(err, pipeline.ErrCustodyNotReturned) {
			return nil, fmt.Errorf("%w: prepared gate custody: %w", errPreparedChanged, err)
		}
		return nil, fmt.Errorf("prepared gate custody: %w", err)
	}
	var tree fleettree.Tree
	if s.Options.PreparedOwnership != nil {
		tree, err = s.Options.PreparedOwnership(ctx, meta)
	} else if s.Options.Tree != nil {
		var terminal host.Record
		terminal, err = host.ReadRecord(s.Store.Home.State, meta.ID)
		if err == nil {
			tree, err = s.Options.Tree.Read(ctx, fleettree.Goblin{Meta: meta, HarnessPID: terminal.ChildPID, HarnessStarted: terminal.ChildStart})
		}
	} else {
		err = errors.New("no ownership reader")
	}
	if err != nil || tree.TaskID != record.ID || tree.Generation != record.Generation || tree.Session != record.Session || len(tree.Unread) > 0 || tree.FetchedAt.Before(record.PreparedAt) || now.Sub(tree.FetchedAt) > evidenceFresh || tree.FetchedAt.After(now.Add(time.Minute)) {
		return nil, fmt.Errorf("prepared ownership is unreadable or stale: %v %s", err, strings.Join(tree.Unread, ", "))
	}
	if tree.Busy().After(record.PreparedAt) {
		return nil, fmt.Errorf("%w: newer working activity", errPreparedChanged)
	}
	jobs, _ := tree.Jobs()
	if len(jobs) > 0 || len(tree.Waits()) > 0 {
		return nil, fmt.Errorf("%w: prepared worker-owned work remains", errPreparedChanged)
	}
	for _, child := range tree.Children {
		if child.State != fleettree.Done && child.State != fleettree.Failed {
			return nil, fmt.Errorf("%w: prepared owned %s remains: %s", errPreparedChanged, child.Kind, child.Label)
		}
		if child.Started.After(record.PreparedAt) || child.LastActivity.After(record.PreparedAt) || child.Finished.After(record.PreparedAt) {
			return nil, fmt.Errorf("%w: newer owned %s activity: %s", errPreparedChanged, child.Kind, child.Label)
		}
	}
	entries, err := os.ReadDir(s.Store.Home.State)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		id, isMeta := strings.CutSuffix(entry.Name(), ".meta")
		if entry.IsDir() || !isMeta {
			continue
		}
		child, err := state.ReadTaskMeta(s.Store.Home.State, id)
		if err != nil {
			return nil, fmt.Errorf("prepared helper ownership is unreadable: %w", err)
		}
		if child.Parent == record.ID {
			return nil, fmt.Errorf("%w: prepared helper ownership remains", errPreparedChanged)
		}
	}
	observation, err := monitor.ReadObservation(s.Store.Home.State, record.ID)
	if err != nil || observation.Endpoint != (herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}).String() || observation.LastObserved.Before(record.PreparedAt) || now.Sub(observation.LastObserved) > evidenceFresh || observation.LastObserved.After(now.Add(time.Minute)) || observation.ScreenUnreadSince != nil || observation.EndpointVerdict != monitor.ProbePresent {
		return nil, errors.New("prepared worker idle evidence is unreadable or stale")
	}
	if observation.BusySince != nil && observation.BusySince.After(record.PreparedAt) || observation.HasJobProgress {
		return nil, fmt.Errorf("%w: newer working activity", errPreparedChanged)
	}
	isIdle, err := s.engineTaskIdle(ctx, meta)
	isIdleHealth := observation.Health == monitor.HealthIdle || observation.Health == monitor.HealthParked && observation.Reason == monitor.AwaitingDecision
	if err != nil || !isIdle || !isIdleHealth {
		return nil, fmt.Errorf("prepared worker is not idle: %v", err)
	}
	// Ownership and idle readers may race a new turn or source write.
	// Check the saved authorization again after those readers return.
	current, err := state.ReadTaskMeta(s.Store.Home.State, record.ID)
	if err != nil || current.SpawnGen != meta.SpawnGen || current.Worktree != meta.Worktree || current.Project != meta.Project || current.TaskTmp != meta.TaskTmp {
		return nil, fmt.Errorf("%w: prepared source changed during admission", errPreparedChanged)
	}
	lines, err = state.TailStatus(s.Store.Home.State, record.ID, 200)
	if err != nil {
		return nil, err
	}
	_, report = ownReport(lines, spawnTime(record.Generation))
	if report != record.Report() {
		return nil, fmt.Errorf("%w: newer worker activity during admission", errPreparedChanged)
	}
	head, branch, err = preparedSource(ctx, s.Options.Progress, meta)
	if err != nil {
		return nil, err
	}
	if head != record.Head || branch != record.Branch {
		return nil, fmt.Errorf("%w: prepared source changed during admission", errPreparedChanged)
	}
	data, err = preparedHandoff(s.Store.Home, record)
	if err != nil {
		return nil, err
	}
	isPrompted, err = NativePromptSince(s.Store.Home.State, record.ID, record.Generation, record.PreparedAt.Add(time.Nanosecond))
	if err != nil {
		return nil, fmt.Errorf("prepared prompt evidence is unreadable: %w", err)
	}
	if isPrompted {
		return nil, fmt.Errorf("%w: worker took a newer prompt during admission", errPreparedChanged)
	}
	return data, nil
}

func (s *Service) preparedProblem(record *state.PreparedWait, phase string, problem error) error {
	if phase == "prepared" && errors.Is(problem, errPreparedChanged) {
		phase = "invalid"
	}
	text := problem.Error()
	if record.Phase == phase && record.Problem == text {
		return nil
	}
	record.Phase, record.Problem = phase, text
	if err := state.WritePreparedWait(s.Store.Home.State, *record); err != nil {
		return err
	}
	_, _, err := wake.AppendFirst(s.Store.Home.State, record.Operation+"/"+phase+"/"+text, "check", record.ID, "prepared dependency: "+text)
	if err != nil {
		return err
	}
	_, err = wake.PublishEpisode(s.Store.Home.State)
	return err
}

func (s *Service) reconcilePreparedWaits(ctx context.Context, watched *fleetWakes, now time.Time) error {
	if s.Options.Dispatch == nil || s.Options.Dispatch.Spawn == nil {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(s.Store.Home.State, "prepared-waits"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var problems error
	for _, entry := range entries {
		id, isRecord := strings.CutSuffix(entry.Name(), ".json")
		if entry.IsDir() || !isRecord {
			continue
		}
		err := s.reconcilePreparedWait(ctx, watched, id, now)
		if err != nil {
			problems = errors.Join(problems, fmt.Errorf("prepared wait for %s: %w", id, err))
		}
	}
	return problems
}

func (s *Service) reconcilePreparedWait(ctx context.Context, watched *fleetWakes, id string, now time.Time) error {
	if err := state.ValidTaskID(id); err != nil {
		return err
	}
	name := ".prepared-wait-" + id + ".lock"
	if _, err := lock.AcquireExclusiveNamed(s.Store.Home.State, name); err != nil {
		return err
	}
	defer lock.ReleaseExclusiveNamed(s.Store.Home.State, name)
	record, err := state.ReadPreparedWait(s.Store.Home.State, id)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if record.Phase == "cleared" || record.Phase == "invalid" || record.Phase == "failed" {
		return nil
	}
	lifecycle, err := state.ReadLifecycle(s.Store.Home.State, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return s.preparedProblem(&record, record.Phase, err)
	}
	if record.Phase != "prepared" {
		switch {
		case record.Phase != "resume-requested" && lifecycle.Operation == record.Operation && lifecycle.Generation == record.Generation && lifecycle.Phase == "paused":
			record.Phase, record.Problem = "paused", ""
			return state.WritePreparedWait(s.Store.Home.State, record)
		case lifecycle.Operation == "resume-"+record.Operation && lifecycle.Phase == "running":
			record.Phase, record.Problem = "cleared", ""
			return state.WritePreparedWait(s.Store.Home.State, record)
		case preparedTransitionInFlight(s.Store.Home.State, record, lifecycle):
			// The admitted controller still runs, so a transition a restart
			// found in flight publishes its completed record instead of being
			// called failed before it can.
			return nil
		default:
			return s.preparedProblem(&record, "failed", errors.New("prepared lifecycle admission has no completed matching transition; inspect before retrying"))
		}
	}
	if lifecycle.Generation == record.Generation && lifecycle.Phase != "" && lifecycle.Phase != "running" {
		return s.preparedProblem(&record, "prepared", fmt.Errorf("%w: another lifecycle operation owns the worker", errPreparedChanged))
	}
	data, err := s.preparedEvidence(ctx, record, now)
	if err != nil {
		return s.preparedProblem(&record, "prepared", err)
	}
	isCleared, err := s.preparedCleared(ctx, record, now, watched)
	if err != nil {
		return s.preparedProblem(&record, "prepared", err)
	}
	if isCleared {
		record.Phase, record.Problem = "cleared", ""
		if err := state.WritePreparedWait(s.Store.Home.State, record); err != nil {
			return err
		}
		if _, _, err := wake.AppendFirst(s.Store.Home.State, record.Operation+"/cleared", "check", id, "prepared dependency already cleared; worker remains running"); err != nil {
			return err
		}
		_, err := wake.PublishEpisode(s.Store.Home.State)
		return err
	}
	s.starts.Lock()
	if s.starting != "" || len(s.changing) > 0 || len(s.asked) > 0 {
		s.starts.Unlock()
		return nil
	}
	s.markChanging(id, "pause")
	s.starts.Unlock()
	defer func() {
		s.starts.Lock()
		delete(s.changing, id)
		s.starts.Unlock()
		s.notify()
	}()
	isCleared, err = s.preparedCleared(ctx, record, now, watched)
	if err != nil {
		return s.preparedProblem(&record, "prepared", err)
	}
	if isCleared {
		record.Phase = "cleared"
		return state.WritePreparedWait(s.Store.Home.State, record)
	}
	// Re-read all mutable evidence after the prerequisite lookup, immediately
	// before the existing lifecycle path.
	data, err = s.preparedEvidence(ctx, record, now)
	if err != nil {
		return s.preparedProblem(&record, "prepared", err)
	}
	meta, err := state.ReadTaskMeta(s.Store.Home.State, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(meta.TaskTmp, 0o700); err != nil {
		return err
	}
	if err := fsx.AtomicWriteFile(filepath.Join(meta.TaskTmp, "pause-"+record.Operation+".md"), data); err != nil {
		return err
	}
	record.Phase, record.Problem = "pause-requested", ""
	if err := state.WritePreparedWait(s.Store.Home.State, record); err != nil {
		return err
	}
	output, err := s.Options.Dispatch.Spawn(ctx, []string{"pause", id, "--generation", record.Generation, "--operation", record.Operation, "--reason", "dependency", "--until", record.Condition.Until})
	if err != nil {
		return s.preparedProblem(&record, "failed", errors.New(spawnFailure(output, err)))
	}
	lifecycle, err = state.ReadLifecycle(s.Store.Home.State, id)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// The pause ran, so its outcome is still to be read: keep the
		// admission and let the next scan reconcile it.
		return s.preparedProblem(&record, record.Phase, fmt.Errorf("prepared pause record is unreadable: %w", err))
	}
	if lifecycle.Operation != record.Operation || lifecycle.Generation != record.Generation || lifecycle.Phase != "paused" {
		return s.preparedProblem(&record, "failed", errors.New("prepared pause did not produce a matching completed lifecycle record"))
	}
	record.Phase = "paused"
	return state.WritePreparedWait(s.Store.Home.State, record)
}

// preparedTransitionInFlight reports whether lifecycle is the transition this
// prepared admission asked for and the controller running it still holds the
// task's lifecycle lock, so its completed record is still to come.
func preparedTransitionInFlight(directory string, record state.PreparedWait, lifecycle state.Lifecycle) bool {
	switch record.Phase {
	case "pause-requested":
		if lifecycle.Operation != record.Operation || lifecycle.Phase != "pausing" {
			return false
		}
	case "resume-requested":
		if lifecycle.Operation != "resume-"+record.Operation || lifecycle.Phase != "resuming" {
			return false
		}
	default:
		return false
	}
	controller, err := lock.ReadNamed(directory, state.LifecycleLockName(record.ID))
	return err == nil && controller.Alive()
}

func (s *Service) admitPreparedResume(paused state.Lifecycle) (string, bool, error) {
	name := ".prepared-wait-" + paused.ID + ".lock"
	if _, err := lock.AcquireExclusiveNamed(s.Store.Home.State, name); err != nil {
		return "", false, err
	}
	defer lock.ReleaseExclusiveNamed(s.Store.Home.State, name)
	record, err := state.ReadPreparedWait(s.Store.Home.State, paused.ID)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Sprintf("auto-resume-%d", time.Now().UnixNano()), true, nil
	}
	if err != nil {
		return "", false, err
	}
	if paused.Operation != record.Operation {
		return fmt.Sprintf("auto-resume-%d", time.Now().UnixNano()), true, nil
	}
	if record.Phase != "paused" || paused.Generation != record.Generation {
		return "", false, nil
	}
	record.Phase = "resume-requested"
	return "resume-" + record.Operation, true, state.WritePreparedWait(s.Store.Home.State, record)
}

func (s *Service) finishPreparedResume(paused state.Lifecycle, operation string, resumeErr error) error {
	if operation != "resume-"+paused.Operation {
		return nil
	}
	name := ".prepared-wait-" + paused.ID + ".lock"
	if _, err := lock.AcquireExclusiveNamed(s.Store.Home.State, name); err != nil {
		return err
	}
	defer lock.ReleaseExclusiveNamed(s.Store.Home.State, name)
	record, err := state.ReadPreparedWait(s.Store.Home.State, paused.ID)
	if err != nil || record.Operation != paused.Operation {
		return err
	}
	if resumeErr != nil {
		return s.preparedProblem(&record, "failed", fmt.Errorf("prepared resume failed; inspect before retrying: %w", resumeErr))
	}
	current, err := state.ReadLifecycle(s.Store.Home.State, record.ID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		// The resume ran, so its outcome is still to be read: keep the
		// admission and let the next scan reconcile it.
		return s.preparedProblem(&record, record.Phase, fmt.Errorf("prepared resume record is unreadable: %w", err))
	}
	if current.Operation != operation || current.Phase != "running" {
		return s.preparedProblem(&record, "failed", errors.New("prepared resume has no matching completed lifecycle transition; inspect before retrying"))
	}
	record.Phase, record.Problem = "cleared", ""
	return state.WritePreparedWait(s.Store.Home.State, record)
}

func (s *Service) preparedCleared(ctx context.Context, record state.PreparedWait, now time.Time, watched *fleetWakes) (bool, error) {
	if err := s.preparedDependency(record); err != nil {
		return false, err
	}
	kind, target, _ := strings.Cut(record.Condition.Until, ":")
	if kind == "pr" {
		if s.Options.PullRequestState == nil {
			return false, errors.New("prepared pull request evidence is unavailable")
		}
		probe, cancel := context.WithTimeout(ctx, ghCallTimeout)
		defer cancel()
		pull, err := s.Options.PullRequestState(probe, target)
		if err != nil {
			return false, err
		}
		switch pull.State {
		case "MERGED":
			return true, nil
		case "OPEN":
			return false, nil
		case "CLOSED":
			return false, errors.New("prepared pull request closed without merging")
		default:
			return false, errors.New("prepared pull request is unknown")
		}
	}
	return s.pauseCleared(ctx, record.Condition, now, watched)
}
