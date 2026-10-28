package janitor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/proc"
	"github.com/fpresta0607/code-goblins/internal/reap"
)

// On 2026-10-27 the janitor returned 77 worktrees the Overlord's own sessions
// had left in his checkouts, seven of them under a folder a session of his was
// still running in, because a clean worktree made over an hour ago was all it
// asked for. A worktree a live process works in, or one that changed within
// the hour, is a session's, and the janitor and the orphan sweep keep it with
// one reason; one a process merely sits above, outside its repository, is not.
func TestSweepKeepsACheckoutWorktreeALiveSessionUses(t *testing.T) {
	cases := []struct {
		name        string
		holder      string
		isCommitted bool
		isKept      bool
		reason      string
	}{
		{name: "a live process works in it", holder: "worktree", isKept: true, reason: "works in"},
		{name: "a live process works in the checkout that holds it", holder: "checkout", isKept: true, reason: "works in"},
		{name: "a commit made ten minutes ago", isCommitted: true, isKept: true, reason: "changed"},
		{name: "a live process works outside its repository", holder: "outside", isKept: false},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			f := newSweepFixture(t)
			worktree := f.checkoutWorktree(t, filepath.Join(".claude", "worktrees", "session")).Path
			gitDir := gitIn(t, worktree, "rev-parse", "--absolute-git-dir")
			makeOld(t, worktree)
			makeOld(t, gitDir)
			setCreated(t, worktree, time.Now().Add(-2*time.Hour))
			if test.isCommitted {
				gitIn(t, worktree, "commit", "-q", "--allow-empty", "-m", "work of a live session")
				tenMinutesAgo := time.Now().Add(-10 * time.Minute)
				for _, name := range []string{"HEAD", "index", filepath.Join("logs", "HEAD")} {
					if err := os.Chtimes(filepath.Join(gitDir, name), tenMinutesAgo, tenMinutesAgo); err != nil {
						t.Fatal(err)
					}
				}
			}
			var rows listed
			switch test.holder {
			case "worktree":
				rows = listed{startSession(t, worktree)}
			case "checkout":
				rows = listed{startSession(t, f.project)}
			case "outside":
				rows = listed{startSession(t, filepath.Dir(f.project))}
			}
			collector := reap.Collector{
				Home:             f.home,
				Session:          "default",
				Commands:         execx.OSRunner{},
				ProjectsRoot:     func() (string, error) { return filepath.Dir(f.project), nil },
				Processes:        rows,
				WorkingDirectory: proc.WorkingDirectory,
			}
			inv, notes, err := collector.Collect(t.Context())
			if err != nil {
				t.Fatal(err)
			}

			// Act
			record := Sweep(t.Context(), f.config(inv))

			// Assert
			if !test.isKept {
				if _, err := os.Stat(worktree); !os.IsNotExist(err) {
					t.Fatalf("a worktree no live process of its repository works in survived: %v; kept %+v notes %v", err, record.Kept, notes)
				}
				return
			}
			if _, err := os.Stat(filepath.Join(worktree, "README.md")); err != nil {
				t.Fatalf("the live session's worktree lost its files: %v; removed %+v kept %+v", err, record.Removed, record.Kept)
			}
			item, ok := itemAt(record.Kept, worktree)
			if !ok || !strings.Contains(item.Detail, test.reason) {
				t.Fatalf("kept = %+v, want the worktree kept with a reason naming what %s; notes %v", record.Kept, test.reason, notes)
			}
			if stray, ok := itemAt(record.Strays, worktree); ok {
				t.Errorf("strays = %+v, want a live session's worktree left out of what the CFO is woken to remove", stray)
			}
			for _, finding := range reap.Classify(inv) {
				if finding.Class != reap.OrphanWorktree || !sameFolder(finding.Path, worktree) {
					continue
				}
				if !finding.Held() || !strings.Contains(finding.Hold(), item.Detail) {
					t.Errorf("orphan sweep hold = %q, want it held for the janitor's reason %q", finding.Hold(), item.Detail)
				}
				return
			}
			t.Errorf("the orphan sweep did not report the worktree at all: %+v", reap.Classify(inv))
		})
	}
}

// TestLiveSessionFixture stands in for a session working in a folder: it
// only waits, in the directory it was started in, until the test ends it.
func TestLiveSessionFixture(t *testing.T) {
	if os.Getenv("CFO_LIVE_SESSION_FIXTURE") != "wait" {
		return
	}
	time.Sleep(10 * time.Minute)
}

// startSession starts a hidden process working in dir, ends it by its pid
// when the test ends, and describes it as the process listing does.
func startSession(t *testing.T, dir string) reap.Process {
	t.Helper()
	program, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	session := exec.Command(program, "-test.run=^TestLiveSessionFixture$")
	session.Dir = dir
	session.Env = append(os.Environ(), "CFO_LIVE_SESSION_FIXTURE=wait")
	session.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
	if err := session.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = session.Process.Kill()
		_ = session.Wait()
	})
	return reap.Process{PID: session.Process.Pid, ParentPID: os.Getpid(), Name: filepath.Base(program), CommandLine: strings.Join(session.Args, " "), Path: program}
}

// setCreated sets when a folder was made, which nothing in os can.
func setCreated(t *testing.T, path string, when time.Time) {
	t.Helper()
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	handle, err := syscall.CreateFile(name, syscall.FILE_WRITE_ATTRIBUTES, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.CloseHandle(handle)
	created := syscall.NsecToFiletime(when.UnixNano())
	if err := syscall.SetFileTime(handle, &created, nil, nil); err != nil {
		t.Fatal(err)
	}
}

// itemAt finds the item naming path, by folder identity, since git spells a
// path through an 8.3 folder name where the test holds it in full.
func itemAt(items []Item, path string) (Item, bool) {
	for _, item := range items {
		if sameFolder(item.Path, path) {
			return item, true
		}
	}
	return Item{}, false
}

func sameFolder(left, right string) bool {
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false
	}
	rightInfo, err := os.Stat(right)
	return err == nil && os.SameFile(leftInfo, rightInfo)
}
