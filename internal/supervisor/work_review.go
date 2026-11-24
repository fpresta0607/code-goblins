package supervisor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/workreview"
)

// WorkReview is what the supervisor needs to review the home's queued rows,
// parked rows and paused tasks on a schedule, and to hold the start of a queued
// task that duplicates work in flight. Without it neither happens.
type WorkReview struct {
	// Resolve names the repository and the checkout of a project as a row or a
	// task record names it.
	Resolve workreview.Resolver
	// States reads the states of numbers of one repository in one query.
	States workreview.StateReader
}

const (
	// workReviewCheckEvery is how often the supervisor asks whether a review is
	// due. The review itself costs a GitHub query for each repository, so it
	// runs only when due.
	workReviewCheckEvery = time.Hour
	// workReviewEvery is the longest a review waits for the one before it,
	// unless an allowance renewal brings it earlier.
	workReviewEvery = 24 * time.Hour
	// workReviewFile records, in the state folder, when the review last ran and
	// the findings it reported.
	workReviewFile = "work-review.json"
	// workReviewListLimit bounds the findings the review's wake names, which
	// goblins backlog review lists in full.
	workReviewListLimit = 1500
)

// workReviewLedger is what the review remembers between runs, so a finding is
// reported once however many runs find it.
type workReviewLedger struct {
	LastRun  time.Time `json:"last_run"`
	Reported []string  `json:"reported,omitempty"`
}

// keepReviewingWork checks every workReviewCheckEvery, and when an allowance
// renewal is signalled, whether a work review is due, until ctx ends.
func (s *Service) keepReviewingWork(ctx context.Context) {
	ticker := time.NewTicker(workReviewCheckEvery)
	defer ticker.Stop()
	isRenewed := false
	for {
		if err := s.reviewWork(ctx, time.Now().UTC(), isRenewed); err != nil {
			s.publish(err)
		}
		isRenewed = false
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.allowanceRenewed:
			isRenewed = true
		}
	}
}

// reviewWork runs the work review when it is due: it never ran, it last ran
// workReviewEvery ago or more, or isRenewed says an allowance renewed since.
// The supervisor's start alone does not make it due, so a restart within
// workReviewEvery of the last run reviews nothing.
// It costs no model tokens and one GitHub query for each repository the home's
// work names. Each finding not reported before raises, all in one wake, a
// notify keyed work-review; a run that finds nothing new raises none. What a
// run could not read is returned, never woken for, and such a run forgets no
// finding it reported before.
func (s *Service) reviewWork(ctx context.Context, now time.Time, isRenewed bool) error {
	review := s.Options.WorkReview
	h := s.Store.Home
	ledger, ledgerErr := readWorkReviewLedger(h.State)
	if !isRenewed && !ledger.LastRun.IsZero() && now.Sub(ledger.LastRun) < workReviewEvery {
		return ledgerErr
	}
	settings, err := fleetconfig.Read(h.Root)
	if err != nil {
		return errors.Join(ledgerErr, err)
	}
	report, err := workreview.Run(ctx, h, review.Resolve, review.States, now, time.Duration(settings.StaleWorkDays)*24*time.Hour)
	if err != nil {
		return errors.Join(ledgerErr, err)
	}
	var problems error
	for _, problem := range report.Problems {
		problems = errors.Join(problems, fmt.Errorf("work review: %s", problem))
	}
	var fresh []workreview.Finding
	var keys []string
	for _, finding := range report.Findings {
		keys = append(keys, finding.Key())
		if !slices.Contains(ledger.Reported, finding.Key()) {
			fresh = append(fresh, finding)
		}
	}
	// A run that could not read part of the home may miss findings it
	// reported before, so it forgets none of them.
	if len(report.Problems) > 0 {
		for _, key := range ledger.Reported {
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	if len(fresh) > 0 {
		if err := raiseFleetWake(h.State, "notify", "work-review", workReviewDetail(fresh)); err != nil {
			return errors.Join(ledgerErr, problems, err)
		}
	}
	return errors.Join(ledgerErr, problems, writeWorkReviewLedger(h.State, workReviewLedger{LastRun: now, Reported: keys}))
}

// workReviewDetail is the review's wake: how many findings are new, each in a
// line of its own, and where the evidence is.
func workReviewDetail(findings []workreview.Finding) string {
	lines := make([]string, len(findings))
	for index, finding := range findings {
		lines[index] = fmt.Sprintf("- %s: %s; proposed: %s", finding.Kind, strings.Join(finding.Items, ", "), finding.Proposal)
	}
	noun := "findings"
	if len(findings) == 1 {
		noun = "finding"
	}
	return fmt.Sprintf("work review: %d new %s\n%s\nRun goblins backlog review for the evidence.", len(findings), noun, bounded(strings.Join(lines, "\n"), workReviewListLimit))
}

// readWorkReviewLedger reads what the review remembers. A missing ledger is a
// fresh start; one that cannot be read is also started afresh, and the error
// says so, since a lost ledger repeats the findings it reported.
func readWorkReviewLedger(stateDir string) (workReviewLedger, error) {
	path := filepath.Join(stateDir, workReviewFile)
	data, err := fsx.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return workReviewLedger{}, nil
	}
	if err != nil {
		return workReviewLedger{}, fmt.Errorf("work review: %w", err)
	}
	var ledger workReviewLedger
	if err := json.Unmarshal(data, &ledger); err != nil {
		return workReviewLedger{}, fmt.Errorf("work review: %s is unreadable and starts afresh", path)
	}
	return ledger, nil
}

func writeWorkReviewLedger(stateDir string, ledger workReviewLedger) error {
	data, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(filepath.Join(stateDir, workReviewFile), data)
}
