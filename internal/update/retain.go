package update

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// KeptBuilds is how many builds before the current one a home's bin keeps.
const KeptBuilds = 2

// asideCopy names a copy of one of bin's programs that an update or an
// install moved out of the way: <program>.<digits>.update-old from an update,
// <program>.<text>.old from an install, or <program>.held-<text>. A staged
// candidate (.update-new) or restore (.update-restore) belongs to an update
// in progress and is never one.
var asideCopy = regexp.MustCompile(`(?i)^(cfo\.exe|goblins\.exe|goblins-window\.exe)\.([0-9]+\.update-old|[A-Za-z0-9]+\.old|held-[A-Za-z0-9]+)$`)

// program names the program a file in bin is a copy of. cfo.exe and
// goblins.exe are one program under two names, so their copies are counted
// as one program's builds.
func program(name string) string {
	if strings.EqualFold(name, "goblins.exe") {
		return "cfo.exe"
	}
	return strings.ToLower(name)
}

// KeepRecent removes from bin every copy an update or an install moved aside
// past the keep newest builds of its program, and every copy of a build bin
// already holds, so bin keeps the current build and the keep before it
// however many updates ran. Builds are told apart by their content, so the
// two names of one build count once. Nothing else in bin is touched, and an
// update's own verified copies, which its rollback restores from, live in the
// state folder and are never touched here. A copy something still runs cannot
// be removed; it is returned, and goes on a later pass once nothing runs it.
func KeepRecent(bin string, keep int) []string {
	entries, err := os.ReadDir(bin)
	if err != nil {
		return nil
	}
	type copyOf struct {
		path     string
		modified time.Time
		hash     string
	}
	copies := map[string][]copyOf{}
	for _, entry := range entries {
		match := asideCopy.FindStringSubmatch(entry.Name())
		if match == nil || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(bin, entry.Name())
		hash, err := HashFile(path)
		if err != nil {
			continue
		}
		copies[program(match[1])] = append(copies[program(match[1])], copyOf{path: path, modified: info.ModTime(), hash: hash})
	}
	var left []string
	for name, list := range copies {
		seen := map[string]bool{}
		for _, alias := range []string{name, "goblins.exe"} {
			if program(alias) != name {
				continue
			}
			if hash, err := HashFile(filepath.Join(bin, alias)); err == nil {
				seen[hash] = true
			}
		}
		sort.Slice(list, func(i, j int) bool { return list[i].modified.After(list[j].modified) })
		builds := 0
		for _, c := range list {
			if !seen[c.hash] && builds < keep {
				seen[c.hash] = true
				builds++
				continue
			}
			if err := os.Remove(c.path); err != nil && !os.IsNotExist(err) {
				left = append(left, c.path)
			}
		}
	}
	sort.Strings(left)
	return left
}
