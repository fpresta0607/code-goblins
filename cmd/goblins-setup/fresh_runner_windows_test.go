package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// freshRunnerRelease names the release the setup installs from on a fresh
// runner. The install workflow's setup job sets it; anywhere else this test
// is skipped, since it installs Code Goblins onto the machine it runs on.
const freshRunnerRelease = "CODE_GOBLINS_SETUP_RELEASE"

// The setup installs Code Goblins as the window does once Install is
// pressed, the window aside: it downloads the release's install script, runs
// it out of sight and reads its plain lines, and the install sets dictation
// up on the way, so the first press works at once. The workflow's next step
// checks the home it left, dictation's engine and model among it.
func TestTheSetupInstallsWithDictationReadyOnAFreshRunner(t *testing.T) {
	// Arrange
	release := os.Getenv(freshRunnerRelease)
	if release == "" {
		t.Skip("installs onto this machine; the install workflow's setup job sets " + freshRunnerRelease)
	}
	setup := Setup{
		Script: strings.TrimSuffix(release, "/") + "/install.ps1",
		Shell:  "powershell.exe",
		Log:    filepath.Join(os.TempDir(), "CodeGoblinsInstall.log"),
		Client: http.DefaultClient,
	}
	var doing, notes []string

	// Act
	err := setup.Install(context.Background(), func(progress Progress) {
		switch {
		case progress.Doing != "":
			doing = append(doing, progress.Doing)
		case progress.Note != "":
			notes = append(notes, progress.Note)
		}
	})

	// Assert
	if err != nil {
		t.Fatalf("the setup = %v:\n%s", err, setup.Details())
	}
	if !slices.Contains(doing, "Setting up dictation") {
		t.Errorf("the setup showed %q, want it to say it sets dictation up", doing)
	}
	for _, note := range notes {
		if strings.Contains(note, "Dictation") {
			t.Errorf("the setup noted %q, want dictation set up", note)
		}
	}
}
