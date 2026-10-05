package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/herdr"
	"github.com/fpresta0607/code-goblins/internal/monitor"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
	"github.com/fpresta0607/code-goblins/internal/wake"
)

const overlapPollEvery = 10 * time.Minute

type overlapNotice struct {
	Task       string `json:"task"`
	Generation string `json:"generation"`
	Detail     string `json:"detail"`
	Reported   bool   `json:"reported,omitempty"`
}

func (s *Service) isOverlapLive(meta state.TaskMeta, now time.Time) bool {
	current, err := state.ReadTaskMeta(s.Store.Home.State, meta.ID)
	if err != nil || current.SpawnGen != meta.SpawnGen || current.Worktree == "" ||
		!fsx.SamePath(current.Worktree, meta.Worktree) || !fsx.SamePath(current.Project, meta.Project) ||
		current.HerdrSession != meta.HerdrSession || current.HerdrPaneID != meta.HerdrPaneID || current.Backend != meta.Backend {
		return false
	}
	if lifecycle, err := s.lifecycle(meta.ID); err == nil {
		if lifecycle.Generation == meta.SpawnGen && lifecycle.SuppressesMonitoring(s.Store.Home.State) {
			return false
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return false
	}
	observation, err := s.observation(meta.ID)
	return err == nil && observation.EndpointVerdict == monitor.ProbePresent && observation.Health != monitor.HealthPaused &&
		observation.Endpoint == (herdr.Target{Session: meta.HerdrSession, Pane: meta.HerdrPaneID}).String() &&
		!observation.LastObserved.Before(spawnTime(meta.SpawnGen)) && now.Sub(observation.LastObserved) <= 2*time.Minute &&
		!observation.LastObserved.After(now.Add(time.Minute))
}

func (s *Service) pollOverlaps(ctx context.Context, runner execx.Runner, w *fleetWakes, repo string, mine []ciGoblin, currentTime func() time.Time) error {
	if ctx.Err() != nil {
		return nil
	}
	var active []ciGoblin
	for _, goblin := range mine {
		if s.isOverlapLive(goblin.meta, currentTime()) {
			active = append(active, goblin)
		}
	}
	if len(active) == 0 {
		delete(w.OverlapUnread, repo)
		return nil
	}
	for identity, notice := range w.OverlapNotices {
		if notice.Reported || !slices.ContainsFunc(active, func(goblin ciGoblin) bool {
			return goblin.id == notice.Task && goblin.meta.SpawnGen == notice.Generation
		}) {
			continue
		}
		if err := s.deliverOverlap(ctx, w, identity, notice); err != nil {
			return err
		}
	}
	now := currentTime()
	if now.Sub(w.OverlapPolled[repo]) < overlapPollEvery {
		return nil
	}
	if w.OverlapPolled == nil {
		w.OverlapPolled = map[string]time.Time{}
	}
	w.OverlapPolled[repo] = now
	reading, cancel := context.WithTimeout(ctx, ghCallTimeout)
	defer cancel()
	reader := tickets.GitHub{Commands: runner}
	repository, err := reader.RepositoryOf(reading, repo)
	if errors.Is(err, tickets.ErrNotGitHub) {
		delete(w.OverlapUnread, repo)
		return nil
	}
	var activity tickets.Activity
	if err == nil {
		activity, err = reader.ReadOpenWork(reading, repository, now)
	}
	if ctx.Err() != nil {
		return nil
	}
	var unread error
	if err != nil {
		unread = err
	} else if activity.Viewer.Login == "" {
		unread = errors.New("the GitHub viewer is unread")
	} else {
		for _, problem := range activity.Unread {
			unread = errors.Join(unread, errors.New(problem))
		}
		for _, goblin := range active {
			started := spawnTime(goblin.meta.SpawnGen)
			if started.IsZero() {
				unread = errors.Join(unread, fmt.Errorf("%s generation creation time is unread", goblin.id))
				continue
			}
			area, brief, err := s.overlapArea(reading, runner, goblin)
			if err != nil {
				unread = errors.Join(unread, fmt.Errorf("%s branch area: %w", goblin.id, err))
				continue
			}
			if !s.isOverlapLive(goblin.meta, currentTime()) {
				continue
			}
			report := tickets.Build(activity, now, &area)
			matches := report.TeammateOverlaps()
			for _, match := range matches.Files {
				if match.PullRequest == 0 {
					continue
				}
				if match.Author.Login == "" {
					unread = errors.Join(unread, fmt.Errorf("PR #%d author is unread", match.PullRequest))
					continue
				}
				index := slices.IndexFunc(activity.PullRequests, func(pull tickets.PullRequest) bool { return pull.Number == match.PullRequest })
				if index < 0 {
					continue
				}
				created := activity.PullRequests[index].CreatedAt
				if created.IsZero() {
					unread = errors.Join(unread, fmt.Errorf("PR #%d creation time is unread", match.PullRequest))
					continue
				}
				if !created.After(started) || !report.Collaborative && created.Before(now.Add(-tickets.CollaborationWindow)) {
					continue
				}
				line := (tickets.Overlaps{Files: []tickets.FileOverlap{match}}).Lines()[0]
				if err := s.recordOverlap(ctx, w, goblin, repository, "pr", match.PullRequest, line, match.URL); err != nil {
					return err
				}
			}
			own := map[int]bool{}
			if number, ok := tickets.ClaimedIssue(brief, repository); ok {
				own[number] = true
			}
			if record, err := tickets.ReadRecord(s.Store.Home.State, goblin.id); err == nil {
				if strings.EqualFold(record.Repository, repository) {
					own[record.Number] = true
				}
			} else if !errors.Is(err, os.ErrNotExist) {
				unread = errors.Join(unread, err)
				continue
			}
			for _, match := range matches.Issues {
				if own[match.Number] {
					continue
				}
				if match.Author.Login == "" {
					unread = errors.Join(unread, fmt.Errorf("issue #%d author is unread", match.Number))
					continue
				}
				index := slices.IndexFunc(activity.Issues, func(issue tickets.Issue) bool { return issue.Number == match.Number })
				if index < 0 {
					continue
				}
				created := activity.Issues[index].CreatedAt
				if created.IsZero() {
					unread = errors.Join(unread, fmt.Errorf("issue #%d creation time is unread", match.Number))
					continue
				}
				if !created.After(started) || !report.Collaborative && created.Before(now.Add(-tickets.CollaborationWindow)) {
					continue
				}
				line := (tickets.Overlaps{Issues: []tickets.IssueMatch{match}}).Lines()[0]
				if err := s.recordOverlap(ctx, w, goblin, repository, "issue", match.Number, line, match.URL); err != nil {
					return err
				}
			}
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	if unread == nil {
		delete(w.OverlapUnread, repo)
		return nil
	}
	if w.OverlapUnread == nil {
		w.OverlapUnread = map[string]string{}
	}
	w.OverlapUnread[repo] = fmt.Sprintf("overlap read of %s incomplete: %v", repo, unread)
	return errors.New(w.OverlapUnread[repo])
}

func (s *Service) overlapArea(ctx context.Context, runner execx.Runner, goblin ciGoblin) (tickets.Area, string, error) {
	worktree := goblin.meta.Worktree
	branch, err := defaultBranch(ctx, runner, goblin.repo)
	if err != nil {
		return tickets.Area{}, "", err
	}
	head, err := runOutput(ctx, runner, worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return tickets.Area{}, "", err
	}
	base, err := runOutput(ctx, runner, worktree, "git", "rev-parse", "origin/"+branch)
	if err != nil {
		return tickets.Area{}, "", err
	}
	ancestor, err := runOutput(ctx, runner, worktree, "git", "merge-base", head, base)
	if err != nil {
		return tickets.Area{}, "", err
	}
	result, err := runner.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"diff", "--name-only", "--no-renames", "-z", ancestor, head}})
	if err != nil {
		return tickets.Area{}, "", err
	}
	if result.ExitCode != 0 {
		return tickets.Area{}, "", fmt.Errorf("git branch diff exited %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	currentHead, err := runOutput(ctx, runner, worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		return tickets.Area{}, "", err
	}
	currentBase, err := runOutput(ctx, runner, worktree, "git", "rev-parse", "origin/"+branch)
	if err != nil {
		return tickets.Area{}, "", err
	}
	currentBranch, err := runOutput(ctx, runner, worktree, "git", "branch", "--show-current")
	if err != nil {
		return tickets.Area{}, "", err
	}
	if head != currentHead || base != currentBase || currentBranch != goblin.branch {
		return tickets.Area{}, "", errors.New("branch head, base or checkout changed during the read")
	}
	briefPath := goblin.meta.Brief
	if briefPath == "" {
		briefPath = filepath.Join(s.Store.Home.Data, goblin.id, "brief.md")
	}
	brief, err := fsx.ReadFile(briefPath)
	if errors.Is(err, os.ErrNotExist) {
		queued, queuedErr := fleet.ReadQueuedTask(s.Store.Home, goblin.id)
		if queuedErr != nil {
			return tickets.Area{}, "", errors.Join(err, queuedErr)
		}
		brief = []byte(queued.Detail)
	} else if err != nil {
		return tickets.Area{}, "", err
	}
	area := tickets.BriefArea(string(brief), func(string) bool { return false })
	area.Paths = nil
	var paths []string
	for _, path := range strings.Split(string(result.Stdout), "\x00") {
		if path != "" {
			paths = append(paths, path)
		}
	}
	area, ignored := area.WithPaths(paths...)
	if len(ignored) > 0 {
		return tickets.Area{}, "", fmt.Errorf("branch paths are unread: %q", ignored)
	}
	return area, string(brief), nil
}

func (s *Service) recordOverlap(ctx context.Context, w *fleetWakes, goblin ciGoblin, repository, kind string, number int, line, url string) error {
	if ctx.Err() != nil {
		return nil
	}
	identity := fmt.Sprintf("overlap:%s:%s:%s:%s:%d", goblin.id, goblin.meta.SpawnGen, strings.ToLower(repository), kind, number)
	if notice, ok := w.OverlapNotices[identity]; ok {
		if notice.Reported {
			return nil
		}
		return s.deliverOverlap(ctx, w, identity, notice)
	}
	notice := overlapNotice{Task: goblin.id, Generation: goblin.meta.SpawnGen, Detail: fmt.Sprintf("pr_overlap: %s: %s (%s) overlaps branch %s; next: CFO decide whether to continue, wait or narrow %s", goblin.id, line, url, goblin.branch, goblin.id)}
	if w.OverlapNotices == nil {
		w.OverlapNotices = map[string]overlapNotice{}
	}
	w.OverlapNotices[identity] = notice
	// Keep the first proved detail before queueing so a crash retry uses the same notice.
	if err := writeFleetWakes(s.Store.Home.State, *w); err != nil {
		return err
	}
	return s.deliverOverlap(ctx, w, identity, notice)
}

func (s *Service) deliverOverlap(ctx context.Context, w *fleetWakes, identity string, notice overlapNotice) error {
	if ctx.Err() != nil {
		return nil
	}
	if _, err := wake.AppendOnce(s.Store.Home.State, identity, "pr", notice.Task, notice.Detail); err != nil {
		return err
	}
	if _, err := wake.PublishEpisode(s.Store.Home.State); err != nil {
		return err
	}
	notice.Reported = true
	w.OverlapNotices[identity] = notice
	return nil
}
