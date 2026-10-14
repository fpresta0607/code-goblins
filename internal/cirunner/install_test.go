package cirunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
)

// installOptions are options for an install into dir, whose every seam
// records what it was asked to do.
func installOptions(t *testing.T, dir string, commands *recorder) (InstallOptions, *[]string) {
	t.Helper()
	var asked []string
	return InstallOptions{
		AppID:          1,
		InstallationID: 2,
		Owner:          "fpresta0607",
		Repo:           "PrecisionDocs-AI",
		KeyFile:        filepath.Join(dir, "key.pem"),
		User:           "S-1-5-21-1-2-3-1001",
		Dir:            filepath.Join(dir, "ci-runner"),
		Program:        `C:\Program Files\Code Goblins\ci-runner\cg-ci-runner.exe`,
		Commands:       commands,
		Download: func(_ context.Context, url, _ string) error {
			asked = append(asked, "download "+url)
			return nil
		},
		Ask: func(_ context.Context, request Request, _ func(Reply)) error {
			asked = append(asked, "ask "+request.Kind)
			return nil
		},
		Sleep: func(context.Context, time.Duration) {},
		Now:   time.Now,
	}, &asked
}

// The install's steps, in the order they run.
func TestInstallStepsInTheirOrder(t *testing.T) {
	// Arrange
	options, _ := installOptions(t, t.TempDir(), &recorder{})

	// Act
	steps := InstallSteps(options)

	// Assert
	var titles []string
	for _, step := range steps {
		titles = append(titles, step.Title)
	}
	want := []string{
		"check this machine",
		"make the service's folder, which only SYSTEM, Administrators and the service may open",
		"download and check the image",
		"make the disk",
		"write the first boot's settings",
		"create the VM",
		"first boot and its checkpoint",
		"create the service and its account",
		"keep the App's settings and key",
		"start the service",
		"readiness check",
	}
	if !slices.Equal(titles, want) {
		t.Errorf("steps %q; want %q", titles, want)
	}
}

// A dry run says every step and every line it would run, and runs, writes
// and asks nothing.
func TestDryRunNamesEveryStepAndChangesNothing(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	commands := &recorder{}
	options, asked := installOptions(t, dir, commands)
	steps := InstallSteps(options)
	var out bytes.Buffer

	// Act
	err := RunSteps(context.Background(), steps, true, &out)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	said := out.String()
	for i, step := range steps {
		if heading := fmt.Sprintf("step %d of %d: %s\n", i+1, len(steps), step.Title); !strings.Contains(said, heading) {
			t.Errorf("the dry run did not say %q", heading)
		}
		for _, line := range step.Says {
			if !strings.Contains(said, "    "+line+"\n") {
				t.Errorf("the dry run did not say %q of step %q", line, step.Title)
			}
		}
	}
	for _, script := range []string{checkScript, folderScript, diskScript, settingsScript, vmScript, checkpointScript, serviceScript, startScript} {
		for _, line := range strings.Split(script, "\n") {
			if !strings.Contains(said, "    "+line+"\n") {
				t.Errorf("the dry run did not say the script line %q", line)
			}
		}
	}
	for _, line := range []string{ImageURL, ImageSHA256, options.Program, ServiceSID(ServiceName), options.User} {
		if !strings.Contains(said, line) {
			t.Errorf("the dry run did not name %q", line)
		}
	}
	if len(commands.ran) != 0 || len(*asked) != 0 {
		t.Errorf("the dry run ran %d command(s) and did %q; want nothing", len(commands.ran), *asked)
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Errorf("the dry run left %d file(s) in its folder (%v); want none", len(entries), err)
	}
}

// Each script takes its paths from its environment, never from its text, and
// names the VM, the checkpoint and the service only as the constants they
// are.
func TestInstallScriptsTakeTheirPathsFromTheEnvironment(t *testing.T) {
	// Arrange
	dir := t.TempDir()
	commands := &recorder{}
	options, _ := installOptions(t, dir, commands)
	options.Dir = filepath.Join(dir, `x'; Remove-VM -Force; '`)
	steps := InstallSteps(options)

	// Act
	for _, index := range []int{0, 1, 5, 7, 9} {
		_ = steps[index].Run(context.Background())
	}

	// Assert
	if len(commands.ran) != 5 {
		t.Fatalf("ran %d scripts; want the 5 steps' scripts", len(commands.ran))
	}
	for _, request := range commands.ran {
		if strings.Contains(script(request), options.Dir) || strings.Contains(script(request), options.Program) {
			t.Errorf("the script %q holds a path; want every path in its environment", script(request))
		}
	}
	for index, want := range map[int]string{
		1: "CG_CI_DIR=" + options.Dir,
		2: "CG_CI_VM=" + filepath.Join(options.Dir, "vm"),
		3: "CG_CI_PROGRAM=" + options.Program,
	} {
		if !slices.Contains(commands.ran[index].Env, want) {
			t.Errorf("script %d's environment lacks %q", index, want)
		}
	}
	if !slices.Contains(commands.ran[1].Env, "CG_CI_SERVICE_SID="+ServiceSID(ServiceName)) {
		t.Errorf("the folder's script does not name the service's SID")
	}
}

// The service's SID is the one Windows gives NT SERVICE\cg-ci-runner, as
// sc.exe showsid names it, before the service exists.
func TestServiceSIDIsTheServicesOwn(t *testing.T) {
	if got := ServiceSID("cg-ci-runner"); got != "S-1-5-80-3378420271-2222921232-1105503310-2410367952-2229631032" {
		t.Errorf("ServiceSID = %q; want the SID sc.exe showsid names", got)
	}
}

// The first boot is done once the VM powers itself off, and one that never
// does is an error that says where to look, at the wait's limit.
func TestWaitForFirstBootWaitsForThePowerOff(t *testing.T) {
	for name, row := range map[string]struct {
		offAt   int
		wantErr string
	}{
		"the setup ends": {offAt: 3},
		"it never ends":  {wantErr: "did not power it off within 50ms"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			vm := &fakeJob{offAt: row.offAt}
			sleep := func(ctx context.Context, _ time.Duration) {
				select {
				case <-ctx.Done():
				case <-time.After(5 * time.Millisecond):
				}
			}

			// Act
			err := waitForFirstBoot(context.Background(), vm, sleep, 50*time.Millisecond)

			// Assert
			if row.wantErr == "" && err != nil || row.wantErr != "" && (err == nil || !strings.Contains(err.Error(), row.wantErr)) {
				t.Errorf("waitForFirstBoot = %v; want error %q", err, row.wantErr)
			}
			if calls := vm.callsMade(); len(calls) == 0 || calls[0] != "start" {
				t.Errorf("calls %q; want the VM started first", calls)
			}
		})
	}
}

// A download whose SHA-256 is not the pinned one is refused and removed.
func TestADownloadIsKeptOnlyWithItsPinnedSHA256(t *testing.T) {
	data := []byte("the image")
	sum := sha256.Sum256(data)
	for name, row := range map[string]struct {
		want   string
		isKept bool
	}{
		"its own": {want: hex.EncodeToString(sum[:]), isKept: true},
		"another": {want: strings.Repeat("0", 64)},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			path := filepath.Join(t.TempDir(), "disk.img")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			err := checkSHA256(path, row.want)

			// Assert
			_, statErr := os.Stat(path)
			if (err == nil) != row.isKept || (statErr == nil) != row.isKept {
				t.Errorf("checkSHA256 = %v, the file %v; want it kept %v", err, statErr, row.isKept)
			}
		})
	}
}

// The App's settings and key are kept where the service reads them, and a
// key that is not an RSA key is refused before anything is written.
func TestKeepAppWritesWhatTheServiceReads(t *testing.T) {
	for name, keyType := range map[string]string{"an RSA key": "pkcs1", "another key": "ecdsa"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			keys, _ := serviceFolder(t, goodSettings, keyType)
			dir := t.TempDir()
			options, _ := installOptions(t, dir, &recorder{})
			options.KeyFile = filepath.Join(keys, "app-key.pem")
			if err := os.MkdirAll(options.Dir, 0o700); err != nil {
				t.Fatal(err)
			}

			// Act
			err := keepApp(options)

			// Assert
			config, _, readErr := ReadConfig(options.Dir)
			want := Config{AppID: 1, InstallationID: 2, Owner: "fpresta0607", Repo: "PrecisionDocs-AI", Allowed: "S-1-5-21-1-2-3-1001"}
			if keyType == "pkcs1" && (err != nil || readErr != nil || config != want) {
				t.Errorf("keepApp = %v, read back %+v, %v; want %+v", err, config, readErr, want)
			}
			if entries, _ := os.ReadDir(options.Dir); keyType == "ecdsa" && (err == nil || len(entries) != 0) {
				t.Errorf("keepApp = %v, leaving %d file(s); want the key refused before anything is written", err, len(entries))
			}
		})
	}
}

// The install waits for the started service to answer, and says where to
// look when it never does.
func TestReadyWaitsForTheServiceToAnswer(t *testing.T) {
	for name, row := range map[string]struct {
		answersAt int
		wantErr   string
	}{
		"it answers":       {answersAt: 3},
		"it never answers": {wantErr: "did not answer within 50ms"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			asks := 0
			ask := func(context.Context, Request, func(Reply)) error {
				asks++
				if row.answersAt != 0 && asks >= row.answersAt {
					return nil
				}
				return ErrNotServing
			}
			sleep := func(ctx context.Context, _ time.Duration) {
				select {
				case <-ctx.Done():
				case <-time.After(5 * time.Millisecond):
				}
			}

			// Act
			err := ready(context.Background(), ask, sleep, 50*time.Millisecond)

			// Assert
			if row.wantErr == "" && err != nil || row.wantErr != "" && (err == nil || !strings.Contains(err.Error(), row.wantErr) || !errors.Is(err, ErrNotServing)) {
				t.Errorf("ready = %v; want error %q", err, row.wantErr)
			}
		})
	}
}

// A download keeps what a 200 answer holds, and refuses any other answer.
func TestDownloadFileKeepsOnlyAnAnswerOf200(t *testing.T) {
	for name, status := range map[string]int{"found": http.StatusOK, "missing": http.StatusNotFound} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				w.Write([]byte("the image"))
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "disk.img")

			// Act
			err := DownloadFile(context.Background(), server.Client(), server.URL, path)

			// Assert
			data, readErr := os.ReadFile(path)
			if status == http.StatusOK && (err != nil || readErr != nil || string(data) != "the image") {
				t.Errorf("DownloadFile = %v, kept %q (%v); want the answer kept", err, data, readErr)
			}
			if status != http.StatusOK && (err == nil || !strings.Contains(err.Error(), "HTTP 404") || readErr == nil) {
				t.Errorf("DownloadFile = %v, kept %v; want HTTP 404 refused and nothing kept", err, readErr)
			}
		})
	}
}

// The service's folder, where the App's key goes, drops what it would
// inherit, so Users cannot read it, and is open to SYSTEM, Administrators
// and the service alone.
func TestTheServicesFolderIsOpenToItsThreeAlone(t *testing.T) {
	// Arrange
	icacls := ""
	for _, line := range strings.Split(folderScript, "\n") {
		if strings.HasPrefix(line, "& icacls.exe ") {
			icacls = line
		}
	}

	// Act
	grants := regexp.MustCompile(`'\*([^:']*):\(OI\)\(CI\)([FM])'|'\*' \+ \$env:CG_CI_SERVICE_SID \+ ':\(OI\)\(CI\)M'`).FindAllStringSubmatch(icacls, -1)

	// Assert
	if !strings.Contains(icacls, " /inheritance:r ") || !strings.Contains(icacls, " /grant:r ") {
		t.Errorf("icacls %q; want inherited access removed and the grants replaced", icacls)
	}
	var who []string
	for _, grant := range grants {
		if grant[1] == "" {
			who = append(who, "the service")
			continue
		}
		who = append(who, grant[1]+" "+grant[2])
	}
	if want := []string{"S-1-5-18 F", "S-1-5-32-544 F", "the service"}; !slices.Equal(who, want) || strings.Count(icacls, ":(OI)(CI)") != 3 {
		t.Errorf("icacls %q grants %q; want %q alone", icacls, who, want)
	}
}
