package supervisor

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
	"github.com/fpresta0607/code-goblins/internal/wake"
	"github.com/fpresta0607/code-goblins/internal/workreview"
)

// foldProject is a project's repository and checkout as WorkReview.Resolve
// read them.
type foldProject struct{ repository, checkout string }

// foldProjects keeps what Resolve read of each project for the life of the
// supervisor, so a scheduler reading asks for each once. A project with no
// repository, or one it could not resolve, is asked for again at the next
// reading.
type foldProjects struct {
	mu    sync.Mutex
	known map[string]foldProject
}

func (p *foldProjects) resolve(review *WorkReview, name string) (foldProject, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if project, isKnown := p.known[name]; isKnown {
		return project, nil
	}
	repository, checkout, err := review.Resolve(name)
	if err != nil {
		return foldProject{}, err
	}
	project := foldProject{repository: repository, checkout: checkout}
	if repository != "" {
		if p.known == nil {
			p.known = map[string]foldProject{}
		}
		p.known[name] = project
	}
	return project, nil
}

// startHeldByFold says the scheduler's start of the queued task plan describes
// is held, because a paused or live task of the same project names an issue it
// names: the two are one piece of work, so the CEO is woken once for the pair
// with the proposal to fold the queued task into the other. It reads local
// files only. A Start on the board and goblins spawn are never held by it.
func (s *Service) startHeldByFold(plan startPlan) (bool, error) {
	review := s.Options.WorkReview
	if review == nil {
		return false, nil
	}
	h := s.Store.Home
	project, err := s.folds.resolve(review, plan.project)
	if err != nil || project.repository == "" {
		return false, err
	}
	backlog, err := fleet.ReadBacklog(h)
	if err != nil {
		return false, err
	}
	queuedIssues, err := issuesOfTask(h, plan.id, plan.brief, project.repository, backlog)
	if err != nil || len(queuedIssues) == 0 {
		return false, err
	}
	for _, meta := range liveTasks(h.State) {
		if meta.ID == plan.id || !fsx.EqualPath(meta.Project, project.checkout) {
			continue
		}
		holderIssues, err := issuesOfTask(h, meta.ID, meta.Brief, project.repository, backlog)
		if err != nil {
			return false, err
		}
		var shared []string
		for _, issue := range queuedIssues {
			if slices.Contains(holderIssues, issue) {
				shared = append(shared, issue.String())
			}
		}
		if len(shared) == 0 {
			continue
		}
		holding := "live"
		if record, err := state.ReadLifecycle(h.State, meta.ID); err == nil && record.Phase == "paused" && record.Generation == meta.SpawnGen {
			holding = "paused"
		}
		detail := fmt.Sprintf("start_held: the start of %s is held because %s (%s) of the same project names %s; fold %s into %s, or start it by hand with a Start on the board or goblins spawn.", plan.id, meta.ID, holding, strings.Join(shared, ", "), plan.id, meta.ID)
		_, isNew, err := wake.AppendFirst(h.State, "start-fold/"+plan.id+"/"+meta.ID, "notify", plan.id, detail)
		if err == nil && isNew {
			_, err = wake.PublishEpisode(h.State)
		}
		return true, err
	}
	return false, nil
}

// issuesOfTask are the issues task id names in its brief and its backlog row,
// each once, leaving out the bare numbers of a project that has no repository.
func issuesOfTask(h home.Home, id, briefPath, repository string, backlog fleet.BacklogRows) ([]tickets.IssueRef, error) {
	brief, err := tickets.TaskBrief(h, id, briefPath)
	if err != nil {
		return nil, fmt.Errorf("read the brief of %s: %w", id, err)
	}
	var row string
	for _, candidate := range slices.Concat(backlog.Queued, backlog.Parked) {
		if candidate.ID == id {
			row = candidate.Raw
		}
	}
	return workreview.NamedIssues(brief, row, repository), nil
}
