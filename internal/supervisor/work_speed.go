package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleettree"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	PROGRESS_THRESHOLD    = 20 * time.Minute
	PROGRESS_PASS_TIMEOUT = 10 * time.Second
)

// evidenceFresh is how old the monitor's look at a goblin's screen, or the
// board's reading of its family tree, may be and still count as a reading.
const evidenceFresh = 2 * time.Minute

type WorkProgress struct {
	Generation string    `json:"generation"`
	At         time.Time `json:"at"`
	Source     string    `json:"source"`
	Seconds    int64     `json:"seconds"`
	Head       string    `json:"head,omitempty"`
	Pushed     string    `json:"pushed,omitempty"`
	Gate       string    `json:"gate,omitempty"`
	Report     string    `json:"report,omitempty"`
	// Screen is the digest of the output on the goblin's terminal, Records
	// the latest write its harness's records show, and Busy the latest
	// moment a job of its own processes started or used the processor, each
	// as the last reading that could read it found it.
	Screen  string    `json:"screen,omitempty"`
	Records time.Time `json:"records,omitzero"`
	Busy    time.Time `json:"busy,omitzero"`
	Woken   bool      `json:"woken,omitempty"`
}

func (s *Service) checkProgress(ctx context.Context, watched *fleetWakes, now time.Time) error {
	if s.Options.Progress == nil {
		return nil
	}
	probe, cancel := context.WithTimeout(ctx, PROGRESS_PASS_TIMEOUT)
	defer cancel()
	if watched.Progress == nil {
		watched.Progress = map[string]WorkProgress{}
	}
	var problems error
	database := s.Store.Snapshot()
	type observation struct {
		meta               state.TaskMeta
		prior              WorkProgress
		isNew              bool
		head, pushed, gate string
		err                error
	}
	tasks := liveTasks(s.Store.Home.State)
	observations := make(chan observation, len(tasks))
	pending := map[string]bool{}
	seen := map[string]bool{}
	for _, meta := range tasks {
		seen[meta.ID] = true
		prior := watched.Progress[meta.ID]
		isNew := prior.Generation != meta.SpawnGen || prior.At.IsZero()
		if isNew {
			prior = WorkProgress{Generation: meta.SpawnGen, At: now, Source: "watch started"}
		}
		record, err := state.ReadLifecycle(s.Store.Home.State, meta.ID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			problems = errors.Join(problems, err)
			continue
		}
		if record.Generation == meta.SpawnGen && record.SuppressesMonitoring(s.Store.Home.State) {
			watched.Progress[meta.ID] = prior
			continue
		}
		pending[meta.ID] = true
		go func() {
			head, pushed, gate, err := s.observeWork(probe, meta, database.Tasks[meta.ID].GateStep)
			observations <- observation{meta: meta, prior: prior, isNew: isNew, head: head, pushed: pushed, gate: gate, err: err}
		}()
	}
	for id := range watched.Progress {
		if !seen[id] {
			delete(watched.Progress, id)
		}
	}
	// Every goblin's own progress is measured before any stall is judged, so
	// a parent waiting on its helper can take the helper's as its own.
	s.mu.Lock()
	trees := s.trees
	s.mu.Unlock()
	unread := map[string][]string{}
	measured := map[string]bool{}
	awaitedHelper := map[string]string{}
	helpers := map[string]state.TaskMeta{}
	for _, meta := range tasks {
		if meta.Parent != "" {
			helpers[strings.ToLower(meta.ID)] = meta
		}
	}
measuring:
	for len(pending) > 0 {
		var observed observation
		select {
		case observed = <-observations:
			delete(pending, observed.meta.ID)
		case <-probe.Done():
			for id := range pending {
				problems = errors.Join(problems, fmt.Errorf("progress for %s: %w", id, probe.Err()))
			}
			break measuring
		}
		if observed.err != nil {
			problems = errors.Join(problems, observed.err)
			continue
		}
		meta, prior, isNew := observed.meta, observed.prior, observed.isNew
		head, pushed, gate := observed.head, observed.pushed, observed.gate
		lines, err := state.TailStatus(s.Store.Home.State, meta.ID, 200)
		if err != nil {
			problems = errors.Join(problems, err)
			continue
		}
		var reportedAt time.Time
		report := ""
		for index := len(lines) - 1; index >= 0; index-- {
			stamp, event := state.SplitStatus(lines[index])
			if born := spawnTime(meta.SpawnGen); !born.IsZero() && stamp.Before(born.Truncate(time.Second)) {
				break
			}
			if reportKind(event) != "" {
				reportedAt, report = stamp, event
				break
			}
		}
		tree, isTreeRead := trees[meta.ID]
		screen, records, busy, missing := s.workEvidence(meta, tree, isTreeRead, now)
		source := ""
		if !isNew && head != prior.Head {
			source = "commit"
		} else if !isNew && pushed != prior.Pushed {
			source = "push"
		} else if !isNew && gate != prior.Gate {
			source = "gate step"
		} else if report != prior.Report && !reportedAt.IsZero() && !reportedAt.After(now) && !reportedAt.Before(prior.At) {
			source = "status report"
		} else if screen != "" && prior.Screen != "" && screen != prior.Screen {
			source = "screen output"
		} else if !prior.Records.IsZero() && records.After(prior.Records) {
			source = "transcript"
		} else if !prior.Busy.IsZero() && busy.After(prior.Busy) {
			source = "its own processes"
		}
		if source != "" {
			prior.At, prior.Source, prior.Woken = now, source, false
		}
		prior.Head, prior.Pushed, prior.Gate, prior.Report = head, pushed, gate, report
		if screen != "" {
			prior.Screen = screen
		}
		if records.After(prior.Records) {
			prior.Records = records
		}
		if busy.After(prior.Busy) {
			prior.Busy = busy
		}
		unread[meta.ID] = missing
		watched.Progress[meta.ID] = prior
		measured[meta.ID] = true
		standingAt, standing := standingReport(lines, spawnTime(meta.SpawnGen))
		phase, _, target, isReported := reportedProgress(s.statusTail, meta.ID, database.Reviews, standingAt, standing)
		if helper, isHelper := helpers[strings.ToLower(target)]; isReported && phase == "waiting" && isHelper && strings.EqualFold(helper.Parent, meta.ID) {
			awaitedHelper[meta.ID] = helper.ID
		}
	}
	for id := range measured {
		prior := watched.Progress[id]
		// While a goblin waits on its helper, the helper's progress is its
		// own, and a helper that stalls reports that itself; a paused one
		// reports nothing, so its parent's stall is the parent's to report.
		helper, isWaiting := awaitedHelper[id]
		if progress := watched.Progress[helper]; isWaiting && progress.At.After(prior.At) {
			prior.At, prior.Source, prior.Woken = progress.At, progress.Source+" (helper "+helper+")", false
		}
		prior.Seconds = max(0, int64(now.Sub(prior.At)/time.Second))
		// A goblin whose latest report is done, a question, or a wait on CI,
		// a deploy, the Overlord or memory expects no progress until that
		// changes, and each of those wakes the CFO on its own when it does.
		kind := reportKind(prior.Report)
		isReportedElsewhere := kind == "done" || kind == "blocked" || kind == "failed" || slices.ContainsFunc([]string{"ci", "deploy", "overlord", "memory"}, func(on string) bool { return strings.HasPrefix(prior.Report, "waiting on "+on+": ") })
		if now.Sub(prior.At) >= PROGRESS_THRESHOLD && !prior.Woken && !(isWaiting && measured[helper]) && !isReportedElsewhere {
			detail := fmt.Sprintf("progress_stalled: %s has shown no new commit, push, gate step, status report, screen output, transcript write or processor use by its own processes for %d minutes; last progress: %s; next: inspect its work and decide whether it should pause", id, prior.Seconds/60, prior.Source)
			if missing := unread[id]; len(missing) > 0 {
				detail += "; not read: " + bounded(strings.Join(missing, ", "), 400)
			}
			if err := raiseFleetWake(s.Store.Home.State, "check", id, detail); err != nil {
				problems = errors.Join(problems, err)
			} else {
				prior.Woken = true
			}
		}
		watched.Progress[id] = prior
	}
	return problems
}

// workEvidence is what the goblin's terminal and harness show of its work,
// read where the rest of the supervisor reads it: the digest of the output on
// its screen from the monitor's last look at it, and from the board's last
// reading of its family tree the latest write its own records show (its
// conversation, or a sub-agent, shell, monitor or gate step under it) and the
// latest moment a job of its own processes started or used the processor. A
// helper hanging under it is a goblin of its own, whose progress its parent
// takes only while it waits on it. Evidence that could not be read is left
// empty and named in missing.
func (s *Service) workEvidence(meta state.TaskMeta, tree fleettree.Tree, isTreeRead bool, now time.Time) (screen string, records, busy time.Time, missing []string) {
	observation, err := monitor.ReadObservation(s.Store.Home.State, meta.ID)
	if err != nil || observation.EndpointVerdict != monitor.ProbePresent || observation.ScreenUnreadSince != nil ||
		observation.Endpoint != (herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}).String() ||
		observation.LastObserved.Before(spawnTime(meta.SpawnGen)) || now.Sub(observation.LastObserved) > evidenceFresh || observation.LastObserved.After(now.Add(time.Minute)) {
		missing = append(missing, "its screen, which the monitor has not read in the last two minutes")
	} else {
		screen = observation.Digest
	}
	if !isTreeRead || tree.Generation != meta.SpawnGen || now.Sub(tree.FetchedAt) > evidenceFresh {
		return screen, records, busy, append(missing, "its transcript and processes, which the board has not read in the last two minutes")
	}
	missing = append(missing, tree.Unread...)
	tree.Children = slices.DeleteFunc(slices.Clone(tree.Children), func(child fleettree.Node) bool { return child.Kind == fleettree.KindHelper })
	for _, child := range tree.Children {
		if child.Kind == fleettree.KindProcess && child.LastActivity.After(busy) {
			busy = child.LastActivity
		}
	}
	return screen, tree.ActivityAt(), busy, missing
}

func (s *Service) observeWork(ctx context.Context, meta state.TaskMeta, gate string) (string, string, string, error) {
	head, err := runOutput(ctx, s.Options.Progress, meta.Worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", fmt.Errorf("progress for %s: %w", meta.ID, err)
	}
	refs, err := runOutput(ctx, s.Options.Progress, meta.Worktree, "git", "for-each-ref", "--format=%(HEAD)%09%(refname)%09%(objectname)", "refs/heads", "refs/remotes")
	if err != nil {
		return "", "", "", fmt.Errorf("push progress for %s: %w", meta.ID, err)
	}
	branch, pushed := "", ""
	for _, line := range strings.Split(refs, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) == 3 && fields[0] == "*" {
			branch = strings.TrimPrefix(fields[1], "refs/heads/")
		}
	}
	for _, line := range strings.Split(refs, "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) == 3 && branch != "" && fields[1] == "refs/remotes/origin/"+branch {
			pushed = fields[2]
		}
	}
	if meta.Mode == "no-mistakes" && branch != "" && s.Options.Gate != nil {
		progress, err := s.Options.Gate.Progress(ctx, meta.Project, branch)
		if err != nil && !errors.Is(err, pipeline.ErrNoProgress) {
			return "", "", "", fmt.Errorf("gate progress for %s: %w", meta.ID, err)
		}
		if err == nil {
			steps, err := json.Marshal(progress.Steps)
			if err != nil {
				return "", "", "", err
			}
			gate = progress.RunID + ":" + string(steps)
		}
	}
	return head, pushed, gate, nil
}
