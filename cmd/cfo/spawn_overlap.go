package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// teammateOverlap is what teammates have in flight where a task's brief
// works, read before the task starts so nobody begins work someone else
// already has under way without knowing it. It is empty when only the
// Overlord works in the repository or nothing of a teammate's meets the
// brief's area. Beside it comes what the read stopped short of, so a check
// that was incomplete never passes for a complete one.
//
// The issue the task itself is for is not an overlap: the one its ticket is
// on, or the one its brief names. A runtime with no repository read (a
// test's) checks nothing. The read runs under overlapTimeout, so one that
// hangs is a failed read.
func teammateOverlap(runtime commandRuntime, h home.Home, id, checkout, brief string, now time.Time) (tickets.Overlaps, []string, error) {
	if runtime.repoActivity == nil {
		return tickets.Overlaps{}, nil, nil
	}
	timeout := overlapTimeout
	if runtime.overlapTimeout > 0 {
		timeout = runtime.overlapTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	activity, err := runtime.repoActivity(ctx, checkout, now)
	if err != nil {
		return tickets.Overlaps{}, nil, err
	}
	area := tickets.BriefArea(brief, checkoutHas(checkout))
	report := tickets.Build(activity, now, &area)
	if !report.Collaborative {
		return tickets.Overlaps{}, nil, nil
	}
	own := map[int]bool{}
	if number, ok := tickets.ClaimedIssue(brief, activity.Repository); ok {
		own[number] = true
	}
	if record, err := tickets.ReadRecord(h.State, id); err == nil {
		own[record.Number] = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return tickets.Overlaps{}, nil, err
	}
	theirs := report.TeammateOverlaps()
	theirs.Issues = slices.DeleteFunc(theirs.Issues, func(match tickets.IssueMatch) bool { return own[match.Number] })
	return theirs, report.Unread, nil
}

// maxUnreadShown is how many of the things a read stopped short of cfo spawn
// names before it counts the rest.
const maxUnreadShown = 3

// warnIncompleteCheck says in one line what the read stopped short of, in
// the words cfo tickets uses for it.
func warnIncompleteCheck(stderr io.Writer, unread []string) {
	shown := unread
	if len(shown) > maxUnreadShown {
		shown = append(slices.Clone(shown[:maxUnreadShown]), fmt.Sprintf("and %d more", len(unread)-maxUnreadShown))
	}
	count := fmt.Sprintf("%d things were", len(unread))
	if len(unread) == 1 {
		count = "1 thing was"
	}
	fmt.Fprintf(stderr, "cfo spawn: the check for teammates' work was incomplete, %s not read: %s\n", count, strings.Join(shown, "; "))
}

// refuseOverlap says what a teammate has in flight in the task's area and
// how the CFO starts the task beside it.
func refuseOverlap(stderr io.Writer, overlap tickets.Overlaps) {
	fmt.Fprintln(stderr, "cfo spawn: a teammate has work in flight where this task works:")
	for _, line := range overlap.Lines() {
		fmt.Fprintf(stderr, "- %s\n", line)
	}
	fmt.Fprintln(stderr, `cfo spawn: not started. To start beside it, repeat with --overlap-ok "<why>"; the reason goes on the task's ticket and status log.`)
}

// recordOverlapAccepted keeps the CFO's reason for starting a task beside a
// teammate's work: in the task's status log with what it overlaps, and as
// the note the supervisor puts on the task's ticket.
func recordOverlapAccepted(h home.Home, id, why string, overlap tickets.Overlaps) error {
	var beside []string
	for _, file := range overlap.Files {
		if file.PullRequest != 0 {
			beside = append(beside, fmt.Sprintf("PR #%d", file.PullRequest))
		} else {
			beside = append(beside, "branch "+file.Branch)
		}
	}
	for _, match := range overlap.Issues {
		beside = append(beside, fmt.Sprintf("issue #%d", match.Number))
	}
	note := strings.Join(beside, ", ") + ": " + why
	return errors.Join(
		tickets.WriteOverlapNote(h.State, id, note),
		state.AppendStatus(h.State, id, fmt.Sprintf("overlap-accepted: %s (%s)", why, strings.Join(overlap.Lines(), "; "))),
	)
}
