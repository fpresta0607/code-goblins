package installtest

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// scanner stands in for a virus scanner. It opens the file named name as soon
// as the install has written it into one of its own folders in the temp
// folder, without sharing it for deletion, as a scanner reading a file it
// just saw holds it, and lets go once the install's record shows until, a
// line the install writes only after it has tried to remove that folder. With
// no until it holds the file until the install has ended.
type scanner struct {
	name  string
	until string

	// holding closes once the file is held.
	holding chan struct{}
	stop    chan struct{}
	stopped chan struct{}
	once    sync.Once
	// path is the file it held, and sawUntil whether the record showed until
	// while it held it. Both are read only once stopped has closed.
	path     string
	sawUntil bool
}

func newScanner(name, until string) *scanner {
	return &scanner{name: name, until: until, holding: make(chan struct{}), stop: make(chan struct{}), stopped: make(chan struct{})}
}

// start watches temp for the file and record for the line that lets it go.
func (s *scanner) start(temp, record string) {
	go func() {
		defer close(s.stopped)
		var held *os.File
		defer func() {
			if held != nil {
				_ = held.Close()
			}
		}()
		for {
			select {
			case <-s.stop:
				return
			default:
			}
			if held == nil {
				// os.Open shares a file for reading and writing, so the
				// install goes on using it, and not for deletion.
				if found, _ := filepath.Glob(filepath.Join(temp, "code-goblins-*", s.name)); len(found) > 0 {
					if file, err := os.Open(found[0]); err == nil {
						held, s.path = file, found[0]
						close(s.holding)
					}
				}
			} else if s.until != "" {
				if recorded, _ := os.ReadFile(record); strings.Contains(string(recorded), s.until) {
					s.sawUntil = true
					return
				}
			}
			time.Sleep(time.Millisecond)
		}
	}()
}

// letGo ends the scanner, which closes the file it holds, and waits for that.
func (s *scanner) letGo() {
	s.once.Do(func() { close(s.stop) })
	<-s.stopped
}

// sumsOnceHeld is publishedSums, answered only once scan holds its file. The
// install asks for checksums.txt after it has saved the archive, so a scanner
// of the archive holds it before the install can reach its removal.
func sumsOnceHeld(scan *scanner) func(string, [32]byte) string {
	return func(archive string, sum [32]byte) string {
		select {
		case <-scan.holding:
		case <-time.After(30 * time.Second):
		}
		return publishedSums(archive, sum)
	}
}

// couldNotRemove matches the one line that names what the install could not
// remove from the temp folder.
var couldNotRemove = regexp.MustCompile(`(?m)^Note: The install could not remove its temporary files, which another program still holds\. Delete them when you like: (.*)$`)

// A virus scanner still reading a file it just saw refuses the install's
// first try at removing the folder that file is in. On 2026-10-10 that one
// silenced try left a download folder behind on a CI runner, and on a real PC
// it leaves the archive in the user's temp folder with nothing said. The
// install tries again, so the folder is gone by the time it ends, and it says
// nothing of it. Each case holds a file the install has finished writing, in
// one of the two folders a stand-in install reaches, until the install has
// tried to remove the folder and gone on.
func TestOneLineInstallRemovesAFolderAScannerHeldForAMoment(t *testing.T) {
	version := pinnedNoMistakes(t)
	for name, held := range map[string]struct{ file, until string }{
		"the release's download":   {"SHA256SUMS", "download https://claude.ai/install.ps1"},
		"the no-mistakes download": {"no-mistakes-v" + version + "-windows-amd64.zip", "no-mistakes daemon start"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			scan := newScanner(held.file, held.until)
			releases, _ := serveNoMistakesReleases(t, zipped(t, "no-mistakes.exe", standIn(t)), 0, sumsOnceHeld(scan))

			// Act
			run := runInstallWithNoMistakes(t, WindowsPowerShell(), releases, noMistakesSetup{scanner: scan})

			// Assert
			if scan.path == "" || !scan.sawUntil {
				t.Fatalf("the stand-in scanner held %q and let go on %q: %v, so this run proved nothing:\n%s\n%s", scan.path, held.until, scan.sawUntil, run.record, run.output)
			}
			assertNoDownloadLeft(t, run.temp)
			if !strings.Contains(run.output, "Done: Code Goblins is installed") {
				t.Errorf("the install did not run to its end:\n%s", run.output)
			}
			if couldNotRemove.MatchString(run.output) {
				t.Errorf("the install named a folder it did remove:\n%s", run.output)
			}
		})
	}
}

// A folder something never lets go of does not stop the install: it tries for
// a bounded time, runs to its end, and names the folder in one plain line so
// it can be deleted by hand. Everything else it made in the temp folder is
// gone.
func TestOneLineInstallNamesAFolderItCouldNotRemove(t *testing.T) {
	// Arrange
	scan := newScanner("no-mistakes-v"+pinnedNoMistakes(t)+"-windows-amd64.zip", "")
	releases, _ := serveNoMistakesReleases(t, zipped(t, "no-mistakes.exe", standIn(t)), 0, sumsOnceHeld(scan))

	// Act
	run := runInstallWithNoMistakes(t, WindowsPowerShell(), releases, noMistakesSetup{scanner: scan})

	// Assert
	if scan.path == "" {
		t.Fatalf("the stand-in scanner held nothing, so this run proved nothing:\n%s", run.output)
	}
	folder := filepath.Dir(scan.path)
	named := couldNotRemove.FindStringSubmatch(run.output)
	if named == nil || !strings.HasSuffix(strings.TrimSpace(named[1]), filepath.Base(folder)) {
		t.Errorf("the install did not name %s, and only it, as what it could not remove:\n%s", folder, run.output)
	}
	if !strings.Contains(run.output, "Done: Code Goblins is installed") || strings.Contains(run.output, "Failed: ") {
		t.Errorf("the install did not run to its end:\n%s", run.output)
	}
	if installed, err := os.Stat(installedNoMistakes(run.local)); err != nil || installed.Size() == 0 {
		t.Errorf("no-mistakes.exe was not installed (%v):\n%s", err, run.output)
	}
	if left, _ := filepath.Glob(filepath.Join(run.temp, "code-goblins-*")); len(left) != 1 || left[0] != folder {
		t.Errorf("the temp folder holds %v, want only the folder the scanner held, %s", left, folder)
	}
}

// An install that stops says the same of a folder it could not remove: its
// one sentence on what stopped it, then the line that names the folder, then
// its log. Here the release's cfo.exe does not match its checksum, so the
// install stops before it runs anything.
func TestOneLineInstallThatStopsNamesAFolderItCouldNotRemove(t *testing.T) {
	// Arrange
	base := ServeRelease(t, standIn(t), fmt.Sprintf("%x  cfo.exe\n", sha256.Sum256([]byte("another build"))))
	cmd, _, temp := StrippedCommand(t, base, map[string]string{"git": "@exit /b 0\r\n", "gh": "@exit /b 0\r\n"}, WindowsPowerShell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		"Get-Content -Raw -LiteralPath '"+installScript(t)+"' | Invoke-Expression; exit $LASTEXITCODE")
	scan := newScanner("SHA256SUMS", "")
	scan.start(temp, "")

	// Act
	printed, err := cmd.CombinedOutput()
	scan.letGo()

	// Assert
	said := string(printed)
	if scan.path == "" {
		t.Fatalf("the stand-in scanner held nothing, so this run proved nothing:\n%s", Said(printed, temp))
	}
	if err == nil {
		t.Errorf("the install exited 0, want it to stop on the checksum:\n%s", said)
	}
	stopped := strings.Index(said, "Failed: The downloaded cfo.exe does not match the release's checksum")
	named := couldNotRemove.FindStringSubmatchIndex(said)
	log := strings.Index(said, "The full log is ")
	if stopped < 0 || named == nil || log < 0 || stopped > named[0] || named[0] > log {
		t.Fatalf("want what stopped the install, then what it could not remove, then its log:\n%s", said)
	}
	if folder := filepath.Base(filepath.Dir(scan.path)); !strings.HasSuffix(strings.TrimSpace(said[named[2]:named[3]]), folder) {
		t.Errorf("the install did not name %s as what it could not remove:\n%s", folder, said)
	}
}

// Every folder or file install.ps1 makes in the temp folder leaves through
// Remove-Temporary, which tries again and names what stays. The install's log
// alone is kept, since the install's last line names it. A new download that
// is removed with one silenced Remove-Item, as each was before 2026-10-10,
// fails here.
func TestInstallRemovesEverythingItMakesInTheTempFolder(t *testing.T) {
	// Arrange
	source, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatal(err)
	}

	// Act
	made := regexp.MustCompile(`(?m)^\s*\$(\w+) = Join-Path \(\[IO\.Path\]::GetTempPath\(\)\)`).FindAllSubmatch(source, -1)

	// Assert
	// A guard that stops seeing approves everything, so it counts what it
	// read: the install makes six of its own beside its log.
	if len(made) < 7 {
		t.Fatalf("found %d things install.ps1 makes in the temp folder, want its log and at least 6 more; the guard no longer reads the script", len(made))
	}
	makes := map[string]int{}
	for _, match := range made {
		makes[string(match[1])]++
	}
	delete(makes, "log")
	for name, times := range makes {
		removes := len(regexp.MustCompile(`(?m)^\s*Remove-Temporary \$`+name+`\r?$`).FindAll(source, -1))
		if removes < times {
			t.Errorf("install.ps1 makes $%s in the temp folder %d times and hands it to Remove-Temporary %d times", name, times, removes)
		}
	}
}
