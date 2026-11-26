package home

import (
	"errors"
	"path/filepath"
)

// defaultRoot is %LOCALAPPDATA%\CodeGoblins.
func defaultRoot(getenv func(string) string) (string, error) {
	local := getenv("LOCALAPPDATA")
	if local == "" {
		return "", errors.New("home: LOCALAPPDATA is not set, so there is no per-user folder for a CEO home; set GOBLINS_HOME")
	}
	return filepath.Abs(filepath.Join(local, "CodeGoblins"))
}
