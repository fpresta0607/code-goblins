package workreview

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/tickets"
)

var (
	// pullAddress is a pull request's address on GitHub, wherever a text has it.
	pullAddress = regexp.MustCompile(`https://github\.com/([\w.-]+/[\w.-]+)/pull/(\d+)`)
	// noteStamp opens a note line of a backlog row, written either as a date,
	// 2026-11-18, or as a time, a day and a month with no year, 05:43Z 18 Nov.
	noteStamp = regexp.MustCompile(`^(?:(\d{4}-\d{2}-\d{2})|(\d{1,2}:\d{2}Z \d{1,2} [A-Z][a-z]{2}))\b`)
)

// Resolver names the GitHub repository (owner/name, lower case, "" when it has
// none) and the checkout of a project as a row or task record names it, a name
// or a path.
type Resolver func(project string) (repository, checkout string)

// Gather reads the home's queued rows, parked rows and paused tasks as items,
// with what it could not read as problems. now places the note lines that
// carry no year. Paused tasks come first, by when they began, then the queued
// rows and the parked rows in file order. A row whose task has a record is no
// row item: the paused or live task stands for it.
func Gather(h home.Home, resolve Resolver, now time.Time) (items []Item, problems []string, err error) {
	scan, err := state.ScanIDs(h.State)
	if err != nil {
		return nil, nil, err
	}
	current, err := fleet.ReadCurrentBacklog(h)
	if err != nil {
		return nil, nil, err
	}
	// A paused task's own row stays queued, which the current backlog leaves out.
	written, err := fleet.ReadBacklog(h)
	if err != nil {
		return nil, nil, err
	}
	ownRows := map[string]fleet.BacklogRow{}
	for _, rows := range [][]fleet.BacklogRow{written.Queued, written.Parked} {
		for _, row := range rows {
			if row.Structured {
				ownRows[row.ID] = row
			}
		}
	}

	hasRecord := map[string]bool{}
	for _, id := range scan.MetaIDs {
		hasRecord[id] = true
	}
	var paused []Item
	began := map[string]time.Time{}
	for _, id := range scan.MetaIDs {
		item, startedAt, isPaused, err := pausedItem(h, resolve, id, ownRows[id], now)
		if err != nil {
			problems = append(problems, fmt.Sprintf("task %s is not read: %v", id, err))
			continue
		}
		if isPaused {
			paused = append(paused, item)
			began[id] = startedAt
		}
	}
	slices.SortStableFunc(paused, func(a, b Item) int { return began[a.ID].Compare(began[b.ID]) })
	items = paused

	for _, section := range []struct {
		source string
		rows   []fleet.BacklogRow
	}{{SourceQueued, current.Queued}, {SourceParked, current.Parked}} {
		for _, row := range section.rows {
			if !row.Structured || state.ValidTaskID(row.ID) != nil || hasRecord[row.ID] {
				continue
			}
			item, err := rowItem(h, resolve, section.source, row, now)
			if err != nil {
				problems = append(problems, fmt.Sprintf("row %s is not read: %v", row.ID, err))
				continue
			}
			items = append(items, item)
		}
	}
	for index := range items {
		items[index].Order = index
	}
	return items, problems, nil
}

// pausedItem is task id as an item, when its lifecycle says it is paused for
// its current generation, with when it began. row is its backlog row, if it
// has one.
func pausedItem(h home.Home, resolve Resolver, id string, row fleet.BacklogRow, now time.Time) (item Item, startedAt time.Time, isPaused bool, err error) {
	meta, err := state.ReadTaskMeta(h.State, id)
	if err != nil {
		return Item{}, time.Time{}, false, err
	}
	record, err := state.ReadLifecycle(h.State, id)
	if errors.Is(err, fs.ErrNotExist) {
		return Item{}, time.Time{}, false, nil
	}
	if err != nil {
		return Item{}, time.Time{}, false, err
	}
	if record.Phase != "paused" || record.Generation != meta.SpawnGen {
		return Item{}, time.Time{}, false, nil
	}
	brief, err := tickets.TaskBrief(h, id, meta.Brief)
	if err != nil {
		return Item{}, time.Time{}, false, err
	}
	pulls, err := tickets.TaskPullRequests(h.State, id)
	if err != nil {
		return Item{}, time.Time{}, false, err
	}
	latest, err := lastActivity(h, id, meta.Brief, "", now)
	if err != nil {
		return Item{}, time.Time{}, false, err
	}
	repository, checkout := resolve(meta.Project)
	item = Item{
		ID:           id,
		Source:       SourcePaused,
		Project:      meta.Project,
		Repository:   repository,
		Issues:       namedIssues(brief, row.Raw, repository),
		Pulls:        withPullRefs(pullRefs(strings.Join(pulls, " ")), rowPulls(row)),
		Paths:        tickets.BriefArea(brief, inCheckout(checkout)).Paths,
		LastActivity: latest,
	}
	if record.Pause != nil {
		item.IsOwnerStep = record.Pause.Reason == "overlord"
		if date, isDate := pauseDate(record.Pause.Until); isDate {
			item.Dates = []time.Time{date}
		}
	}
	return item, record.Started, true, nil
}

// rowItem is one queued or parked backlog row as an item.
func rowItem(h home.Home, resolve Resolver, source string, row fleet.BacklogRow, now time.Time) (Item, error) {
	brief, err := tickets.TaskBrief(h, row.ID, "")
	if err != nil {
		return Item{}, err
	}
	latest, err := lastActivity(h, row.ID, "", row.Detail, now)
	if err != nil {
		return Item{}, err
	}
	project := row.Repo
	if project == "" {
		project = fleet.BriefProject(strings.Split(brief, "\n"))
	}
	repository, checkout := resolve(project)
	item := Item{
		ID:           row.ID,
		Source:       source,
		Project:      project,
		Repository:   repository,
		IsOwnerStep:  strings.EqualFold(row.Kind, "his") || strings.EqualFold(row.Kind, "ceo"),
		IsHeld:       source == SourceParked,
		Issues:       namedIssues(brief, row.Raw, repository),
		Paths:        tickets.BriefArea(brief, inCheckout(checkout)).Paths,
		LastActivity: latest,
	}
	// A step for the Overlord or the CEO is about the pull request it waits
	// on; other work only waits for it.
	if item.IsOwnerStep {
		item.Pulls = pullRefs(row.Raw)
	} else {
		item.Pulls = rowPulls(row)
	}
	for _, blocker := range row.Blockers {
		if blocker.Kind == "hold" {
			item.IsHeld = true
		}
		if !blocker.Until.IsZero() {
			item.Dates = append(item.Dates, blocker.Until)
		}
	}
	return item, nil
}

// inCheckout says a repository path is there in checkout, and nothing is when
// the checkout is unknown.
func inCheckout(checkout string) func(string) bool {
	return func(repositoryPath string) bool {
		if checkout == "" {
			return false
		}
		_, err := os.Stat(filepath.Join(checkout, filepath.FromSlash(repositoryPath)))
		return err == nil
	}
}

// rowPulls are the pull requests a backlog row's line names as its own work:
// every pull request address on it except those it waits on, which are other
// work its start waits for.
func rowPulls(row fleet.BacklogRow) []tickets.IssueRef {
	var awaited []tickets.IssueRef
	for _, blocker := range row.Blockers {
		if blocker.Kind == "pr" {
			awaited = append(awaited, pullRefs(blocker.Target)...)
		}
	}
	var own []tickets.IssueRef
	for _, ref := range pullRefs(row.Raw) {
		if !slices.Contains(awaited, ref) {
			own = append(own, ref)
		}
	}
	return own
}

// withPullRefs is refs with each of more added that it does not hold yet.
func withPullRefs(refs, more []tickets.IssueRef) []tickets.IssueRef {
	for _, ref := range more {
		if !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	return refs
}

// namedIssues are the issues a brief and a backlog row line name, each once,
// leaving out the bare numbers of a project that has no repository.
func namedIssues(brief, rowLine, repository string) []tickets.IssueRef {
	var issues []tickets.IssueRef
	for _, ref := range append(tickets.BriefIssueRefs(brief, repository), tickets.RowIssueRefs(rowLine, repository)...) {
		if ref.Repository != "" && !slices.Contains(issues, ref) {
			issues = append(issues, ref)
		}
	}
	return issues
}

// pullRefs are the pull requests whose GitHub addresses text holds, each once.
func pullRefs(text string) []tickets.IssueRef {
	var refs []tickets.IssueRef
	for _, match := range pullAddress.FindAllStringSubmatch(text, -1) {
		number, err := strconv.Atoi(match[2])
		if err != nil || number < 1 {
			continue
		}
		if ref := (tickets.IssueRef{Repository: strings.ToLower(match[1]), Number: number}); !slices.Contains(refs, ref) {
			refs = append(refs, ref)
		}
	}
	return refs
}

// pauseDate is the date a pause's until names, written as RFC 3339 or as
// 2006-01-02, with or without the "date:" a dependency pause puts before it.
func pauseDate(until string) (time.Time, bool) {
	until = strings.TrimPrefix(until, "date:")
	for _, layout := range []string{time.RFC3339, time.DateOnly} {
		if date, err := time.Parse(layout, until); err == nil {
			return date.UTC(), true
		}
	}
	return time.Time{}, false
}

// lastActivity is the latest of the stamps that open the note lines in detail,
// the stamp of the last line of task id's status log and the modification time
// of its brief, which is at briefPath or else in the home's data folder. It is
// zero when none is on record.
func lastActivity(h home.Home, id, briefPath, detail string, now time.Time) (time.Time, error) {
	var latest time.Time
	for _, line := range strings.Split(detail, "\n") {
		if stamp := noteLineStamp(line, now); stamp.After(latest) {
			latest = stamp
		}
	}
	lines, err := state.TailStatus(h.State, id, 1)
	if err != nil {
		return time.Time{}, err
	}
	for _, line := range lines {
		if stamp, _ := state.SplitStatus(line); stamp.After(latest) {
			latest = stamp
		}
	}
	for _, candidate := range []string{briefPath, filepath.Join(h.Data, id, "brief.md")} {
		if candidate == "" {
			continue
		}
		info, err := os.Stat(candidate)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return time.Time{}, err
		}
		if modified := info.ModTime().UTC(); modified.After(latest) {
			latest = modified
		}
		break
	}
	return latest, nil
}

// noteLineStamp is the moment a note line opens with, or zero when it opens
// with none. A time with no year is the most recent such moment not after now.
func noteLineStamp(line string, now time.Time) time.Time {
	match := noteStamp.FindStringSubmatch(strings.TrimLeft(line, "-* \t"))
	if match == nil {
		return time.Time{}
	}
	if match[1] != "" {
		date, err := time.Parse(time.DateOnly, match[1])
		if err != nil {
			return time.Time{}
		}
		return date
	}
	moment, err := time.Parse("15:04Z 2 Jan", match[2])
	if err != nil {
		return time.Time{}
	}
	stamp := time.Date(now.Year(), moment.Month(), moment.Day(), moment.Hour(), moment.Minute(), 0, 0, time.UTC)
	if stamp.After(now) {
		stamp = stamp.AddDate(-1, 0, 0)
	}
	return stamp
}
