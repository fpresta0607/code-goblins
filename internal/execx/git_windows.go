package execx

import (
	"os"
	"path/filepath"
	"strings"
)

// realGit is the git a start of path runs. Git for Windows puts its
// launcher, cmd\git.exe, on PATH, and the launcher only starts
// mingw64\bin\git.exe beside it and waits: two processes for every git
// command, and a console host besides when the first had no console. The
// real git sets what the launcher would for what it starts (MSYSTEM,
// PLINK_PROTOCOL, HOME, and its own folders on PATH, which hooks and
// credential helpers read), so a launcher is run as that git, and any other
// git as it was found.
func realGit(path string) string {
	folder := filepath.Dir(path)
	if !strings.EqualFold(filepath.Base(path), "git.exe") || !strings.EqualFold(filepath.Base(folder), "cmd") {
		return path
	}
	real := filepath.Join(filepath.Dir(folder), "mingw64", "bin", "git.exe")
	if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() {
		return path
	}
	return real
}
