package train

import (
	"context"
	"fmt"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/goblinname"
)

// finish ends the train in state: it records how it ended first, so a
// finish cut short never leaves a train running that is over, then closes
// the train's pull request and deletes its branch, and tells each goblin
// whose pull request broke the train or conflicted, and the CFO.
func (e Engine) finish(ctx context.Context, t *Train, state, note string) error {
	t.State, t.Note, t.Finished, t.Landing = state, note, e.Now().UTC(), false
	for _, i := range append(t.carsIn(CarRiding), t.carsIn(CarWaiting)...) {
		t.Cars[i].State = CarReturned
	}
	past, _ := List(e.StateDir)
	if err := e.save(t); err != nil {
		return err
	}
	var leftovers []string
	if t.PR != "" {
		if _, err := e.run(ctx, t.Checkout, "gh", "pr", "close", t.PR, "--comment", "Merge train "+t.ID+" is over: "+e.summary(*t)); err != nil {
			leftovers = append(leftovers, "its pull request stayed open: "+err.Error())
		}
	}
	if t.Runs > 0 {
		if _, err := e.run(ctx, t.Checkout, "git", "push", "--quiet", "origin", "--delete", t.Branch); err != nil {
			leftovers = append(leftovers, "its branch "+t.Branch+" stayed: "+err.Error())
		}
	}
	var saveErr error
	if len(leftovers) > 0 {
		t.Note = strings.TrimPrefix(t.Note+"; "+strings.Join(leftovers, "; "), "; ")
		saveErr = e.save(t)
	}
	var undelivered []string
	for _, car := range t.Cars {
		text := e.goblinNotice(*t, car, past)
		if text == "" {
			continue
		}
		if car.Task == "" || e.TellGoblin == nil {
			undelivered = append(undelivered, fmt.Sprintf("#%d has no goblin to tell", car.Number))
			continue
		}
		if err := e.TellGoblin(ctx, car.Task, text); err != nil {
			undelivered = append(undelivered, fmt.Sprintf("%s was not told about #%d (%v): relay it with cfo send", goblinname.Called(car.Goblin, car.Task), car.Number, err))
		}
	}
	summary := "merge_train: " + t.Repository + " train " + t.ID + " " + e.summary(*t)
	if len(undelivered) > 0 {
		summary += "; " + strings.Join(undelivered, "; ")
	}
	e.tellCFO(ctx, summary)
	return saveErr
}

// summary says how a finished train ended, what landed, and what goes to
// the next train.
func (e Engine) summary(t Train) string {
	parts := []string{t.State}
	if t.Note != "" {
		parts[0] += ": " + t.Note
	}
	if landed := t.carsIn(CarLanded); len(landed) > 0 {
		parts = append(parts, fmt.Sprintf("landed %s in %d CI run(s)", t.numbers(landed), t.Runs))
	} else {
		parts = append(parts, "nothing landed")
	}
	if culprits := t.carsIn(CarCulprit); len(culprits) > 0 {
		parts = append(parts, t.numbers(culprits)+" went back to its goblin with the failure")
	}
	if conflicts := t.carsIn(CarConflict); len(conflicts) > 0 {
		parts = append(parts, t.numbers(conflicts)+" conflicted and its goblin was told to merge "+t.Base)
	}
	if returned := t.carsIn(CarReturned); len(returned) > 0 {
		parts = append(parts, t.numbers(returned)+" waits for the next train")
	}
	if t.PR != "" {
		parts = append(parts, "("+t.PR+")")
	}
	return strings.Join(parts, "; ")
}

// goblinNotice is what car's goblin is told when its train finishes: the
// failure when its pull request broke the train, and to merge the base when
// it conflicted, once per head. Every other car's goblin is told nothing.
func (e Engine) goblinNotice(t Train, car Car, past []Train) string {
	switch car.State {
	case CarCulprit:
		return fmt.Sprintf("CFO: merge train %s found that your PR #%d (%s) breaks CI when it merges onto %s at %s: %s. Fix it on your branch (merge origin/main in with a merge commit first, never rebase or force-push), verify locally, push, and report done again; the next train takes it once its head changes. Train PR: %s",
			t.ID, car.Number, car.URL, t.Base, short(t.BaseSHA), strings.TrimPrefix(car.Note, "failed: "), t.PR)
	case CarConflict:
		for _, before := range past {
			for _, told := range before.Cars {
				if before.ID != t.ID && before.IsFinished() && told.State == CarConflict && told.URL == car.URL && told.Head == car.Head {
					return ""
				}
			}
		}
		return fmt.Sprintf("CFO: merge train %s could not merge your PR #%d (%s) onto %s with the PRs ahead of it: %s. Merge origin/main into your branch now with a merge commit (never rebase or force-push), resolve it, verify locally, push, and report done again; the next train takes it.",
			t.ID, car.Number, car.URL, t.Base, car.Note)
	}
	return ""
}

// checkNames names the failed checks, each with its own page.
func checkNames(failed []Check) string {
	var names []string
	for _, check := range failed {
		name := check.name()
		if link := check.link(); link != "" {
			name += " (" + link + ")"
		}
		names = append(names, name)
	}
	if len(names) == 0 {
		return "no check named its failure"
	}
	return strings.Join(names, ", ")
}

func (e Engine) tellCFO(ctx context.Context, text string) {
	if e.TellCFO != nil {
		_ = e.TellCFO(ctx, text)
	}
}
