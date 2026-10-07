package main

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// identity is what a window is known by on this machine: its one-instance id,
// and whether it raises Windows notifications.
type identity struct {
	instance string
	notifies bool
}

// windowIdentity is the identity of the window on the board of the home whose
// state folder is stateDir, given userEnv, the environment Windows gives a new
// process of this user. The home that environment names, the user's own,
// keeps the one instance id and the one notification registration Windows
// holds for Code Goblins. Any other home, such as a scratch home or a second
// install, is an instance of its own, keyed by its state folder as Windows
// compares paths, so opening its window never brings the user's own home's
// window to the front; and it raises no notification, since Windows hands a
// click on any Code Goblins notification to the program that registered last.
// A window on a profile of its own, as the window's tests start, is an
// instance of its own and raises no notification.
func windowIdentity(stateDir, profile string, userEnv []string) identity {
	window := identity{instance: "dev.codegoblins.window", notifies: profile == ""}
	if profile != "" {
		sum := sha256.Sum256([]byte(profile))
		window.instance += "." + hex.EncodeToString(sum[:8])
		return window
	}
	if !sameFolder(usersOwnState(userEnv), stateDir) {
		sum := sha256.Sum256([]byte(strings.ToLower(filepath.Clean(stateDir))))
		window.instance += "." + hex.EncodeToString(sum[:8])
		window.notifies = false
	}
	return window
}

// usersOwnState is the state folder of the home userEnv names, as cfo
// resolves one: CFO_STATE_OVERRIDE, or else the state folder of CFO_HOME, or
// else of the per-user home under LOCALAPPDATA. It is empty when userEnv
// names no home at all.
func usersOwnState(userEnv []string) string {
	value := func(name string) string {
		for _, entry := range userEnv {
			if key, found, ok := strings.Cut(entry, "="); ok && strings.EqualFold(key, name) {
				return found
			}
		}
		return ""
	}
	if state := value("CFO_STATE_OVERRIDE"); state != "" {
		return state
	}
	root := value("CFO_HOME")
	if root == "" {
		local := value("LOCALAPPDATA")
		if local == "" {
			return ""
		}
		root = filepath.Join(local, "CodeGoblins")
	}
	return filepath.Join(root, "state")
}

func sameFolder(a, b string) bool {
	return a != "" && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// userEnvironment is the environment Windows gives a new process of this
// user: the user's and the machine's configured variables, and nothing this
// process inherited from whatever started it.
func userEnvironment() ([]string, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY|windows.TOKEN_DUPLICATE, &token); err != nil {
		return nil, err
	}
	defer token.Close()
	return token.Environ(false)
}
