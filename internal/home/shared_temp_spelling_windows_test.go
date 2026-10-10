package home

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// shortSpelling is path with its folders in their short names, and whether
// the volume gives it any: a Dev Drive gives none.
func shortSpelling(t *testing.T, path string) (string, bool) {
	t.Helper()
	from, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, windows.MAX_LONG_PATH)
	length, err := windows.GetShortPathName(from, &buffer[0], uint32(len(buffer)))
	if err != nil || length == 0 || int(length) > len(buffer) {
		return "", false
	}
	short := syscall.UTF16ToString(buffer[:length])
	return short, !strings.EqualFold(short, path)
}

// Windows hands out one folder under more than one spelling. The temporary
// folder of a user whose name is long or holds a space comes in its short
// form, C:\Users\RUNNER~1\AppData\Local\Temp, and a folder reached through a
// junction has the junction's path and its own. A live /tmp is compared with
// the folder about to be removed, so each has to be read through to the one
// folder it names, the part of it that no longer exists included: Git Bash
// keeps a /tmp mounted after its folder is gone. On 2026-10-10 the check
// passed everywhere but on a runner, whose temporary folder is short: a /tmp
// inside the folder that had gone was compared in its short spelling with
// the folder's long one, read as elsewhere, and the folder holding a live
// /tmp was removed.
func TestALiveTmpHoldsItsFolderUnderAnotherSpellingOfIt(t *testing.T) {
	// Arrange
	root := t.TempDir()
	scratch := filepath.Join(root, "a folder with a long name", "scratch", "task-1")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	spellings := map[string]string{}
	junction := filepath.Join(root, "junction")
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", junction, filepath.Join(root, "a folder with a long name")).CombinedOutput(); err != nil {
		t.Fatalf("make a junction: %v\n%s", err, out)
	}
	spellings["through a junction"] = filepath.Join(junction, "scratch", "task-1")
	if short, hasShort := shortSpelling(t, scratch); hasShort {
		spellings["in its short names"] = short
	} else {
		t.Logf("this volume gives %s no short names, so only the junction's spelling is tested here", scratch)
	}

	for name, other := range spellings {
		for _, test := range []struct {
			name   string
			folder string
			tmp    string
		}{
			{name: "the folder, with /tmp " + name, folder: scratch, tmp: other},
			{name: "the folder, with /tmp " + name + " in a folder that is gone", folder: scratch, tmp: filepath.Join(other, "gone", "deeper")},
			{name: "the folder " + name + ", with /tmp in a folder that is gone", folder: other, tmp: filepath.Join(scratch, "gone", "deeper")},
			{name: "a folder that is gone " + name + ", with /tmp in it", folder: filepath.Join(other, "gone"), tmp: filepath.Join(scratch, "gone", "deeper")},
		} {
			t.Run(test.name, func(t *testing.T) {
				// Act
				held, isHeld := LiveTempIn(test.folder, []LiveTemp{{Runtime: `C:\Git\usr\bin`, Folder: test.tmp}})

				// Assert
				if !isHeld || held.Runtime != `C:\Git\usr\bin` {
					t.Errorf("LiveTempIn(%s) with /tmp at %s = %+v, %v, want the folder held by that runtime", test.folder, test.tmp, held, isHeld)
				}
			})
		}
	}

	// Another folder whose name the first begins is still another folder.
	if _, isHeld := LiveTempIn(scratch, []LiveTemp{{Runtime: `C:\Git\usr\bin`, Folder: filepath.Join(junction, "scratch", "task-10", "gone")}}); isHeld {
		t.Error("a /tmp in task-10's folder that is gone reads as holding task-1's")
	}
}
