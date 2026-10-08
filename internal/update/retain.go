package update

import (
	"os"
	"path/filepath"
	"regexp"
)

// asideCopy names a copy of one of the home's programs that an update or an
// install moved out of the way: <program>.<digits>.update-old from an update,
// <program>.<text>.old from an install, or <program>.held-<text>, and any of
// them that an older install moved again under a second .<text>.old. A
// staged candidate (.update-new) or restore (.update-restore) belongs to an
// update in progress and is never one.
var asideCopy = regexp.MustCompile(`(?i)^(cfo\.exe|goblins\.exe|goblins-window\.exe)\.([0-9]+\.update-old|[A-Za-z0-9]+\.old|held-[A-Za-z0-9]+)(\.[A-Za-z0-9]+\.old)*$`)

// RemoveAsideCopies removes from programs, the folder holding the home's build
// (bin, or the root of a home a build before bin set up), every copy of one of
// its programs that an update or an install moved aside, so the folder holds
// the current build alone, as the Overlord asked on 2026-10-08 ("no redudant
// exe files"). An update's own verified copy of the build it replaced, which
// its rollback restores from, lives in the state folder and is never touched
// here, and nothing else in the folder is either. It returns the bytes it
// freed and each copy something still runs, which Windows cannot remove and a
// later pass removes once nothing runs it.
func RemoveAsideCopies(programs string) (int64, []string) {
	entries, err := os.ReadDir(programs)
	if err != nil {
		return 0, nil
	}
	var freed int64
	var left []string
	for _, entry := range entries {
		if !asideCopy.MatchString(entry.Name()) || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(programs, entry.Name())
		switch err := os.Remove(path); {
		case err == nil:
			freed += info.Size()
		case !os.IsNotExist(err):
			left = append(left, path)
		}
	}
	return freed, left
}
