package uninstall

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
)

// AsidePrefix starts the name of the folder the uninstall moves its own
// program to, beside the home, while it removes the home.
const AsidePrefix = ".CodeGoblins-uninstall-"

// home is what goes last: the entry in Installed apps, the environment and
// PATH, then the home itself. It goes only once nothing before it is left,
// so a run that left something keeps what a second run needs: the program,
// on PATH, and the home's records of what it made.
func (m Machine) home(aside *string) func(context.Context) ([]Item, error) {
	return func(context.Context) ([]Item, error) {
		var items []Item
		if entry := m.appsEntry(); entry != nil {
			items = append(items, *entry)
		}
		if wired, err := m.Install.EnvironmentWired(); err != nil {
			return nil, err
		} else if len(wired) > 0 {
			items = append(items, Item{Action: Remove, Group: GroupHome, What: "its place in your environment, " + strings.Join(wired, " and "), Where: `HKCU\Environment`, do: func(context.Context) error {
				// What else of the install's is still there goes too, the
				// environment last.
				return m.Install.Uninstall(io.Discard)
			}})
		}
		isThere, err := there(m.Home.Root)
		if err != nil {
			return nil, err
		}
		what, bytes := homeWhat, int64(0)
		if m.Checkout {
			isThere, err = there(filepath.Join(m.Home.Root, home.InstalledMarker))
			if err != nil {
				return nil, err
			}
			// Only the home's own folders go, and the checkout keeps its code,
			// data and config, so the plan says no more than that.
			what = "its own folders " + list(checkoutHomeFolders[:len(checkoutHomeFolders)-1]) + " in your checkout"
			for _, folder := range checkoutHomeFolders {
				bytes += sizeOf(filepath.Join(m.Home.Root, folder))
			}
		} else if isThere {
			bytes = sizeOf(m.Home.Root)
		}
		if isThere {
			items = append(items, Item{Action: Remove, Group: GroupHome, What: what, Where: m.Home.Root, Bytes: bytes, do: func(context.Context) error {
				moved, err := m.removeHome()
				*aside = moved
				return err
			}})
		}
		return items, nil
	}
}

// appsEntry is Code Goblins' entry in Installed apps, which goes with the
// home, since it runs the uninstall again while the home is there.
func (m Machine) appsEntry() *Item {
	if !m.Install.AppsEntryThere() {
		return nil
	}
	return &Item{Action: Remove, Group: GroupHome, What: "its entry in Installed apps", Where: `HKCU\` + m.Install.AppsEntryKey, do: func(context.Context) error {
		return m.Install.RemoveAppsEntry()
	}}
}

// removeHome removes the home. The uninstall's own program, when it runs
// from the home, cannot be deleted while it runs but can be renamed, so it
// moves to a folder of its own beside the home first, which is removed once
// the program ends. It returns that folder, or "" when the program was
// elsewhere.
func (m Machine) removeHome() (string, error) {
	aside := ""
	if m.Self != "" && within(m.Self, m.Home.Root) {
		aside = filepath.Join(filepath.Dir(m.Home.Root), AsidePrefix+strings.ToLower(rand.Text()[:8]))
		if err := os.MkdirAll(aside, 0o755); err != nil {
			return "", err
		}
		if err := os.Rename(m.Self, filepath.Join(aside, filepath.Base(m.Self))); err != nil {
			return aside, fmt.Errorf("the uninstall could not move its own program out of %s: %w", m.Home.Root, err)
		}
	}
	if m.Checkout {
		// The checkout and what git tracks in it stay: only the folders a
		// home keeps beside them go.
		for _, folder := range checkoutHomeFolders {
			if err := fsx.RemoveAll(filepath.Join(m.Home.Root, folder)); err != nil {
				return aside, err
			}
		}
		return aside, nil
	}
	return aside, fsx.RemoveAll(m.Home.Root)
}

// checkoutHomeFolders are what a home keeps in a checkout an older build
// made the home, none of which git tracks: its folders, then the file that
// marks it installed.
var checkoutHomeFolders = []string{home.BinDir, "state", home.WorktreesDir, home.ScratchDir, home.CachesDir, home.InstalledMarker}

// homeWhat is the home's own item in words, where the home is a folder of
// its own.
const homeWhat = "the app and its folder"

// Asides are the folders earlier uninstalls moved their program to beside
// the home and could not remove, which goes with this one.
func Asides(home string) []string {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(home), AsidePrefix+"*"))
	return matches
}
