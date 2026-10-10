package defender

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// recorded is what the read script prints for one detection of two files,
// one detection of command lines alone, and two uploads.
const recorded = `{"detections":[` +
	`{"id":"{A1}","time":"2026-10-07T20:43:25.1230000Z","threat":"Trojan:Win32/Bearfoos.B!ml","process":"C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe","files":["C:\\Users\\o\\AppData\\Local\\cfo\\gotmp\\0b49f5f9\\cg-auth-probe-hang\\TestOneLineInstallKeepsANewerNoMistakesThatThisWindowCannotSee1150654119\\002\\no-mistakes\\no-mistakes.exe","C:\\Users\\o\\AppData\\Local\\cfo\\gotmp\\0b49f5f9\\cg-auth-probe-hang\\TestOneLineInstallKeepsANewerNoMistakesThatThisWindowCannotSee1150654119\\003\\code-goblins-a3\\cfo.exe.download"],"command_lines":0},` +
	`{"id":"{B2}","time":"2026-10-02T04:16:16Z","threat":"Trojan:Win32/ClickFix.DQ!MTB","process":"Unknown","files":[],"command_lines":42}],` +
	`"uploads":[` +
	`{"time":"2026-10-10T13:31:17.5000000Z","file":"C:\\dev\\code-goblins\\scratch\\candidates\\cand-v0.5.13\\install.ps1","sha256":"98be6d6b1ffa14adac0cb872aeed0e42b0f25c2260821891dffd5ef92bf3e277"},` +
	`{"time":"2026-10-09T08:00:00Z","file":"D:\\Photos\\slideshow.exe","sha256":"00aa"}]}`

type scriptRunner struct {
	result  execx.Result
	err     error
	request execx.Request
}

func (runner *scriptRunner) Run(_ context.Context, request execx.Request) (execx.Result, error) {
	runner.request = request
	return runner.result, runner.err
}

// Read asks Defender for its own records since a time and returns them as
// they are: each detection with its files, and each sample it sent to
// Microsoft.
func TestReadReturnsWhatDefenderRecorded(t *testing.T) {
	// Arrange
	runner := &scriptRunner{result: execx.Result{Stdout: []byte(recorded)}}
	since := time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)

	// Act
	report, err := Read(context.Background(), runner, since)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Detections) != 2 || len(report.Uploads) != 2 {
		t.Fatalf("read %d detections and %d uploads, want 2 and 2", len(report.Detections), len(report.Uploads))
	}
	first := report.Detections[0]
	if first.ID != "{A1}" || first.Threat != "Trojan:Win32/Bearfoos.B!ml" || !first.Time.Equal(time.Date(2026, 10, 7, 20, 43, 25, 123000000, time.UTC)) || len(first.Files) != 2 || !strings.HasSuffix(first.Files[1], `cfo.exe.download`) {
		t.Errorf("first detection = %+v", first)
	}
	if second := report.Detections[1]; second.CommandLines != 42 || len(second.Files) != 0 {
		t.Errorf("second detection = %+v, want 42 command lines and no file", second)
	}
	if upload := report.Uploads[0]; !strings.HasSuffix(upload.File, `cand-v0.5.13\install.ps1`) || !strings.HasPrefix(upload.SHA256, "98be6d6b1ffa") || upload.Time.IsZero() {
		t.Errorf("first upload = %+v", upload)
	}
	script := runner.request.Args[len(runner.request.Args)-1]
	if runner.request.Name != "powershell.exe" || !strings.Contains(script, "2026-10-01T12:30:00Z") {
		t.Errorf("the read ran %s without the time it reads from:\n%s", runner.request.Name, script)
	}
}

// The read only reads. It names Defender's three reading commands and its
// log, and nothing that scans, restores, removes or changes a setting.
func TestTheReadChangesNothingOfDefenders(t *testing.T) {
	// Arrange
	runner := &scriptRunner{result: execx.Result{Stdout: []byte(`{"detections":[],"uploads":[]}`)}}

	// Act
	if _, err := Read(context.Background(), runner, time.Now()); err != nil {
		t.Fatal(err)
	}

	// Assert
	script := runner.request.Args[len(runner.request.Args)-1]
	for _, reads := range []string{"Get-MpThreatDetection", "Get-MpThreat ", "Get-WinEvent", "Microsoft-Windows-Windows Defender/Operational"} {
		if !strings.Contains(script, reads) {
			t.Errorf("the read does not use %q:\n%s", reads, script)
		}
	}
	for _, changes := range []string{"Set-Mp", "Add-Mp", "Remove-Mp", "Start-Mp", "Update-Mp", "MpCmdRun", "Restore", "Exclusion", "Submit", "Remove-Item", "Set-Content", "Out-File"} {
		if strings.Contains(script, changes) {
			t.Errorf("the read names %q, want a script that only reads:\n%s", changes, script)
		}
	}
	if strings.Contains(script, "CmdLine:_*' } | ForEach") {
		t.Errorf("the read prints a command line's text, want their count alone:\n%s", script)
	}
}

// A read Defender did not answer is an error with what it said, never an
// empty report: a detector that stops seeing reads as a quiet machine.
func TestAReadDefenderDidNotAnswerIsAnError(t *testing.T) {
	for name, runner := range map[string]*scriptRunner{
		"the script failed":     {result: execx.Result{ExitCode: 1, Stderr: []byte("Get-MpThreatDetection : access denied")}},
		"it could not start":    {err: errors.New("powershell.exe is not there")},
		"it printed no records": {result: execx.Result{}},
		"it printed no report":  {result: execx.Result{Stdout: []byte(`{"detections":[]}`)}},
	} {
		t.Run(name, func(t *testing.T) {
			// Act
			report, err := Read(context.Background(), runner, time.Now())

			// Assert
			if err == nil {
				t.Fatalf("Read returned %+v, want an error", report)
			}
			if name == "the script failed" && !strings.Contains(err.Error(), "access denied") {
				t.Errorf("the error %q does not say what Defender said", err)
			}
		})
	}
}

// Under keeps what names a file in the fleet's folders, whatever the case of
// the path, and every detection that names no file at all, and counts what it
// leaves out without naming it.
func TestUnderKeepsTheFleetsOwnAndCountsTheRest(t *testing.T) {
	// Arrange
	runner := &scriptRunner{result: execx.Result{Stdout: []byte(recorded)}}
	report, err := Read(context.Background(), runner, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	report.Detections = append(report.Detections,
		Detection{ID: "{C3}", Threat: "Trojan:Win32/Other", Files: []string{`D:\Photos\viewer.exe`}},
		Detection{ID: "{D4}", Threat: "Trojan:Win32/Bearfoos.B!ml", Files: []string{`C:\Users\o\AppData\Local\Temp\TestOneLineInstallRetriesTheNoMistakesDownloadThatFailsTwice1554438682\003\x\cfo.exe.download`}})

	// Act
	fleet := report.Under([]string{`c:\DEV\code-goblins`, `C:\Users\o\AppData\Local\cfo`})

	// Assert
	var ids []string
	for _, detection := range fleet.Detections {
		ids = append(ids, detection.ID)
	}
	if !slices.Equal(ids, []string{"{A1}", "{B2}", "{D4}"}) {
		t.Errorf("kept detections %q, want the one under the fleet's folders, the one that names no file and the one in a test's temporary folder", ids)
	}
	if len(fleet.Uploads) != 1 || !strings.HasSuffix(fleet.Uploads[0].File, "install.ps1") {
		t.Errorf("kept uploads %+v, want the one under the fleet's folders", fleet.Uploads)
	}
	if fleet.Elsewhere != 2 {
		t.Errorf("counted %d elsewhere, want the 1 detection and the 1 upload left out", fleet.Elsewhere)
	}
	if kept := report.Under([]string{`C:\dev\code`}); len(kept.Uploads) != 0 {
		t.Errorf("a folder whose name only starts the same counted as the fleet's: %+v", kept.Uploads)
	}
}

// Origin reads from a path which task's folder it is in and which test's
// temporary folder, as far as the path says.
func TestOriginNamesTheTaskAndTheTestThatMadeAFile(t *testing.T) {
	for path, want := range map[string]Origin{
		`C:\Users\o\AppData\Local\cfo\gotmp\0b49f5f9\cg-auth-probe-hang\TestOneLineInstallKeepsANoMistakesAtThePin67573441\003\code-goblins-29\cfo.exe`: {Task: "cg-auth-probe-hang", Test: "TestOneLineInstallKeepsANoMistakesAtThePin"},
		`C:\dev\code-goblins\scratch\cg-install-leaves-nothing\claude\session\scratchpad\main-tree\install.ps1`:                                         {Task: "cg-install-leaves-nothing"},
		`C:\dev\code-goblins\state\tasktmp\cg-dev-drive\proof\cfo.exe`:                                                                                  {Task: "cg-dev-drive"},
		`C:\dev\code-goblins\worktrees\code-goblins\cg-defender-quiet\install.ps1`:                                                                      {Task: "cg-defender-quiet"},
		`D:\CodeGoblins\scratch\pd-billing\TestDevStartsAloneTheWindowItBuilt3943610803\002\built`:                                                      {Task: "pd-billing", Test: "TestDevStartsAloneTheWindowItBuilt"},
		`C:\Users\o\AppData\Local\Temp\TestOneLineInstallRetriesTheNoMistakesDownloadThatFailsTwice1554438682\003\x\cfo.exe.download`:                   {Test: "TestOneLineInstallRetriesTheNoMistakesDownloadThatFailsTwice"},
		`C:\Users\o\.no-mistakes\worktrees\a3a29a05a5f9\01M4DPXSR8JG7RFT43P3AC8AB0\install.ps1`:                                                         {Gate: "01M4DPXSR8JG7RFT43P3AC8AB0"},
		`C:\dev\code-goblins\scratch\candidates\cand-v0.5.13\install.ps1`:                                                                               {},
		`C:\dev\code-goblins\scratch\.tmp\go-build1\b001\pkg.test.exe`:                                                                                  {},
		`C:\dev\code-goblins\bin\cfo.exe`: {},
		`C:\dev\Testing\notes.txt`:        {},
	} {
		if got := OriginOf(path); got != want {
			t.Errorf("OriginOf(%s) = %+v, want %+v", path, got, want)
		}
	}
}

func TestOriginSaysWhatItKnowsInWords(t *testing.T) {
	for origin, want := range map[Origin]string{
		{Task: "cg-x", Test: "TestA"}: "test TestA of task cg-x",
		{Task: "cg-x"}:                "task cg-x",
		{Test: "TestA"}:               "test TestA",
		{Gate: "01M4"}:                "gate run 01M4",
		{}:                            "",
	} {
		if got := origin.String(); got != want {
			t.Errorf("%+v says %q, want %q", origin, got, want)
		}
	}
}
