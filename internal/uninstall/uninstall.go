// Package uninstall removes Code Goblins from a machine and leaves it as the
// install found it.
//
// It first makes a plan from what the machine holds, which changes nothing:
// what it stops, what it keeps for the person, what it removes, and what it
// leaves because other programs may use it, each named with where it is. A
// dry run prints the plan and stops there. Run carries it out group by group,
// in an order that keeps work before anything that holds it goes: it stops
// what Code Goblins runs, keeps the person's unlanded work and fleet data in
// one folder, then removes the rest. Each group is read back by finding it
// again once it is done, so what is still there is named, with how to remove
// it, and a second run, which plans again from what is left, finishes it.
package uninstall

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

// Action is what the uninstall does with one thing.
type Action string

const (
	// Stop ends a program Code Goblins started.
	Stop Action = "stop"
	// Keep copies the person's own work or data into the kept folder.
	Keep Action = "keep"
	// Remove removes a thing Code Goblins made.
	Remove Action = "remove"
	// Leave leaves a thing the install fetched, which other programs may
	// use, and names it.
	Leave Action = "leave"
)

// Group is the line of the summary a thing counts toward. Groups run in this
// order: the checkouts go before the Dev Drive, which may hold their
// worktrees, and the home goes last.
type Group int

const (
	GroupRunning Group = iota
	GroupWork
	GroupFleetData
	GroupCheckouts
	GroupDevDrive
	GroupCredentials
	GroupHarnesses
	GroupShortcuts
	GroupFolders
	GroupTools
	GroupHome
)

// Item is one thing the uninstall does.
type Item struct {
	Action Action `json:"action"`
	Group  Group  `json:"group"`
	// What says what the thing is, in words, such as "the board".
	What string `json:"what"`
	// Where is its pid, path, key or name.
	Where string `json:"where"`
	// Bytes is what it holds on disk, where it holds anything.
	Bytes int64 `json:"bytes,omitempty"`
	// Admin is a thing only an administrator can remove. Every one of them
	// is removed by one script, so Windows asks once, and the script reads
	// each back itself, since only an administrator can see them.
	Admin bool `json:"admin,omitempty"`
	// Script is the part of the administrator's script that removes it and
	// adds its Where to $still when it is still there.
	Script string `json:"-"`
	// Fix is how a person finishes when the uninstall could not, the end of
	// one sentence, such as "restart the PC and run goblins uninstall again".
	Fix string `json:"-"`
	do  func(context.Context) error
}

// Choices are what the person chose beyond the plan's defaults.
type Choices struct {
	// RemoveWork removes the unlanded work and fleet data instead of
	// keeping them.
	RemoveWork bool
	// RemoveTools removes the tools the install fetched instead of leaving
	// them.
	RemoveTools bool
}

// finder finds one group's things on the machine as it is now. Run calls it
// again once the group is done, which is how every removal is read back.
type finder struct {
	group Group
	find  func(context.Context) ([]Item, error)
}

// Plan is what an uninstall of one home will do.
type Plan struct {
	// Home is the home it removes.
	Home string
	// Kept is the folder it keeps the person's work and data in.
	Kept    string
	Items   []Item
	finders []finder
	// admin runs the administrator's script, asking Windows once, and
	// returns the Where of each thing the script found still there.
	admin func(context.Context, string) ([]string, error)
	// aside is the folder the run moved its own program to.
	aside *string
}

// Aside is the folder the run moved its own program to while it removed the
// home, which is removed once the program ends, or "".
func (p Plan) Aside() string {
	if p.aside == nil {
		return ""
	}
	return *p.aside
}

// Left is a thing the uninstall could not remove or stop, with why and how
// to finish.
type Left struct {
	Item
	Why string
}

// Sentence says what is left and how to finish, in one sentence.
func (l Left) Sentence() string {
	fix := l.Fix
	if fix == "" {
		fix = "run goblins uninstall again to finish"
	}
	return fmt.Sprintf("Left %s, %s, so %s.", l.What, l.Why, fix)
}

// Outcome is what a run did.
type Outcome struct {
	Done []Item
	Left []Left
	// Stopped says the run stopped before it removed anything that holds
	// the person's work, because a keep did not complete.
	Stopped bool
}

// Progress is told each group as it starts, and once it is done with what
// it did and what it left.
type Progress func(group Group, done []Item, left []Left, finished bool)

// Run carries the plan out, group by group, and reads each back. A keep that
// does not complete stops the run there, before anything that holds the work
// is removed.
func (p Plan) Run(ctx context.Context, progress Progress) Outcome {
	var outcome Outcome
	for _, f := range p.finders {
		items := p.itemsOf(f.group)
		if len(items) == 0 {
			continue
		}
		// What a second run needs stays while anything before it is left.
		if f.group == GroupHome && len(outcome.Left) > 0 {
			for _, item := range items {
				outcome.Left = append(outcome.Left, Left{Item: item, Why: "it stays so the uninstall can run again"})
			}
			progress(f.group, nil, outcome.Left[len(outcome.Left)-len(items):], true)
			continue
		}
		progress(f.group, nil, nil, false)
		done, left := p.runGroup(ctx, f, items)
		outcome.Done = append(outcome.Done, done...)
		outcome.Left = append(outcome.Left, left...)
		progress(f.group, done, left, true)
		if (f.group == GroupWork || f.group == GroupFleetData) && len(left) > 0 {
			outcome.Stopped = true
			return outcome
		}
	}
	return outcome
}

// itemsOf is the plan's things in group that the run acts on.
func (p Plan) itemsOf(group Group) []Item {
	var items []Item
	for _, item := range p.Items {
		if item.Group == group && item.Action != Leave {
			items = append(items, item)
		}
	}
	return items
}

// runGroup does each thing of one group, the administrator's in one script,
// then reads the group back: what is still there to stop or remove is left.
// A keep reads back its own copy, and the administrator's script reads back
// what it removes.
func (p Plan) runGroup(ctx context.Context, f finder, items []Item) ([]Item, []Left) {
	failed := map[string]string{}
	var script []string
	for _, item := range items {
		if item.Admin {
			script = append(script, item.Script)
			continue
		}
		if item.do == nil {
			continue
		}
		if err := item.do(ctx); err != nil {
			failed[key(item)] = reason(err)
		}
	}
	still := map[string]bool{}
	if len(script) > 0 {
		remaining, err := p.admin(ctx, strings.Join(script, "\r\n"))
		for _, item := range items {
			switch {
			case !item.Admin:
			case err != nil:
				failed[key(item)] = reason(err)
			case slices.Contains(remaining, item.Where):
				still[key(item)] = true
			}
		}
	}
	if again, err := f.find(ctx); err != nil {
		for _, item := range items {
			failed[key(item)] = "it could not be read back: " + reason(err)
		}
	} else {
		for _, item := range again {
			if (item.Action == Stop || item.Action == Remove) && !item.Admin {
				still[key(item)] = true
			}
		}
	}
	var done []Item
	var left []Left
	for _, item := range items {
		why, isFailed := failed[key(item)]
		switch {
		case isFailed:
			left = append(left, Left{Item: item, Why: why})
		case item.Action != Keep && still[key(item)]:
			left = append(left, Left{Item: item, Why: "it is still there"})
		default:
			done = append(done, item)
		}
	}
	return done, left
}

func key(item Item) string { return string(item.Action) + "\x00" + item.What + "\x00" + item.Where }

// reason is an error as the clause of a sentence, which the uninstall's
// words never break with a semicolon.
func reason(err error) string {
	text := strings.TrimSuffix(strings.TrimSpace(err.Error()), ".")
	return strings.ReplaceAll(text, ";", ",")
}

// Acts reports whether the plan stops, keeps or removes anything.
func (p Plan) Acts() bool {
	return slices.ContainsFunc(p.Items, func(item Item) bool { return item.Action != Leave })
}

// Remaining are the things a plan made after a run still finds to stop or
// remove: what the run left.
func (p Plan) Remaining() []Item {
	var remaining []Item
	for _, item := range p.Items {
		if item.Action == Stop || item.Action == Remove {
			remaining = append(remaining, item)
		}
	}
	return remaining
}

// Named reports whether the run already named item as left.
func (o Outcome) Named(item Item) bool {
	return slices.ContainsFunc(o.Left, func(l Left) bool { return l.What == item.What && l.Where == item.Where })
}

// Count is how many things of group the plan acts on with action.
func (p Plan) Count(group Group, action Action) int {
	count := 0
	for _, item := range p.Items {
		if item.Group == group && item.Action == action {
			count++
		}
	}
	return count
}
