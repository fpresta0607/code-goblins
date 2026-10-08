package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/devdrive"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// machineWith is a machine that can have a Dev Drive, its system drive
// roomy, with the given drives.
func machineWith(volumes ...devdrive.Volume) devdrive.Machine {
	return devdrive.Machine{Build: 26200, Revision: 9457, Defender: true, SystemDrive: "C:", SystemFree: 374 << 30, Volumes: volumes}
}

func runtimeOn(m devdrive.Machine) commandRuntime {
	runtime := defaultCommandRuntime()
	runtime.readDevDrive = func(context.Context) (devdrive.Machine, error) { return m, nil }
	return runtime
}

// cfo doctor says what the home's Dev Drive is, in one line: a machine that
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
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			t.Setenv("CFO_HOME", t.TempDir())
			t.Setenv("CFO_STATE_OVERRIDE", "")

			// Act
			var stdout, stderr bytes.Buffer
			runWithRuntime([]string{"doctor"}, &stdout, &stderr, runtimeOn(test.m))

			// Assert
			if !strings.Contains(stdout.String(), test.says) {
				t.Errorf("stdout lacks %q\n%s", test.says, stdout.String())
			}
		})
	}
}

// cfo dev-drive move points a home at a folder on a trusted Dev Drive, after
// which the home's new worktrees, scratch and caches are there, and cfo
// dev-drive says so.
func TestDevDriveMovePutsNewWorkOnTheDrive(t *testing.T) {
	// Arrange
	root := t.TempDir()
	t.Setenv("CFO_HOME", root)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	folder := filepath.Join(t.TempDir(), "CodeGoblins")
	runtime := runtimeOn(machineWith(devdrive.Volume{Root: filepath.VolumeName(folder) + `\`, FileSystem: "ReFS", Dev: true, Trust: devdrive.Trusted}))

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
		t.Errorf("cfo dev-drive = %d, %q; want it on", exit, stdout.String())
	}
}

// A refused move changes nothing and says why.
func TestDevDriveMoveRefusesADriveThatIsNoDevDrive(t *testing.T) {
	root := t.TempDir()
	t.Setenv("CFO_HOME", root)
	t.Setenv("CFO_STATE_OVERRIDE", "")
	folder := filepath.Join(t.TempDir(), "CodeGoblins")
	runtime := runtimeOn(machineWith(devdrive.Volume{Root: filepath.VolumeName(folder) + `\`, FileSystem: "NTFS"}))

	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"dev-drive", "move", "--to", folder}, &stdout, &stderr, runtime)

	if exit != 1 || !strings.Contains(stderr.String(), "is not a Dev Drive") {
		t.Errorf("exit %d, stderr %q; want a refusal naming a drive that is no Dev Drive", exit, stderr.String())
	}
	if h, err := home.Resolve(); err != nil || h.DevDrive != "" {
		t.Errorf("home = %+v, %v; want nothing moved", h, err)
	}
}

// cfo dev-drive setup asks for the next step from a terminal, as Set up on the
// board does: the home records the answer, and nothing moves.
func TestDevDriveSetupAsksForTheNextStep(t *testing.T) {
	// Arrange
	root := t.TempDir()
	t.Setenv("CFO_HOME", root)
	t.Setenv("CFO_STATE_OVERRIDE", "")

	// Act
	var stdout, stderr bytes.Buffer
	exit := runWithRuntime([]string{"dev-drive", "setup"}, &stdout, &stderr, runtimeOn(machineWith()))

	// Assert
	config, err := home.ReadDevDriveConfig(root)
	if exit != 0 || err != nil || config.Choice != home.DevDriveWanted || config.AskedAt.IsZero() || config.Root != "" || !strings.Contains(stdout.String(), "Command Center") {
		t.Errorf("exit %d, stdout %q, stderr %q, config %+v, %v; want the answer kept and the Command Center named", exit, stdout.String(), stderr.String(), config, err)
	}
}
