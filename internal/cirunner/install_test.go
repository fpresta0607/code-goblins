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
		"make the service's folder, owned by Administrators, which only SYSTEM, Administrators and the service may open",
		"download and check the image",
		"make the disk",
		"write the first boot's settings to its DVD",
		"create the VM",
		"close the VM's network to the host and the LAN, but for DNS and DHCP",
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

// Told to, the install ends by deleting the key file it was given, which any
// program of the user's can read once the service holds its own copy, and
// otherwise leaves that file alone.
func TestTheInstallRemovesTheGivenKeyOnlyWhenTold(t *testing.T) {
	for name, isRemoved := range map[string]bool{"told to": true, "not told": false} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			options, _ := installOptions(t, t.TempDir(), &recorder{})
			options.RemoveKey = isRemoved
			if err := os.WriteFile(options.KeyFile, []byte("the App's key"), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			steps := InstallSteps(options)
			last := steps[len(steps)-1]
			var err error
			if isRemoved {
				err = last.Run(context.Background())
			}

			// Assert
			_, statErr := os.Stat(options.KeyFile)
			if isRemoved && (last.Title != "remove the App's key file it was given" || err != nil || !os.IsNotExist(statErr)) {
				t.Errorf("the last step %q = %v, the key %v; want the key file removed", last.Title, err, statErr)
			}
			if !isRemoved && (last.Title != "readiness check" || statErr != nil) {
				t.Errorf("the last step is %q, the key %v; want the readiness check and the key kept", last.Title, statErr)
			}
		})
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
	for _, script := range []string{checkScript, folderScript, diskScript, vmScript, networkScript, checkpointScript, serviceScript, startScript} {
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
	for _, index := range []int{0, 1, 5, 6, 8, 10} {
		_ = steps[index].Run(context.Background())
	}

	// Assert
	if len(commands.ran) != 6 {
		t.Fatalf("ran %d scripts; want the 6 steps' scripts", len(commands.ran))
	}
	for _, request := range commands.ran {
		if strings.Contains(script(request), options.Dir) || strings.Contains(script(request), options.Program) {
			t.Errorf("the script %q holds a path; want every path in its environment", script(request))
		}
	}
	for index, want := range map[int]string{
		1: "CG_CI_DIR=" + options.Dir,
		2: "CG_CI_SEED=" + filepath.Join(options.Dir, "vm", "seed.iso"),
		4: "CG_CI_PROGRAM=" + options.Program,
	} {
		if !slices.Contains(commands.ran[index].Env, want) {
			t.Errorf("script %d's environment lacks %q", index, want)
		}
	}
	if !slices.Contains(commands.ran[1].Env, "CG_CI_SERVICE_SID="+ServiceSID(ServiceName)) {
		t.Errorf("the folder's script does not name the service's SID")
	}
}

// installAndUninstallScripts are every script the install and the uninstall
// run, by what each does.
var installAndUninstallScripts = map[string]string{"check": checkScript, "folder": folderScript, "disk": diskScript, "vm": vmScript, "network": networkScript, "checkpoint": checkpointScript, "service": serviceScript, "start": startScript, "remove the service": removeServiceScript, "remove the VM": removeVMScript, "remove the folder": removeFolderScript}

// No script mounts a volume on the host: a mounted volume is open to every
// program on it, which could change the VM's first boot settings before the
// VM read them.
func TestNoScriptMountsAVolumeOnTheHost(t *testing.T) {
	for name, text := range installAndUninstallScripts {
		for _, mounting := range []string{"Mount-VHD", "Mount-DiskImage", "AssignDriveLetter", "Add-PartitionAccessPath"} {
			if strings.Contains(text, mounting) {
				t.Errorf("the %s script runs %s", name, mounting)
			}
		}
	}
}

// Every Windows tool a script runs is named by its path in Windows' own
// folder, never by a name a search path finds.
func TestScriptsRunWindowsToolsByTheirFullPath(t *testing.T) {
	bare := regexp.MustCompile(`(?i)(^|[\s&;({])(sc|icacls)(\.exe)?\s`)
	for name, text := range installAndUninstallScripts {
		if found := bare.FindString(text); found != "" {
			t.Errorf("the %s script runs %q by its name alone", name, strings.TrimSpace(found))
		}
	}
}

// Windows starts the service again a minute after it stops on a failure,
// each time, whether it crashed or stopped itself with an exit code that
// says it failed, as when GitHub is out of reach at its start, so no failure
// leaves a VM running job code with no service to put it back.
func TestTheServiceIsStartedAgainAfterAFailure(t *testing.T) {
	for _, want := range []string{"& $sc failure " + ServiceName + " reset= 86400 actions= restart/60000/restart/60000/restart/60000", "& $sc failureflag " + ServiceName + " 1"} {
		if !strings.Contains(serviceScript, want) {
			t.Errorf("the service script:\n%s\nlacks %q", serviceScript, want)
		}
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
		limit   time.Duration
		wantErr string
	}{
		"the setup ends": {offAt: 3, limit: time.Minute},
		"it never ends":  {limit: 50 * time.Millisecond, wantErr: "did not power it off within 50ms"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			vm := &fakeJob{offAt: row.offAt}
			sleep := func(ctx context.Context, _ time.Duration) {
				select {
				case <-ctx.Done():
				case <-time.After(5 * time.Millisecond): // stall-accepted: a stand-in sleep that only spaces the polls, so a stall cannot fail the test.
				}
			}

			// Act
			err := waitForFirstBoot(context.Background(), vm, sleep, row.limit)

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

// The App's settings and key go into files the install makes, never into
// one that is there already, which another user could own and so read.
func TestKeepAppRefusesAFileThereAlready(t *testing.T) {
	for _, name := range []string{"app.json", "app-key.pem"} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			keys, _ := serviceFolder(t, goodSettings, "pkcs1")
			dir := t.TempDir()
			options, _ := installOptions(t, dir, &recorder{})
			options.KeyFile = filepath.Join(keys, "app-key.pem")
			if err := os.MkdirAll(options.Dir, 0o700); err != nil {
				t.Fatal(err)
			}
			planted := filepath.Join(options.Dir, name)
			if err := os.WriteFile(planted, []byte("made by another"), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			err := keepApp(options)

			// Assert
			if data, readErr := os.ReadFile(planted); err == nil || readErr != nil || string(data) != "made by another" {
				t.Errorf("keepApp = %v, and %s holds %q; want it refused and the file untouched", err, name, data)
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
		limit     time.Duration
		wantErr   string
	}{
		"it answers":       {answersAt: 3, limit: time.Minute},
		"it never answers": {limit: 50 * time.Millisecond, wantErr: "did not answer within 50ms"},
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
				case <-time.After(5 * time.Millisecond): // stall-accepted: a stand-in sleep that only spaces the asks, so a stall cannot fail the test.
				}
			}

			// Act
			err := ready(context.Background(), ask, sleep, row.limit)

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
			err := DownloadFile(context.Background(), server.Client(), server.URL, path, 100)

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

// A download past its limit is refused, and nothing of it is kept: one that
// says its length first is refused before a byte of it is read, and one that
// does not, once it has sent a byte past the limit.
func TestDownloadFileRefusesMoreThanItsLimit(t *testing.T) {
	for name, isLengthSaid := range map[string]bool{"a length said": true, "a length unsaid": false} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !isLengthSaid {
					w.(http.Flusher).Flush()
					w.Write([]byte("the image!!"))
					return
				}
				// A gibibyte said, and none sent until the download gives up.
				w.Header().Set("Content-Length", "1073741824")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer server.Close()
			client := server.Client()
			client.Timeout = 5 * time.Second
			path := filepath.Join(t.TempDir(), "disk.img")

			// Act
			err := DownloadFile(context.Background(), client, server.URL, path, 10)

			// Assert
			if _, statErr := os.Stat(path); err == nil || !strings.Contains(err.Error(), "more than its 10 bytes") || !os.IsNotExist(statErr) {
				t.Errorf("DownloadFile = %v, the file %v; want it refused and nothing kept", err, statErr)
			}
		})
	}
}

// The install's download step keeps the image only when its SHA-256 is the
// pinned one, and removes any other.
func TestTheImageIsKeptOnlyWithItsPinnedSHA256(t *testing.T) {
	// Arrange
	options, _ := installOptions(t, t.TempDir(), &recorder{})
	options.Download = func(_ context.Context, _, path string) error {
		return os.WriteFile(path, []byte("not Canonical's image"), 0o600)
	}
	step := InstallSteps(options)[2]

	// Act
	err := step.Run(context.Background())

	// Assert
	image := filepath.Join(options.Dir, "download", "disk.img")
	if _, statErr := os.Stat(image); err == nil || !strings.Contains(err.Error(), "not the pinned "+ImageSHA256) || statErr == nil {
		t.Errorf("%s = %v, the image %v; want it refused and removed", step.Title, err, statErr)
	}
}
