//go:build windows

package uninstall

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// The keys under HKEY_CURRENT_USER the desktop window and Windows write for
// Code Goblins.
const (
	appUserModelKey = `Software\Classes\AppUserModelId\Code Goblins`
	classesKey      = `Software\Classes\CLSID`
	// goToastKey is the class go-toast registers under its own fixed id,
	// which any program built with it shares: it is the window's only where
	// it names the window.
	goToastKey           = `Software\Classes\CLSID\{0F82E845-CB89-4039-BDBF-67CA33254C76}`
	categoriesKey        = `Software\Code Goblins`
	notificationsKey     = `Software\Microsoft\Windows\CurrentVersion\Notifications\Settings\Code Goblins`
	notifyIconSettingKey = `Control Panel\NotifyIconSettings`
)

// registered are what the window registered with Windows to show
// notifications and what Windows keeps of them and of its tray icon.
func (m Machine) registered() ([]Item, error) {
	windows := []string{m.Home.Bin(), filepath.Join(m.LocalAppData, "CodeGoblinsWindow")}
	isWindow := func(command string) bool {
		program := strings.Trim(strings.Fields(command + " ")[0], `"`)
		if strings.HasPrefix(command, `"`) {
			program, _, _ = strings.Cut(strings.TrimPrefix(command, `"`), `"`)
		}
		for _, folder := range windows {
			if strings.EqualFold(filepath.Base(program), "goblins-window.exe") && within(program, folder) {
				return true
			}
		}
		return false
	}
	var items []Item
	// Each key goes with what is under it. The keys above it, such as CLSID
	// and NotifyIconSettings, are Windows' own, which a profile may have held
	// empty before, so they stay.
	remove := func(what, key string) {
		items = append(items, Item{Action: Remove, Group: GroupShortcuts, What: what, Where: `HKCU\` + key, do: func(context.Context) error {
			return deleteTree(key)
		}})
	}
	if keyThere(appUserModelKey) {
		remove("its notification registration", appUserModelKey)
		if activator, ok := stringValue(appUserModelKey, "CustomActivator"); ok && activator != "" {
			class := classesKey + `\` + activator
			if server, ok := stringValue(class+`\LocalServer32`, ""); ok && isWindow(server) {
				remove("its notification click handler", class)
			}
		}
	}
	if server, ok := stringValue(goToastKey+`\LocalServer32`, ""); ok && isWindow(server) {
		remove("its second notification click handler", goToastKey)
	}
	for _, key := range []struct{ what, path string }{
		{"the window's notification categories", categoriesKey},
		{"Windows' settings for its notifications", notificationsKey},
	} {
		if keyThere(key.path) {
			remove(key.what, key.path)
		}
	}
	icons, err := subkeys(notifyIconSettingKey)
	if err != nil {
		return nil, err
	}
	for _, icon := range icons {
		key := notifyIconSettingKey + `\` + icon
		if path, ok := stringValue(key, "ExecutablePath"); ok && isWindow(path) {
			remove("Windows' settings for its tray icon", key)
		}
	}
	return items, nil
}

func keyThere(path string) bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	key.Close()
	return true
}

// stringValue reads a string value, the key's default where name is empty.
func stringValue(path, name string) (string, bool) {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	return value, err == nil
}

func subkeys(path string) ([]string, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, path, registry.ENUMERATE_SUB_KEYS)
	if errors.Is(err, registry.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer key.Close()
	return key.ReadSubKeyNames(-1)
}

// deleteTree deletes a key under HKEY_CURRENT_USER and every key under it.
func deleteTree(path string) error {
	names, err := subkeys(path)
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := deleteTree(path + `\` + name); err != nil {
			return err
		}
	}
	if err := registry.DeleteKey(registry.CURRENT_USER, path); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
