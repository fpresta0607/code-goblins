//go:build windows

package install

import (
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codegoblins "github.com/fpresta0607/code-goblins"
	"golang.org/x/sys/windows/registry"
)

// ownStartAtLogin gives the fixture Start at login entries of its own, under
// a key of this test's, never the user's, and returns that key.
func ownStartAtLogin(t *testing.T, f *fixture) registry.Key {
	t.Helper()
	f.service.StartAtLoginKey = `Software\CodeGoblinsTest\` + rand.Text()
	key, _, err := registry.CreateKey(registry.CURRENT_USER, f.service.StartAtLoginKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		key.Close()
		_ = registry.DeleteKey(registry.CURRENT_USER, f.service.StartAtLoginKey)
		// Removed only once no other run's key is under it.
		_ = registry.DeleteKey(registry.CURRENT_USER, `Software\CodeGoblinsTest`)
	})
	return key
}

// An uninstall removes the desktop window's Start at login entry where it
// starts a program in the home being uninstalled, so that home does not start
// again at the next login, and leaves another home's entry as it is.
func TestUninstallRemovesOnlyThisHomesStartAtLoginEntry(t *testing.T) {
	for name, test := range map[string]struct {
		command func(root string) string
		removed bool
	}{
		"started by this home's goblins": {func(root string) string {
			return `"` + filepath.Join(root, "goblins.exe") + `" --window --background`
		}, true},
		"this home's window on its own": {func(root string) string {
			return `"` + filepath.Join(root, "goblins-window.exe") + `" --board http://127.0.0.1:4310 --state "` + filepath.Join(root, "state") + `" --background`
		}, true},
		"another home's goblins": {func(string) string {
			return `"C:\elsewhere\CodeGoblins\goblins.exe" --window --background`
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := newFixture(t, adopterSettings, nil)
			key := ownStartAtLogin(t, f)
			command := test.command(f.root)
			if err := key.SetStringValue(startAtLoginValue, command); err != nil {
				t.Fatal(err)
			}
			f.install()

			// Act
			output := f.uninstall()

			// Assert
			_, _, err := key.GetStringValue(startAtLoginValue)
			if removed := errors.Is(err, registry.ErrNotExist); removed != test.removed {
				t.Errorf("the entry %s: removed %v (%v), want removed %v", command, removed, err, test.removed)
			}
			if reported := strings.Contains(output, "removed the desktop window's entry, which ran "+command); reported != test.removed {
				t.Errorf("the uninstall reports the entry removed: %v, want %v:\n%s", reported, test.removed, output)
			}
		})
	}
}

// A machine where nothing ever started at login, or where the window never
// asked to, is left as it is, with nothing said.
func TestUninstallWithNoStartAtLoginEntryChangesNothing(t *testing.T) {
	f := newFixture(t, adopterSettings, nil)
	f.service.StartAtLoginKey = `Software\CodeGoblinsTest\` + rand.Text()
	f.install()

	output := f.uninstall()

	if strings.Contains(output, "start at login") {
		t.Errorf("the uninstall speaks of a Start at login entry that was never there:\n%s", output)
	}
	if key, err := registry.OpenKey(registry.CURRENT_USER, f.service.StartAtLoginKey, registry.QUERY_VALUE); err == nil {
		key.Close()
		t.Errorf("the uninstall made the key %s", f.service.StartAtLoginKey)
	}
}

// An install that takes an earlier window's place makes Start at login start
// this home where it started that window, as the window itself writes the
// entry when this home's goblins starts it. An entry that starts anything
// else is left as it is, and none is made where there was none.
func TestInstallMakesStartAtLoginStartThisHomeInTheEarlierWindowsPlace(t *testing.T) {
	for name, test := range map[string]struct {
		command func(earlier string) string
		adopted bool
	}{
		"the earlier window": {func(earlier string) string {
			return `"` + filepath.Join(earlier, "goblins-window.exe") + `" --board http://127.0.0.1:4310 --state "C:\home\state" --background`
		}, true},
		"a window in a folder beside the earlier one": {func(earlier string) string {
			return `"` + filepath.Join(earlier+"Other", "goblins-window.exe") + `" --board http://127.0.0.1:4310 --state "C:\home\state" --background`
		}, false},
		"another home's goblins": {func(string) string {
			return `"C:\elsewhere\CodeGoblins\goblins.exe" --window --background`
		}, false},
		"no entry": {func(string) string { return "" }, false},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
			earlier := earlierWindow(t, f)
			key := ownStartAtLogin(t, f)
			command := test.command(earlier)
			if command != "" {
				if err := key.SetStringValue(startAtLoginValue, command); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			output := f.install()

			// Assert
			want := command
			if test.adopted {
				want = `"` + filepath.Join(f.root, "goblins.exe") + `" --window --background`
			}
			got, _, err := key.GetStringValue(startAtLoginValue)
			if want == "" && !errors.Is(err, registry.ErrNotExist) {
				t.Errorf("Start at login runs %q (%v), want no entry made", got, err)
			}
			if want != "" && (err != nil || got != want) {
				t.Errorf("Start at login runs %q (%v), want %s", got, err, want)
			}
			if reported := strings.Contains(output, "now runs "+want+", in place of the earlier desktop window"); reported != test.adopted {
				t.Errorf("the install reports Start at login changed: %v, want %v:\n%s", reported, test.adopted, output)
			}
		})
	}
}

// A home that holds no desktop window leaves Start at login starting the
// earlier window, which is the only one there is.
func TestInstallWithNoWindowLeavesStartAtLoginToTheEarlierWindow(t *testing.T) {
	// Arrange
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	earlier := earlierWindow(t, f)
	if err := os.Remove(filepath.Join(filepath.Dir(f.service.Binary), "goblins-window.exe")); err != nil {
		t.Fatal(err)
	}
	key := ownStartAtLogin(t, f)
	command := `"` + filepath.Join(earlier, "goblins-window.exe") + `" --board http://127.0.0.1:4310 --state "C:\home\state" --background`
	if err := key.SetStringValue(startAtLoginValue, command); err != nil {
		t.Fatal(err)
	}

	// Act
	f.install()

	// Assert
	if got, _, err := key.GetStringValue(startAtLoginValue); err != nil || got != command {
		t.Errorf("Start at login runs %q (%v), want the earlier window's %s", got, err, command)
	}
}
