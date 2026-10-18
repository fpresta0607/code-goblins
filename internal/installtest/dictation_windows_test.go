package installtest

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/standin"
)

// dictationNote is the one sentence the install says when dictation could
// not be set up.
const dictationNote = "Note: Dictation could not be set up now, so it finishes setting itself up the first time you dictate; the log says why."

// homeCfo puts the stand-in where cfo install puts cfo.exe in the per-user
// home, which the stand-in's own install does not.
func homeCfo(t *testing.T) func(local string) {
	t.Helper()
	return func(local string) {
		bin := filepath.Join(local, "CodeGoblins", "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		standin.Put(t, filepath.Join(bin, "cfo.exe"))
	}
}

// The install sets dictation up with the build the home now holds, after
// that build has set the home up, and says so in its one plain line.
func TestOneLineInstallSetsDictationUpWithTheHomesBuild(t *testing.T) {
	// Arrange
	release := serveNoMistakesRelease(t, standin.Bytes(t), 0, publishedSums, nil)

	// Act
	run := runInstallWithNoMistakes(t, WindowsPowerShell(), release, noMistakesSetup{seed: homeCfo(t)})

	// Assert
	installed := strings.Index(run.record, "cfo install\r\n")
	dictation := strings.Index(run.record, "cfo dictation setup\r\n")
	if installed < 0 || dictation < installed {
		t.Fatalf("want cfo install, then cfo dictation setup:\n%s\n%s", run.record, run.output)
	}
	if !strings.Contains(run.output, "\n      Setting up dictation\r\n") {
		t.Errorf("the install did not say it sets dictation up:\n%s", run.output)
	}
	if strings.Contains(run.output, dictationNote) || strings.Contains(run.output, "Failed:") {
		t.Errorf("a dictation that was set up was reported as not:\n%s", run.output)
	}
	if !strings.Contains(run.output, "Done: Code Goblins is installed") {
		t.Errorf("the install did not end well:\n%s", run.output)
	}
}

// A dictation that cannot be set up never fails the install: it says so in
// one sentence and the install ends well, and the first dictation sets it up
// as it always could.
func TestOneLineInstallEndsWellWhenDictationCannotBeSetUp(t *testing.T) {
	// Arrange
	release := serveNoMistakesRelease(t, standin.Bytes(t), 0, publishedSums, nil)

	// Act
	run := runInstallWithNoMistakes(t, WindowsPowerShell(), release, noMistakesSetup{seed: homeCfo(t), rules: []standin.Rule{{Args: "dictation setup", Exit: 1}}})

	// Assert
	if !strings.Contains(run.record, "cfo dictation setup\r\n") {
		t.Fatalf("the install did not try to set dictation up:\n%s\n%s", run.record, run.output)
	}
	if !strings.Contains(run.output, dictationNote) {
		t.Errorf("the install did not say dictation finishes at the first dictation:\n%s", run.output)
	}
	if strings.Contains(run.output, "Failed:") || !strings.Contains(run.output, "Done: Code Goblins is installed") {
		t.Errorf("a dictation that could not be set up failed the install:\n%s", run.output)
	}
}

// interruptAtNoMistakes is PowerShell that stands in for the internet as
// runInstallWithNoMistakes does, where %RECORD% names the record and %BASE%
// the release, and presses Ctrl+C at the console as the install asks for
// no-mistakes.exe, while dictation's setup runs beside the tools. It presses
// it only while nothing but the install and that setup is on the console,
// and ends the session itself should the Ctrl+C not. The session first
// hears Ctrl+C again, as one a person starts at a console does, since a
// test may hand it down a way of ignoring it.
const interruptAtNoMistakes = `Add-Type -Namespace InstallTest -Name Console -MemberDefinition @'
[DllImport("kernel32.dll")] public static extern bool SetConsoleCtrlHandler(IntPtr handler, bool add);
[DllImport("kernel32.dll")] public static extern bool GenerateConsoleCtrlEvent(uint ctrlEvent, uint processGroupId);
[DllImport("kernel32.dll")] public static extern uint GetConsoleProcessList(uint[] processes, uint count);
'@
[void][InstallTest.Console]::SetConsoleCtrlHandler([IntPtr]::Zero, $false)
function Invoke-WebRequest {
  [CmdletBinding()] param([string]$Uri, [string]$OutFile, [switch]$UseBasicParsing)
  Add-Content -LiteralPath '%RECORD%' -Value "download $Uri"
  if ($Uri.EndsWith('/no-mistakes.exe')) {
    $console = New-Object 'uint32[]' 64
    $count = [InstallTest.Console]::GetConsoleProcessList($console, 64)
    $others = @($console[0..($count - 1)] | Where-Object { $_ -ne $PID -and $_ -ne $dictation.Id })
    if (-not $dictation -or $dictation.HasExited -or $others) {
      Add-Content -LiteralPath '%RECORD%' -Value "no Ctrl+C: dictation's setup is $(if ($dictation) { $dictation.Id }), and the console also holds $($others -join ', ')"
      [Environment]::Exit(8)
    }
    Add-Content -LiteralPath '%RECORD%' -Value "Ctrl+C while dictation's setup runs as $($dictation.Id)"
    [void][InstallTest.Console]::GenerateConsoleCtrlEvent(0, 0)
    foreach ($wait in 1..200) { [Threading.Thread]::Sleep(50) }
    [Environment]::Exit(9)
  }
  if ($Uri.StartsWith('%BASE%/')) { Microsoft.PowerShell.Utility\Invoke-WebRequest -Uri $Uri -OutFile $OutFile -UseBasicParsing; return }
  throw "offline: $Uri"
}
`

// An install that stops ends dictation's setup with it, as it did when it
// ran the setup in turn: the setup runs on the install's own console, so the
// Ctrl+C that stops the install reaches it too. One on a console of its own
// went on fetching the 483 MB engine and model out of sight after the
// install had stopped, and the setup of a rerun beside it could find its
// folders taken.
func TestOneLineInstallThatIsInterruptedEndsDictationsSetup(t *testing.T) {
	// Arrange
	release := serveNoMistakesRelease(t, standin.Bytes(t), 0, publishedSums, nil)
	record := filepath.Join(t.TempDir(), "record.txt")
	internet := strings.NewReplacer("%RECORD%", record, "%BASE%", release.base).Replace(interruptAtNoMistakes)
	cmd, local, _ := StrippedCommand(t, release.base, map[string]string{"git": "@exit /b 0\r\n", "gh": "@exit /b 0\r\n"}, WindowsPowerShell(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command",
		internet+"Get-Content -Raw -LiteralPath '"+installScript(t)+"' | Invoke-Expression; exit $LASTEXITCODE")
	// A Ctrl+C reaches every program on the console it is pressed at, so the
	// session gets a console of its own, with no window, that this test and
	// what started it do not share.
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
	// A setup the Ctrl+C missed holds the session's output for the hour it
	// runs, so the run ends a moment after the session does.
	cmd.WaitDelay = 5 * time.Second
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	cmd.Env = append(cmd.Env, standin.Env(record, standin.Rule{Args: "dictation setup", Hold: true})...)
	homeCfo(t)(local)

	// Act: the install runs in a job of its own, which holds what it starts
	// and ends all of it with the test, or after three minutes should the
	// install never stop.
	tree, err := execx.StartTree(cmd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tree.End() })
	bound := time.AfterFunc(3*time.Minute, func() { _, _ = tree.End() })
	_ = cmd.Wait()
	if !bound.Stop() {
		t.Fatalf("the install did not stop within three minutes:\n%s", output.String())
	}

	// Assert
	recorded, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^Ctrl\+C while dictation's setup runs as \d+\r?$`).Match(recorded) {
		t.Fatalf("no Ctrl+C was pressed while dictation's setup ran, so this run proved nothing:\n%s\n%s", recorded, output.String())
	}
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		members, err := tree.Members()
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(members, func(member execx.EndedProcess) bool { return strings.EqualFold(member.Name, "cfo.exe") }) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("dictation's setup still ran 30 s after the Ctrl+C had stopped the install, among %+v:\n%s\n%s", members, recorded, output.String())
		}
	}
}
