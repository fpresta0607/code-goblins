package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/proc"
)

// saysItsFolder is an install script that says the folder it runs from, the
// one the setup downloaded it to, as what it is doing.
const saysItsFolder = "Write-Host \"      $PSScriptRoot\"\n"

// A virus scanner still reading the install script it just saw refuses the
// setup's first try at removing the folder it downloaded the script to. One
// silenced try left that folder in the temp folder. The setup tries again, so
// the folder is gone once the install has ended.
func TestTheSetupRemovesItsFolderAScannerHeldForAMoment(t *testing.T) {
	// Arrange
	setup := published(t, saysItsFolder, http.StatusOK)
	var folder string
	released := make(chan struct{})

	// Act
	err := setup.Install(context.Background(), func(progress Progress) {
		if progress.Doing == "" {
			return
		}
		folder = progress.Doing
		// os.Open does not share a file for deletion, as a scanner's read
		// does not.
		reader, err := os.Open(filepath.Join(folder, "install.ps1"))
		if err != nil {
			t.Errorf("the stand-in scanner could not hold the script: %v", err)
			close(released)
			return
		}
		go func() {
			time.Sleep(1500 * time.Millisecond)
			reader.Close()
			close(released)
		}()
	})
	if folder == "" {
		t.Fatalf("the script never said its folder (Install = %v), so this run proved nothing", err)
	}
	<-released

	// Assert
	if err != nil {
		t.Fatalf("Install = %v, want the script run to its end", err)
	}
	if _, err := os.Stat(folder); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the setup left %s behind (%v)", folder, err)
	}
}

// A folder something never lets go of does not fail the install: the setup
// tries for a bounded time, the install ends well, and the log names the
// folder so it can be deleted by hand.
func TestTheSetupNamesAFolderItCouldNotRemove(t *testing.T) {
	// Arrange
	setup := published(t, saysItsFolder, http.StatusOK)
	var folder string
	var reader *os.File
	t.Cleanup(func() {
		if reader != nil {
			reader.Close()
		}
	})

	// Act
	err := setup.Install(context.Background(), func(progress Progress) {
		if progress.Doing == "" {
			return
		}
		folder = progress.Doing
		var err error
		if reader, err = os.Open(filepath.Join(folder, "install.ps1")); err != nil {
			t.Errorf("the stand-in scanner could not hold the script: %v", err)
		}
	})

	// Assert
	if folder == "" || reader == nil {
		t.Fatalf("the stand-in scanner held nothing in %q (Install = %v), so this run proved nothing", folder, err)
	}
	if err != nil {
		t.Errorf("Install = %v, want an install that ended well whatever stays in the temp folder", err)
	}
	// The script may spell the temp folder's own path another way than the
	// setup does, a short name against a long one, so the folder is known by
	// its name.
	named := regexp.MustCompile(`(?m)^Setup could not remove \S*` + regexp.QuoteMeta(filepath.Base(folder)) + `, which another program still holds`)
	if kept := readLog(t, setup); !named.MatchString(kept) {
		t.Errorf("the log does not name %s as what the setup could not remove:\n%s", folder, kept)
	}
}

// The install leaves programs running, as it leaves the no-mistakes daemon,
// and one started with no folder of its own runs in the script's. Windows
// removes no folder a program runs in, so the script runs in the temp folder
// itself, never in the folder the setup removes.
func TestAProgramTheInstallLeavesRunningDoesNotKeepTheSetupsFolder(t *testing.T) {
	// Arrange
	setup := published(t, strings.Join([]string{
		"$left = Start-Process -FilePath powershell.exe -ArgumentList '-NoProfile', '-Command', 'Start-Sleep -Seconds 60' -WindowStyle Hidden -PassThru",
		"Write-Host \"      left $($left.Id)\"",
		saysItsFolder,
	}, "\n"), http.StatusOK)
	var folder string
	left := 0
	t.Cleanup(func() {
		if left == 0 {
			return
		}
		if process, err := os.FindProcess(left); err == nil {
			_ = process.Kill()
		}
	})

	// Act
	err := setup.Install(context.Background(), func(progress Progress) {
		if id, ok := strings.CutPrefix(progress.Doing, "left "); ok {
			left, _ = strconv.Atoi(id)
		} else if progress.Doing != "" {
			folder = progress.Doing
		}
	})

	// Assert
	if err != nil {
		t.Fatalf("Install = %v, want the script run to its end", err)
	}
	if folder == "" || left == 0 {
		t.Fatalf("the script said folder %q and left program %d, so this run proved nothing", folder, left)
	}
	runsIn, err := proc.WorkingDirectory(left)
	if err != nil {
		t.Fatalf("the program the script left running, %d, is gone (%v), so this run proved nothing", left, err)
	}
	if _, err := os.Stat(folder); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the setup left %s behind while program %d, which the install started, runs in %s (%v)", folder, left, runsIn, err)
	}
}
