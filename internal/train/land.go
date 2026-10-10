package train

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// land takes a green run to the base by merging the train's own pull
// request, pinned to the head CI tested: the base takes that commit, so its
// tree is the run's, and GitHub marks each rider merged, since the base then
// holds its head. It checks both, and tests the waiting half next if a red
// run left one.
//
// Before the merge it rechecks every rider: one closed, turned into a draft,
// labelled hold or moved to another head since it rode must not land as it
// rode, so nothing merges and the rest is tested again without it. A base
// that moved during the run is tested again too, since what the run proved
// is not what would land. The merge lands every rider or none. Before it
// starts the record says so, and a landing cut short finds on the base
// whether it went through. The pull request that merges wears the train's
// label, which is made sure of here: an older build opened one without it,
// and a label can be taken off by hand.
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
	if riders := t.carsIn(CarRiding); len(riders) > 0 {
		// A run an older build pushed names no pull request, and its pull
		// request still says it is never merged: it is told what it is before
		// it merges.
		if run := t.openRun(); run != nil && run.PR == "" {
			if err := e.describe(ctx, t); err != nil {
				return err
			}
			run.PR = t.PR
			if err := e.save(t); err != nil {
				return err
			}
		}
		e.wear(ctx, *t)
		if err := e.mergeTrain(ctx, t, riders); err != nil {
			return err
		}
		matches, landed, err := e.holdsTheRun(ctx, *t)
		if err != nil {
			return err
		}
		t.endRun(RunLanded, "")
		for _, i := range riders {
			t.Cars[i].State = CarLanded
			if err := e.save(t); err != nil {
				return err
			}
			if e.Landed != nil {
				e.Landed(ctx, *t, t.Cars[i])
			}
		}
		if !matches {
			return e.finish(ctx, t, StateFailed, fmt.Sprintf("after the run landed, %s at %s does not hold the tree CI tested (%s): something else merged during the landing, so %s's own push CI decides", t.Base, short(landed), short(t.Head), t.Base))
		}
	}
	if err := e.confirmMerged(ctx, t); err != nil {
		return err
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
	open, err := e.openPulls(ctx, *t)
	if err != nil {
		return nil, err
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

// openPulls reads the open pull requests into the train's base as GitHub has
// them now.
func (e Engine) openPulls(ctx context.Context, t Train) ([]PullRequest, error) {
	out, err := e.run(ctx, t.Checkout, "gh", "pr", "list", "--repo", t.Repository, "--state", "open", "--base", t.Base, "--limit", "100", "--json", "url,headRefOid,isDraft,labels")
	if err != nil {
		return nil, err
	}
	var open []PullRequest
	if err := json.Unmarshal([]byte(out), &open); err != nil {
		return nil, fmt.Errorf("gh listed the open pull requests of %s in a shape it cannot read: %w", t.Repository, err)
	}
	return open, nil
}

// landingSubject starts the subject of the merge commit a train's own pull
// request leaves on the base, before the train's id and the pull requests
// that commit landed: "Merge train r-20261010-124036: #1, #2".
const landingSubject = "Merge train "

// landedIn finds the pull requests in the subject of a train's merge commit.
var landedIn = regexp.MustCompile(`^` + landingSubject + `\S+: (#\d+(?:, #\d+)*)$`)

// LandedIn names the pull requests the commit titled subject landed, when it
// is the merge commit of a train's own pull request, and none for any other
// commit. A push run on the base is titled by its commit, so it tells which
// pull requests that run deploys.
func LandedIn(subject string) []string {
	found := landedIn.FindStringSubmatch(subject)
	if found == nil {
		return nil
	}
	return strings.Split(strings.ReplaceAll(found[1], "#", ""), ", ")
}

// mergeTrain merges the train's own pull request with a merge commit, only
// while its head is the one CI tested. One merge lands every rider or none:
// when GitHub refuses it nothing merged, so the landing is over, and the
// next step reads the base and the riders again before it tries. A merge
// that went through although its answer was lost is found on the base and
// taken as landed.
func (e Engine) mergeTrain(ctx context.Context, t *Train, riders []int) error {
	_, err := e.run(ctx, t.Checkout, "gh", "pr", "merge", t.PR, "--merge", "--match-head-commit", t.Head, "--subject", landingSubject+t.ID+": "+t.numbers(riders), "--body", t.Evidence())
	if err == nil {
		return nil
	}
	onBase, checkErr := e.isOnBase(ctx, *t, t.Head)
	switch {
	case checkErr != nil:
		return errors.Join(err, checkErr)
	case onBase:
		return nil
	}
	t.Landing = false
	return errors.Join(fmt.Errorf("its pull request %s did not merge: %w", t.PR, err), e.save(t))
}

// confirmMerged reads that GitHub shows each rider of the run that landed as
// merged. GitHub marks a pull request merged once the base holds its head, a
// moment after the push, so one it still lists open is read again, up to
// mergedReads times. One still open then is named to the CFO and keeps why
// on its car: its head moved after the train last read it, so the base holds
// what rode and the rest stays open, or GitHub did not mark it.
func (e Engine) confirmMerged(ctx context.Context, t *Train) error {
	var landed []int
	if last := len(t.History) - 1; last >= 0 {
		landed = t.History[last].Riders
	}
	var stillOpen []PullRequest
	for read := 1; ; read++ {
		open, err := e.openPulls(ctx, *t)
		if err != nil {
			return err
		}
		stillOpen = slices.DeleteFunc(open, func(pr PullRequest) bool {
			return !slices.ContainsFunc(t.Cars, func(car Car) bool {
				return car.URL == pr.URL && car.State == CarLanded && slices.Contains(landed, car.Number)
			})
		})
		if len(stillOpen) == 0 || read == mergedReads {
			break
		}
		e.Wait(mergedReadAfter)
	}
	if len(stillOpen) == 0 {
		return nil
	}
	var said []string
	for i := range t.Cars {
		car := &t.Cars[i]
		at := slices.IndexFunc(stillOpen, func(pr PullRequest) bool { return pr.URL == car.URL })
		if at < 0 {
			continue
		}
		car.Note = fmt.Sprintf("%s holds its head %s, and GitHub has not marked its pull request merged", t.Base, short(car.Head))
		next := "Read it again, and close it by hand if it stays open."
		if moved := stillOpen[at].HeadRefOid; moved != car.Head {
			car.Note = fmt.Sprintf("%s holds the head that rode, %s, and its pull request stays open with what was pushed after, at %s", t.Base, short(car.Head), short(moved))
			next = "It rides the next train once its checks pass on that head."
		}
		said = append(said, fmt.Sprintf("#%d: %s. %s", car.Number, car.Note, next))
	}
	e.tellCFO(ctx, fmt.Sprintf("merge_train: %s train %s landed on %s, and GitHub still shows a pull request it landed open. %s", t.Repository, t.ID, t.Base, strings.Join(said, " ")))
	return e.save(t)
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
