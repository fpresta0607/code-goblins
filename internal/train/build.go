package train

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// rebuild builds the riders again on the current base and pushes them for
// the next run, saying why in the note. The record says first that a build
// is due, so a step cut short anywhere in it is built again by the next one.
func (e Engine) rebuild(ctx context.Context, t *Train, why string) error {
	if why != "" {
		t.Note = why
	}
	t.Head, t.Landing = "", false
	if err := e.save(t); err != nil {
		return err
	}
	if err := e.build(ctx, t); err != nil {
		t.Head = ""
		return errors.Join(err, e.save(t))
	}
	if len(t.carsIn(CarRiding)) == 0 {
		return e.finish(ctx, t, StateStopped, "no pull request left to test merged cleanly onto "+t.Base)
	}
	return e.save(t)
}

// build merges the train's riders onto the current base in order, with
// git merge-tree and commit-tree, so neither the checkout's working tree nor
// its branches are touched; a rider that does not merge cleanly onto what is
// ahead of it is set aside as a conflict, and one already on the base as
// landed. Each rider's merge commit is the one GitHub's own merge of it would
// leave, "Merge pull request #N from owner/branch" with the rider's head as
// its second parent, since the commits CI tests are the ones the base takes.
// It records the run it built before it pushes it to the train's branch,
// then opens the train's pull request, which wears the train's label from
// then on, or retitles it for a later run. A pull request that merged took
// its run to the base, so the run after it gets one of its own.
func (e Engine) build(ctx context.Context, t *Train) error {
	base, err := e.baseSHA(ctx, *t)
	if err != nil {
		return err
	}
	if err := e.fetch(ctx, *t, base); err != nil {
		return err
	}
	head := base
	owner, _, _ := strings.Cut(t.Repository, "/")
	for _, i := range t.carsIn(CarRiding) {
		car := &t.Cars[i]
		isLanded, err := e.isAncestor(ctx, t.Checkout, car.Head, base)
		if err != nil {
			car.State, car.Note = CarReturned, fmt.Sprintf("its head %s could not be fetched, so it moved since it was listed", short(car.Head))
			continue
		}
		if isLanded {
			car.State, car.Note = CarLanded, "already on "+t.Base
			continue
		}
		merged, conflicts, err := e.mergeTree(ctx, t.Checkout, head, car.Head)
		if err != nil {
			return err
		}
		if len(conflicts) > 0 {
			car.State, car.Note = CarConflict, "it conflicts in "+strings.Join(conflicts, ", ")
			continue
		}
		commit := []string{"commit-tree", merged, "-p", head, "-p", car.Head, "-m", fmt.Sprintf("Merge pull request #%d from %s/%s", car.Number, owner, car.Branch)}
		if car.Title != "" {
			commit = append(commit, "-m", car.Title)
		}
		head, err = e.run(ctx, t.Checkout, "git", commit...)
		if err != nil {
			return err
		}
	}
	riders := t.carsIn(CarRiding)
	if len(riders) == 0 {
		return nil
	}
	if t.hasMerged() {
		t.PR = ""
	}
	isResumed := t.PR == "" && t.Runs > 0
	t.BaseSHA, t.Head, t.Pushed = base, head, e.Now().UTC()
	t.Runs++
	t.endRun(RunRepushed, "")
	run := Run{Number: t.Runs, Base: base, Head: head, Pushed: t.Pushed}
	for _, i := range riders {
		run.Riders = append(run.Riders, t.Cars[i].Number)
	}
	t.History = append(t.History, run)
	if err := e.save(t); err != nil {
		return err
	}
	if _, err := e.run(ctx, t.Checkout, "git", "push", "--quiet", "--force", "origin", head+":refs/heads/"+t.Branch); err != nil {
		return err
	}
	if t.PR == "" && isResumed {
		// A build cut short after it opened the pull request left it
		// unrecorded: it is found by its branch rather than opened twice.
		found, err := e.run(ctx, t.Checkout, "gh", "pr", "list", "--repo", t.Repository, "--head", t.Branch, "--state", "open", "--json", "url", "--jq", ".[0].url")
		if err != nil {
			return err
		}
		t.PR = found
	}
	if t.PR != "" {
		if err := e.describe(ctx, t); err != nil {
			return err
		}
		t.openRun().PR = t.PR
		return nil
	}
	title, body := t.words()
	out, err := e.run(ctx, t.Checkout, "gh", "pr", "create", "--repo", t.Repository, "--base", t.Base, "--head", t.Branch, "--title", title, "--body", body)
	if err != nil {
		return err
	}
	lines := strings.Split(out, "\n")
	t.PR = strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(t.PR, "https://") {
		t.PR = ""
		return fmt.Errorf("gh pr create printed no pull request URL: %q", out)
	}
	t.openRun().PR = t.PR
	e.wear(ctx, *t)
	return nil
}

// words are the title and the body of the train's pull request for the run
// CI tests.
func (t Train) words() (title, body string) {
	riders := t.carsIn(CarRiding)
	title = "chore(cfo): merge train for PRs " + t.numbers(riders)
	body = fmt.Sprintf("The CFO's merge train %s: %s merged onto %s at %s in this order, so CI tests them together once. When this run is green, this pull request merges: %s takes the commit CI tested, and GitHub marks each of those pull requests merged, since %s then holds its head. When it is red, its failed checks run again once, and when it is red a second time the train is halved until the pull request that breaks it is found, and every half that passes lands by a pull request like this one. Leave it to the train: it merges this pull request, or closes it saying why, and deletes its branch when it is over.",
		t.ID, t.riders(riders), t.Base, short(t.BaseSHA), t.Base, t.Base)
	return title, body
}

// describe gives the train's pull request the title and body of the run CI
// tests.
func (e Engine) describe(ctx context.Context, t *Train) error {
	title, body := t.words()
	_, err := e.run(ctx, t.Checkout, "gh", "pr", "edit", t.PR, "--title", title, "--body", body)
	return err
}

// What the train's label looks like in a repository's list of labels, where
// the train had to make it.
const (
	ownLabelSays  = "A merge train's own pull request, which generated release notes leave out"
	ownLabelColor = "8B949E"
)

// wear puts the train's label on its pull request, so the notes GitHub
// writes for a release leave that pull request out and list each pull request
// it landed by its own number. A repository's first train finds no such
// label and makes it, and a label the repository has is left as its owner
// keeps it. The label is housekeeping, so a train never stops for it: one
// that could not be put on is told to the CFO with how to put it on by hand.
func (e Engine) wear(ctx context.Context, t Train) {
	add := func() error {
		_, err := e.run(ctx, t.Checkout, "gh", "pr", "edit", t.PR, "--add-label", OwnLabel)
		return err
	}
	err := add()
	if err != nil {
		if _, makeErr := e.run(ctx, t.Checkout, "gh", "label", "create", OwnLabel, "--repo", t.Repository, "--description", ownLabelSays, "--color", ownLabelColor); makeErr != nil {
			err = fmt.Errorf("%w, and the label could not be made: %w", err, makeErr)
		} else {
			err = add()
		}
	}
	if err != nil && ctx.Err() == nil {
		e.tellCFO(ctx, fmt.Sprintf("merge_train: %s train %s could not put the label %s on its pull request %s (%v). The notes GitHub writes for a release list a merged pull request that does not wear it, so put it on by hand if this one merges without: gh pr edit %s --add-label %s", t.Repository, t.ID, OwnLabel, t.PR, err, t.PR, OwnLabel))
	}
}

// baseSHA reads the commit the base branch is at on origin now.
func (e Engine) baseSHA(ctx context.Context, t Train) (string, error) {
	out, err := e.run(ctx, t.Checkout, "git", "ls-remote", "origin", "refs/heads/"+t.Base)
	if err != nil {
		return "", err
	}
	sha, _, _ := strings.Cut(out, "\t")
	if sha == "" {
		return "", fmt.Errorf("origin has no branch %s", t.Base)
	}
	return sha, nil
}

// fetch fetches the base and every rider's branch from origin in one go,
// without tags and without writing FETCH_HEAD, and makes sure the base
// commit is then in the checkout. A branch that cannot be fetched fails the
// whole fetch, so then each is fetched alone, and a rider whose head did not
// come is found when the train is built.
func (e Engine) fetch(ctx context.Context, t Train, base string) error {
	refs := []string{"refs/heads/" + t.Base}
	for _, i := range t.carsIn(CarRiding) {
		refs = append(refs, "refs/heads/"+t.Cars[i].Branch)
	}
	fetch := []string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin"}
	if _, err := e.run(ctx, t.Checkout, "git", append(fetch, refs...)...); err != nil {
		if _, err := e.run(ctx, t.Checkout, "git", append(fetch, refs[0])...); err != nil {
			return err
		}
		for _, ref := range refs[1:] {
			_, _ = e.run(ctx, t.Checkout, "git", append(fetch, ref)...)
		}
	}
	if _, err := e.run(ctx, t.Checkout, "git", "cat-file", "-e", base+"^{commit}"); err != nil {
		return fmt.Errorf("%s at %s was not fetched: %w", t.Base, short(base), err)
	}
	return nil
}

// isAncestor says whether commit is already part of base, and fails when
// commit is not in the checkout.
func (e Engine) isAncestor(ctx context.Context, checkout, commit, base string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	result, err := e.Commands.Run(ctx, execx.Request{Dir: checkout, Name: "git", Args: []string{"merge-base", "--is-ancestor", commit, base}})
	if err != nil {
		return false, err
	}
	switch result.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	}
	return false, fmt.Errorf("git merge-base exited %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
}

// mergeTree merges head onto parent without a working tree and returns the
// merged tree, or the files that conflict.
func (e Engine) mergeTree(ctx context.Context, checkout, parent, head string) (string, []string, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	result, err := e.Commands.Run(ctx, execx.Request{Dir: checkout, Name: "git", Args: []string{"merge-tree", "--write-tree", "--name-only", "--no-messages", parent, head}})
	if err != nil {
		return "", nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(result.Stdout)), "\n")
	switch result.ExitCode {
	case 0:
		return strings.TrimSpace(lines[0]), nil, nil
	case 1:
		var conflicts []string
		for _, line := range lines[1:] {
			if line = strings.TrimSpace(line); line != "" {
				conflicts = append(conflicts, line)
			}
		}
		if len(conflicts) == 0 {
			conflicts = []string{"files git did not name"}
		}
		return "", conflicts, nil
	}
	return "", nil, fmt.Errorf("git merge-tree exited %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
}
