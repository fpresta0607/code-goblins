package uninstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/install"
	"github.com/fpresta0607/code-goblins/internal/installbase"
)

// ThisMachine is this machine, with the home an install wires in: the one
// CFO_HOME names where a fleet lives there, and otherwise the per-user home.
// goblins uninstall and the window Installed apps opens both uninstall it.
func ThisMachine() (Machine, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return Machine{}, errors.New("LOCALAPPDATA is not set, so there is no per-user folder to find Code Goblins in")
	}
	standard, err := fsx.AbsClean(filepath.Join(local, "CodeGoblins"))
	if err != nil {
		return Machine{}, err
	}
	env := installbase.NewEnvStore(execx.OSRunner{})
	target, err := installbase.FindTarget(env, standard)
	if err != nil {
		return Machine{}, err
	}
	settings, err := install.UserSettingsPath()
	if err != nil {
		return Machine{}, err
	}
	profile, err := os.UserHomeDir()
	if err != nil {
		return Machine{}, fmt.Errorf("find the user's profile folder: %w", err)
	}
	config, err := home.ReadDevDriveConfig(target.Root)
	if err != nil {
		return Machine{}, err
	}
	self, err := os.Executable()
	if err != nil {
		return Machine{}, err
	}
	// A machine whose user environment is a file stands in for this one, as
	// a test's does: its uninstall reaches none of this machine's own
	// credentials, registry, temp folder or administrator.
	standIn := os.Getenv(installbase.UserEnvFileVariable) != ""
	service := install.Service{
		Root:         target.Root,
		UserSettings: settings,
		Env:          env,
		Checkout:     target.Checkout,
		// The board's native hooks go where `cfo hooks install` puts them.
		HarnessDirs: map[string]string{
			"claude": filepath.Join(profile, ".claude"),
			"codex":  filepath.Join(profile, ".codex"),
			"pi":     filepath.Join(profile, ".pi", "agent"),
		},
		StartMenuShortcut: install.StartMenuShortcutPath(),
		DesktopShortcut:   install.DesktopShortcutPath(),
		EarlierWindow:     install.EarlierWindowDir(),
	}
	if !standIn {
		service.StartAtLoginKey, service.AppsEntryKey = install.StartAtLoginKey, install.AppsEntryKey
	}
	machine := Machine{
		Home:         home.Home{Root: target.Root, State: filepath.Join(target.Root, "state"), Data: filepath.Join(target.Root, "data"), DevDrive: config.Root},
		Checkout:     target.Checkout,
		Profile:      profile,
		LocalAppData: local,
		AppData:      os.Getenv("APPDATA"),
		Temp:         os.TempDir(),
		Install:      service,
		Commands:     execx.OSRunner{},
		Self:         self,
		Admin:        RunAsAdministrator,
		Vault:        CredentialManager{},
		Registry:     !standIn,
	}
	if standIn {
		machine.Vault = NoVault{}
		machine.Temp = filepath.Join(local, "Temp")
		machine.Admin = func(context.Context, string) ([]string, error) {
			return nil, errors.New("a stand-in machine has no administrator")
		}
	}
	return machine, nil
}
