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

// RemoveAsideCopies removes recognized aside copies from the home's programs
// folder (bin, or the root of a legacy home). Installed aliases, staged updates
// and verified rollback copies in state/update are untouched.
// It returns the bytes freed and the paths that could not be removed, including
// running copies that Windows keeps until a later pass can remove them.
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
