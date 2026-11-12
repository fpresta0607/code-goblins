package cutover

import (
	"os"
	"strings"
)

// hostIDSetting names the native terminal a process runs in.
var hostIDSetting = setting + "HOST_ID"

// AdoptOldSettings reads each setting in this process's environment that a
// build before the rename named with the old prefix as the setting of the
// same name with goblins' prefix, where that is not set, and then drops the
// old name, so this process and every program it starts see only goblins'
// settings. A release before the rename hands its update the home only by
// the old name, and a terminal opened before the home moved still holds only
// the old names, so without this a home outside the usual folder is not
// found. The CEO's terminal took the role's new name as the home moved, so
// a terminal opened before it that names itself by the old one is read as
// that terminal, as it is now named.
func AdoptOldSettings() {
	prefix := strings.ToUpper(oldName) + "_"
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if len(name) <= len(prefix) || !strings.EqualFold(name[:len(prefix)], prefix) {
			continue
		}
		adopted := setting + strings.ToUpper(name[len(prefix):])
		if adopted == hostIDSetting && strings.EqualFold(value, oldName) {
			value = newName
		}
		if _, isSet := os.LookupEnv(adopted); !isSet {
			_ = os.Setenv(adopted, value)
		}
		_ = os.Unsetenv(name)
	}
}

// HandBackSettings sets each of goblins' settings in this process's
// environment by the old prefix too, so a build from before the rename that
// this process starts reads its home and the rest by the names it knows. A
// rollback that undid the move of the home starts such a build, the one the
// update replaced, which without this would not find a home outside the
// usual folder.
func HandBackSettings() {
	prefix := strings.ToUpper(oldName) + "_"
	for _, entry := range os.Environ() {
		name, value, _ := strings.Cut(entry, "=")
		if len(name) <= len(setting) || !strings.EqualFold(name[:len(setting)], setting) {
			continue
		}
		if strings.EqualFold(name, hostIDSetting) && strings.EqualFold(value, newName) {
			value = oldName
		}
		_ = os.Setenv(prefix+strings.ToUpper(name[len(setting):]), value)
	}
}
