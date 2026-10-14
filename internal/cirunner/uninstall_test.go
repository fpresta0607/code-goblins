package cirunner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// uninstallOptions are options for an uninstall from dir, with commands and
// a runner deletion that returns deleteErr.
func uninstallOptions(dir string, commands *recorder, deleteErr error, deleted *[]string) UninstallOptions {
	return UninstallOptions{
		Dir:      dir,
		Commands: commands,
		DeleteOwnRunners: func(_ context.Context, dir string) error {
			*deleted = append(*deleted, dir)
			return deleteErr
		},
	}
}

// The uninstall removes the service first, then its runners while its key is
// here, then the VM, then the folder with the key.
func TestUninstallStepsInTheirOrder(t *testing.T) {
	// Arrange
	var deleted []string
	steps := UninstallSteps(uninstallOptions(t.TempDir(), &recorder{}, nil, &deleted))

	// Act
	var titles []string
	for _, step := range steps {
		titles = append(titles, step.Title)
	}

	// Assert
	want := []string{
		"remove the service and its account from Hyper-V Administrators",
		"delete the service's runners at GitHub",
		"remove the VM and its checkpoint",
		"remove the service's folder, with the App's key and the VM's disk",
	}
	if !slices.Equal(titles, want) {
		t.Errorf("steps %q; want %q", titles, want)
	}
}

// A dry run of the uninstall says each script and removes nothing.
func TestUninstallDryRunChangesNothing(t *testing.T) {
	// Arrange
	commands := &recorder{}
	var deleted []string
	steps := UninstallSteps(uninstallOptions(`C:\ProgramData\Code Goblins\ci-runner`, commands, nil, &deleted))
	var out bytes.Buffer

	// Act
	err := RunSteps(context.Background(), steps, true, &out)

	// Assert
	for _, script := range []string{removeServiceScript, removeVMScript, removeFolderScript} {
		for _, line := range strings.Split(script, "\n") {
			if !strings.Contains(out.String(), "    "+line+"\n") {
				t.Errorf("the dry run did not say %q", line)
			}
		}
	}
	if err != nil || len(commands.ran) != 0 || len(deleted) != 0 {
		t.Errorf("dry run = %v, ran %d, deleted %q; want nothing done", err, len(commands.ran), deleted)
	}
}

// The uninstall runs each script with its folder in the environment, and a
// folder already gone means no runners to delete, while any other failure to
// delete them stops the uninstall before the key goes.
func TestUninstallDeletesTheRunnersWhileTheKeyIsHere(t *testing.T) {
	for name, row := range map[string]struct {
		deleteErr error
		wantRan   int
		wantErr   bool
	}{
		"deleted":         {wantRan: 3},
		"the folder gone": {deleteErr: fmt.Errorf("read: %w", fs.ErrNotExist), wantRan: 3},
		"GitHub refuses":  {deleteErr: errors.New("GitHub answered HTTP 401"), wantRan: 1, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			commands := &recorder{}
			var deleted []string
			dir := `C:\ProgramData\Code Goblins\ci-runner`
			steps := UninstallSteps(uninstallOptions(dir, commands, row.deleteErr, &deleted))

			// Act
			err := RunSteps(context.Background(), steps, false, &bytes.Buffer{})

			// Assert
			if (err != nil) != row.wantErr || len(commands.ran) != row.wantRan || !slices.Equal(deleted, []string{dir}) {
				t.Errorf("uninstall = %v, ran %d script(s), deleted in %q; want error %v and %d script(s)", err, len(commands.ran), deleted, row.wantErr, row.wantRan)
			}
			for _, request := range commands.ran {
				if strings.Contains(script(request), dir) {
					t.Errorf("the script %q holds the folder; want it in the environment", script(request))
				}
			}
			if row.wantRan == 3 && !slices.Contains(commands.ran[2].Env, "CG_CI_DIR="+dir) {
				t.Errorf("the folder's removal does not name the folder in its environment")
			}
		})
	}
}

// Runners are deleted with the folder's App, and a folder already gone is
// fs.ErrNotExist.
func TestDeleteOwnRunnersInAFolderGone(t *testing.T) {
	// Act
	err := DeleteOwnRunnersIn(context.Background(), filepath.Join(t.TempDir(), "gone"))

	// Assert
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("DeleteOwnRunnersIn = %v; want fs.ErrNotExist", err)
	}
}
