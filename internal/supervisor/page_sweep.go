package supervisor

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
)

const (
	// pageSweepEvery is how often the supervisor goes through every review
	// session lavish-axi keeps, besides once when it starts.
	pageSweepEvery = 10 * time.Minute
	// pageSweepPollTimeout bounds a sweep's poll of a page: the poll takes what
	// waits there and posts its reply, and waits for nothing more.
	pageSweepPollTimeout = 2 * time.Second
)

// sweepPages starts a sweep of every review session lavish-axi keeps when one
// is due and none runs: at start, then every pageSweepEvery. A poller that
// stops because its goblin was retired leaves that goblin's page to the next
// sweep.
func (s *Service) sweepPages(ctx context.Context) {
	if s.Options.PageSessions == nil || s.Options.PollPage == nil || s.Options.EndPage == nil {
		return
	}
	s.pagesMu.Lock()
	defer s.pagesMu.Unlock()
	if s.pageSweeping || !s.pageSwept.IsZero() && time.Since(s.pageSwept) < pageSweepEvery || ctx.Err() != nil {
		return
	}
	s.pageSweeping = true
	s.pageWork.Add(1)
	go func() {
		defer s.pageWork.Done()
		if err := s.sweepSessions(ctx); err != nil {
			s.publish(err)
		}
		s.pagesMu.Lock()
		s.pageSweeping, s.pageSwept = false, time.Now()
		s.pagesMu.Unlock()
	}()
}

// sweepSessions goes through every review session lavish-axi keeps, so that
// nothing the Overlord sent on a fleet task's page goes unread and a retired
// goblin's page says so. A page no poller watches that holds his undelivered
// feedback has it taken and delivered, once: to the goblin while it runs,
// otherwise to the CFO as a review wake naming the task. The open page of a
// retired goblin is ended, with a reply on the page saying why. A page no
// fleet task presented, and a page a poller watches, are left alone.
func (s *Service) sweepSessions(ctx context.Context) error {
	sessions, err := s.Options.PageSessions()
	if err != nil {
		return err
	}
	ownerOf, err := s.pageOwners()
	if err != nil {
		return err
	}
	var problems error
	for _, session := range sessions {
		if ctx.Err() != nil {
			return nil
		}
		key := pageKey(session.File)
		s.pagesMu.Lock()
		watched := s.pages[key]
		s.pagesMu.Unlock()
		owner, found := ownerOf(session.File)
		if watched || !found {
			continue
		}
		retired := owner.task != "" && !s.Store.taskStands(owner.task)
		var err error
		switch {
		case retired && session.Status != "ended":
			// Ended first, so the page refuses anything he sends from now
			// on, saying the review ended, and the poll after it takes all
			// he sent before.
			if err = s.Options.EndPage(ctx, session.File); err == nil {
				err = s.takePage(ctx, session.File, owner, owner.task+" has been retired, so this review is closed. Anything you sent here went to the CFO.")
			}
		case session.Pending > 0:
			err = s.takePage(ctx, session.File, owner, "")
		}
		if err == nil && retired {
			err = s.Store.settlePage(key, Review{})
		}
		problems = errors.Join(problems, err)
	}
	return problems
}

// takePage takes what the Overlord queued on a page no poller watches, a
// poll at a time, posting reply first, and passes each on as it comes, until
// nothing more waits there.
func (s *Service) takePage(ctx context.Context, file string, owner pageOwner, reply string) error {
	identity := ""
	if owner.task != "" {
		if meta, err := state.ReadTaskMeta(s.Store.Home.State, owner.task); err == nil {
			identity = goblinIdentity(meta)
		}
	}
	for {
		poll, err := s.Options.PollPage(ctx, file, reply, pageSweepPollTimeout)
		if err != nil || poll.Status != "feedback" {
			return err
		}
		if reply = s.passOnPageFeedback(ctx, file, owner.task, identity, owner.key, poll); reply == "" {
			return nil
		}
	}
}

// pageOwner is the fleet task a page belongs to, or no task for the CFO's
// own, and the key its review wakes go under.
type pageOwner struct{ task, key string }

// pageOwners returns how a sweep tells whose page a file is: the item that
// last presented it, else the task whose worktree, data folder or scratch
// folder in the state holds it, when the fleet knows that task, running,
// paused or retired. A file none of them holds is no fleet task's page.
func (s *Service) pageOwners() (func(file string) (pageOwner, bool), error) {
	h := s.Store.Home
	presented := map[string]Review{}
	for _, r := range s.Store.Snapshot().Reviews {
		if prior, found := presented[pageKey(r.LavishPage)]; r.LavishPage != "" && (!found || !r.CreatedAt.Before(prior.CreatedAt)) {
			presented[pageKey(r.LavishPage)] = r
		}
	}
	scan, err := state.ScanIDs(h.State)
	if err != nil {
		return nil, err
	}
	known, worktrees := map[string]bool{}, map[string]string{}
	for _, id := range scan.MetaIDs {
		known[id] = true
		if meta, err := state.ReadTaskMeta(h.State, id); err == nil {
			for _, path := range append([]string{meta.Worktree}, meta.Extras...) {
				if path != "" {
					worktrees[path] = id
				}
			}
		}
	}
	for _, id := range scan.OrphanStatusIDs {
		known[id] = true
	}
	archived, err := os.ReadDir(filepath.Join(h.State, state.ArchiveDirName))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	for _, entry := range archived {
		if m := archivedStatusFile.FindStringSubmatch(entry.Name()); m != nil {
			known[m[1]] = true
		} else if m := archivedTaskDir.FindStringSubmatch(entry.Name()); m != nil {
			known[m[1]] = true
		}
	}
	return func(file string) (pageOwner, bool) {
		if r, found := presented[pageKey(file)]; found {
			if r.Task == "" {
				return pageOwner{key: r.ID}, true
			}
			return pageOwner{task: r.Task, key: r.Task}, true
		}
		id := ""
		for worktree, task := range worktrees {
			if rel, err := filepath.Rel(worktree, file); err == nil && filepath.IsLocal(rel) {
				id = task
			}
		}
		if place, found := home.LocateWorktree(filepath.Join(h.Root, home.WorktreesDir), file); found && id == "" {
			id = place.Name
		}
		for _, folder := range []string{h.Data, filepath.Join(h.State, "tasktmp")} {
			if rel, err := filepath.Rel(folder, file); err == nil && filepath.IsLocal(rel) && id == "" {
				if parts := strings.Split(rel, string(filepath.Separator)); len(parts) >= 2 {
					id = parts[0]
				}
			}
		}
		if !known[id] {
			return pageOwner{}, false
		}
		return pageOwner{task: id, key: id}, true
	}, nil
}
