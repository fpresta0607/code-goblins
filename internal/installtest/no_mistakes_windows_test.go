package installtest

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// standInVariable makes a copy of this test binary stand in for a program,
// such as cfo.exe or no-mistakes.exe, whose every command succeeds unless
// standInFailVariable names it.
const standInVariable = "CODE_GOBLINS_TEST_STAND_IN"

// standInRecordVariable names a file a stand-in appends each command it runs
// to, as its program's name and arguments on a line.
const standInRecordVariable = "CODE_GOBLINS_TEST_STAND_IN_RECORD"

// standInVersionVariable is the version a stand-in no-mistakes reports on
// stdout for --version, as no-mistakes version vX.Y.Z does.
const standInVersionVariable = "CODE_GOBLINS_TEST_STAND_IN_VERSION"

// standInStderrVariable is text a stand-in writes to stderr for --version,
// where no-mistakes writes its update notice.
const standInStderrVariable = "CODE_GOBLINS_TEST_STAND_IN_STDERR"

// standInFailVariable names a command, such as daemon stop, a stand-in exits
// 1 for, as no-mistakes refuses to stop its daemon while a gate runs.
const standInFailVariable = "CODE_GOBLINS_TEST_STAND_IN_FAIL"

// standInHold is the command that keeps a stand-in running, as a no-mistakes
// command still running holds its program.
const standInHold = "hold"

func TestMain(m *testing.M) {
	if os.Getenv(standInVariable) != "" {
		command := strings.Join(os.Args[1:], " ")
		if record := os.Getenv(standInRecordVariable); record != "" {
			program := strings.TrimSuffix(strings.ToLower(filepath.Base(os.Args[0])), ".exe")
			file, err := os.OpenFile(record, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
			if err != nil {
				os.Exit(1)
			}
			_, _ = fmt.Fprintf(file, "%s\r\n", strings.Join(append([]string{program}, os.Args[1:]...), " "))
			_ = file.Close()
		}
		switch command {
		case standInHold:
			time.Sleep(time.Hour)
		case "--version":
			_, _ = fmt.Fprint(os.Stderr, os.Getenv(standInStderrVariable))
			if version := os.Getenv(standInVersionVariable); version != "" {
				fmt.Printf("no-mistakes version v%s (0000000) 2026-01-01T00:00:00Z\n", version)
			}
		}
		if fail := os.Getenv(standInFailVariable); fail != "" && command == fail {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func installScript(t *testing.T) string {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	return script
}

// noMistakesDownloads is where no-mistakes publishes its releases. The
// install downloads from here, never through GitHub's API, which answers an
// anonymous caller only 60 times an hour per address and so failed the
// install on shared CI runners.
const noMistakesDownloads = "https://github.com/kunchenguid/no-mistakes/releases/download/"

// pinnedNoMistakes is the no-mistakes release install.ps1 pins, such as
// 1.75.1.
func pinnedNoMistakes(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`\$noMistakesVersion = "(\d+\.\d+\.\d+)"`).FindSubmatch(source)
	if match == nil {
		t.Fatal("install.ps1 pins no no-mistakes release")
	}
	return string(match[1])
}

// zipped is a zip archive holding content as name, the way a no-mistakes
// release holds no-mistakes.exe.
func zipped(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	file, err := writer.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

// serveNoMistakesReleases stands in for github.com: for each tag it serves
// archive as the Windows archive, and the checksums.txt that sums writes
// from the archive's name and SHA256. It answers the first failures requests
// for an archive with 503, and counts every archive request.
func serveNoMistakesReleases(t *testing.T, archive []byte, failures int32, sums func(archive string, sum [32]byte) string) (string, *atomic.Int32) {
	t.Helper()
	requests := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tag, name, found := strings.Cut(strings.TrimPrefix(r.URL.Path, "/kunchenguid/no-mistakes/releases/download/"), "/")
		if !found || !strings.HasPrefix(r.URL.Path, "/kunchenguid/no-mistakes/releases/download/") {
			http.NotFound(w, r)
			return
		}
		want := "no-mistakes-" + tag + "-windows-amd64.zip"
		switch name {
		case want:
			if requests.Add(1) <= failures {
				http.Error(w, "service unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write(archive)
		case "checksums.txt":
			_, _ = w.Write([]byte(sums(want, sha256.Sum256(archive))))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL, requests
}

// publishedSums writes checksums.txt as a no-mistakes release publishes it:
// one line per platform's archive.
func publishedSums(archive string, sum [32]byte) string {
	return fmt.Sprintf("%x  no-mistakes-v0.0.0-darwin-arm64.tar.gz\n%x  %s\n", sha256.Sum256([]byte("another platform")), sum, archive)
}

// readStandIn is this test binary's content, read once: most tests here
// serve a copy as the stand-in cfo.exe, many also as no-mistakes.exe, and it
// is several megabytes.
var readStandIn = sync.OnceValues(func() ([]byte, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return os.ReadFile(executable)
})

// standIn is this test binary's content, for a stand-in program.
func standIn(t *testing.T) []byte {
	t.Helper()
	program, err := readStandIn()
	if err != nil {
		t.Fatal(err)
	}
	return program
}

// noMistakesSetup is what one install run starts from.
type noMistakesSetup struct {
	// stubs are .cmd stand-ins first on PATH, where %RECORD% names the
	// record file.
	stubs map[string]string
	// seed runs first, in the empty profile.
	seed func(local string)
	// staleWindow leaves the folder the install puts no-mistakes in off PATH,
	// as in a window opened before no-mistakes was installed there.
	staleWindow bool
	// env is added to the install's environment.
	env []string
}

// noMistakesInstall is one run of the one-line install with no-mistakes'
// releases served by a stand-in, as far as the install goes with a stand-in
// cfo.exe: every command it runs succeeds and records itself.
type noMistakesInstall struct {
	output string
	// record holds, in order, each download from the internet, each wait
	// between attempts, and each command a stand-in program ran.
	record string
	local  string
	temp   string
	// bin is the folder that holds the .cmd stand-ins, first on PATH.
	bin string
	// userEnv is the file that stands in for the user-scope environment.
	userEnv string
}

// runInstallWithNoMistakes runs the one-line install in shell against a
// stand-in cfo.exe release, with github.com's no-mistakes releases served from
// releases and every other download failing, as offline. A wait between
// attempts is recorded instead of taken. The folder the install puts
// no-mistakes in comes last on PATH unless the setup says the window is stale.
func runInstallWithNoMistakes(t *testing.T, shell, releases string, setup noMistakesSetup) noMistakesInstall {
	t.Helper()
	binary := standIn(t)
	base := ServeRelease(t, binary, fmt.Sprintf("%x  cfo.exe\n", sha256.Sum256(binary)))
	record := filepath.Join(t.TempDir(), "record.txt")
	all := map[string]string{"git": "@exit /b 0\r\n", "gh": "@exit /b 0\r\n"}
	for name, script := range setup.stubs {
		all[name] = strings.ReplaceAll(script, "%RECORD%", record)
	}
	internet := "function Invoke-WebRequest {\n" +
		"  [CmdletBinding()] param([string]$Uri, [string]$OutFile, [switch]$UseBasicParsing)\n" +
		"  if ($Uri.StartsWith('" + base + "/')) { Microsoft.PowerShell.Utility\\Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing; return }\n" +
		"  Add-Content -LiteralPath '" + record + "' -Value \"download $Uri\"\n" +
		"  if ($Uri.StartsWith('" + noMistakesDownloads + "')) { Microsoft.PowerShell.Utility\\Invoke-WebRequest -Uri ($Uri -replace '^https://github\\.com', '" + releases + "') -OutFile $OutFile -UseBasicParsing; return }\n" +
		"  throw \"offline: $Uri\"\n" +
		"}\n" +
		"function Invoke-RestMethod {\n" +
		"  [CmdletBinding()] param([string]$Uri)\n" +
		"  Add-Content -LiteralPath '" + record + "' -Value \"api $Uri\"\n" +
		"  throw \"offline: $Uri\"\n" +
		"}\n" +
		"function Start-Sleep { param([int]$Seconds) Add-Content -LiteralPath '" + record + "' -Value \"wait $Seconds\" }\n"
	cmd, local, temp := StrippedCommand(t, base, all, shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		internet+"Get-Content -Raw -LiteralPath '"+installScript(t)+"' | Invoke-Expression")
	var bin string
	for i, variable := range cmd.Env {
		if path, found := strings.CutPrefix(variable, "PATH="); found {
			bin, _, _ = strings.Cut(path, ";")
			if !setup.staleWindow {
				cmd.Env[i] = variable + ";" + filepath.Dir(installedNoMistakes(local))
			}
		}
	}
	cmd.Env = append(append(cmd.Env, standInVariable+"=1", standInRecordVariable+"="+record), setup.env...)
	if setup.seed != nil {
		setup.seed(local)
	}

	output, _ := cmd.CombinedOutput()

	recorded, err := os.ReadFile(record)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return noMistakesInstall{output: string(output), record: string(recorded), local: local, temp: temp, bin: bin, userEnv: filepath.Join(local, userEnvFileName)}
}

// installedNoMistakes is where the install puts no-mistakes, where
// no-mistakes' own installer and its update put it too.
func installedNoMistakes(local string) string {
	return filepath.Join(local, "no-mistakes", "no-mistakes.exe")
}

// assertNoAPICall checks that the install never asked GitHub's API, or the
// upstream installer that asks it, for anything.
func assertNoAPICall(t *testing.T, run noMistakesInstall) {
	t.Helper()
	for _, line := range strings.Split(run.record, "\r\n") {
		if strings.HasPrefix(line, "api ") || strings.Contains(line, "api.github.com") || strings.Contains(line, "no-mistakes/main/docs/install.ps1") {
			t.Errorf("the install called %q, want only the pinned release's downloads:\n%s", line, run.record)
		}
	}
}

// notCompleted matches no-mistakes among the installs the install's summary
// says did not complete.
var notCompleted = regexp.MustCompile(`(?m)^\s+- no-mistakes\s*$`)

// assertNoDownloadLeft checks that the install's own download folders are gone.
func assertNoDownloadLeft(t *testing.T, temp string) {
	t.Helper()
	if left, _ := filepath.Glob(filepath.Join(temp, "code-goblins-*")); len(left) != 0 {
		t.Errorf("the install left %v behind", left)
	}
}

// assertUpdated checks that the install downloaded the pinned release before
// it stopped the older no-mistakes' daemon, and started the new daemon after,
// with the pinned program in place.
func assertUpdated(t *testing.T, run noMistakesInstall, pinned []byte) {
	t.Helper()
	version := pinnedNoMistakes(t)
	download := strings.Index(run.record, "download "+noMistakesDownloads+"v"+version+"/no-mistakes-v"+version+"-windows-amd64.zip")
	stop := strings.Index(run.record, "no-mistakes daemon stop\r\n")
	start := strings.Index(run.record, "no-mistakes daemon start\r\n")
	if download < 0 || stop < download || start < stop {
		t.Fatalf("want the release downloaded, then the old daemon stopped, then the new one started:\n%s\n%s", run.record, run.output)
	}
	if !strings.Contains(run.output, "v1.0.0 is older than the pinned v"+version) {
		t.Errorf("the install did not name the update from 1.0.0 to %s:\n%s", version, run.output)
	}
	if installed, err := os.ReadFile(installedNoMistakes(run.local)); err != nil || !bytes.Equal(installed, pinned) {
		t.Errorf("no-mistakes.exe is not the pinned release (%v):\n%s", err, run.output)
	}
}

// The install fetches the no-mistakes release it pins from the release's own
// download address, and tries a download that fails again, a bounded number
// of times with a wait between attempts: two failures in a row, as a rate
// limit or a 503 gives, still install it. It verifies the archive against
// the release's checksums.txt, puts no-mistakes.exe where no-mistakes'
// installer does, on the user's PATH, and starts its daemon. The retry does
// not depend on the shell, so Windows PowerShell alone runs it; the install
// workflow installs the real release in both PowerShells on a clean runner.
func TestOneLineInstallRetriesTheNoMistakesDownloadThatFailsTwice(t *testing.T) {
	program := standIn(t)
	releases, requests := serveNoMistakesReleases(t, zipped(t, "no-mistakes.exe", program), 2, publishedSums)

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), releases, noMistakesSetup{})

	version := pinnedNoMistakes(t)
	archive := "no-mistakes-v" + version + "-windows-amd64.zip"
	if n := requests.Load(); n != 3 {
		t.Errorf("the archive was requested %d times, want 3: two failures, then the download:\n%s", n, run.output)
	}
	if !strings.Contains(run.record, "download "+noMistakesDownloads+"v"+version+"/"+archive+"\r\n") || !strings.Contains(run.record, "download "+noMistakesDownloads+"v"+version+"/checksums.txt\r\n") {
		t.Errorf("the pinned release v%s was not downloaded from %s:\n%s", version, noMistakesDownloads, run.record)
	}
	if waits := strings.Count(run.record, "wait "); waits != 2 {
		t.Errorf("the install waited %d times, want once before each of the 2 retries:\n%s", waits, run.record)
	}
	if !strings.Contains(run.output, "Verified "+archive+" against the release's checksums.txt") {
		t.Errorf("the archive was not verified against checksums.txt:\n%s", run.output)
	}
	if installed, err := os.ReadFile(installedNoMistakes(run.local)); err != nil || !bytes.Equal(installed, program) {
		t.Fatalf("no-mistakes.exe was not installed at %s (%v):\n%s", installedNoMistakes(run.local), err, run.output)
	}
	if !strings.Contains(run.record, "no-mistakes daemon start\r\n") {
		t.Errorf("the installed no-mistakes did not start its daemon:\n%s\n%s", run.record, run.output)
	}
	userEnv, err := os.ReadFile(run.userEnv)
	if err != nil || !strings.Contains(string(userEnv), strings.ReplaceAll(filepath.Join(run.local, "no-mistakes"), `\`, `\\`)) {
		t.Errorf("the no-mistakes folder is not on the user's PATH in %s (%v): %s", run.userEnv, err, userEnv)
	}
	assertNoAPICall(t, run)
	assertNoDownloadLeft(t, run.temp)
}

// A no-mistakes download that keeps failing stops after its last attempt
// with a message that says what failed, and the rest of the install goes on
// and names it among the installs that did not complete.
func TestOneLineInstallGivesUpOnANoMistakesDownloadThatKeepsFailing(t *testing.T) {
	releases, requests := serveNoMistakesReleases(t, zipped(t, "no-mistakes.exe", []byte("never served")), 1000, publishedSums)

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), releases, noMistakesSetup{})

	if n := requests.Load(); n != 3 {
		t.Errorf("the archive was requested %d times, want 3 attempts and no more:\n%s", n, run.output)
	}
	if !strings.Contains(run.output, "could not be downloaded after 3 attempts") || !strings.Contains(run.output, "503") {
		t.Errorf("the install did not say the download failed 3 times, and why:\n%s", run.output)
	}
	if !strings.Contains(run.output, "Verifying the toolchain") || !strings.Contains(run.output, "These installs did not complete") || !notCompleted.MatchString(run.output) {
		t.Errorf("the install did not go on and name no-mistakes as not installed:\n%s", run.output)
	}
	if _, err := os.Stat(installedNoMistakes(run.local)); !os.IsNotExist(err) {
		t.Errorf("no-mistakes.exe exists (%v), want none", err)
	}
	assertNoAPICall(t, run)
	assertNoDownloadLeft(t, run.temp)
}

// An archive that does not match the release's checksums.txt is refused, and
// nothing is installed or started.
func TestOneLineInstallRefusesANoMistakesArchiveThatDoesNotMatchItsChecksum(t *testing.T) {
	for name, sums := range map[string]func(string, [32]byte) string{
		"another archive's checksum": func(archive string, _ [32]byte) string {
			return fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte("the archive the release published")), archive)
		},
		"no checksum for the archive": func(_ string, sum [32]byte) string {
			return fmt.Sprintf("%x  no-mistakes-v0.0.0-linux-amd64.tar.gz\n", sum)
		},
	} {
		t.Run(name, func(t *testing.T) {
			releases, _ := serveNoMistakesReleases(t, zipped(t, "no-mistakes.exe", []byte("a build the release did not publish")), 0, sums)

			run := runInstallWithNoMistakes(t, WindowsPowerShell(), releases, noMistakesSetup{})

			if !strings.Contains(run.output, "does not match the release's checksums.txt") {
				t.Errorf("the install did not refuse the archive:\n%s", run.output)
			}
			if _, err := os.Stat(installedNoMistakes(run.local)); !os.IsNotExist(err) {
				t.Errorf("no-mistakes.exe exists (%v), want none", err)
			}
			if strings.Contains(run.record, "daemon start") {
				t.Errorf("a daemon was started:\n%s", run.record)
			}
			assertNoDownloadLeft(t, run.temp)
		})
	}
}

// existingNoMistakes is a no-mistakes on PATH that reports version and
// records every command it runs.
func existingNoMistakes(version string) string {
	return "@>>\"%RECORD%\" echo existing %*\r\n" +
		"@if \"%1\"==\"--version\" echo no-mistakes version v" + version + " (0000000) 2026-01-01T00:00:00Z\r\n" +
		"@exit /b 0\r\n"
}

// olderVersion, in the install's environment, makes the older no-mistakes
// seedNoMistakes puts in place report 1.0.0.
const olderVersion = standInVersionVariable + "=1.0.0"

// seedNoMistakes copies this test binary to where the install puts
// no-mistakes, standing in for an older no-mistakes the install put there,
// and returns its content.
func seedNoMistakes(t *testing.T, local string) []byte {
	t.Helper()
	program := standIn(t)
	if err := os.MkdirAll(filepath.Dir(installedNoMistakes(local)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedNoMistakes(local), program, 0o755); err != nil {
		t.Fatal(err)
	}
	return program
}

// holdRunning keeps the stand-in at path running, as a no-mistakes command
// still running holds its no-mistakes.exe, and returns a channel that closes
// when it exits.
func holdRunning(t *testing.T, path string) chan struct{} {
	t.Helper()
	running := exec.Command(path, standInHold)
	running.Env = append(os.Environ(), standInVariable+"=1")
	running.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	if err := running.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan struct{})
	go func() {
		_ = running.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		_ = running.Process.Kill()
		<-exited
	})
	return exited
}

// pinnedStandIn is a release's program that runs as this test binary does but
// ends in bytes of its own, so it is told apart from an older copy.
func pinnedStandIn(t *testing.T) []byte {
	t.Helper()
	return append(append([]byte{}, standIn(t)...), "the pinned release"...)
}

// Rerunning the install moves an older no-mistakes, where the install put it,
// to the pinned release, even in a window opened before that copy was
// installed, whose PATH does not find it: it reads the older version from
// stdout alone, where an update notice on stderr may name another, downloads
// and verifies the release first, then stops the daemon, which no-mistakes
// itself refuses while a gate runs, replaces the program even while a
// no-mistakes command still runs it, and starts the daemon again. The update
// does not depend on the shell, so Windows PowerShell alone runs it; the
// install workflow installs the real release in both PowerShells on a clean
// runner.
func TestOneLineInstallUpdatesAnOlderNoMistakesToThePinnedRelease(t *testing.T) {
	pinned := pinnedStandIn(t)
	releases, _ := serveNoMistakesReleases(t, zipped(t, "no-mistakes.exe", pinned), 0, publishedSums)
	var older []byte
	var exited chan struct{}

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), releases, noMistakesSetup{
		seed: func(local string) {
			older = seedNoMistakes(t, local)
			exited = holdRunning(t, installedNoMistakes(local))
		},
		staleWindow: true,
		env:         []string{olderVersion, standInStderrVariable + "=A new version of no-mistakes is available: v1.0.0 -> v999.0.0\nno-mistakes version v999.0.0 (0000000) 2026-01-01T00:00:00Z\n"},
	})

	assertUpdated(t, run, pinned)
	if old, err := os.ReadFile(installedNoMistakes(run.local) + ".old"); err != nil || !bytes.Equal(old, older) {
		t.Errorf("the running copy was not moved aside to no-mistakes.exe.old, where no-mistakes removes it (%v)", err)
	}
	select {
	case <-exited:
		t.Errorf("the running no-mistakes command was stopped, want it left running")
	default:
	}
	assertNoAPICall(t, run)
	assertNoDownloadLeft(t, run.temp)
}

// While a gate runs, no-mistakes refuses to stop its daemon, and the install
// then leaves the older no-mistakes exactly as it was and says to rerun it
// once no gate runs.
func TestOneLineInstallLeavesNoMistakesAloneWhileAGateRuns(t *testing.T) {
	releases, _ := serveNoMistakesReleases(t, zipped(t, "no-mistakes.exe", []byte("the pinned release")), 0, publishedSums)
	var older []byte

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), releases, noMistakesSetup{
		seed: func(local string) { older = seedNoMistakes(t, local) },
		env:  []string{olderVersion, standInFailVariable + "=daemon stop"},
	})

	if !strings.Contains(run.record, "no-mistakes daemon stop\r\n") || strings.Contains(run.record, "daemon start") {
		t.Errorf("want the daemon asked to stop and nothing started:\n%s", run.record)
	}
	if !strings.Contains(run.output, "rerun the install once no gate runs") || !notCompleted.MatchString(run.output) {
		t.Errorf("the install did not say to rerun it once no gate runs:\n%s", run.output)
	}
	if installed, err := os.ReadFile(installedNoMistakes(run.local)); err != nil || !bytes.Equal(installed, older) {
		t.Errorf("no-mistakes.exe was changed (%v), want it left as it was", err)
	}
	if _, err := os.Stat(installedNoMistakes(run.local) + ".old"); !os.IsNotExist(err) {
		t.Errorf("no-mistakes.exe.old exists (%v), want nothing moved", err)
	}
	assertNoDownloadLeft(t, run.temp)
}

// An older no-mistakes elsewhere on PATH, which the install did not put
// there, is never replaced: the install says where it is and how to update
// it, downloads nothing, leaves its daemon alone, and names no-mistakes among
// the installs that did not complete, on every rerun alike.
func TestOneLineInstallWarnsOfAnOlderNoMistakesItDidNotInstall(t *testing.T) {
	releases, requests := serveNoMistakesReleases(t, zipped(t, "no-mistakes.exe", []byte("the pinned release")), 0, publishedSums)

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), releases, noMistakesSetup{stubs: map[string]string{"no-mistakes": existingNoMistakes("1.0.0")}})

	stub := filepath.Join(run.bin, "no-mistakes.cmd")
	warning := regexp.MustCompile(`WARN\s+no-mistakes\s+` + regexp.QuoteMeta(stub) + ` is v1\.0\.0, older than the pinned v` + regexp.QuoteMeta(pinnedNoMistakes(t)) + `,.*run: no-mistakes update`)
	if !warning.MatchString(run.output) || !notCompleted.MatchString(run.output) {
		t.Errorf("the install did not warn of %s and how to update it, and name no-mistakes as not installed:\n%s", stub, run.output)
	}
	if n := requests.Load(); n != 0 || strings.Contains(run.record, noMistakesDownloads) {
		t.Errorf("the install downloaded no-mistakes (%d archive requests):\n%s", n, run.record)
	}
	if strings.Contains(run.record, "daemon") {
		t.Errorf("the install touched the daemon:\n%s", run.record)
	}
	if _, err := os.Stat(filepath.Dir(installedNoMistakes(run.local))); !os.IsNotExist(err) {
		t.Errorf("the install made %s (%v), want nothing installed there", filepath.Dir(installedNoMistakes(run.local)), err)
	}
}

// A no-mistakes at the pinned release is left as it is: nothing is
// downloaded and its daemon keeps running.
func TestOneLineInstallKeepsANoMistakesAtThePin(t *testing.T) {
	version := pinnedNoMistakes(t)
	releases, requests := serveNoMistakesReleases(t, zipped(t, "no-mistakes.exe", []byte("the pinned release")), 0, publishedSums)

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), releases, noMistakesSetup{stubs: map[string]string{"no-mistakes": existingNoMistakes(version)}})

	if !regexp.MustCompile(`ok\s+no-mistakes\s+present`).MatchString(run.output) {
		t.Errorf("no-mistakes %s was not reported present:\n%s", version, run.output)
	}
	if n := requests.Load(); n != 0 || strings.Contains(run.record, noMistakesDownloads) {
		t.Errorf("the install downloaded no-mistakes (%d archive requests):\n%s", n, run.record)
	}
	if strings.Contains(run.record, "daemon") {
		t.Errorf("the install touched the daemon:\n%s", run.record)
	}
}

// The install judges no-mistakes by the copy it manages even in a window
// opened before that copy was installed, whose PATH does not find it: a newer
// copy is kept, with nothing downloaded, stopped or started, and its folder
// put on the PATH. An older one is updated, as the update test shows in such
// a window.
func TestOneLineInstallKeepsANewerNoMistakesThatThisWindowCannotSee(t *testing.T) {
	releases, requests := serveNoMistakesReleases(t, zipped(t, "no-mistakes.exe", pinnedStandIn(t)), 0, publishedSums)
	var newer []byte

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), releases, noMistakesSetup{
		seed:        func(local string) { newer = seedNoMistakes(t, local) },
		staleWindow: true,
		env:         []string{standInVersionVariable + "=999.0.0"},
	})

	if !regexp.MustCompile(`ok\s+no-mistakes\s+present`).MatchString(run.output) {
		t.Errorf("the newer no-mistakes was not reported present:\n%s", run.output)
	}
	if n := requests.Load(); n != 0 || strings.Contains(run.record, noMistakesDownloads) {
		t.Errorf("the install downloaded no-mistakes (%d archive requests):\n%s", n, run.record)
	}
	if strings.Contains(run.record, "daemon") {
		t.Errorf("the install touched the daemon:\n%s", run.record)
	}
	if installed, err := os.ReadFile(installedNoMistakes(run.local)); err != nil || !bytes.Equal(installed, newer) {
		t.Errorf("no-mistakes.exe was changed (%v), want the newer copy kept", err)
	}
	userEnv, err := os.ReadFile(run.userEnv)
	if err != nil || !strings.Contains(string(userEnv), strings.ReplaceAll(filepath.Join(run.local, "no-mistakes"), `\`, `\\`)) {
		t.Errorf("the no-mistakes folder is not on the user's PATH in %s (%v): %s", run.userEnv, err, userEnv)
	}
}

// An archive that does not hold no-mistakes.exe where a release's archive
// holds it is refused before anything changes: the older no-mistakes' daemon
// keeps running and its program stays where it is.
func TestOneLineInstallRefusesANoMistakesArchiveWithoutItsProgram(t *testing.T) {
	releases, _ := serveNoMistakesReleases(t, zipped(t, "no-mistakes/no-mistakes.exe", []byte("the program in a folder")), 0, publishedSums)
	var older []byte

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), releases, noMistakesSetup{
		seed: func(local string) { older = seedNoMistakes(t, local) },
		env:  []string{olderVersion},
	})

	if !strings.Contains(run.output, "holds no no-mistakes.exe") || !notCompleted.MatchString(run.output) {
		t.Errorf("the install did not refuse the archive for holding no no-mistakes.exe:\n%s", run.output)
	}
	if strings.Contains(run.record, "daemon") {
		t.Errorf("the install touched the daemon, want it left running:\n%s", run.record)
	}
	if installed, err := os.ReadFile(installedNoMistakes(run.local)); err != nil || !bytes.Equal(installed, older) {
		t.Errorf("no-mistakes.exe was changed (%v), want it left as it was", err)
	}
	if _, err := os.Stat(installedNoMistakes(run.local) + ".old"); !os.IsNotExist(err) {
		t.Errorf("no-mistakes.exe.old exists (%v), want nothing moved", err)
	}
	assertNoDownloadLeft(t, run.temp)
}
