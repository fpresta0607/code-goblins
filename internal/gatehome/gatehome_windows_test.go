package gatehome

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/installbase"
)

func TestRootTakesTheProcessThenTheUserThenTheDefaultHome(t *testing.T) {
	profile := t.TempDir()
	for _, test := range []struct {
		name, process, user, want string
	}{
		{name: "the process names it", process: `D:\process\no-mistakes`, user: `D:\user\no-mistakes`, want: `D:\process\no-mistakes`},
		{name: "a process started before the move", user: `D:\user\no-mistakes`, want: `D:\user\no-mistakes`},
		{name: "nothing names it", want: filepath.Join(profile, ".no-mistakes")},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			t.Setenv("USERPROFILE", profile)
			t.Setenv(Variable, test.process)
			t.Setenv(installbase.UserEnvFileVariable, filepath.Join(t.TempDir(), "user-env.json"))
			if test.user != "" {
				if err := installbase.NewEnvStore(nil).Set(Variable, test.user); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			got, err := Root()

			// Assert
			if err != nil || got != test.want {
				t.Errorf("Root() = %q, %v, want %q", got, err, test.want)
			}
		})
	}
}

func TestReadSaysWhereTheGateHomeIsForEachPlacement(t *testing.T) {
	scratch := t.TempDir()
	devDrive := filepath.Join(scratch, "dev")
	system := filepath.Join(scratch, "profile", ".no-mistakes")
	linked := filepath.Join(scratch, "linked", ".no-mistakes")
	for _, folder := range []string{filepath.Join(devDrive, "no-mistakes", "repos"), system, linked} {
		if err := os.MkdirAll(folder, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	junction(t, filepath.Join(linked, "repos"), filepath.Join(devDrive, "no-mistakes", "repos"))
	for _, test := range []struct {
		name, root, devDrive string
		want                 Place
		line                 string
	}{
		{name: "no Dev Drive", root: system, want: PlaceNoDevDrive, line: "as the fleet has no Dev Drive"},
		{name: "on the Dev Drive", root: filepath.Join(devDrive, "no-mistakes"), devDrive: devDrive, want: PlaceDevDrive, line: "on the Dev Drive, named by NM_HOME"},
		{name: "on the system drive", root: system, devDrive: devDrive, want: PlaceSystem, line: "WARNING: " + system + " is not on the Dev Drive"},
		{name: "linked into the Dev Drive", root: linked, devDrive: devDrive, want: PlaceLinked, line: "through junctions (" + filepath.Join(linked, "repos") + ")"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Act
			placement := Read(test.root, test.devDrive)

			// Assert
			if placement.Place != test.want {
				t.Errorf("place = %s, want %s", placement.Place, test.want)
			}
			if line := placement.Line(); !strings.Contains(line, test.line) {
				t.Errorf("line = %q, want it to say %q", line, test.line)
			}
			if test.want == PlaceSystem || test.want == PlaceLinked {
				if move := "cfo dev-drive move --to " + devDrive; !strings.Contains(placement.Line(), move) {
					t.Errorf("line = %q, want it to name %q", placement.Line(), move)
				}
			}
		})
	}
}
