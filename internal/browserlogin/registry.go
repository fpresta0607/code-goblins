package browserlogin

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/fpresta0607/code-goblins/internal/fsx"
)

const (
	registryFile = "browser-logins.json"
	profilesDir  = "browser-logins"
)

// registryDocument is the registry file's shape.
type registryDocument struct {
	Logins []Login `json:"logins"`
}

// RegistryPath is where a home's registry lives.
func RegistryPath(stateDir string) string {
	return filepath.Join(stateDir, registryFile)
}

// ProfileDir is the Chrome profile of login id.
func ProfileDir(stateDir, id string) string {
	return filepath.Join(stateDir, profilesDir, id)
}

// Read is every login the registry holds; a home that never kept one holds
// none.
func Read(stateDir string) ([]Login, error) {
	data, err := fsx.ReadFile(RegistryPath(stateDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var read registryDocument
	if err := json.Unmarshal(data, &read); err != nil {
		return nil, fmt.Errorf("browser logins: %s is not valid: %w", RegistryPath(stateDir), err)
	}
	for _, login := range read.Logins {
		if !ValidID(login.ID) {
			return nil, fmt.Errorf("browser logins: %s holds a login whose id cannot name a profile", RegistryPath(stateDir))
		}
	}
	return read.Logins, nil
}

// Write replaces the registry with logins. The broker is its one writer.
func Write(stateDir string, logins []Login) error {
	data, err := json.MarshalIndent(registryDocument{Logins: logins}, "", "  ")
	if err != nil {
		return err
	}
	return fsx.AtomicWriteFile(RegistryPath(stateDir), append(data, '\n'))
}

// MakeProfile makes login id's profile folder, if it is not there yet,
// readable and writable by the current Windows user alone: Chrome keeps the
// login's session there.
func MakeProfile(stateDir, id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("%q cannot name a browser login", id)
	}
	if err := os.MkdirAll(filepath.Join(stateDir, profilesDir), 0o700); err != nil {
		return "", err
	}
	path := ProfileDir(stateDir, id)
	if err := os.Mkdir(path, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return "", err
	}
	if err := ownerOnly(path); err != nil {
		return "", fmt.Errorf("browser login %s: its profile could not be made owner-only: %w", id, err)
	}
	return path, nil
}
