package supervisor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// absentDrive is a drive letter this machine has no drive under, so a folder
// on it cannot be read.
func absentDrive(t *testing.T) string {
	t.Helper()
	for letter := 'Z'; letter >= 'E'; letter-- {
		drive := string(letter) + ":"
		if _, err := os.Stat(drive + `\`); err != nil {
			return drive
		}
	}
	t.Skip("every drive letter from E: to Z: is in use")
	return ""
}

// Every start, wake and AFK decision reads the least free of the home's drive
// and the Dev Drive, exactly as before the meter showed each drive: the gate
// home's drive is shown, never decided on, so an unreadable gate home neither
// stops a start nor reads as room.
func TestMachineDiskDecidesOnTheHomeAndDevDriveAndShowsTheGateHome(t *testing.T) {
	// Arrange
	root := t.TempDir()
	devDrive := filepath.Join(t.TempDir(), "CodeGoblins")
	if err := os.MkdirAll(devDrive, 0o755); err != nil {
		t.Fatal(err)
	}
	gate := absentDrive(t) + `\no-mistakes`
	t.Setenv("NM_HOME", gate)
	least, err := disk.ReadLeast(root, devDrive)
	if err != nil {
		t.Fatal(err)
	}

	// Act
	reading, err := MachineDisk(home.Home{Root: root, DevDrive: devDrive})

	// Assert
	if err != nil || reading.Drive != least.Drive || reading.Total != least.Total {
		t.Errorf("MachineDisk = %+v, %v; want the decision on %+v, the least free of the home and the Dev Drive", reading.Reading, err, least)
	}
	if len(reading.Drives) != 2 || reading.Drives[0].Drive != filepath.VolumeName(root) || !reading.Drives[0].IsDevDrive || reading.Drives[0].Error != "" {
		t.Errorf("drives = %+v, want the home and the Dev Drive's one shared drive, named the Dev Drive, first", reading.Drives)
	}
	if gateDrive := reading.Drives[len(reading.Drives)-1]; gateDrive.Drive != filepath.VolumeName(gate) || gateDrive.Error == "" || gateDrive.Free != 0 {
		t.Errorf("gate home's drive = %+v, want an error line with no free space", gateDrive)
	}
}

// A Dev Drive that cannot be read stops every start as before, and the board
// still has its line to show as an error.
func TestMachineDiskKeepsAnUnreadableDevDrivesLine(t *testing.T) {
	// Arrange
	root := t.TempDir()
	t.Setenv("NM_HOME", filepath.Join(root, "gate"))
	detached := absentDrive(t) + `\CodeGoblins`

	// Act
	reading, err := MachineDisk(home.Home{Root: root, DevDrive: detached})

	// Assert
	if err == nil {
		t.Fatal("an unreadable Dev Drive gave a reading, so a start would land on a drive that is not there")
	}
	want := []string{filepath.VolumeName(root), filepath.VolumeName(detached)}
	if len(reading.Drives) != 2 || reading.Drives[0].Drive != want[0] || reading.Drives[0].Error != "" || reading.Drives[1].Drive != want[1] || !reading.Drives[1].IsDevDrive || reading.Drives[1].Error == "" || reading.Floor == 0 {
		t.Errorf("drives = %+v (floor %d), want %v with the Dev Drive an error line beside the floor", reading.Drives, reading.Floor, want)
	}
}

// A home or Dev Drive folder that cannot be read stops every start, so its
// drive is an error line even when other folders on that drive, the gate home
// among them, read: the board shows why nothing starts.
func TestMachineDiskShowsAnUnreadableHomeOrDevDriveFolderOnASharedDriveAsAnErrorLine(t *testing.T) {
	cases := []struct {
		name     string
		root     string
		devDrive string
	}{
		{name: "a missing Dev Drive folder", root: "home", devDrive: `missing\CodeGoblins`},
		{name: "a missing home", root: `missing\home`, devDrive: "CodeGoblins"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			// Arrange: the home, the Dev Drive and the gate home share one drive,
			// and only the folders under missing are not there.
			base := t.TempDir()
			t.Setenv("NM_HOME", base)
			for _, folder := range []string{"home", "CodeGoblins"} {
				if err := os.MkdirAll(filepath.Join(base, folder), 0o755); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			reading, err := MachineDisk(home.Home{Root: filepath.Join(base, test.root), DevDrive: filepath.Join(base, test.devDrive)})

			// Assert
			if err == nil {
				t.Fatal("a folder that cannot be read gave a reading, so a start would land on a folder that is not there")
			}
			if len(reading.Drives) != 1 || reading.Drives[0].Drive != filepath.VolumeName(base) || !reading.Drives[0].IsDevDrive || reading.Drives[0].Error == "" || reading.Drives[0].Free != 0 {
				t.Errorf("drives = %+v, want the one shared drive, named the Dev Drive, as an error line with no free space", reading.Drives)
			}
		})
	}
}
