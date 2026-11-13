package defender

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

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
		Detection{ID: "{D4}", Threat: "Trojan:Win32/Bearfoos.B!ml", Files: []string{`C:\Users\o\AppData\Local\Temp\TestOneLineInstallRetriesTheNoMistakesDownloadThatFailsTwice1554438682\003\x\goblins.exe.download`}})

	// Act
	fleet := report.Under([]string{`c:\DEV\code-goblins`, `C:\Users\o\AppData\Local\goblins`})

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
		`C:\Users\o\AppData\Local\goblins\gotmp\0b49f5f9\cg-auth-probe-hang\TestOneLineInstallKeepsANoMistakesAtThePin67573441\003\code-goblins-29\goblins.exe`: {Task: "cg-auth-probe-hang", Test: "TestOneLineInstallKeepsANoMistakesAtThePin"},
		`C:\dev\code-goblins\scratch\cg-install-leaves-nothing\claude\session\scratchpad\main-tree\install.ps1`:                                                 {Task: "cg-install-leaves-nothing"},
		`C:\dev\code-goblins\state\tasktmp\cg-dev-drive\proof\goblins.exe`:                                                                                      {Task: "cg-dev-drive"},
		`C:\dev\code-goblins\worktrees\code-goblins\cg-defender-quiet\install.ps1`:                                                                              {Task: "cg-defender-quiet"},
		`D:\CodeGoblins\scratch\pd-billing\TestDevStartsAloneTheWindowItBuilt3943610803\002\built`:                                                              {Task: "pd-billing", Test: "TestDevStartsAloneTheWindowItBuilt"},
		`C:\Users\o\AppData\Local\Temp\TestOneLineInstallRetriesTheNoMistakesDownloadThatFailsTwice1554438682\003\x\goblins.exe.download`:                       {Test: "TestOneLineInstallRetriesTheNoMistakesDownloadThatFailsTwice"},
		`C:\Users\o\.no-mistakes\worktrees\a3a29a05a5f9\01M4DPXSR8JG7RFT43P3AC8AB0\install.ps1`:                                                                 {Gate: "01M4DPXSR8JG7RFT43P3AC8AB0"},
		`D:\CodeGoblins\no-mistakes\worktrees\a3a29a05a5f9\01M4DPXSR8JG7RFT43P3AC8AB0\install.ps1`:                                                              {Gate: "01M4DPXSR8JG7RFT43P3AC8AB0"},
		`C:\dev\code-goblins\scratch\candidates\cand-v0.5.13\install.ps1`:                                                                                       {},
		`C:\dev\code-goblins\scratch\.tmp\go-build1\b001\pkg.test.exe`:                                                                                          {},
		`C:\dev\code-goblins\bin\goblins.exe`:                                                                                                                   {},
		`C:\dev\Testing\notes.txt`:                                                                                                                              {},
	} {
		if got := OriginOf(path); got != want {
			t.Errorf("OriginOf(%s) = %+v, want %+v", path, got, want)
		}
	}
}
