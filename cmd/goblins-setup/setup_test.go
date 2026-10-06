package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// standInVariable names the file a stand-in for the app writes its arguments
// to. Set, it makes this test binary that stand-in: copied into a home as
// goblins-window.exe, it records how it was started and ends.
const standInVariable = "GOBLINS_SETUP_TEST_STANDIN"

func TestMain(m *testing.M) {
	if record := os.Getenv(standInVariable); record != "" {
		if err := os.WriteFile(record, []byte(strings.Join(os.Args[1:], " ")), 0o600); err != nil {
			os.Exit(90)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
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
		Log:    filepath.Join(t.TempDir(), "setup.log"),
		Client: release.Client(),
	}
}

// The setup runs the release's install script out of sight and hands on each
// line it prints, in order and without the empty ones, and keeps them all in
// its log. A line a tool drew its progress over is shown as it ended.
func TestTheSetupRunsTheReleasesInstallScriptAndShowsEachLine(t *testing.T) {
	// Arrange
	setup := published(t, "Write-Host 'Downloading cfo.exe'\nWrite-Host ''\nWrite-Host \"10%`r60%`rVerified cfo.exe\"\n", http.StatusOK)
	var shown []string

	// Act
	err := setup.Install(context.Background(), func(line string) { shown = append(shown, line) })

	// Assert
	if err != nil {
		t.Fatalf("Install = %v, want the script run to its end; it showed %q", err, shown)
	}
	want := []string{"Downloading the installer from " + setup.Script + " ...", "Downloading cfo.exe", "Verified cfo.exe"}
	if !slices.Equal(shown, want) {
		t.Errorf("the setup showed %q, want %q", shown, want)
	}
	kept, err := os.ReadFile(setup.Log)
	if err != nil {
		t.Fatal(err)
	}
	if string(kept) != strings.Join(want, "\n")+"\n" {
		t.Errorf("the log holds %q, want every line shown, one to a line", kept)
	}
}

// An install that stops is explained with the script's own last lines, which
// say what to fix.
func TestAnInstallThatStopsIsExplainedWithItsOwnLastLines(t *testing.T) {
	// Arrange
	setup := published(t, "Write-Host 'Install App Installer from the Microsoft Store for winget, then run this again.'\nthrow 'winget is missing, and the install needs it for git and gh.'\n", http.StatusOK)

	// Act
	err := setup.Install(context.Background(), func(string) {})

	// Assert
	if err == nil {
		t.Fatal("Install reports no error for a script that stopped")
	}
	for _, want := range []string{"the install stopped", "Install App Installer from the Microsoft Store", "winget is missing, and the install needs it for git and gh."} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error lacks %q:\n%v", want, err)
		}
	}
}

// A release whose install script cannot be downloaded is named with what its
// server answered, and nothing is run.
func TestAScriptThatCannotBeDownloadedIsNamedAndNothingRuns(t *testing.T) {
	// Arrange
	setup := published(t, "", http.StatusNotFound)
	var shown []string

	// Act
	err := setup.Install(context.Background(), func(line string) { shown = append(shown, line) })

	// Assert
	if err == nil || !strings.Contains(err.Error(), setup.Script) || !strings.Contains(err.Error(), "404") {
		t.Errorf("Install = %v, want the address named with the server's 404", err)
	}
	if len(shown) != 1 {
		t.Errorf("the setup showed %q, want only that it was downloading", shown)
	}
}

// Closing the setup ends the install with it: the script is stopped, what it
// had not done yet is never done, and the setup says the install was stopped.
func TestClosingTheSetupEndsTheInstall(t *testing.T) {
	// Arrange
	setup := published(t, "Write-Host 'started'\nStart-Sleep -Seconds 120\nWrite-Host 'finished'\n", http.StatusOK)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var shown []string
	began := time.Now()

	// Act
	err := setup.Install(ctx, func(line string) {
		shown = append(shown, line)
		if line == "started" {
			cancel()
		}
	})

	// Assert
	if err == nil || !strings.Contains(err.Error(), "was stopped before it finished") {
		t.Errorf("Install = %v, want it to say the install was stopped", err)
	}
	if slices.Contains(shown, "finished") {
		t.Errorf("the script ran to its end after the setup was closed: %q", shown)
	}
	if took := time.Since(began); took > time.Minute {
		t.Errorf("the install took %s to stop, so the script was waited for, not ended", took)
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

// Once installed, the app in the home is opened on its own, with nothing
// else: that is how it finds or starts the supervisor and shows the board.
func TestTheSetupOpensTheAppTheInstallPutInTheHome(t *testing.T) {
	// Arrange
	home := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	program, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, appName), program, 0o755); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "arguments")
	t.Setenv(standInVariable, record)

	// Act
	err = openApp(home)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	var ran []byte
	for deadline := time.Now().Add(time.Minute); ; time.Sleep(100 * time.Millisecond) {
		if ran, err = os.ReadFile(record); err == nil || time.Now().After(deadline) {
			break
		}
	}
	if err != nil || string(ran) != "" {
		t.Errorf("the app was started with %q (%v), want it started with no arguments", ran, err)
	}
}

// A home with no app in it, as a release that ships none leaves, is said so
// with what to do instead.
func TestAHomeWithNoAppIsSaidSo(t *testing.T) {
	home := t.TempDir()

	err := openApp(home)

	if err == nil || !strings.Contains(err.Error(), "has no desktop app in it") || !strings.Contains(err.Error(), "run: goblins") {
		t.Errorf("openApp = %v, want it to say the release has no app and to run goblins", err)
	}
}
