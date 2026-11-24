// Package workreview decides which queued rows, parked rows and paused tasks
// need a decision: work that is done already, steps for the owner that were
// overtaken, work that should fold into other work, and work gone stale. It
// reads no repository and no clock: the caller hands it every state and the
// time.
package workreview

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/tickets"
)

// The sources of an item.
const (
	// SourceQueued is a queued backlog row not yet started.
	SourceQueued = "queued"
	// SourceParked is a parked backlog row.
	SourceParked = "parked"
	// SourcePaused is a paused task.
	SourcePaused = "paused"
)

// The kinds of a finding, in the order a review lists them.
const (
	KindDone      = "done already"
	KindOvertaken = "overtaken"
	KindFold      = "fold"
	KindStale     = "stale"
)

const (
	stateClosed = "closed"
	stateMerged = "merged"

	staleProposal     = "ask the owner once whether to resume, fold or retire it"
	overtakenProposal = "close the step, or say what it still waits on"
)

// kindOrder is the place of each kind in a review's list.
var kindOrder = map[string]int{KindDone: 0, KindOvertaken: 1, KindFold: 2, KindStale: 3}

// Item is one queued row, parked row or paused task, as far as a review
// needs to know it.
type Item struct {
	ID         string
	Source     string
	Project    string // the project as its row or record names it
	Repository string // owner/name in lower case, "" when unknown
	// IsOwnerStep is a step only the Overlord or the CEO takes: a row of kind
	// his or ceo, or a task paused for the Overlord.
	IsOwnerStep  bool
	IsHeld       bool               // a parked row, or a row a hold holds
	Issues       []tickets.IssueRef // issues it names
	Pulls        []tickets.IssueRef // pull requests it names, by repository and number
	Paths        []string           // repository paths its brief's area names
	Dates        []time.Time        // its dated conditions: a blocked-by until, a hold-until, a pause's until
	LastActivity time.Time          // zero when none is on record
	Order        int                // lower is older
}

// States are the states GitHub gave, by repository and number: "open",
// "closed", "merged" or "missing". A number absent was not read.
type States map[tickets.IssueRef]string

// Finding is one thing a review reports: its kind, the items it names, the
// evidence for it and what to do about it.
type Finding struct {
	Kind     string
	Items    []string // the IDs it names, the one others fold into first for a fold
	Evidence []string
	Proposal string
}

// Key is the finding's identity, its kind and the items it names, so a
// finding seen again is known.
func (f Finding) Key() string {
	return f.Kind + ":" + strings.Join(f.Items, ",")
}

// Review reports what needs a decision among items, as of now, given the
// states of the issues and pull requests they name. Work with no activity for
// staleAfter and no dated condition is stale. Findings come in kind order,
// done already, overtaken, fold and stale, and within a kind by the order of
// the first item each names.
func Review(items []Item, states States, now time.Time, staleAfter time.Duration) []Finding {
	ordered := slices.Clone(items)
	slices.SortStableFunc(ordered, func(a, b Item) int { return a.Order - b.Order })

	var findings []Finding
	for _, item := range ordered {
		if evidence, isDone := doneEvidence(item, states); isDone {
			findings = append(findings, Finding{Kind: KindDone, Items: []string{item.ID}, Evidence: evidence, Proposal: doneProposal(item)})
		}
	}
	for _, item := range ordered {
		if evidence := overtakenEvidence(item, states, now); len(evidence) > 0 {
			findings = append(findings, Finding{Kind: KindOvertaken, Items: []string{item.ID}, Evidence: evidence, Proposal: overtakenProposal})
		}
	}
	findings = append(findings, foldFindings(ordered, states)...)
	for _, item := range ordered {
		if evidence := staleEvidence(item, states, now, staleAfter); len(evidence) > 0 {
			findings = append(findings, Finding{Kind: KindStale, Items: []string{item.ID}, Evidence: evidence, Proposal: staleProposal})
		}
	}

	orderOf := make(map[string]int, len(ordered))
	for _, item := range ordered {
		orderOf[item.ID] = item.Order
	}
	slices.SortStableFunc(findings, func(a, b Finding) int {
		if a.Kind != b.Kind {
			return kindOrder[a.Kind] - kindOrder[b.Kind]
		}
		return orderOf[a.Items[0]] - orderOf[b.Items[0]]
	})
	return findings
}

// isIssueSettled is whether an issue in state is closed, or merged as a row
// may name a pull request by its number.
func isIssueSettled(state string) bool {
	return state == stateClosed || state == stateMerged
}

// settledEvidence is a line for each issue of item that is settled and each
// pull request of item that merged, issues first.
func settledEvidence(item Item, states States) []string {
	var lines []string
	for _, ref := range item.Issues {
		if state := states[ref]; isIssueSettled(state) {
			lines = append(lines, ref.String()+" "+state)
		}
	}
	for _, ref := range item.Pulls {
		if states[ref] == stateMerged {
			lines = append(lines, "pull request "+ref.String()+" merged")
		}
	}
	return lines
}

// doneEvidence is the line for each issue and pull request item names, and
// whether item is done already: it names at least one, and every issue is
// settled and every pull request merged.
func doneEvidence(item Item, states States) ([]string, bool) {
	named := len(item.Issues) + len(item.Pulls)
	evidence := settledEvidence(item, states)
	return evidence, named > 0 && len(evidence) == named
}

// doneProposal is what to do with an item that is done already.
func doneProposal(item Item) string {
	if item.Source == SourcePaused {
		return "retire the paused task, with this evidence"
	}
	return "mark the row done, with this evidence"
}

// overtakenEvidence is why a step for the owner that is not done already was
// overtaken: each issue settled, each pull request merged and each date passed
// by now. It is empty when item is not overtaken.
func overtakenEvidence(item Item, states States, now time.Time) []string {
	if !item.IsOwnerStep {
		return nil
	}
	if _, isDone := doneEvidence(item, states); isDone {
		return nil
	}
	evidence := settledEvidence(item, states)
	for _, date := range item.Dates {
		if !date.After(now) {
			evidence = append(evidence, "until "+date.Format(time.DateOnly)+" passed")
		}
	}
	return evidence
}

// staleEvidence is why a paused or held item that is not done already is
// stale: it has no dated condition and no activity for staleAfter. It is empty
// when item is not stale.
func staleEvidence(item Item, states States, now time.Time, staleAfter time.Duration) []string {
	if item.Source != SourcePaused && !item.IsHeld {
		return nil
	}
	if len(item.Dates) > 0 {
		return nil
	}
	if _, isDone := doneEvidence(item, states); isDone {
		return nil
	}
	if item.LastActivity.IsZero() {
		return []string{"no activity on record"}
	}
	if now.Sub(item.LastActivity) >= staleAfter {
		return []string{"last activity " + item.LastActivity.Format(time.DateOnly)}
	}
	return nil
}

// isSameFoldGroup is whether two items are of the same repository, or, when
// either has none, of the same project, compared ignoring case.
func isSameFoldGroup(a, b Item) bool {
	if a.Repository != "" && b.Repository != "" {
		return a.Repository == b.Repository
	}
	return a.Project != "" && strings.EqualFold(a.Project, b.Project)
}

// foldLink is the evidence that two queued or paused items, by their place in
// the list, overlap.
type foldLink struct {
	first, second int
	evidence      []string
}

// foldLinks are the links between items of the same group that name a common
// issue not yet settled or share a path, for each pair in order.
func foldLinks(items []Item, states States) []foldLink {
	var links []foldLink
	for first := range items {
		for second := first + 1; second < len(items); second++ {
			a, b := items[first], items[second]
			if !isSameFoldGroup(a, b) {
				continue
			}
			var evidence []string
			for _, ref := range a.Issues {
				if slices.Contains(b.Issues, ref) && !isIssueSettled(states[ref]) {
					evidence = append(evidence, fmt.Sprintf("%s and %s both name %s", a.ID, b.ID, ref))
				}
			}
			for _, path := range a.Paths {
				if slices.Contains(b.Paths, path) {
					evidence = append(evidence, fmt.Sprintf("%s and %s both touch %s", a.ID, b.ID, path))
				}
			}
			if len(evidence) > 0 {
				links = append(links, foldLink{first, second, evidence})
			}
		}
	}
	return links
}

// foldFindings are the findings that fold queued and paused items, given in
// order, that overlap: one for each group the links connect.
func foldFindings(ordered []Item, states States) []Finding {
	var foldable []Item
	for _, item := range ordered {
		if item.Source == SourceQueued || item.Source == SourcePaused {
			foldable = append(foldable, item)
		}
	}
	links := foldLinks(foldable, states)

	group := make([]int, len(foldable))
	for index := range group {
		group[index] = index
	}
	root := func(index int) int {
		for group[index] != index {
			index = group[index]
		}
		return index
	}
	isLinked := make([]bool, len(foldable))
	for _, link := range links {
		first, second := root(link.first), root(link.second)
		group[max(first, second)] = min(first, second)
		isLinked[link.first], isLinked[link.second] = true, true
	}

	var roots []int
	members := map[int][]Item{}
	evidence := map[int][]string{}
	for index, item := range foldable {
		if !isLinked[index] {
			continue
		}
		groupRoot := root(index)
		if _, isKnown := members[groupRoot]; !isKnown {
			roots = append(roots, groupRoot)
		}
		members[groupRoot] = append(members[groupRoot], item)
	}
	for _, link := range links {
		groupRoot := root(link.first)
		evidence[groupRoot] = append(evidence[groupRoot], link.evidence...)
	}

	slices.Sort(roots)
	findings := make([]Finding, 0, len(roots))
	for _, groupRoot := range roots {
		findings = append(findings, foldFinding(members[groupRoot], evidence[groupRoot]))
	}
	return findings
}

// foldFinding is the finding for one group of overlapping items, given in
// order: the paused member that is oldest, else the oldest member, is the
// target and the others fold into it.
func foldFinding(members []Item, evidence []string) Finding {
	target := 0
	for index, member := range members {
		if member.Source == SourcePaused {
			target = index
			break
		}
	}
	ids := []string{members[target].ID}
	for index, member := range members {
		if index != target {
			ids = append(ids, member.ID)
		}
	}
	into := members[target].ID
	if members[target].Source == SourcePaused {
		into = "the paused goblin " + into
	}
	return Finding{
		Kind:     KindFold,
		Items:    ids,
		Evidence: evidence,
		Proposal: "fold " + strings.Join(ids[1:], ", ") + " into " + into,
	}
}
