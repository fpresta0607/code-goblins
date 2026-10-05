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
// this home's window where it started that one, as the window itself writes
// the entry when this home's goblins starts it. An entry that starts anything
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
				want = `"` + filepath.Join(f.root, "goblins-window.exe") + `" --background`
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

// earlierWindowAtLogin gives the fixture an earlier copy of the desktop window
// in a folder of its own, and returns the Start at login entry that starts it.
func earlierWindowAtLogin(t *testing.T, f *fixture) string {
	t.Helper()
	f.service.EarlierWindow = filepath.Join(t.TempDir(), "CodeGoblinsWindow")
	program := filepath.Join(f.service.EarlierWindow, windowName)
	writeFile(t, program, "window 0")
	return `"` + program + `" --board http://127.0.0.1:4310 --state "C:\home\state" --background`
}

// An install makes Start at login start this home, where it started an
// earlier window, by whether it put the home's window there itself. A window
// it supplied, shipped beside its binary or built into a checkout for it,
// opens the app when started alone, so the entry starts that window. A window
// the home held before, which the install only kept, may be from before a
// window could do that, so the entry runs goblins with --window, which opens
// any window, and that window is left as it was.
func TestInstallStartsAtLoginTheWindowItSuppliedAndGoblinsForOneItKept(t *testing.T) {
	const kept = "a window from before"
	for name, test := range map[string]struct {
		arrange  func(t *testing.T) *fixture
		supplied bool
		window   string
	}{
		"a window beside the binary, into a home with none": {func(t *testing.T) *fixture {
			f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
			writeFile(t, filepath.Join(filepath.Dir(f.service.Binary), windowName), "window 1")
			return f
		}, true, "window 1"},
		"a window beside the binary that the home already holds": {func(t *testing.T) *fixture {
			f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
			writeFile(t, filepath.Join(filepath.Dir(f.service.Binary), windowName), "window 1")
			writeFile(t, filepath.Join(f.root, windowName), "window 1")
			return f
		}, true, "window 1"},
		"a build with no window beside it, over a window the home held": {func(t *testing.T) *fixture {
			f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
			writeFile(t, filepath.Join(f.root, windowName), kept)
			return f
		}, false, kept},
		"an install run from the home, whose window is beside its own binary": {func(t *testing.T) *fixture {
			f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
			f.service.Binary = filepath.Join(f.root, "goblins.exe")
			writeFile(t, f.service.Binary, "build 1")
			writeFile(t, filepath.Join(f.root, windowName), kept)
			return f
		}, false, kept},
		"a checkout whose window was built for this install": {func(t *testing.T) *fixture {
			f := newFixture(t, adopterSettings, nil)
			f.service.BuiltWindow = true
			writeFile(t, filepath.Join(f.root, windowName), "window 1")
			return f
		}, true, "window 1"},
		"a checkout wired by a binary with a window elsewhere": {func(t *testing.T) *fixture {
			f := newFixture(t, adopterSettings, nil)
			f.service.Binary = filepath.Join(t.TempDir(), "cfo.exe")
			writeFile(t, f.service.Binary, "build 1")
			writeFile(t, filepath.Join(filepath.Dir(f.service.Binary), windowName), "window elsewhere")
			writeFile(t, filepath.Join(f.root, windowName), kept)
			return f
		}, false, kept},
		"a checkout whose window was built before": {func(t *testing.T) *fixture {
			f := newFixture(t, adopterSettings, nil)
			writeFile(t, filepath.Join(f.root, windowName), kept)
			return f
		}, false, kept},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := test.arrange(t)
			command := earlierWindowAtLogin(t, f)
			key := ownStartAtLogin(t, f)
			if err := key.SetStringValue(startAtLoginValue, command); err != nil {
				t.Fatal(err)
			}

			// Act
			output := f.install()

			// Assert
			want := `"` + filepath.Join(f.root, "goblins.exe") + `" --window --background`
			if test.supplied {
				want = `"` + filepath.Join(f.root, windowName) + `" --background`
			}
			if got, _, err := key.GetStringValue(startAtLoginValue); err != nil || got != want {
				t.Errorf("Start at login runs %q (%v), want %s", got, err, want)
			}
			if got := readFile(t, filepath.Join(f.root, windowName)); got != test.window {
				t.Errorf("%s in the home = %q, want %q", windowName, got, test.window)
			}
			if !strings.Contains(output, "now runs "+want+", in place of the earlier desktop window") {
				t.Errorf("the install does not report what Start at login runs now, %s:\n%s", want, output)
			}
		})
	}
}

// An install that kept the home's window adopts only the earlier window's
// Start at login entry, as one that supplied the window does: an entry that
// starts anything else, this home's own included, is left as it is, none is
// made where there was none, and the window the home held stays as it was.
func TestInstallThatKeptTheHomesWindowLeavesEveryOtherStartAtLoginEntry(t *testing.T) {
	const kept = "a window from before"
	for name, command := range map[string]func(root, earlier string) string{
		"a window in a folder beside the earlier one": func(_, earlier string) string {
			return `"` + filepath.Join(earlier+"Other", windowName) + `" --board http://127.0.0.1:4310 --state "C:\home\state" --background`
		},
		"another home's goblins": func(string, string) string {
			return `"C:\elsewhere\CodeGoblins\goblins.exe" --window --background`
		},
		"this home's window on its own": func(root, _ string) string {
			return `"` + filepath.Join(root, windowName) + `" --board http://127.0.0.1:4310 --state "` + filepath.Join(root, "state") + `" --background`
		},
		"this home's window alone": func(root, _ string) string {
			return `"` + filepath.Join(root, windowName) + `" --background`
		},
		"no entry": func(string, string) string { return "" },
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
			writeFile(t, filepath.Join(f.root, windowName), kept)
			earlierWindowAtLogin(t, f)
			key := ownStartAtLogin(t, f)
			want := command(f.root, f.service.EarlierWindow)
			if want != "" {
				if err := key.SetStringValue(startAtLoginValue, want); err != nil {
					t.Fatal(err)
				}
			}

			// Act
			output := f.install()

			// Assert
			got, _, err := key.GetStringValue(startAtLoginValue)
			if want == "" && !errors.Is(err, registry.ErrNotExist) {
				t.Errorf("Start at login runs %q (%v), want no entry made", got, err)
			}
			if want != "" && (err != nil || got != want) {
				t.Errorf("Start at login runs %q (%v), want it left as %s", got, err, want)
			}
			if strings.Contains(output, "start at login") {
				t.Errorf("the install reports Start at login changed:\n%s", output)
			}
			if got := readFile(t, filepath.Join(f.root, windowName)); got != kept {
				t.Errorf("%s in the home = %q, want %q", windowName, got, kept)
			}
		})
	}
}

func TestCoreOnlyReinstallKeepsTheStandaloneWindowAndAdoptsItsLogin(t *testing.T) {
	// Arrange
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	writeFile(t, filepath.Join(f.root, windowName), "retained older window")
	f.install()
	command := earlierWindowAtLogin(t, f)
	writeFile(t, filepath.Join(f.service.EarlierWindow, windowPicture), "standalone picture")
	key := ownStartAtLogin(t, f)
	if err := key.SetStringValue(startAtLoginValue, command); err != nil {
		t.Fatal(err)
	}

	// Act
	f.install()
	f.install()

	// Assert
	want := `"` + filepath.Join(f.root, "goblins.exe") + `" --window --background`
	if got, _, err := key.GetStringValue(startAtLoginValue); err != nil || got != want {
		t.Errorf("Start at login runs %q (%v), want %s", got, err, want)
	}
	for path, want := range map[string]string{
		filepath.Join(f.root, windowName):                     "retained older window",
		filepath.Join(f.service.EarlierWindow, windowName):    "window 0",
		filepath.Join(f.service.EarlierWindow, windowPicture): "standalone picture",
	} {
		if got, err := os.ReadFile(path); err != nil || string(got) != want {
			t.Errorf("%s = %q (%v), want it kept as %q", path, got, err, want)
		}
	}
	if info, err := os.Stat(f.service.EarlierWindow); err != nil || !info.IsDir() {
		t.Errorf("standalone folder = %v (%v), want it kept", info, err)
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
