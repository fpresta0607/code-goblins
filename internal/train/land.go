package train

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// land merges the riders of a green run in order, each pinned to the head
// that rode, checks the base's tree is then the run's tree, and tests the
// waiting half next if a red run left one.
//
// Before the first merge it rechecks every rider: one closed, turned into a
// draft, labelled hold or moved to another head since it rode would leave
// the base holding part of what CI tested, so nothing merges and the rest is
// tested again without it. A base that moved during the run is tested again
// too, since what the run proved is not what would land. Once merging starts
// the record says so, and a landing cut short merges the rest.
func (e Engine) land(ctx context.Context, t *Train) error {
	if !t.Landing {
		base, err := e.baseSHA(ctx, *t)
		if err != nil {
			return err
		}
		if base != t.BaseSHA {
			t.Moved++
			t.endRun(RunMoved, "")
			if t.Moved >= maxMoved {
				return e.finish(ctx, t, StateFailed, fmt.Sprintf("%s moved during %d runs in a row, so nothing they tested could land: hold other merges to %s, then start the train again", t.Base, t.Moved, t.Base))
			}
			return e.rebuild(ctx, t, fmt.Sprintf("%s moved from %s to %s during the run, so it is tested again on the new %s", t.Base, short(t.BaseSHA), short(base), t.Base))
		}
		changed, err := e.recheck(ctx, t)
		if err != nil {
			return err
		}
		if len(changed) > 0 {
			t.endRun(RunChanged, "")
			return e.rebuild(ctx, t, "nothing merged, since "+strings.Join(changed, "; ")+", so the rest is tested again without it")
		}
		t.Landing = true
		if err := e.save(t); err != nil {
			return err
		}
	}
	riders := t.carsIn(CarRiding)
	for _, i := range riders {
		if err := e.merge(ctx, t, i); err != nil {
			return err
		}
	}
	t.endRun(RunLanded, "")
	matches, landed, err := e.holdsTheRun(ctx, *t)
	if err != nil {
		return err
	}
	if !matches {
		return e.finish(ctx, t, StateFailed, fmt.Sprintf("after the run landed, %s at %s does not hold the tree CI tested (%s): something else merged during the landing, so %s's own push CI decides", t.Base, short(landed), short(t.Head), t.Base))
	}
	t.Landing, t.Moved = false, 0
	waiting := t.carsIn(CarWaiting)
	if len(waiting) == 0 {
		return e.finish(ctx, t, StateLanded, "")
	}
	for _, i := range waiting[:(len(waiting)+1)/2] {
		t.Cars[i].State = CarRiding
	}
	return e.rebuild(ctx, t, fmt.Sprintf("the green half landed; next on the new %s: %s", t.Base, t.numbers(t.carsIn(CarRiding))))
}

// recheck reads the riders as GitHub has them now and returns each one that
// may no longer land as it rode, with why, after returning it to the queue.
func (e Engine) recheck(ctx context.Context, t *Train) ([]string, error) {
	out, err := e.run(ctx, t.Checkout, "gh", "pr", "list", "--repo", t.Repository, "--state", "open", "--base", t.Base, "--limit", "100", "--json", "url,headRefOid,isDraft,labels")
	if err != nil {
		return nil, err
	}
	var open []PullRequest
	if err := json.Unmarshal([]byte(out), &open); err != nil {
		return nil, fmt.Errorf("gh listed the open pull requests of %s in a shape it cannot read: %w", t.Repository, err)
	}
	var changed []string
	for _, i := range t.carsIn(CarRiding) {
		car := &t.Cars[i]
		why := "it is no longer open"
		for _, pr := range open {
			if pr.URL != car.URL {
				continue
			}
			switch {
			case pr.HeadRefOid != car.Head:
				why = fmt.Sprintf("its head moved from %s to %s", short(car.Head), short(pr.HeadRefOid))
			case pr.IsDraft:
				why = "it became a draft"
			case isHeld(pr):
				why = "it was labelled " + HoldLabel
			default:
				why = ""
			}
		}
		if why != "" {
			car.State, car.Note = CarReturned, why
			changed = append(changed, fmt.Sprintf("#%d %s", car.Number, why))
		}
	}
	return changed, nil
}

// merge merges rider i's pull request with a merge commit, only while its
// head is the one that rode, and records it landed. GitHub refuses a merge
// whose base moved under it, which merging several in a row can meet, so
// that refusal is tried again; and a merge that went through although its
// answer was lost is found on the base and taken as landed.
func (e Engine) merge(ctx context.Context, t *Train, i int) error {
	car := &t.Cars[i]
	for attempt := 1; ; attempt++ {
		_, err := e.run(ctx, t.Checkout, "gh", "pr", "merge", car.URL, "--merge", "--match-head-commit", car.Head)
		if err == nil {
			break
		}
		if onBase, checkErr := e.isOnBase(ctx, *t, car.Head); checkErr == nil && onBase {
			break
		}
		if attempt == mergeAttempts || !strings.Contains(err.Error(), "Base branch was modified") {
			return fmt.Errorf("#%d did not merge: %w", car.Number, err)
		}
		e.Wait(mergeRetryAfter)
	}
	car.State = CarLanded
	if err := e.save(t); err != nil {
		return err
	}
	if e.Landed != nil {
		e.Landed(ctx, *t, *car)
	}
	return nil
}

// isOnBase says whether commit is on the base branch as origin has it now.
func (e Engine) isOnBase(ctx context.Context, t Train, commit string) (bool, error) {
	base, err := e.baseSHA(ctx, t)
	if err != nil {
		return false, err
	}
	if _, err := e.run(ctx, t.Checkout, "git", "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin", "refs/heads/"+t.Base); err != nil {
		return false, err
	}
	return e.isAncestor(ctx, t.Checkout, commit, base)
}

// holdsTheRun says whether the base as origin has it now holds the tree of
// the run that landed, and names the commit it read.
func (e Engine) holdsTheRun(ctx context.Context, t Train) (bool, string, error) {
	landed, err := e.baseSHA(ctx, t)
	if err != nil {
		return false, "", err
	}
	if _, err := e.run(ctx, t.Checkout, "git", "fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "origin", "refs/heads/"+t.Base); err != nil {
		return false, landed, err
	}
	baseTree, err := e.run(ctx, t.Checkout, "git", "rev-parse", landed+"^{tree}")
	if err != nil {
		return false, landed, err
	}
	trainTree, err := e.run(ctx, t.Checkout, "git", "rev-parse", t.Head+"^{tree}")
	if err != nil {
		return false, landed, err
	}
	return baseTree == trainTree, landed, nil
}
