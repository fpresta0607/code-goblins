package supervisor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/fpresta0607/code-goblins/internal/githubapi"
	"github.com/fpresta0607/code-goblins/internal/handoff"
	"github.com/fpresta0607/code-goblins/internal/lock"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/train"
)

// CloudHandoff is a handed-off task as its card and the Command Center see
// it: the pull request its packet is in, and the cloud's pull requests into
// its branch, each a Cloud work ready item until it is left or its head
// moves.
type CloudHandoff struct {
	PullRequest string                  `json:"pull_request"`
	Cloud       []handoff.CloudSighting `json:"cloud"`
}

// handedOffGoblins is the goblins whose tasks are handed off in their
// current session, each with its handoff record.
func handedOffGoblins(stateDir string, goblins []ciGoblin) map[string]handoff.Record {
	handedOff := map[string]handoff.Record{}
	for _, goblin := range goblins {
		if record, isHandedOff, err := handoff.HandedOff(stateDir, goblin.meta); err == nil && isHandedOff {
			handedOff[goblin.id] = record
		}
	}
	return handedOff
}

// isHandedOffWork says pr is a handed-off task's, its goblin's own or one
// into its branch, which no pr_health wake tells of while the task is in
// the cloud.
func isHandedOffWork(handedOff map[string]handoff.Record, goblin string, pr ghPullRequest) bool {
	if _, isGoblins := handedOff[goblin]; isGoblins {
		return true
	}
	for _, record := range handedOff {
		if record.Branch == pr.BaseRefName {
			return true
		}
	}
	return false
}

// sightCloudWork records, for each goblin of goblins handedOff names, the
// open pull requests listed into its branch, and refreshes the board when
// one is new or moved. With the account gh works as unread, it sights none,
// so none is told as a teammate's against no account, and that read fails
// as the train's does, once it failed failingPasses polls of repo in a row.
func (s *Service) sightCloudWork(ctx context.Context, github *githubapi.Client, w *fleetWakes, repo string, goblins []ciGoblin, handedOff map[string]handoff.Record, listed []train.PullRequest, owners *fleetOwners, now time.Time) error {
	if len(handedOff) == 0 {
		return nil
	}
	account, err := owners.account(ctx, github)
	if err != nil {
		return w.failing("cloud viewer:"+repo, fmt.Errorf("cloud work: read the account gh works as in %s: %w", repo, err))
	}
	delete(w.Failing, "cloud viewer:"+repo)
	open := make([]handoff.OpenPullRequest, 0, len(listed))
	for _, pr := range listed {
		open = append(open, handoff.OpenPullRequest{URL: pr.URL, Title: pr.Title, Base: pr.BaseRefName, Head: pr.HeadRefOid, Author: pr.Author.Login, IsCrossRepository: pr.IsCrossRepository})
	}
	var errs error
	isNew := false
	for _, goblin := range goblins {
		if _, isHandedOff := handedOff[goblin.id]; !isHandedOff {
			continue
		}
		sighted, err := handoff.Sight(s.Store.Home.State, goblin.meta, open, account, now)
		isNew = isNew || sighted
		errs = errors.Join(errs, err)
	}
	if isNew {
		s.notify()
	}
	return errs
}

// cloudWorkRequest is Bring back or Leave it pressed on a Cloud work ready
// item, naming the task, its session, the cloud's pull request and the head
// the item showed.
type cloudWorkRequest struct {
	Task        string `json:"task"`
	Generation  string `json:"generation"`
	Action      string `json:"action"`
	PullRequest string `json:"pull_request"`
	Head        string `json:"head"`
}

// cloudWork serves POST /api/tasks/cloud. Leave it closes the item until
// its head moves. Bring back records the Overlord's press on the head the
// item showed, which is what lets a teammate's pull request back, as it
// runs goblins handoff return on that pull request in its turn, since it
// resumes the goblin: like a Resume, it waits for memory, disk and a launch
// under way rather than being refused.
func (h *HTTP) cloudWork(w http.ResponseWriter, r *http.Request) {
	var input cloudWorkRequest
	if err := decodeBody(w, r, &input, 4096); err != nil {
		apiError(w, 400, err.Error())
		return
	}
	if state.ValidTaskID(input.Task) != nil || input.Action != "bring_back" && input.Action != "leave" || !githubPullRequest.MatchString(input.PullRequest) || !isCommitHash(input.Head) {
		apiError(w, 400, "Invalid cloud work action")
		return
	}
	s := h.Service
	if s.Options.Dispatch == nil {
		apiError(w, 409, "This board cannot control task processes")
		return
	}
	var err error
	if input.Action == "leave" {
		err = s.leaveCloudWork(input)
	} else {
		err = s.askBringBack(input)
	}
	if err != nil {
		status := 500
		if errors.As(err, new(taskRefusal)) {
			status = 409
		}
		apiError(w, status, err.Error())
		return
	}
	s.mu.Lock()
	revision := s.revision
	s.mu.Unlock()
	respond(w, 202, struct {
		Accepted bool   `json:"accepted"`
		Revision uint64 `json:"revision"`
	}{true, revision})
}

func isCommitHash(head string) bool {
	if len(head) != 40 {
		return false
	}
	for _, digit := range head {
		if (digit < '0' || digit > '9') && (digit < 'a' || digit > 'f') {
			return false
		}
	}
	return true
}

// cloudSession reads the task input names, refusing a session it does not
// name.
func (s *Service) cloudSession(input cloudWorkRequest) (state.TaskMeta, error) {
	meta, err := state.ReadTaskMeta(s.Store.Home.State, input.Task)
	if err != nil || input.Generation == "" || input.Generation != meta.SpawnGen {
		return meta, taskRefusal{reason: "The task session changed; refresh its card"}
	}
	return meta, nil
}

// leaveCloudWork closes the item and withdraws a Bring back on that pull
// request still waiting its turn, so it neither presses nor returns it.
func (s *Service) leaveCloudWork(input cloudWorkRequest) error {
	meta, err := s.cloudSession(input)
	if err != nil {
		return err
	}
	s.starts.Lock()
	s.asked = slices.DeleteFunc(s.asked, func(change askedChange) bool {
		return change.task == input.Task && change.bringBack != nil && change.bringBack.PullRequest == input.PullRequest
	})
	s.starts.Unlock()
	if err := handoff.Leave(s.Store.Home.State, meta, input.PullRequest, input.Head); err != nil {
		return taskRefusal{reason: err.Error()}
	}
	s.notify()
	return nil
}

// askBringBack files the Bring back on the head the item showed to run in
// its turn, refusing a head the watch no longer sees.
func (s *Service) askBringBack(input cloudWorkRequest) error {
	meta, err := s.cloudSession(input)
	if err != nil {
		return err
	}
	s.starts.Lock()
	isUnderWay := s.starting == input.Task || s.changing[input.Task] != "" || s.isAsked(input.Task)
	s.starts.Unlock()
	if isUnderWay {
		return taskRefusal{reason: "This task is already changing"}
	}
	if err := handoff.CheckSighting(s.Store.Home.State, meta, input.PullRequest, input.Head); err != nil {
		return taskRefusal{reason: err.Error()}
	}
	s.ask(askedChange{task: input.Task, bringBack: &input})
	return nil
}

// bringBack runs a Bring back whose turn has come: it records the
// Overlord's press on the head the item showed, then runs goblins handoff
// return on the pull request it names, which resumes the goblin. One that
// must wait for memory, disk or another launch is a passing refusal.
func (s *Service) bringBack(input cloudWorkRequest) error {
	meta, err := s.cloudSession(input)
	if err != nil {
		return err
	}
	s.starts.Lock()
	if s.starting == input.Task || s.changing[input.Task] != "" {
		s.starts.Unlock()
		return taskRefusal{reason: "This task is already changing", isPassing: true}
	}
	if launching := s.launching(); launching != "" {
		s.starts.Unlock()
		return taskRefusal{reason: launching + " is starting or resuming", isPassing: true}
	}
	s.markChanging(input.Task, "resume")
	s.starts.Unlock()
	isDispatched := false
	defer func() {
		if !isDispatched {
			s.starts.Lock()
			delete(s.changing, input.Task)
			s.starts.Unlock()
			s.notify()
		}
	}()
	if err := s.checkResumeRoom(input.Task); err != nil {
		return err
	}
	if err := handoff.RecordPress(s.Store.Home.State, meta, input.PullRequest, input.Head, time.Now()); errors.Is(err, lock.ErrHeld) {
		return taskRefusal{reason: "The handoff record is in use", isPassing: true}
	} else if err != nil {
		return err
	}
	s.starts.Lock()
	delete(s.changeErrors, input.Task)
	s.starts.Unlock()
	isDispatched = true
	go func() {
		launched := make(chan struct{})
		go s.watchLaunch(input.Task, launched)
		output, err := s.runPastTheSpawnLock(s.Options.Dispatch, []string{"handoff", "return", input.Task, "--pr", input.PullRequest})
		close(launched)
		var failure taskChangeError
		if err != nil {
			failure = taskChangeError{Message: spawnFailure(output, err), Generation: input.Generation}
			s.tellCEOOfFailure(input.Task, "Bring back failed: "+failure.Message)
			if record, readErr := state.ReadLifecycle(s.Store.Home.State, input.Task); readErr == nil {
				failure.Operation, failure.Updated = record.Operation, record.Updated
			}
		}
		s.starts.Lock()
		delete(s.changing, input.Task)
		if err != nil {
			s.changeErrors[input.Task] = failure
		}
		s.starts.Unlock()
		s.notify()
		s.runAsked()
	}()
	s.notify()
	return nil
}
