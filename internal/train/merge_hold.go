package train

import (
	"context"
	"fmt"
)

func sameChecks(first, second Checks) bool {
	if len(first) != len(second) {
		return false
	}
	counts := make(map[Check]int, len(first))
	for _, check := range first {
		counts[check]++
	}
	for _, check := range second {
		if counts[check] == 0 {
			return false
		}
		counts[check]--
	}
	return true
}

func (e Engine) holdMerge(ctx context.Context, t *Train, reason, readiness string) error {
	run := t.openRun()
	if run.MergeHold == "" {
		run.MergeHold = reason
	}
	run.MergeReadiness = readiness
	t.Landing = false
	t.Note = fmt.Sprintf("holds its merge after %d attempt(s): %s; keeps the green proof on %s at %s, built on %s at %s", run.MergeAttempts, reason, run.PR, short(run.Head), t.Base, short(run.Base))
	if err := e.save(t); err != nil {
		return err
	}
	return e.notifyMergeHold(ctx, t)
}

func (e Engine) notifyMergeHold(ctx context.Context, t *Train) error {
	run := t.openRun()
	if run.IsMergeNotified || e.TellCFO == nil {
		return nil
	}
	identity := fmt.Sprintf("train-merge-hold:%s:%d", t.ID, run.Number)
	if err := e.TellCFO(ctx, identity, fmt.Sprintf("merge_train: %s train %s holds its merge: %s; keeps the green proof on %s at %s, built on %s at %s", t.Repository, t.ID, run.MergeHold, run.PR, short(run.Head), t.Base, short(run.Base))); err != nil {
		return err
	}
	run.IsMergeNotified = true
	return e.save(t)
}

func (e Engine) mayMerge(ctx context.Context, t *Train, readiness string) (bool, error) {
	run := t.openRun()
	isReady := readiness == "CLEAN" || readiness == "HAS_HOOKS"
	if run.MergeHold != "" {
		if err := e.notifyMergeHold(ctx, t); err != nil {
			return false, err
		}
		previous := run.MergeReadiness
		run.MergeReadiness = readiness
		if err := e.save(t); err != nil {
			return false, err
		}
		return isReady && previous != readiness, nil
	}
	if !isReady || run.MergeAttempts >= maxErrors {
		return false, e.holdMerge(ctx, t, "GitHub reports merge readiness "+readiness, readiness)
	}
	run.MergeReadiness = readiness
	return true, nil
}
