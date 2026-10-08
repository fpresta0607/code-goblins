package devdrive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/home"
)

// Move points h's worktrees, scratch and package caches at folder, which must
// be on a Dev Drive of m that Windows does not distrust: it makes the three
// folders, proves it can write there, and records the folder and when in the
// home's config\dev-drive.json. Nothing is copied and no task loses anything:
// a task made before keeps the folders its record names until it ends, and new
// goblins start on the drive. Run again with the same folder it changes
// nothing; it returns what it did, in a line for the person.
func Move(m Machine, h home.Home, folder string, now time.Time) (string, error) {
	if !filepath.IsAbs(folder) {
		return "", fmt.Errorf("%s is not an absolute folder", folder)
	}
	folder = filepath.Clean(folder)
	if strings.EqualFold(h.DevDrive, folder) {
		return "already moved: new goblins' worktrees, scratch and package caches are in " + folder + "; nothing changed", nil
	}
	if h.DevDrive != "" {
		return "", fmt.Errorf("the home's worktrees, scratch and caches are already on %s, and moving them again is not supported", h.DevDrive)
	}
	if rel, err := filepath.Rel(h.Root, folder); err == nil && (rel == "." || filepath.IsLocal(rel)) {
		return "", fmt.Errorf("%s is inside the home %s; the folder goes at a Dev Drive's root, such as D:\\CodeGoblins", folder, h.Root)
	}
	if reason := m.Unavailable(); reason != "" {
		return "", errors.New(reason)
	}
	v, found := m.VolumeOf(folder)
	switch {
	case !found:
		return "", fmt.Errorf("%s is on no fixed drive of this machine", folder)
	case !v.Dev:
		return "", fmt.Errorf("%s is not a Dev Drive, so nothing would be faster there", v.Letter())
	case v.Trust == Untrusted:
		return "", fmt.Errorf("%s is a Dev Drive Windows does not trust, so Defender would scan it in real-time mode; trust it first (fsutil devdrv trust %s, as administrator: the trust item on the board runs it)", v.Letter(), v.Letter())
	}
	for _, sub := range []string{home.WorktreesDir, home.ScratchDir, home.CachesDir} {
		if err := os.MkdirAll(filepath.Join(folder, sub), 0o755); err != nil {
			return "", err
		}
	}
	probe := filepath.Join(folder, home.ScratchDir, ".cfo-write-probe")
	if err := os.WriteFile(probe, []byte("Code Goblins can write here\n"), 0o644); err != nil {
		return "", fmt.Errorf("cannot write in %s: %w", folder, err)
	}
	if err := os.Remove(probe); err != nil {
		return "", err
	}
	config, err := home.ReadDevDriveConfig(h.Root)
	if err != nil {
		return "", err
	}
	config.Root, config.MovedAt = folder, now
	if err := home.WriteDevDriveConfig(h.Root, config); err != nil {
		return "", err
	}
	return fmt.Sprintf("moved: new goblins' worktrees, scratch and package caches go to %s, %s; goblins already started keep their folders where their records say until they finish, and the home's old package caches go once no goblin started before the move is running", folder, trustPhrase(v)), nil
}
