package home

import (
	"errors"
	"os"
	"path/filepath"
)

// defaultRoot is %LOCALAPPDATA%\CodeGoblins.
func defaultRoot() (string, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return "", errors.New("home: LOCALAPPDATA is not set, so there is no per-user folder for a CEO home; set GOBLINS_HOME")
	}
	return filepath.Abs(filepath.Join(local, "CodeGoblins"))
}
