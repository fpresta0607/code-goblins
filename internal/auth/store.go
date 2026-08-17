package auth

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Store holds credentials outside every repository. Keys are environment
// variable names, so there is exactly one name to know per credential.
type Store interface {
	// Get returns a stored secret. A missing key is (", false, nil"), not an
	// error: absence is an ordinary state this package reports, not a fault.
	Get(key string) (string, bool, error)
	Set(key, value string) error
	// Keys lists what is stored. Values are never returned here so a listing
	// can be printed without redaction.
	Keys() ([]string, error)
	// Describe names the store in reports.
	Describe() string
}

// StoreDirName is the fallback store location under the user's home.
const StoreDirName = ".cfo"

// CredentialDirName is the fallback store's directory of secret files.
const CredentialDirName = "credentials"

// credentialTarget prefixes Windows Credential Manager entries so cfo's
// credentials are distinguishable from everything else in the user's vault.
const credentialTarget = "cfo:"

// StoreDirEnv points the credential store at a directory instead of the
// system vault. It is the escape hatch when Credential Manager is unavailable
// or unwanted, and what tests use so they never touch the real vault.
const StoreDirEnv = "CFO_CREDENTIAL_DIR"

// OpenStore returns the credential store to use. Windows Credential Manager
// is preferred because it encrypts at rest under the user's login; the file
// store is the documented fallback everywhere else, and when the vault cannot
// be reached.
func OpenStore() (Store, error) {
	if dir := os.Getenv(StoreDirEnv); dir != "" {
		return OpenFileStore(dir)
	}
	if store, err := openCredentialManager(); err == nil {
		return store, nil
	}
	return OpenFileStore("")
}

// OpenFileStore returns the file-backed store. An empty root resolves to
// ~/.cfo/credentials.
func OpenFileStore(root string) (Store, error) {
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("auth: locate user home: %w", err)
		}
		root = filepath.Join(home, StoreDirName, CredentialDirName)
	}
	return &fileStore{root: root}, nil
}

// fileStore keeps one secret per file so a single credential can be replaced
// without rewriting the rest, and so a stray read cannot spill the whole set.
type fileStore struct {
	root string
}

func (s *fileStore) Describe() string {
	return "file store " + s.root
}

func (s *fileStore) path(key string) (string, error) {
	if !ValidEnvName(key) {
		return "", fmt.Errorf("auth: invalid credential key %q", key)
	}
	return filepath.Join(s.root, key), nil
}

func (s *fileStore) Get(key string) (string, bool, error) {
	path, err := s.path(key)
	if err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	// A secret written by hand or by a shell redirect picks up a trailing
	// newline; a token with one is not the same token.
	return strings.TrimRight(string(data), "\r\n"), true, nil
}

func (s *fileStore) Set(key, value string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		return err
	}
	return restrictToOwner(path)
}

func (s *fileStore) Keys() ([]string, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var keys []string
	for _, entry := range entries {
		if !entry.IsDir() && ValidEnvName(entry.Name()) {
			keys = append(keys, entry.Name())
		}
	}
	sort.Strings(keys)
	return keys, nil
}

// WriteSecretFile writes content that contains credentials: 0600 plus an
// owner-only ACL on Windows, where the permission bits alone do nothing. Use
// it for anything derived from the store that has to land on disk.
func WriteSecretFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	return restrictToOwner(path)
}

// Resolved is one environment variable's value and where it came from.
type Resolved struct {
	Name   string
	Value  string
	Source string // "environment" or "store"
}

// Resolve reads every requested name from the process environment first, then
// the store. The process environment wins so an operator can override a
// stored credential for one command without editing the store.
func Resolve(store Store, names []string) (map[string]Resolved, []string, error) {
	found := map[string]Resolved{}
	var missing []string
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok && value != "" {
			found[name] = Resolved{Name: name, Value: value, Source: "environment"}
			continue
		}
		if store != nil {
			value, ok, err := store.Get(name)
			if err != nil {
				return nil, nil, err
			}
			if ok && value != "" {
				found[name] = Resolved{Name: name, Value: value, Source: "store"}
				continue
			}
		}
		missing = append(missing, name)
	}
	return found, missing, nil
}

// Redact reduces a secret to a shape that proves which credential it is
// without disclosing it. Short values disclose nothing at all, because a
// four-character prefix of a six-character secret is most of the secret.
func Redact(value string) string {
	if value == "" {
		return ""
	}
	if len(value) < 12 {
		return "***"
	}
	return value[:4] + "***" + value[len(value)-2:]
}
