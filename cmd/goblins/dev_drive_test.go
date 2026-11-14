package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/gatehome"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// systemGateHome is a gate home on the system drive, where no test's Dev
// Drive folder is.
const systemGateHome = `C:\Users\someone\.no-mistakes`

// machineWith is a machine that can have a Dev Drive, its system drive
// roomy, with the given drives.
func machineWith(volumes ...devdrive.Volume) devdrive.Machine {
	return devdrive.Machine{Build: 26200, Revision: 9457, Defender: true, SystemDrive: "C:", SystemFree: 374 << 30, Volumes: volumes}
}

// runtimeOn is a runtime on machine m that never touches the machine's own
// gate home or board, and whose carried gate home names itself in this
// test's process alone.
func runtimeOn(t *testing.T, m devdrive.Machine) commandRuntime {
	t.Setenv(gatehome.Variable, "")
	runtime := defaultCommandRuntime()
	runtime.readDevDrive = func(context.Context) (devdrive.Machine, error) { return m, nil }
	runtime.gateHome = func() (string, error) { return systemGateHome, nil }
	runtime.carryGateHome = func(_ context.Context, _, devDrive, _ string, _ int, _ time.Duration, _ io.Writer) (string, bool, error) {
		return "moved: the gate's home is " + gatehome.Target(devDrive), true, nil
	}
	runtime.restartBoard = func(home.Home, io.Writer) {}
	return runtime
}

// goblins dev-drive move carries the gate's home onto the folder, waiting the
// half hour for the gate runs in flight, and says where it went; a gate home
// that does not move fails the command once the heavy folders moved, with an
// exit code of its own where the gate only stayed busy, which the supervisor
// reads to carry it again by itself.
func TestDevDriveMoveCarriesTheGateHome(t *testing.T) {
	for _, test := range []struct {
		name     string
		carryErr error
		wantExit int
		want     string
	}{
		{name: "it moves", wantExit: 0, want: "gate home: moved: the gate's home is "},
		{name: "gate runs stay in flight", carryErr: fmt.Errorf("gate home: nothing moved, and the daemon was never stopped: %w", gatehome.ErrGateBusy), wantExit: exitGateBusy, want: "goblins dev-drive move: gate home: nothing moved, and the daemon was never stopped"},
		{name: "the copy fails", carryErr: errors.New("gate home: the copy failed, so nothing moved"), wantExit: 1, want: "goblins dev-drive move: gate home: the copy failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			t.Setenv("GOBLINS_HOME", t.TempDir())
			t.Setenv("GOBLINS_STATE_OVERRIDE", "")
			folder := filepath.Join(t.TempDir(), "CodeGoblins")
			runtime := runtimeOn(t, machineWith(devdrive.Volume{Root: filepath.VolumeName(folder) + `\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted}))
			var carried []string
			var tries int
			var lock string
			runtime.carryGateHome = func(_ context.Context, old, devDrive, gotLock string, gotTries int, _ time.Duration, _ io.Writer) (string, bool, error) {
				carried, tries, lock = append(carried, old+" -> "+devDrive), gotTries, gotLock
				return "moved: the gate's home is " + gatehome.Target(devDrive), test.carryErr == nil, test.carryErr
			}

			// Act
			var stdout, stderr bytes.Buffer
			exit := runWithRuntime([]string{"dev-drive", "move", "--to", folder}, &stdout, &stderr, runtime)

			// Assert
			if exit != test.wantExit || !strings.Contains(stdout.String()+stderr.String(), test.want) {
				t.Errorf("exit %d, stdout %q, stderr %q; want exit %d and %q", exit, stdout.String(), stderr.String(), test.wantExit, test.want)
			}
			if len(carried) != 1 || carried[0] != systemGateHome+" -> "+folder || tries != gateHomeTries {
				t.Errorf("carried %q with %d tries, want %s into %s with %d", carried, tries, systemGateHome, folder, gateHomeTries)
			}
			if want := filepath.Join(os.Getenv("GOBLINS_HOME"), "state", "gate-home-move.lock"); lock != want {
				t.Errorf("the move's lock = %s, want %s in the home's state", lock, want)
			}
			if h, err := home.Resolve(); err != nil || h.DevDrive != folder {
				t.Errorf("home = %+v, %v; want the heavy folders moved all the same", h, err)
			}
		})
	}
}

// goblins doctor and goblins dev-drive say where the gate's home is, and warn when the
// heavy folders are on a Dev Drive while it is not.
func TestDoctorAndDevDriveSayWhereTheGateHomeIs(t *testing.T) {
	for _, test := range []struct {
		name, gateHome string
		isMoved        bool
		want           string
	}{
		{name: "no Dev Drive", gateHome: systemGateHome, want: "gate home: " + systemGateHome + ", where no-mistakes keeps it, as the fleet has no Dev Drive"},
		{name: "on the system drive", gateHome: systemGateHome, isMoved: true, want: "gate home: WARNING: " + systemGateHome + " is not on the Dev Drive"},
		{name: "on the Dev Drive", isMoved: true, want: ", on the Dev Drive, named by NM_HOME"},
	} {
		for _, command := range []string{"doctor", "dev-drive"} {
			t.Run(test.name+"/"+command, func(t *testing.T) {
				// Arrange
				root := t.TempDir()
				t.Setenv("GOBLINS_HOME", root)
				t.Setenv("GOBLINS_STATE_OVERRIDE", "")
				folder := filepath.Join(t.TempDir(), "CodeGoblins")
				if test.isMoved {
					if err := home.WriteDevDriveConfig(root, home.DevDriveConfig{Root: folder}); err != nil {
						t.Fatal(err)
					}
				}
				gateHome := test.gateHome
				if gateHome == "" {
					gateHome = gatehome.Target(folder)
				}
				runtime := runtimeOn(t, machineWith(devdrive.Volume{Root: filepath.VolumeName(folder) + `\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted}))
				runtime.gateHome = func() (string, error) { return gateHome, nil }

				// Act
				var stdout, stderr bytes.Buffer
				runWithRuntime([]string{command}, &stdout, &stderr, runtime)

				// Assert
				if !strings.Contains(stdout.String(), test.want) {
					t.Errorf("stdout lacks %q\n%s", test.want, stdout.String())
				}
			})
		}
	}
}

// goblins install puts the gate's home on the home's Dev Drive without waiting
// for a gate run in flight, and leaves it alone in a home without one.
func TestInstallPlacesTheGateHomeOnTheHomesDevDrive(t *testing.T) {
	for _, test := range []struct {
		name      string
		isMoved   bool
		carryErr  error
		wantCarry bool
		want      string
	}{
		{name: "a home without a Dev Drive"},
		{name: "a home on a Dev Drive", isMoved: true, wantCarry: true, want: "gate home: moved"},
		{name: "a gate run in flight", isMoved: true, carryErr: errors.New("gate runs stay in flight"), wantCarry: true, want: "The gate's home did not move to the Dev Drive (gate runs stay in flight)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			folder := filepath.Join(t.TempDir(), "CodeGoblins")
			if test.isMoved {
				if err := home.WriteDevDriveConfig(root, home.DevDriveConfig{Root: folder}); err != nil {
					t.Fatal(err)
				}
			}
			runtime := runtimeOn(t, machineWith())
			var tries []int
			runtime.carryGateHome = func(_ context.Context, _, devDrive, lock string, gotTries int, _ time.Duration, _ io.Writer) (string, bool, error) {
				tries = append(tries, gotTries)
				if want := filepath.Join(root, "state", "gate-home-move.lock"); lock != want {
					t.Errorf("the move's lock = %s, want %s in the home's state", lock, want)
				}
				if devDrive != folder {
					t.Errorf("carried into %s, want %s", devDrive, folder)
				}
				return "moved", test.carryErr == nil, test.carryErr
			}

			// Act
			var stdout bytes.Buffer
			placeGateHome(home.Home{Root: root, State: filepath.Join(root, "state")}, &stdout, runtime)

			// Assert
			if test.wantCarry != (len(tries) == 1) || test.wantCarry && tries[0] != 1 {
				t.Errorf("carries = %v, want one with one try %v", tries, test.wantCarry)
			}
			if !strings.Contains(stdout.String(), test.want) {
				t.Errorf("stdout = %q, want %q", stdout.String(), test.want)
			}
			if isPlaced := test.wantCarry && test.carryErr == nil; isPlaced != (os.Getenv(gatehome.Variable) == gatehome.Target(folder)) {
				t.Errorf("this process's NM_HOME = %q, want it to name %s %v, for the board install restarts", os.Getenv(gatehome.Variable), gatehome.Target(folder), isPlaced)
			}
		})
	}
}

// goblins dev-drive move restarts the board serving the home once, after the
// gate's home is carried and named by NM_HOME in its own process, so the
// board it starts again reads the moved gate home: when the heavy folders
// move now, whatever the gate's home did, or when the gate's home moved or
// was placed by this move. A gate home already on the Dev Drive is no reason.
func TestDevDriveMoveRestartsTheBoardAfterTheGateHomeMoves(t *testing.T) {
	for _, test := range []struct {
		name               string
		isHeavyMoved       bool
		isCarried          bool
		carryErr           error
		wantRestart        bool
		wantRestartGateEnv bool
	}{
		{name: "heavy folders and the gate's home move now", isCarried: true, wantRestart: true, wantRestartGateEnv: true},
		{name: "heavy folders move now, the gate's home stays for gate runs in flight", carryErr: errors.New("gate runs stay in flight"), wantRestart: true},
		{name: "heavy folders already there, the gate's home moves", isHeavyMoved: true, isCarried: true, wantRestart: true, wantRestartGateEnv: true},
		{name: "heavy folders and the gate's home already there", isHeavyMoved: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			t.Setenv("GOBLINS_HOME", root)
			t.Setenv("GOBLINS_STATE_OVERRIDE", "")
			folder := filepath.Join(t.TempDir(), "CodeGoblins")
			runtime := runtimeOn(t, machineWith(devdrive.Volume{Root: filepath.VolumeName(folder) + `\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted}))
			if test.isHeavyMoved {
				var stdout, stderr bytes.Buffer
				if exit := runWithRuntime([]string{"dev-drive", "move", "--to", folder}, &stdout, &stderr, runtime); exit != 0 {
					t.Fatalf("the first move exited %d: %s", exit, stderr.String())
				}
				t.Setenv(gatehome.Variable, "")
			}
			var steps []string
			runtime.carryGateHome = func(context.Context, string, string, string, int, time.Duration, io.Writer) (string, bool, error) {
				steps = append(steps, "carry")
				return "moved", test.isCarried, test.carryErr
			}
			runtime.restartBoard = func(h home.Home, _ io.Writer) {
				steps = append(steps, "restart "+h.Root+" with NM_HOME "+os.Getenv(gatehome.Variable))
			}

			// Act
			var stdout, stderr bytes.Buffer
			runWithRuntime([]string{"dev-drive", "move", "--to", folder}, &stdout, &stderr, runtime)

			// Assert
			want := []string{"carry"}
			if test.wantRestart {
				gateEnv := ""
				if test.wantRestartGateEnv {
					gateEnv = gatehome.Target(folder)
				}
				want = append(want, "restart "+root+" with NM_HOME "+gateEnv)
			}
			if !slices.Equal(steps, want) {
				t.Errorf("steps = %q, want %q", steps, want)
			}
		})
	}
}

// goblins doctor says what the home's Dev Drive is, in one line: a machine that
// cannot have one is told why and that nothing changes, one that can is told
// it is optional and no Defender exclusion.
func TestRunDoctorSaysWhatTheDevDriveIs(t *testing.T) {
	for name, test := range map[string]struct {
		m    devdrive.Machine
		says string
	}{
		"Windows 10":         {devdrive.Machine{Build: 19045, Revision: 5000, Defender: true, SystemDrive: "C:", SystemFree: 374 << 30}, "dev drive: not available here: this Windows (build 19045.5000) is older than Windows 11 build 22621.2338"},
		"a machine that can": {machineWith(), "dev drive: absent: this machine can have one"},
		"an untrusted one":   {machineWith(devdrive.Volume{Root: `E:\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Untrusted}), "dev drive: present, not used yet: E: is a Dev Drive Windows does not trust"},
		"one in a file": {machineWith(devdrive.Volume{Root: `E:\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted, Image: devdrive.Image{Path: `C:\DevDrives\CodeGoblins.vhdx`, Unread: "only a process run as administrator may read it"}}),
			`Command Center and takes the rest by itself (file: C:\DevDrives\CodeGoblins.vhdx holds space on C: that stays taken when goblins free space on E:, and how much could not be read: only a process run as administrator may read it)` + "\n"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			t.Setenv("GOBLINS_HOME", t.TempDir())
			t.Setenv("GOBLINS_STATE_OVERRIDE", "")

			// Act
			var stdout, stderr bytes.Buffer
			runWithRuntime([]string{"doctor"}, &stdout, &stderr, runtimeOn(t, test.m))

			// Assert
			if !strings.Contains(stdout.String(), test.says) {
				t.Errorf("stdout lacks %q\n%s", test.says, stdout.String())
			}
		})
	}
}

// goblins dev-drive move points a home at a folder on a trusted Dev Drive, after
// which the home's new worktrees, scratch and caches are there, and goblins
// dev-drive says so.
func TestDevDriveMovePutsNewWorkOnTheDrive(t *testing.T) {
	// Arrange
	root := t.TempDir()
	t.Setenv("GOBLINS_HOME", root)
	t.Setenv("GOBLINS_STATE_OVERRIDE", "")
	folder := filepath.Join(t.TempDir(), "CodeGoblins")
	runtime := runtimeOn(t, machineWith(devdrive.Volume{Root: filepath.VolumeName(folder) + `\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted}))

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"dev-drive", "move", "--to", folder}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 || !strings.Contains(stdout.String(), "moved: new goblins' worktrees, scratch and package caches go to "+folder) {
		t.Fatalf("exit %d, stdout %q, stderr %q", exit, stdout.String(), stderr.String())
	}
	h, err := home.Resolve()
	if err != nil || h.Worktrees() != filepath.Join(folder, "worktrees") || h.Scratch() != filepath.Join(folder, "scratch") || h.Caches() != filepath.Join(folder, "caches") {
		t.Errorf("home = %+v, %v; want its heavy folders in %s", h, err, folder)
	}
	stdout.Reset()
	if exit := runWithRuntime([]string{"dev-drive"}, &stdout, &stderr, runtime); exit != 0 || !strings.Contains(stdout.String(), "dev drive: on: the home's worktrees, scratch and caches are on "+folder) {
		t.Errorf("goblins dev-drive = %d, %q; want it on", exit, stdout.String())
	}
}

// goblins dev-drive names the file a Dev Drive lives in and how much of the
// system drive it holds, on a line of its own under the dev drive line.
func TestDevDriveSaysHowMuchOfTheSystemDriveItsFileHolds(t *testing.T) {
	// Arrange
	t.Setenv("GOBLINS_HOME", t.TempDir())
	t.Setenv("GOBLINS_STATE_OVERRIDE", "")
	runtime := runtimeOn(t, machineWith(devdrive.Volume{Root: `E:\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted, Image: devdrive.Image{Path: `C:\DevDrives\CodeGoblins.vhdx`, Size: 142 << 30 / 10}}))

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"dev-drive"}, &stdout, &stderr, runtime)

	// Assert
	lines := strings.Split(stdout.String(), "\n")
	if exit != 0 || len(lines) < 2 || !strings.HasPrefix(lines[0], "dev drive: present, not used yet: E:") || lines[1] != `file: C:\DevDrives\CodeGoblins.vhdx holds 14.2 GB of C:, and stays that size when goblins free space on E:` {
		t.Errorf("goblins dev-drive = %d, %q, %q; want the file line under the dev drive line", exit, stdout.String(), stderr.String())
	}
}

// goblins doctor and goblins dev-drive end a missing drive's line with who
// attaches it: the board, through its startup task and then Attach, for a
// drive Code Goblins made, whose file the home's config\dev-drive.json
// names, and the person, by hand, for one it did not. Neither names an item.
func TestDoctorAndDevDriveSayWhoAttachesAMissingDrive(t *testing.T) {
	for _, test := range []struct {
		name   string
		isMade bool
		how    string
	}{
		{name: "made by Code Goblins", isMade: true, how: devdrive.AttachByBoard},
		{name: "one the person had", how: devdrive.AttachByHand},
	} {
		for _, command := range []string{"doctor", "dev-drive"} {
			t.Run(test.name+"/"+command, func(t *testing.T) {
				// Arrange
				root := t.TempDir()
				t.Setenv("GOBLINS_HOME", root)
				t.Setenv("GOBLINS_STATE_OVERRIDE", "")
				config := home.DevDriveConfig{Root: `Q:\CodeGoblins`}
				if test.isMade {
					config.VHD = filepath.Join(t.TempDir(), "CodeGoblins.vhdx")
				}
				if err := home.WriteDevDriveConfig(root, config); err != nil {
					t.Fatal(err)
				}

				// Act
				var stdout, stderr bytes.Buffer
				runWithRuntime([]string{command}, &stdout, &stderr, runtimeOn(t, machineWith()))

				// Assert
				var line string
				for _, said := range strings.Split(strings.ReplaceAll(stdout.String(), "\r\n", "\n"), "\n") {
					if strings.HasPrefix(said, "dev drive: ") {
						line = said
					}
				}
				if !strings.Contains(line, `Q: is not there`) || !strings.HasSuffix(line, test.how) || strings.Contains(line, "item") {
					t.Errorf("dev drive line = %q, want it to end %q and name no item\n%s", line, test.how, stdout.String())
				}
			})
		}
	}
}

// goblins dev-drive says what the drive's startup task did since the boot,
// from the result it writes beside the drive's file, which is where the
// person reads whether the last restart attached the drive by itself.
func TestDevDriveSaysWhatTheStartupTaskDid(t *testing.T) {
	// Arrange
	root := t.TempDir()
	t.Setenv("GOBLINS_HOME", root)
	t.Setenv("GOBLINS_STATE_OVERRIDE", "")
	vhd := filepath.Join(t.TempDir(), "CodeGoblins.vhdx")
	if err := home.WriteDevDriveConfig(root, home.DevDriveConfig{Root: `E:\CodeGoblins`, VHD: vhd}); err != nil {
		t.Fatal(err)
	}
	runtime := runtimeOn(t, machineWith(devdrive.Volume{Root: `E:\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted}))
	finished := time.Now().UTC().Truncate(time.Second)
	result := `{"started": "` + finished.Add(-time.Second).Format(time.RFC3339) + `", "finished": "` + finished.Format(time.RFC3339) + `", "attached": true, "letter": "E:", "tries": 2, "errors": ["try 1: The device is not ready."]}`
	if err := os.WriteFile(devdrive.AttachResultPath(vhd), []byte(result), 0o600); err != nil {
		t.Fatal(err)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"dev-drive"}, &stdout, &stderr, runtime)

	// Assert
	want := "it attached E: at " + finished.Format(time.RFC3339) + ", on try 2 of 12"
	if exit != 0 || !strings.Contains(stdout.String(), "\nstartup task: since this boot at ") || !strings.Contains(stdout.String(), want) {
		t.Errorf("goblins dev-drive = %d, %q, %q; want the startup task line saying %q", exit, stdout.String(), stderr.String(), want)
	}
}

// A refused move changes nothing and says why.
func TestDevDriveMoveRefusesADriveThatIsNoDevDrive(t *testing.T) {
	root := t.TempDir()
	t.Setenv("GOBLINS_HOME", root)
	t.Setenv("GOBLINS_STATE_OVERRIDE", "")
	folder := filepath.Join(t.TempDir(), "CodeGoblins")
	runtime := runtimeOn(t, machineWith(devdrive.Volume{Root: filepath.VolumeName(folder) + `\`, FileSystem: "NTFS"}))

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"dev-drive", "move", "--to", folder}, &stdout, &stderr, runtime)

	if exit != 1 || !strings.Contains(stderr.String(), "is not a Dev Drive") {
		t.Errorf("exit %d, stderr %q; want a refusal naming a drive that is no Dev Drive", exit, stderr.String())
	}
	if h, err := home.Resolve(); err != nil || h.DevDrive != "" {
		t.Errorf("home = %+v, %v; want nothing moved", h, err)
	}
}

// goblins dev-drive setup asks for the next step from a terminal, as Set up on the
// board does: the home records the answer, and nothing moves.
func TestDevDriveSetupAsksForTheNextStep(t *testing.T) {
	// Arrange
	root := t.TempDir()
	t.Setenv("GOBLINS_HOME", root)
	t.Setenv("GOBLINS_STATE_OVERRIDE", "")

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"dev-drive", "setup"}, &stdout, &stderr, runtimeOn(t, machineWith()))

	// Assert
	config, err := home.ReadDevDriveConfig(root)
	if exit != 0 || err != nil || config.Choice != home.DevDriveWanted || config.AskedAt.IsZero() || config.Root != "" || !strings.Contains(stdout.String(), "Command Center") {
		t.Errorf("exit %d, stdout %q, stderr %q, config %+v, %v; want the answer kept and the Command Center named", exit, stdout.String(), stderr.String(), config, err)
	}
}
