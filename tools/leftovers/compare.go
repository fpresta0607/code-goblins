package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// The ways a thing can differ between the snapshot before an install and the
// one after its uninstall.
const (
	// changeLeft is a thing that is there after and was not before: what the
	// uninstall left behind.
	changeLeft = "left"
	// changeGone is a thing that was there before and is not after: what the
	// install or the uninstall took that was not theirs.
	changeGone = "gone"
	// changeChanged is a thing there both times that holds something else.
	changeChanged = "changed"
)

// difference is one thing that differs between two snapshots.
type difference struct {
	Change   string `json:"change"`
	Kind     string `json:"kind"`
	Identity string `json:"identity"`
	Before   string `json:"before,omitempty"`
	After    string `json:"after,omitempty"`
}

func (d difference) String() string {
	switch d.Change {
	case changeChanged:
		return fmt.Sprintf("%s %s %s: %s, was %s", d.Change, d.Kind, d.Identity, shown(d.After), shown(d.Before))
	case changeLeft:
		return strings.TrimSpace(fmt.Sprintf("%s %s %s %s", d.Change, d.Kind, d.Identity, shown(d.After)))
	}
	return strings.TrimSpace(fmt.Sprintf("%s %s %s %s", d.Change, d.Kind, d.Identity, shown(d.Before)))
}

// shown is a detail as a line shows it, cut short where it is long.
func shown(detail string) string {
	const longest = 160
	if len(detail) > longest {
		return detail[:longest] + "..."
	}
	return detail
}

// rule passes over the differences that are no install's doing, such as a
// file Windows writes by itself, and says why.
type rule struct {
	kind    string
	change  string
	pattern *regexp.Regexp
	why     string
}

// matches reports whether the rule passes over d. A rule with no change
// passes over every change of its kind.
func (r rule) matches(d difference) bool {
	return r.kind == d.Kind && (r.change == "" || r.change == d.Change) && r.pattern.MatchString(d.Identity)
}

// passed is a difference a rule or a kept folder passed over, with why.
type passed struct {
	difference
	Why string `json:"why"`
}

// compare lists every difference between before and after that neither a
// kept path nor a rule passes over, and apart from them the ones that were
// passed over, each with why. kept are the folders and files the uninstall
// named as kept, at the person's choice: a file under one, or a PATH entry
// naming one, is passed over.
func compare(before, after Snapshot, kept []string, rules []rule) ([]difference, []passed) {
	var found []difference
	kinds := make(map[string]bool)
	for kind := range before.Things {
		kinds[kind] = true
	}
	for kind := range after.Things {
		kinds[kind] = true
	}
	for kind := range kinds {
		was, is := before.Things[kind], after.Things[kind]
		for identity, detail := range is {
			previous, there := was[identity]
			switch {
			case !there:
				found = append(found, difference{Change: changeLeft, Kind: kind, Identity: identity, After: detail})
			case previous != detail:
				found = append(found, difference{Change: changeChanged, Kind: kind, Identity: identity, Before: previous, After: detail})
			}
		}
		for identity, detail := range was {
			if _, there := is[identity]; !there {
				found = append(found, difference{Change: changeGone, Kind: kind, Identity: identity, Before: detail})
			}
		}
	}
	slices.SortFunc(found, func(a, b difference) int {
		return strings.Compare(a.Kind+"\x00"+a.Identity, b.Kind+"\x00"+b.Identity)
	})
	var remaining []difference
	var over []passed
	for _, d := range found {
		if folder, ok := keptFolder(d, kept); ok {
			over = append(over, passed{difference: d, Why: "the uninstall named " + folder + " as kept"})
			continue
		}
		if index := slices.IndexFunc(rules, func(r rule) bool { return r.matches(d) }); index >= 0 && (d.Change == changeGone || !excepted.MatchString(d.Identity)) {
			over = append(over, passed{difference: d, Why: rules[index].why})
			continue
		}
		remaining = append(remaining, d)
	}
	return remaining, over
}

// keptFolder is what the uninstall named as kept that a difference left
// behind or changed is: a file under a kept folder, a PATH entry naming one, a
// program running from one, as a kept tool's daemon does, or a scheduled task
// the uninstall named. What the person chose to keep may differ from before,
// but nothing is ever taken from under it.
func keptFolder(d difference, kept []string) (string, bool) {
	if d.Change == changeGone {
		return "", false
	}
	path := d.Identity
	switch d.Kind {
	case kindFile, kindProcess:
	case kindPathEntry:
		_, path, _ = strings.Cut(path, " ")
	case kindTask:
		for _, name := range kept {
			if strings.EqualFold(name, d.Identity) {
				return name, true
			}
		}
		return "", false
	default:
		return "", false
	}
	for _, folder := range kept {
		if filepath.IsAbs(folder) && within(path, folder) {
			return folder, true
		}
	}
	return "", false
}

// within reports whether path is folder or under it, compared as Windows
// compares paths, without regard to case.
func within(path, folder string) bool {
	path, folder = filepath.Clean(path), filepath.Clean(folder)
	if strings.EqualFold(path, folder) {
		return true
	}
	return len(path) > len(folder) && strings.EqualFold(path[:len(folder)], folder) && (path[len(folder)] == '\\' || strings.HasSuffix(folder, `\`))
}
