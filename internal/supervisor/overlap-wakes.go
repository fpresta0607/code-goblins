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

const (
	// overlapPollEvery is how often an overlap read of a repository starts.
	overlapPollEvery = 10 * time.Minute
	// overlapPagesPerPass is how many pages of GitHub's answer one CI poll
	// reads of an overlap read, each on ghCallTimeout of its own; a read with
	// more takes them up on the next poll.
	overlapPagesPerPass = 2
)

// Overlap is one piece of a teammate's open work that meets a live goblin's
// area: who, what it is, such as "PR #1446", and its link.
type Overlap struct {
	tickets.Actor
	What string `json:"what"`
	URL  string `json:"url"`
}

// sameArea is the teammates' open work a goblin's area meets, as its
// generation's last overlap read found it.
type sameArea struct {
	Generation string    `json:"generation"`
	Overlaps   []Overlap `json:"overlaps"`
}

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

// pollOverlaps starts an overlap read of repo every overlapPollEvery while a
// goblin works there live, and reads overlapPagesPerPass pages of it a poll:
// a read with more pages, or one whose page failed, takes up where it left
// off on the next poll. What a read could not read stays on the board, and
// is returned, to wake the CFO, once it went unread failingPasses polls in a
// row.
func (s *Service) pollOverlaps(ctx context.Context, runner execx.Runner, w *fleetWakes, repo string, mine []ciGoblin, currentTime func() time.Time) error {
	if ctx.Err() != nil {
		return nil
	}
	var active []ciGoblin
	for _, goblin := range mine {
		if s.isOverlapLive(goblin.meta, currentTime()) {
			active = append(active, goblin)
		} else {
			delete(w.SameArea, goblin.id)
		}
	}
	if len(active) == 0 {
		delete(w.OverlapUnread, repo)
		delete(w.Failing, "overlap:"+repo)
		delete(s.overlapReads, repo)
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
	reader := tickets.GitHub{Commands: runner}
	work := s.overlapReads[repo]
	var err error
	if work == nil {
		if now.Sub(w.OverlapPolled[repo]) < overlapPollEvery {
			return nil
		}
		if w.OverlapPolled == nil {
			w.OverlapPolled = map[string]time.Time{}
		}
		w.OverlapPolled[repo] = now
		probe, cancel := context.WithTimeout(ctx, ghCallTimeout)
		repository, originErr := reader.RepositoryOf(probe, repo)
		cancel()
		if errors.Is(originErr, tickets.ErrNotGitHub) {
			delete(w.OverlapUnread, repo)
			delete(w.Failing, "overlap:"+repo)
			return nil
		}
		err = originErr
		if err == nil {
			work, err = tickets.StartOpenWork(repository, now)
		}
		if err == nil {
			if s.overlapReads == nil {
				s.overlapReads = map[string]*tickets.OpenWork{}
			}
			s.overlapReads[repo] = work
		}
	}
	pages := 0
	if err == nil {
		pages, err = reader.Continue(ctx, work, overlapPagesPerPass, ghCallTimeout)
		if work.IsWhole() {
			delete(s.overlapReads, repo)
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	unread := err
	if pages > 0 {
		unreadAreas, err := s.readOverlaps(ctx, runner, w, work.Activity(), active, now, currentTime)
		if err != nil {
			return err
		}
		unread = errors.Join(unread, unreadAreas)
	}
	if ctx.Err() != nil {
		return nil
	}
	if unread == nil {
		delete(w.OverlapUnread, repo)
		delete(w.Failing, "overlap:"+repo)
		return nil
	}
	if w.OverlapUnread == nil {
		w.OverlapUnread = map[string]string{}
	}
	w.OverlapUnread[repo] = fmt.Sprintf("overlap read of %s incomplete: %v", repo, unread)
	if w.failing("overlap:"+repo, unread) == nil {
		return nil
	}
	return errors.New(w.OverlapUnread[repo])
}

// readOverlaps tells the CFO of each teammate's pull request or issue, as
// activity has them so far, that meets a live goblin's area and opened after
// the goblin started, and keeps on each goblin's card the teammates' work its
// area meets. It returns what it could not read, apart from what went wrong
// telling the CFO.
func (s *Service) readOverlaps(ctx context.Context, runner execx.Runner, w *fleetWakes, activity tickets.Activity, active []ciGoblin, now time.Time, currentTime func() time.Time) (unread, err error) {
	repository := activity.Repository
	if activity.Viewer.Login == "" {
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
			area, brief, branch, err := s.overlapArea(ctx, runner, goblin)
			if errors.Is(err, errGoblinMoving) {
				continue
			}
			if err != nil {
				unread = errors.Join(unread, fmt.Errorf("%s branch area: %w", goblin.id, err))
				continue
			}
			goblin.branch = branch
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
					return unread, err
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
			if w.SameArea == nil {
				w.SameArea = map[string]sameArea{}
			}
			w.SameArea[goblin.id] = sameArea{Generation: goblin.meta.SpawnGen, Overlaps: overlapsOf(matches, own)}
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
					return unread, err
				}
			}
		}
	}
	return unread, nil
}

// overlapsOf is the teammates' work an area meets, as a card names it: each
// pull request or branch that changes its files, and each issue other than
// the task's own that names it, whenever it was opened.
func overlapsOf(matches tickets.Overlaps, own map[int]bool) []Overlap {
	var overlaps []Overlap
	for _, match := range matches.Files {
		what := "branch " + match.Branch
		if match.PullRequest != 0 {
			what = fmt.Sprintf("PR #%d", match.PullRequest)
		}
		overlaps = append(overlaps, Overlap{Actor: match.Author, What: what, URL: match.URL})
	}
	for _, match := range matches.Issues {
		if !own[match.Number] {
			overlaps = append(overlaps, Overlap{Actor: match.Author, What: fmt.Sprintf("issue #%d", match.Number), URL: match.URL})
		}
	}
	return overlaps
}

// errGoblinMoving says a goblin's own git work moved its branch, head or base
// through both reads of its area: the goblin is working, so its area is read
// on the next pass, and nothing is reported.
var errGoblinMoving = errors.New("the goblin's branch moved through both reads")

// overlapArea reads the files goblin's branch changes against the default
// branch, with its brief, and the branch it has checked out. It runs on a
// deadline of its own, never the one the GitHub read before it used, and
// names the branch itself rather than trusting one read at the start of the
// poll: on 2026-10-08 a goblin that switched branches in the tens of seconds
// between the two read as a failed read, and goblins read late ran out of
// the shared deadline.
func (s *Service) overlapArea(ctx context.Context, runner execx.Runner, goblin ciGoblin) (tickets.Area, string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, ghCallTimeout)
	defer cancel()
	paths, branch, err := branchPaths(ctx, runner, goblin)
	if err != nil {
		return tickets.Area{}, "", "", err
	}
	briefPath := goblin.meta.Brief
	if briefPath == "" {
		briefPath = filepath.Join(s.Store.Home.Data, goblin.id, "brief.md")
	}
	brief, err := fsx.ReadFile(briefPath)
	if errors.Is(err, os.ErrNotExist) {
		queued, queuedErr := fleet.ReadQueuedTask(s.Store.Home, goblin.id)
		if queuedErr != nil {
			return tickets.Area{}, "", "", errors.Join(err, queuedErr)
		}
		brief = []byte(queued.Detail)
	} else if err != nil {
		return tickets.Area{}, "", "", err
	}
	area := tickets.BriefArea(string(brief), func(string) bool { return false })
	area.Paths = nil
	area, ignored := area.WithPaths(paths...)
	if len(ignored) > 0 {
		return tickets.Area{}, "", "", fmt.Errorf("branch paths are unread: %q", ignored)
	}
	return area, string(brief), branch, nil
}

// branchPaths reads the files goblin's checked-out branch changes against
// the default branch, and that branch. The read names the branch first and
// checks at its end that the branch, its head and the base did not move under
// it; a goblin's own git work that moved one is read again once, and one that
// moves through both reads is errGoblinMoving.
func branchPaths(ctx context.Context, runner execx.Runner, goblin ciGoblin) ([]string, string, error) {
	worktree := goblin.meta.Worktree
	defaultName, err := defaultBranch(ctx, runner, goblin.repo)
	if err != nil {
		return nil, "", err
	}
	read := func(args ...string) (string, error) { return runOutput(ctx, runner, worktree, "git", args...) }
	for range 2 {
		branch, err := read("branch", "--show-current")
		if err != nil {
			return nil, "", err
		}
		head, err := read("rev-parse", "HEAD")
		if err != nil {
			return nil, "", err
		}
		base, err := read("rev-parse", "origin/"+defaultName)
		if err != nil {
			return nil, "", err
		}
		ancestor, err := read("merge-base", head, base)
		if err != nil {
			return nil, "", err
		}
		result, err := runner.Run(ctx, execx.Request{Dir: worktree, Name: "git", Args: []string{"diff", "--name-only", "--no-renames", "-z", ancestor, head}})
		if err != nil {
			return nil, "", err
		}
		if result.ExitCode != 0 {
			return nil, "", fmt.Errorf("git branch diff exited %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
		}
		currentHead, err := read("rev-parse", "HEAD")
		if err != nil {
			return nil, "", err
		}
		currentBase, err := read("rev-parse", "origin/"+defaultName)
		if err != nil {
			return nil, "", err
		}
		currentBranch, err := read("branch", "--show-current")
		if err != nil {
			return nil, "", err
		}
		if head != currentHead || base != currentBase || branch != currentBranch {
			continue
		}
		var paths []string
		for _, path := range strings.Split(string(result.Stdout), "\x00") {
			if path != "" {
				paths = append(paths, path)
			}
		}
		return paths, branch, nil
	}
	return nil, "", errGoblinMoving
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
