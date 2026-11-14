// Package homesetting reads goblins' home setting from the user's settings,
// falling back on the one a build before the rename set. It links nothing of
// the cutover beside it, so the setup can read the home without linking it.
package homesetting

import "github.com/fpresta0607/code-goblins/internal/installbase"

// Old is the setting a build before the rename finds its home by.
const Old = "CFO_HOME"

// Settings is the user's settings, where goblins' home setting falls back on
// Old, so whatever finds the home by the user's settings, an install, an
// uninstall or the setup's plan, finds such a home where it is.
type Settings struct{ installbase.EnvStore }

func (e Settings) Get(name string) (string, bool, error) {
	value, isSet, err := e.EnvStore.Get(name)
	if err != nil || isSet || name != installbase.HomeVariable {
		return value, isSet, err
	}
	root, _, err := e.EnvStore.Get(Old)
	return root, root != "", err
}
