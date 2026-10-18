package install

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/installbase"
)

// The pieces of Uninstall an uninstall of the whole machine runs one at a
// time, each with a check that reads and changes nothing, so it can read its
// removal back and name what is left.

// UserSettingsWired reports whether the user's Claude Code settings still
// hold what an install put there: a CFO hook an older build wrote, or the
// record of the allow rules an install added, with any rule it names.
func (s Service) UserSettingsWired() (bool, error) {
	file, err := loadSettings(s.UserSettings)
	if err != nil {
		return false, err
	}
	before, err := json.Marshal(file.values["hooks"])
	if err != nil {
		return false, err
	}
	if err := file.pruneCFOHooks(); err != nil {
		return false, err
	}
	after, err := json.Marshal(file.values["hooks"])
	if err != nil {
		return false, err
	}
	if !bytes.Equal(before, after) {
		return true, nil
	}
	recorded, err := readRulesRecord(s.UserSettings)
	if err != nil {
		return false, err
	}
	return recorded != nil, nil
}

// RemoveUserSettings takes the CFO hooks and the allow rules an install
// added out of the user's Claude Code settings, and the record of the rules.
func (s Service) RemoveUserSettings() error {
	return s.removeUserSettings(&reporter{out: io.Discard})
}

// RemoveStartAtLoginEntry removes the desktop window's Start at login entry
// where it starts a program in this home.
func (s Service) RemoveStartAtLoginEntry() error {
	return s.removeStartAtLogin(&reporter{out: io.Discard})
}

// EnvironmentWired names what of the install's user environment is still
// set: CFO_HOME naming this home, the home's bin or root on PATH, and the
// projects root.
func (s Service) EnvironmentWired() ([]string, error) {
	var wired []string
	if current, set, err := s.Env.Get(installbase.HomeVariable); err != nil {
		return nil, err
	} else if set && installbase.SameDirectory(current, s.Root) {
		wired = append(wired, installbase.HomeVariable)
	}
	raw, _, err := s.Env.Get(pathVariable)
	if err != nil {
		return nil, err
	}
	if slices.ContainsFunc(pathEntries(raw), func(entry string) bool { return samePathEntry(entry, s.bin()) || samePathEntry(entry, s.Root) }) {
		wired = append(wired, "PATH")
	}
	if _, set, err := s.Env.Get(ProjectsRootVariable); err != nil {
		return nil, err
	} else if set {
		wired = append(wired, ProjectsRootVariable)
	}
	return wired, nil
}

// EmptyJSON reports whether the JSON object in the file at path holds
// nothing, as a settings or hooks file an install made does once an
// uninstall took out what the install added.
func EmptyJSON(path string) bool {
	data, err := fsx.ReadFile(path)
	if err != nil {
		return false
	}
	var values map[string]any
	if json.Unmarshal(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}), &values) != nil {
		return false
	}
	for _, value := range values {
		if nested, ok := value.(map[string]any); !ok || len(nested) > 0 {
			return false
		}
	}
	return true
}

// SettingsBackups are the backups an install and the board's hook setup
// keep beside the harnesses' own files, of the file before each changed it.
func SettingsBackups(files ...string) []string {
	var backups []string
	for _, file := range files {
		for _, suffix := range []string{backupSuffix, ".cfo-native.backup"} {
			if _, err := os.Lstat(file + suffix); err == nil || !errors.Is(err, fs.ErrNotExist) {
				backups = append(backups, file+suffix)
			}
		}
	}
	return backups
}

// WindowShortcut is the Start menu shortcut the desktop window had while it
// was installed on its own, beside the Code Goblins one.
func WindowShortcut(startMenuShortcut string) string {
	return strings.TrimSuffix(startMenuShortcut, "Code Goblins.lnk") + windowShortcutName
}
