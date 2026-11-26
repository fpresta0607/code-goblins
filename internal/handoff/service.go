package handoff

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/lifecycle"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// Service hands a task off, brings it back and cancels its handoff.
type Service struct {
	StateDir string
	// DataDir holds each task's brief and the CEO's answers to it, as
	// data/<task>/, which its handoff may not copy.
	DataDir string
	// Commands runs git in the task's worktree and gh.
	Commands execx.Runner
	// Lifecycle pauses and resumes the task (goblins pause and resume).
	Lifecycle func(context.Context, lifecycle.Request) (state.Lifecycle, error)
	// GateRun names the gate run active or parked on the task's branch, or
	// "" when none is.
	GateRun func(ctx context.Context, meta state.TaskMeta, branch string) (string, error)
	// Screen is what a public issue on the task's repository holding text
	// may not say, as goblins issue file reads it.
	Screen func(ctx context.Context, meta state.TaskMeta, repository, text string) (tickets.Screen, error)
}

// task is what every handoff command reads of the task first.
type task struct {
	meta       state.TaskMeta
	commands   commands
	branch     string
	repository string
}

func (service Service) read(ctx context.Context, id string) (task, error) {
	meta, err := state.ReadTaskMeta(service.StateDir, id)
	if err != nil {
		return task{}, err
	}
	if meta.Worktree == "" {
		return task{}, fmt.Errorf("%s has no worktree", id)
	}
	repository, err := (tickets.GitHub{Commands: service.Commands}).RepositoryOf(ctx, meta.Worktree)
	if err != nil {
		return task{}, err
	}
	run := commands{runner: service.Commands, worktree: meta.Worktree, repository: repository}
	branch, err := run.branch(ctx)
	if err != nil {
		return task{}, err
	}
	return task{meta: meta, commands: run, branch: branch, repository: repository}, nil
}

// lock holds the task's handoff lock, so two handoff commands never act on
// one task at once.
func (service Service) lock(id string) (func() error, error) {
	name := ".handoff-" + id + ".lock"
	if _, err := lock.AcquireExclusiveNamed(service.StateDir, name); err != nil {
		return nil, err
	}
	return func() error { return lock.ReleaseExclusiveNamed(service.StateDir, name) }, nil
}

// Handoff hands task id off to a cloud session and returns its record and
// the prompt to paste into one. It refuses, naming why, before it changes
// anything; past the pause, each step is recorded as it runs, so a failure
// says which ran and a second Handoff goes on from there.
func (service Service) Handoff(ctx context.Context, id string, isWIP bool) (record Record, prompt string, err error) {
	unlock, err := service.lock(id)
	if err != nil {
		return Record{}, "", err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	record, isActive, err := readActiveRecord(service.StateDir, id)
	if err != nil {
		return Record{}, "", err
	}
	if record.IsHandedOff() {
		return record, "", fmt.Errorf("%s is already handed off in %s; goblins handoff return or cancel ends that first", id, record.PullRequest)
	}
	if !isActive {
		record = Record{Task: id}
	}
	current, err := service.read(ctx, id)
	if err != nil {
		return record, "", err
	}
	meta, run := current.meta, current.commands
	// A handoff whose pause no longer holds, as one a plain resume ended
	// after it wrote only the fallback, or one of an earlier session, takes
	// its pause again.
	if record.hasStep(StepPause) {
		isPaused, err := isPausedForHandoff(service.StateDir, meta)
		if err != nil {
			return record, "", err
		}
		if !isPaused {
			record.Steps = nil
		}
	}
	if err := service.refuse(ctx, current, record, isActive, isWIP); err != nil {
		return record, "", err
	}
	defaultBranch, err := run.defaultBranch(ctx)
	if err != nil {
		return record, "", err
	}
	goblinPR, err := run.goblinPullRequest(ctx, current.branch)
	if err != nil {
		return record, "", err
	}
	if !record.hasStep(StepPush) && goblinPR != nil && !goblinPR.IsDraft && goblinPR.isCIRunning() {
		return record, "", fmt.Errorf("CI is still running on %s, and a push now would cancel that run after it billed its minutes; hand off once it has ended", goblinPR.URL)
	}
	save := func(step string) error {
		record.Steps = append(record.Steps, step)
		return WriteRecord(service.StateDir, record)
	}

	if !record.hasStep(StepPause) {
		// The record is written before the pause, so a pause that took hold
		// though its answer was lost is taken up by a second handoff, or ended
		// by cancel.
		if !isActive {
			record.Started, record.Branch = time.Now().UTC(), current.branch
			if err := WriteRecord(service.StateDir, record); err != nil {
				return record, "", err
			}
		}
		isPaused, err := isPausedForHandoff(service.StateDir, meta)
		if err != nil {
			return record, "", err
		}
		if !isPaused {
			paused, err := service.Lifecycle(ctx, lifecycle.Request{ID: id, Generation: meta.SpawnGen, Operation: fmt.Sprintf("handoff-%d", time.Now().UnixNano()), Action: "pause", Reason: "handoff", IsWatched: true})
			if err != nil {
				return record, "", fmt.Errorf("pause %s for its handoff: %w; goblins handoff again goes on if the pause took hold, and goblins handoff cancel resumes it", id, err)
			}
			if paused.Phase != "paused" {
				return record, "", fmt.Errorf("pause %s for its handoff ended %s: %s", id, paused.Phase, strings.Join(paused.Problems, "; "))
			}
		}
		if err := save(StepPause); err != nil {
			return record, "", err
		}
	}
	paused, err := state.ReadLifecycle(service.StateDir, id)
	if err != nil {
		return record, "", err
	}
	if !paused.HandoffSaved || paused.HandoffFallback {
		return record, "", fmt.Errorf("%s wrote no handoff of its own, and a handoff never publishes the one its pause wrote for it, which carries its last status line word for word; it stays paused: goblins resume or goblins handoff cancel resumes it, and a new goblins handoff then asks it again", id)
	}
	written, err := fsx.ReadFile(paused.Handoff)
	if err != nil {
		return record, "", fmt.Errorf("read the handoff %s wrote: %w", id, err)
	}

	if !record.hasStep(StepCommit) {
		isClean, err := run.isClean(ctx)
		if err != nil {
			return record, "", err
		}
		if !isClean && !isWIP {
			return record, "", fmt.Errorf("%s left uncommitted changes at its pause and stays paused; hand it off again with --wip to commit them as wip(handoff): %s", id, id)
		}
		if !isClean {
			if _, err := run.git(ctx, "add", "--all"); err != nil {
				return record, "", err
			}
			err := withBodyFile("wip(handoff): "+id, func(path string) error {
				_, err := run.git(ctx, "commit", "--quiet", "--file", path)
				return err
			})
			if err != nil {
				return record, "", err
			}
		}
		if err := save(StepCommit); err != nil {
			return record, "", err
		}
	}
	head, err := run.head(ctx)
	if err != nil {
		return record, "", err
	}
	packet, err := service.packet(ctx, current, string(written), head, defaultBranch)
	if err != nil {
		return record, "", err
	}
	text := packet.Text()
	screen, err := service.Screen(ctx, meta, current.repository, text)
	if err != nil {
		return record, "", fmt.Errorf("read what the packet may not say: %w", err)
	}
	refusal := Refusal(screen, text)
	if refusal == "" {
		private, err := service.privateTexts(meta)
		if err != nil {
			return record, "", err
		}
		refusal = CopyRefusal(string(written), private)
	}
	if refusal != "" {
		return record, "", fmt.Errorf("%s, so nothing is published and %s stays paused: goblins handoff cancel resumes it to correct its handoff or commits, and a new goblins handoff asks it again", refusal, id)
	}

	if !record.hasStep(StepPush) {
		if err := run.push(ctx, "HEAD", current.branch); err != nil {
			return record, "", err
		}
		if err := save(StepPush); err != nil {
			return record, "", err
		}
	}
	if !record.hasStep(StepPullRequest) {
		if goblinPR == nil {
			url, err := run.createDraft(ctx, defaultBranch, current.branch, "wip: "+current.branch, Splice("", text))
			if err != nil {
				return record, "", err
			}
			record.PullRequest, record.IsOpenedByHandoff = url, true
			record.Steps = append(record.Steps, StepPullRequest)
			if err := save(StepPacket); err != nil {
				return record, "", err
			}
		} else {
			record.PullRequest = goblinPR.URL
			if err := save(StepPullRequest); err != nil {
				return record, "", err
			}
		}
	}
	if !record.hasStep(StepPacket) {
		pr, err := run.pullRequest(ctx, record.PullRequest)
		if err != nil {
			return record, "", err
		}
		if err := run.editBody(ctx, record.PullRequest, Splice(pr.Body, text)); err != nil {
			return record, "", err
		}
		if err := save(StepPacket); err != nil {
			return record, "", err
		}
	}
	handedOff := time.Now().UTC()
	record.Head, record.PacketDigest, record.HandedOff = head, Digest(text), &handedOff
	if err := WriteRecord(service.StateDir, record); err != nil {
		return record, "", err
	}
	return record, Prompt(current.repository, record.PullRequest), nil
}

// refuse says why task may not be handed off now.
func (service Service) refuse(ctx context.Context, current task, record Record, isActive, isWIP bool) error {
	meta := current.meta
	if meta.Mode == "local-only" {
		return fmt.Errorf("%s is local-only, so its work may not be published", meta.ID)
	}
	if meta.Parent != "" {
		return fmt.Errorf("%s is a helper of %s; hand off %s instead", meta.ID, meta.Parent, meta.Parent)
	}
	helpers, err := state.HelpersOf(service.StateDir, meta.ID)
	if err != nil {
		return err
	}
	if len(helpers) > 0 {
		return fmt.Errorf("%s has a helper, %s, still at work; merge or stop it first", meta.ID, helpers[0].ID)
	}
	gateRun, err := service.GateRun(ctx, meta, current.branch)
	if err != nil {
		return fmt.Errorf("read whether a gate run holds %s: %w", current.branch, err)
	}
	if gateRun != "" {
		return fmt.Errorf("gate run %s is active or parked on %s, and the pipeline owns the branch while it runs", gateRun, current.branch)
	}
	if !record.hasStep(StepPause) {
		lifecycleRecord, err := state.ReadLifecycle(service.StateDir, meta.ID)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// An earlier session's record says nothing of the goblin that runs now.
		isCurrentSession := lifecycleRecord.Generation == meta.SpawnGen
		isTakenUp := isActive && lifecycleRecord.Phase == "paused" && lifecycleRecord.Pause != nil && lifecycleRecord.Pause.Reason == "handoff"
		if isCurrentSession && lifecycleRecord.Phase != "" && lifecycleRecord.Phase != "running" && !isTakenUp {
			return fmt.Errorf("%s is %s, and only a running goblin writes the handoff its packet publishes; resume it first", meta.ID, lifecycleRecord.Phase)
		}
	}
	if !record.hasStep(StepCommit) && !isWIP {
		isClean, err := current.commands.isClean(ctx)
		if err != nil {
			return err
		}
		if !isClean {
			return fmt.Errorf("the worktree of %s holds uncommitted changes; commit them, or pass --wip to commit them as wip(handoff): %s", meta.ID, meta.ID)
		}
	}
	return nil
}

// privateTexts are what the packet never carries of task meta: its brief,
// every Markdown file of its data directory, which hold the CEO's answers to
// it, and its status lines.
func (service Service) privateTexts(meta state.TaskMeta) ([]string, error) {
	paths, err := filepath.Glob(filepath.Join(service.DataDir, meta.ID, "*.md"))
	if err != nil {
		return nil, err
	}
	if meta.Brief != "" {
		paths = append(paths, meta.Brief)
	}
	var texts []string
	for _, path := range paths {
		data, err := fsx.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		texts = append(texts, string(data))
	}
	lines, err := state.TailStatus(service.StateDir, meta.ID, math.MaxInt)
	if err != nil {
		return nil, err
	}
	for _, line := range lines {
		_, event := state.SplitStatus(line)
		texts = append(texts, event)
	}
	return texts, nil
}

func (service Service) packet(ctx context.Context, current task, written, head, defaultBranch string) (Packet, error) {
	commits, err := current.commands.commitsSince(ctx, defaultBranch)
	if err != nil {
		return Packet{}, err
	}
	packet := Packet{Handoff: written, Branch: current.branch, Head: head, DefaultBranch: defaultBranch, Commits: commits, Mode: current.meta.Mode}
	ticket, err := tickets.ReadRecord(service.StateDir, current.meta.ID)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return Packet{}, err
	}
	for _, issue := range append([]tickets.Record{ticket}, ticket.Issues...) {
		if issue.Number > 0 && issue.URL != "" && !strings.Contains(strings.Join(packet.Issues, "\n"), issue.URL) {
			packet.Issues = append(packet.Issues, issue.URL)
		}
	}
	delivery, err := state.ReadTaskDelivery(current.meta)
	if err != nil {
		return Packet{}, err
	}
	if delivery != nil {
		workflow, err := fsx.ReadFile(filepath.Join(current.meta.Worktree, workflowPath))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Packet{}, err
		}
		packet.Commands = VerificationCommands(delivery.Verification, LinuxExcept(string(workflow)))
	}
	return packet, nil
}

// Prompt is what the Overlord pastes into a cloud session opened on
// repository.
func Prompt(repository, pullRequest string) string {
	return fmt.Sprintf("Repository %s. Read the goblins handoff packet in the body of %s and follow its rules: branch from the head it names, push only to your own branch and open your pull request into the branch it names.", repository, pullRequest)
}

// Return brings the cloud session's pull request into the goblin's branch,
// pushes it and resumes the goblin, which validates the whole branch
// through its delivery mode before it reports done. url picks the cloud's
// pull request when more than one targets the branch.
func (service Service) Return(ctx context.Context, id, url string) (record Record, err error) {
	unlock, err := service.lock(id)
	if err != nil {
		return Record{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	record, err = ReadRecord(service.StateDir, id)
	if err != nil || !record.IsHandedOff() {
		return record, errors.Join(err, fmt.Errorf("%s is not handed off", id))
	}
	current, err := service.read(ctx, id)
	if err != nil {
		return record, err
	}
	run := current.commands
	save := func(step string) error {
		record.ReturnSteps = append(record.ReturnSteps, step)
		return WriteRecord(service.StateDir, record)
	}

	// Once merged and pushed, GitHub reads the cloud's pull request as
	// merged, so only a return that has not merged it yet looks it up.
	if !record.hasReturnStep(ReturnStepMerge) {
		cloud, err := service.cloudPullRequest(ctx, current, url, record.Press)
		if err != nil {
			return record, err
		}
		if err := service.checkUnmoved(ctx, current, record); err != nil {
			return record, err
		}
		if _, err := run.git(ctx, "fetch", "--quiet", "origin", "refs/heads/"+cloud.HeadRefName); err != nil {
			return record, err
		}
		fetched, err := run.git(ctx, "rev-parse", "FETCH_HEAD")
		if err != nil {
			return record, err
		}
		if fetched != cloud.HeadRefOid {
			return record, fmt.Errorf("%s moved while it was fetched; bring it back again", cloud.URL)
		}
		commits, err := run.git(ctx, "log", "--reverse", "--format=%h", record.Head+".."+cloud.HeadRefOid)
		if err != nil {
			return record, err
		}
		isDescendant, err := run.gitSucceeds(ctx, "merge-base", "--is-ancestor", record.Head, cloud.HeadRefOid)
		if err != nil {
			return record, err
		}
		if isDescendant {
			_, err = run.git(ctx, "merge", "--quiet", "--ff-only", cloud.HeadRefOid)
		} else {
			_, err = run.git(ctx, "merge", "--quiet", "--no-ff", "--no-edit", cloud.HeadRefOid)
		}
		if err != nil {
			_, abortErr := run.git(ctx, "merge", "--abort")
			return record, errors.Join(fmt.Errorf("%s does not merge into %s, so the merge was aborted, the worktree is as it was and %s is still handed off: %w", cloud.URL, current.branch, id, err), abortErr)
		}
		if record.ReturnHead, err = run.head(ctx); err != nil {
			return record, err
		}
		record.CloudPullRequest, record.CloudCommits = cloud.URL, strings.Fields(commits)
		if err := save(ReturnStepMerge); err != nil {
			return record, err
		}
	}
	if !record.hasReturnStep(ReturnStepPush) {
		if err := service.checkPaused(current); err != nil {
			return record, err
		}
		head, err := run.head(ctx)
		if err != nil {
			return record, err
		}
		if head != record.ReturnHead {
			return record, fmt.Errorf("%s moved since the cloud's work was merged into it at %s (now %s), so it is not pushed; bring the branch back to %s and return again", current.branch, record.ReturnHead, head, record.ReturnHead)
		}
		if err := run.push(ctx, record.ReturnHead, current.branch); err != nil {
			return record, err
		}
		if err := save(ReturnStepPush); err != nil {
			return record, err
		}
	}
	if !record.hasReturnStep(ReturnStepClose) {
		if err := service.closeOwnDraft(ctx, current, record); err != nil {
			return record, err
		}
		if err := save(ReturnStepClose); err != nil {
			return record, err
		}
	}
	note := fmt.Sprintf("Your task was handed off to a cloud session, and goblins handoff return brought its work back onto your branch: commits %s from %s. Read that pull request's body for what the cloud session ran and what it could not run. Your delivery mode, %s, now validates the whole branch, the cloud's commits included, before you report done.", strings.Join(record.CloudCommits, ", "), record.CloudPullRequest, current.meta.Mode)
	if err := service.resume(ctx, current.meta, note); err != nil {
		return record, err
	}
	record.ReturnSteps = append(record.ReturnSteps, ReturnStepResume)
	ended := time.Now().UTC()
	record.Ended, record.Outcome = &ended, OutcomeReturned
	return record, WriteRecord(service.StateDir, record)
}

// cloudPullRequest is the open pull request into the goblin's branch that
// Return brings back: url when given, or the only one there is. A
// teammate's is brought back only with the Overlord's press on it at the
// head GitHub reports now, and one he pressed at another head is not
// brought back whoever opened it, since his press named the head he saw.
func (service Service) cloudPullRequest(ctx context.Context, current task, url string, press *Press) (pullRequest, error) {
	prs, err := current.commands.pullRequests(ctx, "--base", current.branch)
	if err != nil {
		return pullRequest{}, err
	}
	var picked []pullRequest
	for _, pr := range prs {
		if url == "" || pr.URL == url {
			picked = append(picked, pr)
		}
	}
	switch {
	case len(picked) == 0 && url != "":
		return pullRequest{}, fmt.Errorf("%s is not an open pull request into %s", url, current.branch)
	case len(picked) == 0:
		return pullRequest{}, fmt.Errorf("no open pull request targets %s yet", current.branch)
	case len(picked) > 1:
		var urls []string
		for _, pr := range picked {
			urls = append(urls, pr.URL)
		}
		return pullRequest{}, fmt.Errorf("%d open pull requests target %s, so name one with --pr: %s", len(picked), current.branch, strings.Join(urls, ", "))
	}
	cloud := picked[0]
	if cloud.IsCrossRepository {
		return pullRequest{}, fmt.Errorf("%s comes from another repository, and a cloud session pushes to a branch of this one", cloud.URL)
	}
	account, err := current.commands.account(ctx)
	if err != nil {
		return pullRequest{}, err
	}
	if press != nil && press.PullRequest == cloud.URL && press.Head != cloud.HeadRefOid {
		return pullRequest{}, fmt.Errorf("%s moved to %s since the Overlord pressed Bring back on it at %s, and bringing back its new head needs the Overlord's press on it", cloud.URL, cloud.HeadRefOid, press.Head)
	}
	isPressed := press != nil && press.PullRequest == cloud.URL
	if !strings.EqualFold(cloud.Author.Login, account) && !isPressed {
		return pullRequest{}, fmt.Errorf("%s was opened by %s, not by %s, whom gh works as, and bringing a teammate's pull request onto a goblin's branch needs the Overlord's press: Bring back on its Cloud work ready item at its head %s", cloud.URL, cloud.Author.Login, account, cloud.HeadRefOid)
	}
	return cloud, nil
}

// checkUnmoved says why the goblin's branch may not take the cloud's work:
// a goblin of a later session runs on it, it moved since the handoff on
// GitHub or in the worktree, the worktree is not clean, or its ready pull
// request's CI still runs.
func (service Service) checkUnmoved(ctx context.Context, current task, record Record) error {
	if err := service.checkPaused(current); err != nil {
		return err
	}
	run := current.commands
	local, err := run.head(ctx)
	if err != nil {
		return err
	}
	remote, err := run.remoteHead(ctx, current.branch)
	if err != nil {
		return err
	}
	if local != record.Head || remote != record.Head {
		return fmt.Errorf("%s moved since the handoff (handed off at %s, now %s here and %s on GitHub), so the cloud's work is not brought onto it; cancel the handoff or bring the branch back to %s", current.branch, record.Head, local, remote, record.Head)
	}
	isClean, err := run.isClean(ctx)
	if err != nil {
		return err
	}
	if !isClean {
		return fmt.Errorf("the worktree of %s holds uncommitted changes, so the cloud's work is not brought onto it", current.meta.ID)
	}
	pr, err := run.pullRequest(ctx, record.PullRequest)
	if err != nil {
		return err
	}
	if !pr.IsDraft && pr.isCIRunning() {
		return fmt.Errorf("CI is still running on %s, and a push now would cancel that run after it billed its minutes; bring the work back once it has ended", record.PullRequest)
	}
	return nil
}

// checkPaused says why the cloud's work may not be brought onto the
// goblin's branch when a goblin of a later session runs on it.
func (service Service) checkPaused(current task) error {
	isPaused, err := isPausedForHandoff(service.StateDir, current.meta)
	if err != nil {
		return err
	}
	if !isPaused {
		return fmt.Errorf("%s is not paused for its handoff, since a goblin of a later session runs on %s, so the cloud's work is not brought onto it; cancel the handoff", current.meta.ID, current.branch)
	}
	return nil
}

// closeOwnDraft closes the draft the handoff opened for a gated task, so its
// gate opens its own ready pull request; a pull request the handoff reused
// stays as it is.
func (service Service) closeOwnDraft(ctx context.Context, current task, record Record) error {
	if !record.IsOpenedByHandoff || current.meta.Mode != "no-mistakes" {
		return nil
	}
	pr, err := current.commands.pullRequest(ctx, record.PullRequest)
	if err != nil {
		return err
	}
	if pr.State != "OPEN" {
		return nil
	}
	return current.commands.closePullRequest(ctx, record.PullRequest)
}

// isPausedForHandoff says the goblin of meta's session is paused with the
// handoff reason.
func isPausedForHandoff(stateDir string, meta state.TaskMeta) (bool, error) {
	current, err := state.ReadLifecycle(stateDir, meta.ID)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return current.Generation == meta.SpawnGen && current.Phase == "paused" && current.Pause != nil && current.Pause.Reason == "handoff", nil
}

func (service Service) resume(ctx context.Context, meta state.TaskMeta, note string) error {
	isPaused, err := isPausedForHandoff(service.StateDir, meta)
	if err != nil || !isPaused {
		return err
	}
	_, err = service.Lifecycle(ctx, lifecycle.Request{ID: meta.ID, Generation: meta.SpawnGen, Operation: fmt.Sprintf("handoff-end-%d", time.Now().UnixNano()), Action: "resume", Reason: "handoff ended", ResumeNote: note, IsWatched: true, IsHandoffEnd: true})
	if err != nil {
		return fmt.Errorf("resume %s: %w", meta.ID, err)
	}
	return nil
}

// Cancel ends task id's handoff without the cloud's work: it closes the
// draft the handoff opened for a gated task, marks the packet cancelled and
// resumes the goblin.
func (service Service) Cancel(ctx context.Context, id string) (record Record, err error) {
	unlock, err := service.lock(id)
	if err != nil {
		return Record{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	record, isActive, err := readActiveRecord(service.StateDir, id)
	if err != nil {
		return record, err
	}
	if !isActive {
		return record, fmt.Errorf("%s is not handed off", id)
	}
	current, err := service.read(ctx, id)
	if err != nil {
		return record, err
	}
	if record.hasStep(StepPacket) {
		pr, err := current.commands.pullRequest(ctx, record.PullRequest)
		if err != nil {
			return record, err
		}
		if marked := MarkCancelled(pr.Body); marked != pr.Body {
			if err := current.commands.editBody(ctx, record.PullRequest, marked); err != nil {
				return record, err
			}
		}
	}
	if record.PullRequest != "" {
		if err := service.closeOwnDraft(ctx, current, record); err != nil {
			return record, err
		}
	}
	note := fmt.Sprintf("Your task's handoff to a cloud session was cancelled: your branch %s is as you left it, without the cloud's work, so carry on from it.", current.branch)
	if err := service.resume(ctx, current.meta, note); err != nil {
		return record, err
	}
	ended := time.Now().UTC()
	record.Ended, record.Outcome = &ended, OutcomeCancelled
	return record, WriteRecord(service.StateDir, record)
}
