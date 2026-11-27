package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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
