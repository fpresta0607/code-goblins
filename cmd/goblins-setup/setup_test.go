package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// cfoStandInVariable makes this test binary a stand-in for a tool whose every
// command succeeds, such as a release's cfo.exe, which install puts in the
// per-user home's bin as cfo install does; asked its version, it gives the
// no-mistakes version the variable holds, as the managed no-mistakes.
const cfoStandInVariable = "GOBLINS_SETUP_TEST_CFO"

func TestMain(m *testing.M) {
	if pin := os.Getenv(cfoStandInVariable); pin != "" {
		if len(os.Args) > 1 && os.Args[1] == "--version" {
			fmt.Println("no-mistakes version v" + pin)
		}
		if len(os.Args) > 1 && os.Args[1] == "install" {
			os.Exit(standInInstall())
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// standInInstall puts this binary in the per-user home's bin under both the
// binary's names.
func standInInstall() int {
	self, err := os.Executable()
	if err != nil {
		return 1
	}
	data, err := os.ReadFile(self)
	if err != nil {
		return 1
	}
	bin := filepath.Join(os.Getenv("LOCALAPPDATA"), "CodeGoblins", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return 1
	}
	for _, name := range []string{"cfo.exe", "goblins.exe"} {
		if err := os.WriteFile(filepath.Join(bin, name), data, 0o755); err != nil {
			return 1
		}
	}
	return 0
}

// published is a release of the test's own that publishes script as its
// install script, or answers status in its place, and the setup that installs
// from it. Nothing here installs Code Goblins: the script is the test's.
func published(t *testing.T, script string, status int) Setup {
	t.Helper()
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			http.Error(w, "no such release", status)
			return
		}
		_, _ = w.Write([]byte(script))
	}))
	t.Cleanup(release.Close)
	return Setup{
		Script: release.URL + "/install.ps1",
		Shell:  "powershell.exe",
		Log:    filepath.Join(t.TempDir(), "install.log"),
		Client: release.Client(),
	}
}

func readLog(t *testing.T, setup Setup) string {
	t.Helper()
	kept, err := os.ReadFile(setup.Log)
	if err != nil {
		t.Fatal(err)
	}
	return string(kept)
}

// The window shows only the script's plain lines, as steps, what the step
// under way is doing, and notes; the script writes its details into the log
// the setup names, and anything else it prints is kept there too, never shown.
func TestTheSetupShowsThePlainStepsAndKeepsTheRestInTheLog(t *testing.T) {
	// Arrange
	setup := published(t, strings.Join([]string{
		"Write-Host '[1/4] Download Code Goblins'",
		"Write-Host '      Downloading cfo.exe'",
		"Add-Content -LiteralPath $env:CODE_GOBLINS_LOG 'Verified cfo.exe against SHA256SUMS'",
		"Write-Host '[2/4] Check the download'",
		"Write-Host 'a raw line the script did not mean to show'",
		"Write-Host 'Note: You already run Code Goblins from this folder.'",
		"Write-Host 'Done: Code Goblins is installed.'",
		"Write-Host 'The full log is somewhere'",
	}, "\n"), http.StatusOK)
	var shown []Progress

	// Act
	err := setup.Install(context.Background(), func(progress Progress) { shown = append(shown, progress) })

	// Assert
	if err != nil {
		t.Fatalf("Install = %v, want the script run to its end; it showed %+v", err, shown)
	}
	want := []Progress{{Step: 1}, {Step: 1}, {Doing: "Downloading cfo.exe"}, {Step: 2}, {Note: "You already run Code Goblins from this folder."}}
	if !slices.Equal(shown, want) {
		t.Errorf("the setup showed %+v, want %+v", shown, want)
	}
	kept := readLog(t, setup)
	for _, line := range []string{"Downloading the install script from " + setup.Script, "Verified cfo.exe against SHA256SUMS", "a raw line the script did not mean to show"} {
		if !strings.Contains(kept, line) {
			t.Errorf("the log lacks %q:\n%s", line, kept)
		}
	}
}

// A script that stops says why in its Failed line, and that one sentence is
// what the window says; everything else stays in the log behind Show details.
func TestAFailedInstallSaysTheScriptsOneSentence(t *testing.T) {
	// Arrange
	setup := published(t, strings.Join([]string{
		"Write-Host '[3/4] Install Code Goblins and its tools'",
		"Add-Content -LiteralPath $env:CODE_GOBLINS_LOG 'winget: 0x8a15000f Data required by the source is missing'",
		"Write-Host 'Failed: Git could not be installed. Check the internet connection, then try again.'",
		"exit 1",
	}, "\n"), http.StatusOK)

	// Act
	err := setup.Install(context.Background(), func(Progress) {})

	// Assert
	if err == nil || err.Error() != "Git could not be installed. Check the internet connection, then try again." {
		t.Fatalf("Install = %v, want the script's one sentence", err)
	}
	if details := setup.Details(); !strings.Contains(details, "0x8a15000f") {
		t.Errorf("Show details lacks the script's detail:\n%s", details)
	}
}

// A script that stops without saying why, as one PowerShell itself stops does,
// is said in one plain sentence, and what PowerShell printed is in the log.
func TestAnInstallThatStopsWithoutSayingWhyIsSaidPlainly(t *testing.T) {
	// Arrange
	setup := published(t, "throw 'something PowerShell raised'\n", http.StatusOK)

	// Act
	err := setup.Install(context.Background(), func(Progress) {})

	// Assert
	if err == nil || !strings.HasPrefix(err.Error(), "The install stopped before it finished;") {
		t.Fatalf("Install = %v, want the plain sentence for an install that stopped", err)
	}
	if details := setup.Details(); !strings.Contains(details, "something PowerShell raised") {
		t.Errorf("Show details lacks what PowerShell printed:\n%s", details)
	}
}

// A release whose install script cannot be downloaded is said plainly, with
// what its server answered in the log, and nothing is run.
func TestAScriptThatCannotBeDownloadedIsSaidAndNothingRuns(t *testing.T) {
	// Arrange
	setup := published(t, "", http.StatusNotFound)
	var shown []Progress

	// Act
	err := setup.Install(context.Background(), func(progress Progress) { shown = append(shown, progress) })

	// Assert
	if err == nil || err.Error() != "Code Goblins could not be downloaded. Check the internet connection, then try again." {
		t.Errorf("Install = %v, want the plain sentence for a failed download", err)
	}
	if kept := readLog(t, setup); !strings.Contains(kept, "404") {
		t.Errorf("the log lacks the server's 404:\n%s", kept)
	}
	if !slices.Equal(shown, []Progress{{Step: 1}}) {
		t.Errorf("the setup showed %+v, want only that it began the download", shown)
	}
}

// Closing the setup ends the install with it: the script is stopped, what it
// had not done yet is never done, and the setup says the install was stopped.
func TestClosingTheSetupEndsTheInstall(t *testing.T) {
	// Arrange
	setup := published(t, "Write-Host '      started'\nStart-Sleep -Seconds 120\nWrite-Host '      finished'\n", http.StatusOK)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var shown []Progress
	began := time.Now()

	// Act
	err := setup.Install(ctx, func(progress Progress) {
		shown = append(shown, progress)
		if progress.Doing == "started" {
			cancel()
		}
	})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "was stopped before it finished") {
		t.Errorf("Install = %v, want it to say the install was stopped", err)
	}
	if slices.Contains(shown, Progress{Doing: "finished"}) {
		t.Errorf("the script ran to its end after the setup was closed: %+v", shown)
	}
	if took := time.Since(began); took > time.Minute {
		t.Errorf("the install took %s to stop, so the script was waited for, not ended", took)
	}
}

// Show details shows the log's last lines, not all of a long one.
func TestShowDetailsShowsTheLogsLastLines(t *testing.T) {
	// Arrange
	setup := Setup{Log: filepath.Join(t.TempDir(), "install.log")}
	var lines []string
	for i := range shownLines + 5 {
		lines = append(lines, "line "+string(rune('A'+i)))
	}
	if err := os.WriteFile(setup.Log, []byte(strings.Join(lines, "\r\n")+"\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	details := setup.Details()

	// Assert
	if want := strings.Join(lines[5:], "\n"); details != want {
		t.Errorf("Details = %q, want the last %d lines %q", details, shownLines, want)
	}
}

// A setup built by a release installs that release, and one built with no
// release named installs the latest.
func TestTheSetupInstallsTheReleaseThatBuiltIt(t *testing.T) {
	for name, test := range map[string]struct{ tag, want string }{
		"a release's own setup":     {"v1.2.3", "https://github.com/someone/code-goblins/releases/download/v1.2.3/install.ps1"},
		"a setup built from source": {"", "https://github.com/someone/code-goblins/releases/latest/download/install.ps1"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			wasRepository, wasTag := repository, tag
			t.Cleanup(func() { repository, tag = wasRepository, wasTag })
			repository, tag = "someone/code-goblins", test.tag

			// Act
			got := scriptURL()

			// Assert
			if got != test.want {
				t.Errorf("scriptURL = %s, want %s", got, test.want)
			}
		})
	}
}

// The setup's manifest makes it draw at each monitor's own scale, so its text
// is sharp on a display set larger than 100 percent rather than stretched.
func TestTheSetupDrawsAtEachMonitorsScale(t *testing.T) {
	// Arrange
	data, err := os.ReadFile("winres.json")
	if err != nil {
		t.Fatal(err)
	}
	var resources struct {
		Manifest map[string]map[string]struct {
			DPIAwareness string `json:"dpi-awareness"`
		} `json:"RT_MANIFEST"`
	}

	// Act
	err = json.Unmarshal(data, &resources)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if got := resources.Manifest["#1"]["0409"].DPIAwareness; got != "per monitor v2" {
		t.Errorf("winres.json declares dpi-awareness %q, want \"per monitor v2\"", got)
	}
}

// The setup hands the install script the person's choice of Start at login:
// -NoStartAtLogin when the box is unticked, -StartAtLogin when it is ticked,
// and neither where no choice was made, which keeps the home's.
func TestTheSetupHandsTheScriptTheChoiceOfStartAtLogin(t *testing.T) {
	for choice, want := range map[string]string{"off": "-NoStartAtLogin", "on": "-StartAtLogin", "": ""} {
		t.Run("choice "+choice, func(t *testing.T) {
			// Arrange
			setup := published(t, "Write-Host ('Note: given [' + ($args -join ' ') + ']')", http.StatusOK)
			setup.StartAtLogin = choice
			var shown []Progress

			// Act
			err := setup.Install(context.Background(), func(progress Progress) { shown = append(shown, progress) })

			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(shown, Progress{Note: "given [" + want + "]"}) {
				t.Errorf("the script was given %+v, want [%s]", shown, want)
			}
		})
	}
}
