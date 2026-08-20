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

// Key names one credential in the store. Credentials are per project, so a
// key is a project scope plus an environment variable name. The empty project
// is the shared scope: one value that genuinely is the same everywhere, and
// the scope every credential stored before namespacing already lives in.
type Key struct {
	Project string
	Name    string
}

// Shared returns the shared-scope key for a name.
func Shared(name string) Key { return Key{Name: name} }

// Scoped returns a project-scope key. The project is reduced the same way
// manifests are keyed, so a path and a bare name reach the same scope.
func Scoped(project, name string) Key {
	return Key{Project: ProjectName(project), Name: name}
}

// IsShared reports whether this key lives in the shared scope.
func (k Key) IsShared() bool { return k.Project == "" }

// String is the operator-facing form: `NAME` shared, `project/NAME` scoped.
func (k Key) String() string {
	if k.IsShared() {
		return k.Name
	}
	return k.Project + "/" + k.Name
}

// Scope names where the key lives, for resolution reports.
func (k Key) Scope() string {
	if k.IsShared() {
		return "store/shared"
	}
	return "store/" + k.Project
}

// Valid reports whether the key can be stored. An invalid scope or name is a
// refusal rather than a sanitized guess.
func (k Key) Valid() bool {
	if !ValidEnvName(k.Name) {
		return false
	}
	return k.IsShared() || ValidProjectName(k.Project)
}

// Store holds credentials outside every repository, namespaced on
// (project, name) so two projects declaring the same variable cannot alias.
type Store interface {
	// Get returns a stored secret. A missing key is (", false, nil"), not an
	// error: absence is an ordinary state this package reports, not a fault.
	Get(key Key) (string, bool, error)
	Set(key Key, value string) error
	// Keys lists what is stored. Values are never returned here so a listing
	// can be printed without redaction.
	Keys() ([]Key, error)
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
// A project scope is a subdirectory, which makes every credential written
// before namespacing a shared-scope entry that still reads back unchanged.
type fileStore struct {
	root string
}

func (s *fileStore) Describe() string {
	return "file store " + s.root
}

func (s *fileStore) path(key Key) (string, error) {
	if !key.Valid() {
		return "", fmt.Errorf("auth: invalid credential key %q", key.String())
	}
	if key.IsShared() {
		return filepath.Join(s.root, key.Name), nil
	}
	return filepath.Join(s.root, key.Project, key.Name), nil
}

func (s *fileStore) Get(key Key) (string, bool, error) {
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

func (s *fileStore) Set(key Key, value string) error {
	path, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		return err
	}
	return restrictToOwner(path)
}

func (s *fileStore) Keys() ([]Key, error) {
	keys, err := s.scopeKeys("")
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !ValidProjectName(entry.Name()) {
			continue
		}
		scoped, err := s.scopeKeys(entry.Name())
		if err != nil {
			return nil, err
		}
		keys = append(keys, scoped...)
	}
	sortKeys(keys)
	return keys, nil
}

func (s *fileStore) scopeKeys(project string) ([]Key, error) {
	dir := s.root
	if project != "" {
		dir = filepath.Join(s.root, project)
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var keys []Key
	for _, entry := range entries {
		if !entry.IsDir() && ValidEnvName(entry.Name()) {
			keys = append(keys, Key{Project: project, Name: entry.Name()})
		}
	}
	return keys, nil
}

// sortKeys orders a listing shared scope first, then by project, so an
// operator reading `cfo auth list` sees the fallback before what overrides it.
func sortKeys(keys []Key) {
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Project != keys[j].Project {
			return keys[i].Project < keys[j].Project
		}
		return keys[i].Name < keys[j].Name
	})
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

// Candidate is one place a resolver looked, recorded in the order it looked
// so a report can show how a value was reached instead of asserting it.
type Candidate struct {
	// Source is "env" or a store scope.
	Source string
	// Name is the environment or stored name consulted, which is the
	// declared name or one of its declared aliases.
	Name string
	// Hit marks the candidate that answered.
	Hit bool
	// Note explains a value that was present but deliberately not used.
	Note string
}

// Resolved is one declared variable's value and how it was reached.
type Resolved struct {
	// Name is the name the manifest declared.
	Name string
	// Value is the secret. It is never printed.
	Value string
	// From is the candidate that answered, as `source:name`.
	From string
}

// Resolution is everything one service's variables resolved to, plus the
// ordered candidates behind each, resolved or not.
type Resolution struct {
	Values  map[string]Resolved
	Missing []string
	Chains  map[string][]Candidate
}

// Environ renders the resolved values as a child process environment.
func (r Resolution) Environ() []string { return Environ(r.Values) }

// Resolver reads one project's credentials in a fixed, reportable order: the
// process environment first, so an operator can override for one command;
// then this project's scope; then, only for a service the manifest marks
// shared, the shared scope. Declared aliases are tried after the declared
// name, never before it and never by resemblance.
type Resolver struct {
	Store   Store
	Project string
}

// Resolve reads every variable one service declares.
func (r Resolver) Resolve(service Service) (Resolution, error) {
	resolution := Resolution{
		Values: map[string]Resolved{},
		Chains: map[string][]Candidate{},
	}
	for _, declared := range service.Env {
		value, chain, err := r.lookup(service, declared)
		if err != nil {
			return Resolution{}, err
		}
		resolution.Chains[declared] = chain
		if value.Value == "" {
			resolution.Missing = append(resolution.Missing, declared)
			continue
		}
		resolution.Values[declared] = value
	}
	return resolution, nil
}

func (r Resolver) lookup(service Service, declared string) (Resolved, []Candidate, error) {
	var chain []Candidate
	for _, name := range append([]string{declared}, service.Aliases[declared]...) {
		if value, ok := os.LookupEnv(name); ok && value != "" {
			chain = append(chain, Candidate{Source: "env", Name: name, Hit: true})
			return Resolved{Name: declared, Value: value, From: "env:" + name}, chain, nil
		}
		chain = append(chain, Candidate{Source: "env", Name: name})
		if r.Store == nil {
			continue
		}
		for _, key := range r.scopes(service, name) {
			value, ok, err := r.Store.Get(key)
			if err != nil {
				return Resolved{}, chain, err
			}
			if ok && value != "" {
				chain = append(chain, Candidate{Source: key.Scope(), Name: name, Hit: true})
				return Resolved{Name: declared, Value: value, From: key.Scope() + ":" + name}, chain, nil
			}
			chain = append(chain, Candidate{Source: key.Scope(), Name: name})
		}
		if note := r.refusedShared(service, name); note != "" {
			// A shared value exists but this service is not declared shared.
			// Guessing that it belongs to this project is exactly how an
			// unrelated project's database was reported green.
			chain = append(chain, Candidate{Source: "store/shared", Name: name, Note: note})
		}
	}
	return Resolved{Name: declared}, chain, nil
}

func (r Resolver) scopes(service Service, name string) []Key {
	if r.Project == "" {
		return []Key{Shared(name)}
	}
	keys := []Key{Scoped(r.Project, name)}
	if service.Shared {
		keys = append(keys, Shared(name))
	}
	return keys
}

// refusedShared reports the shared value this lookup declined to use, so the
// report can name it rather than leaving the operator to wonder why a
// credential that is plainly in the store did not resolve.
func (r Resolver) refusedShared(service Service, name string) string {
	if service.Shared || r.Project == "" || r.Store == nil {
		return ""
	}
	value, ok, err := r.Store.Get(Shared(name))
	if err != nil || !ok || value == "" {
		return ""
	}
	return fmt.Sprintf("present but not used: %s is not declared shared; run `cfo auth copy %s --to %s`", service.Name, name, r.Project)
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
