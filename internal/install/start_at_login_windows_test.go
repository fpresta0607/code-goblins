package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codegoblins "github.com/fpresta0607/code-goblins"
	"golang.org/x/sys/windows/registry"
)

// An install makes Windows start Code Goblins at login, in the tray, unless
// Start at login was turned off: in the home, as the board and the tray keep
// it, or with --start-at-login off at this install. A choice made at the
// install is kept in the home, so the next install keeps it, and
// --start-at-login on turns it back on.
func TestInstallStartsCodeGoblinsAtLoginUnlessItWasTurnedOff(t *testing.T) {
	for name, test := range map[string]struct {
		kept, chosen string
		isOn         bool
		wantKept     string
	}{
		"on by default":                  {isOn: true},
		"turned off in the home":         {kept: StartAtLoginOff, wantKept: StartAtLoginOff},
		"turned off at this install":     {chosen: StartAtLoginOff, wantKept: StartAtLoginOff},
		"turned on again at the install": {kept: StartAtLoginOff, chosen: StartAtLoginOn, isOn: true, wantKept: StartAtLoginOn},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
			writeFile(t, filepath.Join(filepath.Dir(f.service.Binary), windowName), "window 1")
			key := ownStartAtLogin(t, f)
			starts := `"` + filepath.Join(f.bin, windowName) + `" --background`
			// The home's own entry from an earlier install, which off removes.
			if err := key.SetStringValue(startAtLoginValue, `"`+filepath.Join(f.bin, windowName)+`" --board http://127.0.0.1:4310 --state "`+filepath.Join(f.root, "state")+`" --background`); err != nil {
				t.Fatal(err)
			}
			if test.kept != "" {
				if err := os.MkdirAll(filepath.Join(f.root, "state"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := WriteStartAtLogin(filepath.Join(f.root, "state"), test.kept); err != nil {
					t.Fatal(err)
				}
			}
			f.service.StartAtLogin = test.chosen

			// Act
			output := f.install()

			// Assert
			got, _, err := key.GetStringValue(startAtLoginValue)
			if test.isOn && (err != nil || got != starts) {
				t.Errorf("Start at login runs %q (%v), want %s", got, err, starts)
			}
			if !test.isOn && !errors.Is(err, registry.ErrNotExist) {
				t.Errorf("Start at login runs %q (%v), want this home's entry removed", got, err)
			}
			if !test.isOn && !strings.Contains(output, "off, as chosen") {
				t.Errorf("the install does not say Start at login is off:\n%s", output)
			}
			if kept, err := ReadStartAtLogin(filepath.Join(f.root, "state")); err != nil || kept != test.wantKept {
				t.Errorf("the home keeps the choice %q (%v), want %q", kept, err, test.wantKept)
			}
		})
	}
}

// A machine with no Start at login entries, such as a test's whose user
// environment is a file, still keeps a choice made at the install.
func TestInstallKeepsTheChoiceOfStartAtLoginWhereThereAreNoEntries(t *testing.T) {
	// Arrange
	f := installedFixture(t, nil, codegoblins.Contract, codegoblins.Policy)
	f.service.StartAtLogin = StartAtLoginOff

	// Act
	f.install()

	// Assert
	if kept, err := ReadStartAtLogin(filepath.Join(f.root, "state")); err != nil || kept != StartAtLoginOff {
		t.Errorf("the home keeps the choice %q (%v), want off", kept, err)
	}
}
