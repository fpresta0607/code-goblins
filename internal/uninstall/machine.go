package uninstall

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
)

// Machine is the machine an uninstall plans for and the home it removes.
// Every place it reads is a field, so a test points the whole uninstall at
// folders of its own.
type Machine struct {
	Home home.Home
	// Checkout says the home is a code-goblins checkout an older build made
	// the home, whose files git tracks: only the home's own folders in it go,
	// never the checkout, and its data stays where it is.
	Checkout bool
	// Profile, LocalAppData, AppData and Temp are the person's folders.
	Profile, LocalAppData, AppData, Temp string
	// Install is this home's install, whose changes outside the home the
	// uninstall reverses.
	Install install.Service
	// Commands runs git, schtasks and the other programs the uninstall asks.
	Commands execx.Runner
	// Self is this program's own path, which the uninstall leaves for last.
	Self string
	// Admin runs a script as administrator, which asks Windows once, and
	// returns the Where of each thing the script found still there.
	Admin func(context.Context, string) ([]string, error)
	// Processes reads the programs running now. Nil reads this machine's.
	Processes func(context.Context) ([]Running, error)
	// Vault lists, deletes and reads back the stored credentials.
	Vault Vault
	// Registry says the uninstall reads and removes the keys under
	// HKEY_CURRENT_USER the window registered, which a stand-in machine, as
	// a test's is, never does: they are the real machine's.
	Registry bool
}

// Vault is where Code Goblins stored credentials.
type Vault interface {
	Targets() ([]string, error)
	Delete(target string) error
}

// KeptFolderName is the folder the uninstall keeps the person's work and
// data in, in their profile.
const KeptFolderName = "Code Goblins kept"

// NewPlan makes the plan for m with the person's choices. It reads and
// changes nothing.
func NewPlan(ctx context.Context, m Machine, choices Choices) (Plan, error) {
	if m.Home.Root == "" || m.Profile == "" || m.LocalAppData == "" || m.AppData == "" || m.Temp == "" {
		return Plan{}, errors.New("uninstall: the home and the profile's folders are all needed to plan")
	}
	kept, err := keptFolder(m.Profile)
	if err != nil {
		return Plan{}, err
	}
	plan := Plan{Home: m.Home.Root, Kept: kept, admin: m.Admin, aside: new(string)}
	plan.finders = []finder{
		{GroupRunning, m.running},
		{GroupWork, func(ctx context.Context) ([]Item, error) { return m.work(ctx, kept, choices) }},
		{GroupFleetData, func(ctx context.Context) ([]Item, error) { return m.fleetData(kept, choices) }},
		{GroupCheckouts, func(ctx context.Context) ([]Item, error) { return m.checkouts(ctx, choices) }},
		{GroupDevDrive, m.devDrive},
		{GroupCredentials, m.credentials},
		{GroupHarnesses, m.harnesses},
		{GroupShortcuts, m.shortcuts},
		{GroupFolders, m.folders},
		{GroupTools, func(ctx context.Context) ([]Item, error) { return m.tools(ctx, choices) }},
		{GroupHome, m.home(plan.aside)},
	}
	for _, f := range plan.finders {
		items, err := f.find(ctx)
		if err != nil {
			return Plan{}, err
		}
		plan.Items = append(plan.Items, items...)
	}
	return plan, nil
}

// keptFolder is the folder in profile the kept work and data go to: Code
// Goblins kept, or the first of Code Goblins kept 2, 3 and on that is not
// there yet, since nothing is ever written over.
func keptFolder(profile string) (string, error) {
	for n := 1; n < 1000; n++ {
		name := KeptFolderName
		if n > 1 {
			name += " " + strconv.Itoa(n)
		}
		path := filepath.Join(profile, name)
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			return path, nil
		} else if err != nil {
			return "", fmt.Errorf("uninstall: look for a folder to keep your work in: %w", err)
		}
	}
	return "", errors.New("uninstall: every name for a folder to keep your work in is taken")
}

// there reports whether path is there, a file, folder or link.
func there(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// sizeOf is how many bytes the files under path hold, links not followed.
func sizeOf(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 || entry.Type()&fs.ModeIrregular != 0 {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total
}

// removal is the item that removes path, a file or a folder and what is in
// it.
func removal(group Group, what, path string) Item {
	return Item{Action: Remove, Group: group, What: what, Where: path, Bytes: sizeOf(path), do: func(context.Context) error {
		return fsx.RemoveAll(path)
	}}
}

// removeIfEmpty removes folder when nothing is left in it, and reports
// whether it did.
func removeIfEmpty(folder string) bool {
	entries, err := os.ReadDir(folder)
	if err != nil || len(entries) > 0 {
		return false
	}
	return os.Remove(folder) == nil
}
