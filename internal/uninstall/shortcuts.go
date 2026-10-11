package uninstall

import (
	"context"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/install"
)

// shortcuts are how Windows shows and starts Code Goblins: its shortcuts in
// the Start menu and on the desktop, its Start at login entry, and what the
// window registered to show notifications and sit in the tray.
func (m Machine) shortcuts(context.Context) ([]Item, error) {
	var items []Item
	for _, shortcut := range []struct{ what, path string }{
		{"its shortcut in the Start menu", m.Install.StartMenuShortcut},
		{"the window's own shortcut in the Start menu, from an earlier install", install.WindowShortcut(m.Install.StartMenuShortcut)},
		{"its shortcut on the desktop", m.Install.DesktopShortcut},
	} {
		if shortcut.path == "" || filepath.Base(shortcut.path) == "Code Goblins Window.lnk" && m.Install.StartMenuShortcut == "" {
			continue
		}
		if isThere, err := there(shortcut.path); err != nil {
			return nil, err
		} else if isThere {
			path := shortcut.path
			items = append(items, Item{Action: Remove, Group: GroupShortcuts, What: shortcut.what, Where: path, do: func(context.Context) error { return os.Remove(path) }})
		}
	}
	if starts, err := m.Install.StartsHereAtLogin(); err != nil {
		return nil, err
	} else if starts {
		items = append(items, Item{Action: Remove, Group: GroupShortcuts, What: "its Start at login entry", Where: `HKCU\` + install.StartAtLoginKey + `\CodeGoblins`, do: func(context.Context) error {
			return m.Install.RemoveStartAtLoginEntry()
		}})
	}
	if !m.Registry {
		return items, nil
	}
	registered, err := m.registered()
	if err != nil {
		return nil, err
	}
	return append(items, registered...), nil
}
