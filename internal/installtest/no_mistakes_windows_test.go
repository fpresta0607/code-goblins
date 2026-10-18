package installtest

import (
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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/standin"
)

// standInHold is the command that keeps a stand-in running, as a no-mistakes
// command still running holds its program.
const standInHold = "hold"

// reportsVersion has a stand-in no-mistakes report version on stdout for
// --version, as no-mistakes version vX.Y.Z does, and write notice to stderr,
// where no-mistakes writes its update notice.
func reportsVersion(version, notice string) standin.Rule {
	return standin.Rule{Args: "--version", Stdout: "no-mistakes version v" + version + " (0000000) 2026-01-01T00:00:00Z\n", Stderr: notice}
}

// gates has a stand-in no-mistakes' axi run take a gate's intent from a file
// and a launch selection, as the pinned build's does and an upstream
// release's does not.
var gates = standin.Rule{Args: "help axi run", Stdout: "      --intent-file string        read intent from this file\n      --launch-assertion string   required JSON proof of the expected trusted SHA\n"}

func installScript(t *testing.T) string {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("..", "..", "install.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	return script
}

// pinnedNoMistakes is the version of the no-mistakes build install.ps1 pins,
// such as 1.88.0-24-gbcbb444.
func pinnedNoMistakes(t *testing.T) string {
	t.Helper()
	source, err := os.ReadFile(installScript(t))
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`\$noMistakesVersion = "([^"]+)"`).FindSubmatch(source)
	if match == nil {
		t.Fatal("install.ps1 pins no no-mistakes build")
	}
	return string(match[1])
}

// noMistakesRelease is a stand-in for the release install.ps1 was published
// with: the stand-in program as cfo.exe, a no-mistakes.exe and SHA256SUMS.
type noMistakesRelease struct {
	base string
	// requests counts the requests for no-mistakes.exe.
	requests *atomic.Int32
}

// serveNoMistakesRelease serves a release whose no-mistakes.exe is program
// and whose SHA256SUMS sums writes from the sums of cfo.exe and program. It
// answers the first failures requests for no-mistakes.exe with 503, and,
// where ready is not nil, any request for it only once ready closes or after
// 30 seconds.
func serveNoMistakesRelease(t *testing.T, program []byte, failures int32, sums func(cfo, noMistakes [32]byte) string, ready <-chan struct{}) noMistakesRelease {
	t.Helper()
	cfo := standin.Bytes(t)
	requests := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/cfo.exe":
			_, _ = w.Write(cfo)
		case "/SHA256SUMS":
			_, _ = w.Write([]byte(sums(sha256.Sum256(cfo), sha256.Sum256(program))))
		case "/no-mistakes.exe":
			if ready != nil {
				select {
				case <-ready:
				case <-time.After(30 * time.Second):
				}
			}
			if requests.Add(1) <= failures {
				http.Error(w, "service unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write(program)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return noMistakesRelease{base: server.URL, requests: requests}
}

// publishedSums writes SHA256SUMS as a release publishes it.
func publishedSums(cfo, noMistakes [32]byte) string {
	return fmt.Sprintf("%x  CodeGoblinsSetup.exe\n%x  cfo.exe\n%x  no-mistakes.exe\n", sha256.Sum256([]byte("the setup")), cfo, noMistakes)
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
	// rules are what the stand-in programs do for the commands the install
	// runs; a command with no rule succeeds.
	rules []standin.Rule
	// scanner holds a file of the install's own open while it runs.
	scanner *scanner
}

// noMistakesInstall is one run of the one-line install against a stand-in
// release, as far as the install goes with a stand-in cfo.exe: every command
// it runs succeeds and records itself.
type noMistakesInstall struct {
	output string
	// record holds, in order, each download, each wait between attempts, and
	// each command a stand-in program ran.
	record string
	local  string
	temp   string
	// bin is the folder that holds the .cmd stand-ins, first on PATH.
	bin string
	// userEnv is the file that stands in for the user-scope environment.
	userEnv string
}

// runInstallWithNoMistakes runs the one-line install in shell against
// release, with every other download failing, as offline. A wait between
// attempts is recorded instead of taken. The folder the install puts
// no-mistakes in comes last on PATH unless the setup says the window is stale.
func runInstallWithNoMistakes(t *testing.T, shell string, release noMistakesRelease, setup noMistakesSetup) noMistakesInstall {
	t.Helper()
	record := filepath.Join(t.TempDir(), "record.txt")
	all := map[string]string{"git": "@exit /b 0\r\n", "gh": "@exit /b 0\r\n"}
	for name, script := range setup.stubs {
		all[name] = strings.ReplaceAll(script, "%RECORD%", record)
	}
	internet := "function Invoke-WebRequest {\n" +
		"  [CmdletBinding()] param([string]$Uri, [string]$OutFile, [switch]$UseBasicParsing)\n" +
		"  Add-Content -LiteralPath '" + record + "' -Value \"download $Uri\"\n" +
		"  if ($Uri.StartsWith('" + release.base + "/')) { Microsoft.PowerShell.Utility\\Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing; return }\n" +
		"  throw \"offline: $Uri\"\n" +
		"}\n" +
		"function Invoke-RestMethod {\n" +
		"  [CmdletBinding()] param([string]$Uri)\n" +
		"  Add-Content -LiteralPath '" + record + "' -Value \"api $Uri\"\n" +
		"  throw \"offline: $Uri\"\n" +
		"}\n" +
		"function Start-Sleep { param([int]$Seconds) Add-Content -LiteralPath '" + record + "' -Value \"wait $Seconds\" }\n"
	cmd, local, temp := StrippedCommand(t, release.base, all, shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		internet+"Get-Content -Raw -LiteralPath '"+installScript(t)+"' | Invoke-Expression; exit $LASTEXITCODE")
	var bin string
	for i, variable := range cmd.Env {
		if path, found := strings.CutPrefix(variable, "PATH="); found {
			bin, _, _ = strings.Cut(path, ";")
			if !setup.staleWindow {
				cmd.Env[i] = variable + ";" + filepath.Dir(installedNoMistakes(local))
			}
		}
	}
	cmd.Env = append(cmd.Env, standin.Env(record, setup.rules...)...)
	if setup.seed != nil {
		setup.seed(local)
	}

	if setup.scanner != nil {
		setup.scanner.start(temp, record)
	}
	output, _ := cmd.CombinedOutput()
	if setup.scanner != nil {
		setup.scanner.letGo()
	}

	recorded, err := os.ReadFile(record)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return noMistakesInstall{output: Said(output, temp), record: string(recorded), local: local, temp: temp, bin: bin, userEnv: filepath.Join(local, UserEnvFile)}
}

// installedNoMistakes is where the install puts no-mistakes, where
// no-mistakes' own installer and its update put it too.
func installedNoMistakes(local string) string {
	return filepath.Join(local, "no-mistakes", "no-mistakes.exe")
}

// assertOnlyTheRelease checks that the install fetched no-mistakes from the
// release it was published with alone: never through GitHub's API, from an
// upstream release or with no-mistakes' own installer.
func assertOnlyTheRelease(t *testing.T, run noMistakesInstall, release noMistakesRelease) {
	t.Helper()
	for _, line := range strings.Split(run.record, "\r\n") {
		uri, isDownload := strings.CutPrefix(line, "download ")
		if strings.HasPrefix(line, "api ") || (isDownload && strings.Contains(uri, "no-mistakes") && !strings.HasPrefix(uri, release.base+"/")) {
			t.Errorf("the install fetched %q, want no-mistakes from its own release alone:\n%s", line, run.record)
		}
	}
}

// notCompleted matches no-mistakes among the tools the install's closing note
// names as not installed.
var notCompleted = regexp.MustCompile(`(?m)^Note: These could not be installed, so only what needs them is left out: (.*, )?no-mistakes[,.]`)

// assertNoDownloadLeft checks that the install's own download folders are gone.
func assertNoDownloadLeft(t *testing.T, temp string) {
	t.Helper()
	if left, _ := filepath.Glob(filepath.Join(temp, "code-goblins-*")); len(left) != 0 {
		t.Errorf("the install left %v behind", left)
	}
}

// assertReplaced checks that the install downloaded the release's
// no-mistakes.exe before it stopped the daemon of the copy it replaced, and
// started the new daemon after, with the release's program in place, and
// named why it replaced the copy.
func assertReplaced(t *testing.T, run noMistakesInstall, release noMistakesRelease, pinned []byte, why string) {
	t.Helper()
	download := strings.Index(run.record, "download "+release.base+"/no-mistakes.exe\r\n")
	stop := strings.Index(run.record, "no-mistakes daemon stop\r\n")
	start := strings.Index(run.record, "no-mistakes daemon start\r\n")
	if download < 0 || stop < download || start < stop {
		t.Fatalf("want the release's no-mistakes.exe downloaded, then the old daemon stopped, then the new one started:\n%s\n%s", run.record, run.output)
	}
	if !regexp.MustCompile(`update\s+no-mistakes\s+.*` + regexp.QuoteMeta(why)).MatchString(run.output) {
		t.Errorf("the install did not say it replaced no-mistakes because %q:\n%s", why, run.output)
	}
	if installed, err := os.ReadFile(installedNoMistakes(run.local)); err != nil || !bytes.Equal(installed, pinned) {
		t.Errorf("no-mistakes.exe is not the release's (%v):\n%s", err, run.output)
	}
}

// The install fetches no-mistakes.exe from the release it was published
// with, which built and signed it, and tries a download that fails again, a
// bounded number of times with a wait between attempts: two failures in a
// row, as a rate limit or a 503 gives, still install it. It verifies the
// program against the release's SHA256SUMS, puts it where no-mistakes'
// installer does, on the user's PATH, starts its daemon and checks that it
// gates as the fleet needs. The retry does not depend on the shell, so
// Windows PowerShell alone runs it; the install workflow installs a real
// build in both PowerShells on a clean runner.
func TestOneLineInstallRetriesTheNoMistakesDownloadThatFailsTwice(t *testing.T) {
	program := standin.Bytes(t)
	release := serveNoMistakesRelease(t, program, 2, publishedSums, nil)

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), release, noMistakesSetup{rules: []standin.Rule{reportsVersion(pinnedNoMistakes(t), ""), gates}})

	if n := release.requests.Load(); n != 3 {
		t.Errorf("no-mistakes.exe was requested %d times, want 3: two failures, then the download:\n%s", n, run.output)
	}
	if waits := strings.Count(run.record, "wait "); waits != 2 {
		t.Errorf("the install waited %d times, want once before each of the 2 retries:\n%s", waits, run.record)
	}
	if !strings.Contains(run.output, "Verified no-mistakes.exe against the release's SHA256SUMS") {
		t.Errorf("no-mistakes.exe was not verified against SHA256SUMS:\n%s", run.output)
	}
	if installed, err := os.ReadFile(installedNoMistakes(run.local)); err != nil || !bytes.Equal(installed, program) {
		t.Fatalf("no-mistakes.exe was not installed at %s (%v):\n%s", installedNoMistakes(run.local), err, run.output)
	}
	if !strings.Contains(run.record, "no-mistakes daemon start\r\n") || !strings.Contains(run.record, "no-mistakes help axi run\r\n") {
		t.Errorf("the installed no-mistakes did not start its daemon and was not checked:\n%s\n%s", run.record, run.output)
	}
	if notCompleted.MatchString(run.output) {
		t.Errorf("the install named no-mistakes as not installed:\n%s", run.output)
	}
	userEnv, err := os.ReadFile(run.userEnv)
	if err != nil || !strings.Contains(string(userEnv), strings.ReplaceAll(filepath.Join(run.local, "no-mistakes"), `\`, `\\`)) {
		t.Errorf("the no-mistakes folder is not on the user's PATH in %s (%v): %s", run.userEnv, err, userEnv)
	}
	assertOnlyTheRelease(t, run, release)
	assertNoDownloadLeft(t, run.temp)
}

// A no-mistakes download that keeps failing stops after its last attempt
// with a message that says what failed, and the rest of the install goes on
// and names it among the installs that did not complete.
func TestOneLineInstallGivesUpOnANoMistakesDownloadThatKeepsFailing(t *testing.T) {
	release := serveNoMistakesRelease(t, []byte("never served"), 1000, publishedSums, nil)

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), release, noMistakesSetup{})

	if n := release.requests.Load(); n != 3 {
		t.Errorf("no-mistakes.exe was requested %d times, want 3 attempts and no more:\n%s", n, run.output)
	}
	if !strings.Contains(run.output, "could not be downloaded after 3 attempts") || !strings.Contains(run.output, "503") {
		t.Errorf("the install did not say the download failed 3 times, and why:\n%s", run.output)
	}
	if !strings.Contains(run.output, "Verifying the toolchain") || !notCompleted.MatchString(run.output) {
		t.Errorf("the install did not go on and name no-mistakes as not installed:\n%s", run.output)
	}
	if _, err := os.Stat(installedNoMistakes(run.local)); !os.IsNotExist(err) {
		t.Errorf("no-mistakes.exe exists (%v), want none", err)
	}
	assertOnlyTheRelease(t, run, release)
	assertNoDownloadLeft(t, run.temp)
}

// A no-mistakes.exe that does not match the release's SHA256SUMS is refused,
// and nothing is installed or started.
func TestOneLineInstallRefusesANoMistakesThatDoesNotMatchItsChecksum(t *testing.T) {
	for name, sums := range map[string]func(cfo, noMistakes [32]byte) string{
		"another program's checksum": func(cfo, _ [32]byte) string {
			return fmt.Sprintf("%x  cfo.exe\n%x  no-mistakes.exe\n", cfo, sha256.Sum256([]byte("the program the release published")))
		},
		"no checksum for no-mistakes.exe": func(cfo, noMistakes [32]byte) string {
			return fmt.Sprintf("%x  cfo.exe\n%x  no-mistakes-NOTICES\n", cfo, noMistakes)
		},
	} {
		t.Run(name, func(t *testing.T) {
			release := serveNoMistakesRelease(t, []byte("a build the release did not publish"), 0, sums, nil)

			run := runInstallWithNoMistakes(t, WindowsPowerShell(), release, noMistakesSetup{})

			if !strings.Contains(run.output, "The downloaded no-mistakes.exe does not match the release's checksum, so it was not installed.") || !notCompleted.MatchString(run.output) {
				t.Errorf("the install did not refuse no-mistakes.exe:\n%s", run.output)
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

// existingNoMistakes is a no-mistakes on PATH that reports version, takes
// what the fleet's gate gives it where isGating, and records every command it
// runs.
func existingNoMistakes(version string, isGating bool) string {
	script := "@>>\"%RECORD%\" echo existing %*\r\n" +
		"@if \"%1\"==\"--version\" echo no-mistakes version v" + version + " (0000000) 2026-01-01T00:00:00Z\r\n"
	if isGating {
		script += "@if \"%1\"==\"help\" echo --intent-file string --launch-assertion string\r\n"
	}
	return script + "@exit /b 0\r\n"
}

// seedNoMistakes puts the stand-in program where the install puts
// no-mistakes, standing in for a no-mistakes already there, and returns its
// content.
func seedNoMistakes(t *testing.T, local string) []byte {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(installedNoMistakes(local)), 0o755); err != nil {
		t.Fatal(err)
	}
	standin.Put(t, installedNoMistakes(local))
	return standin.Bytes(t)
}

// holdRunning keeps the stand-in at path running, as a no-mistakes command
// still running holds its no-mistakes.exe, and returns a channel that closes
// when it exits.
func holdRunning(t *testing.T, path string) chan struct{} {
	t.Helper()
	running := exec.Command(path, standInHold)
	running.Env = append(os.Environ(), standin.Env("", standin.Rule{Args: standInHold, Hold: true})...)
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

// pinnedStandIn is a release's program that runs as the stand-in does but
// ends in bytes of its own, so it is told apart from the copy it replaces.
func pinnedStandIn(t *testing.T) []byte {
	t.Helper()
	return append(standin.Bytes(t), "the pinned build"...)
}

// Rerunning the install moves an older no-mistakes, where the install put it,
// to the release's build, even in a window opened before that copy was
// installed, whose PATH does not find it: it reads the older version from
// stdout alone, where an update notice on stderr may name another, downloads
// and verifies the release's program first, then stops the daemon, which
// no-mistakes itself refuses while a gate runs, replaces the program even
// while a no-mistakes command still runs it, and starts the daemon again. The
// update does not depend on the shell, so Windows PowerShell alone runs it.
func TestOneLineInstallUpdatesAnOlderNoMistakesToThePinnedBuild(t *testing.T) {
	pinned := pinnedStandIn(t)
	release := serveNoMistakesRelease(t, pinned, 0, publishedSums, nil)
	var older []byte
	var exited chan struct{}

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), release, noMistakesSetup{
		seed: func(local string) {
			older = seedNoMistakes(t, local)
			exited = holdRunning(t, installedNoMistakes(local))
		},
		staleWindow: true,
		rules:       []standin.Rule{reportsVersion("1.0.0", "A new version of no-mistakes is available: v1.0.0 -> v999.0.0\nno-mistakes version v999.0.0 (0000000) 2026-01-01T00:00:00Z\n"), gates},
	})

	assertReplaced(t, run, release, pinned, "is v1.0.0, older than the pinned v"+pinnedNoMistakes(t))
	if old, err := os.ReadFile(installedNoMistakes(run.local) + ".old"); err != nil || !bytes.Equal(old, older) {
		t.Errorf("the running copy was not moved aside to no-mistakes.exe.old, where no-mistakes removes it (%v)", err)
	}
	select {
	case <-exited:
		t.Errorf("the running no-mistakes command was stopped, want it left running")
	default:
	}
	assertOnlyTheRelease(t, run, release)
	assertNoDownloadLeft(t, run.temp)
}

// A newer no-mistakes that cannot gate as the fleet needs, as an upstream
// release without the fork's launch selection cannot, is replaced with the
// release's build all the same. The stand-in that takes its place answers as
// the one it replaced, so the check after the install names it too.
func TestOneLineInstallReplacesANoMistakesThatCannotGate(t *testing.T) {
	pinned := pinnedStandIn(t)
	release := serveNoMistakesRelease(t, pinned, 0, publishedSums, nil)

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), release, noMistakesSetup{
		seed:  func(local string) { seedNoMistakes(t, local) },
		rules: []standin.Rule{reportsVersion("999.0.0", "")},
	})

	assertReplaced(t, run, release, pinned, "has no --intent-file and --launch-assertion, which the fleet's gate needs")
	assertOnlyTheRelease(t, run, release)
}

// While a gate runs, no-mistakes refuses to stop its daemon, and the install
// then leaves the older no-mistakes exactly as it was and says to rerun it
// once no gate runs.
func TestOneLineInstallLeavesNoMistakesAloneWhileAGateRuns(t *testing.T) {
	release := serveNoMistakesRelease(t, []byte("the pinned build"), 0, publishedSums, nil)
	var older []byte

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), release, noMistakesSetup{
		seed:  func(local string) { older = seedNoMistakes(t, local) },
		rules: []standin.Rule{reportsVersion("1.0.0", ""), gates, {Args: "daemon stop", Exit: 1}},
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

// A no-mistakes elsewhere on PATH, which the install did not put there, is
// never replaced when it cannot gate as the pinned build does: the install
// says where it is and what to do, downloads nothing, leaves its daemon
// alone, and names no-mistakes among the installs that did not complete, on
// every rerun alike.
func TestOneLineInstallWarnsOfANoMistakesItDidNotInstallThatCannotGate(t *testing.T) {
	for name, test := range map[string]struct {
		stub string
		why  string
	}{
		"an older one":                 {stub: existingNoMistakes("1.0.0", true), why: "is v1.0.0, older than the pinned v"},
		"one with no launch selection": {stub: existingNoMistakes("999.0.0", false), why: "has no --intent-file and --launch-assertion, which the fleet's gate needs"},
	} {
		t.Run(name, func(t *testing.T) {
			release := serveNoMistakesRelease(t, []byte("the pinned build"), 0, publishedSums, nil)

			run := runInstallWithNoMistakes(t, WindowsPowerShell(), release, noMistakesSetup{stubs: map[string]string{"no-mistakes": test.stub}})

			stub := filepath.Join(run.bin, "no-mistakes.cmd")
			warning := regexp.MustCompile(`WARN\s+no-mistakes\s+.*` + regexp.QuoteMeta(stub) + `.*` + regexp.QuoteMeta(test.why) + `.*comes first on PATH; remove it, or put .* before it`)
			if !warning.MatchString(run.output) || !notCompleted.MatchString(run.output) {
				t.Errorf("the install did not warn of %s and what to do, and name no-mistakes as not installed:\n%s", stub, run.output)
			}
			if n := release.requests.Load(); n != 0 {
				t.Errorf("the install downloaded no-mistakes (%d requests):\n%s", n, run.record)
			}
			if strings.Contains(run.record, "daemon") {
				t.Errorf("the install touched the daemon:\n%s", run.record)
			}
			if _, err := os.Stat(filepath.Dir(installedNoMistakes(run.local))); !os.IsNotExist(err) {
				t.Errorf("the install made %s (%v), want nothing installed there", filepath.Dir(installedNoMistakes(run.local)), err)
			}
		})
	}
}

// A no-mistakes at the pinned build that gates as the fleet needs is left as
// it is: nothing is downloaded and its daemon keeps running.
func TestOneLineInstallKeepsANoMistakesAtThePin(t *testing.T) {
	version := pinnedNoMistakes(t)
	release := serveNoMistakesRelease(t, []byte("the pinned build"), 0, publishedSums, nil)

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), release, noMistakesSetup{stubs: map[string]string{"no-mistakes": existingNoMistakes(version, true)}})

	if !regexp.MustCompile(`ok\s+no-mistakes\s+present`).MatchString(run.output) {
		t.Errorf("no-mistakes %s was not reported present:\n%s", version, run.output)
	}
	if n := release.requests.Load(); n != 0 {
		t.Errorf("the install downloaded no-mistakes (%d requests):\n%s", n, run.record)
	}
	if strings.Contains(run.record, "daemon") {
		t.Errorf("the install touched the daemon:\n%s", run.record)
	}
}

// The install judges no-mistakes by the copy it manages even in a window
// opened before that copy was installed, whose PATH does not find it: a newer
// copy that gates as the fleet needs, as a later build of the same fork
// does, is kept, with nothing downloaded, stopped or started, and its folder
// put on the PATH. An older one is updated, as the update test shows in such
// a window.
func TestOneLineInstallKeepsANewerNoMistakesThatThisWindowCannotSee(t *testing.T) {
	release := serveNoMistakesRelease(t, pinnedStandIn(t), 0, publishedSums, nil)
	var newer []byte

	run := runInstallWithNoMistakes(t, WindowsPowerShell(), release, noMistakesSetup{
		seed:        func(local string) { newer = seedNoMistakes(t, local) },
		staleWindow: true,
		rules:       []standin.Rule{reportsVersion("999.0.0-3-g0000000", ""), gates},
	})

	if !regexp.MustCompile(`ok\s+no-mistakes\s+present`).MatchString(run.output) {
		t.Errorf("the newer no-mistakes was not reported present:\n%s", run.output)
	}
	if n := release.requests.Load(); n != 0 {
		t.Errorf("the install downloaded no-mistakes (%d requests):\n%s", n, run.record)
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
