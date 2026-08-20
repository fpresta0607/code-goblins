// Package auth makes per-project authentication automatic: a declarative
// manifest per project, a cheap probe per service, a credential store outside
// every repository, and the resolved environment a goblin's pane needs before
// its harness starts.
package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ManifestDirName is the directory under the CFO home's data/ that holds one
// subdirectory per project.
const ManifestDirName = "projects"

// ManifestFileName is the per-project manifest.
const ManifestFileName = "auth.json"

// Method names how a service is authenticated. The distinction drives what
// --fix is allowed to attempt, not how the credential is read.
const (
	// MethodEnv is a credential that is only ever an environment variable:
	// hydrating it from the store is the whole of authentication.
	MethodEnv = "env"
	// MethodCLI is a tool with a non-interactive login that accepts a stored
	// key, so --fix can authenticate it without a human.
	MethodCLI = "cli"
	// MethodOAuth is a browser handshake. --fix may attempt it, but a human
	// is the fallback.
	MethodOAuth = "oauth"
)

// Service is one authenticated dependency of a project.
type Service struct {
	// Name identifies the service in reports and is the manifest's key.
	Name string `json:"name"`
	// Method is env, cli, or oauth.
	Method string `json:"method"`
	// Env lists the environment variables this service needs. Each name is
	// also its credential-store name, resolved inside this project's scope.
	Env []string `json:"env,omitempty"`
	// Shared lets this service read the store's shared scope when the
	// project scope has nothing. It is opt-in because a credential that
	// differs per project (DATABASE_URL is the standing example) must never
	// be answered from a scope that cannot say which project it belongs to.
	Shared bool `json:"shared,omitempty"`
	// Aliases maps a declared environment name to the stored names that may
	// satisfy it, tried in the order given after the declared name itself.
	// Only an explicit declaration counts; nothing is ever fuzzy-matched.
	Aliases map[string][]string `json:"aliases,omitempty"`
	// Identity proves the resolved credential points at this project's
	// instance. A service without one stays liveness-only and the report
	// says so rather than implying more than it verified.
	Identity *Identity `json:"identity,omitempty"`
	// Probe is a cheap command that exits zero only when the service is
	// genuinely reachable and authenticated. A service with no probe is
	// green as soon as every Env name resolves.
	Probe []string `json:"probe,omitempty"`
	// Login is a non-interactive authentication command --fix may run. Its
	// arguments may reference resolved values as $NAME.
	Login []string `json:"login,omitempty"`
	// URL is where a human authenticates when nothing automatic worked. It
	// is also the page the browser fallback drives.
	URL string `json:"url,omitempty"`
	// Confirm lists button labels the browser fallback clicks when an OAuth
	// page is already signed in and only needs approval.
	Confirm []string `json:"confirm,omitempty"`
	// Optional keeps a service a project can run without out of the red
	// column.
	Optional bool `json:"optional,omitempty"`
	// Note explains a service whose purpose is not obvious from its name.
	Note string `json:"note,omitempty"`
}

// Identity is the check that separates "this project's instance" from "an
// instance". Exactly one of Command or Var is declared.
type Identity struct {
	// Command runs with the resolved credentials and must print Expect. A
	// $NAME in it is substituted the same way a probe's arguments are.
	Command []string `json:"command,omitempty"`
	// Var names a resolved variable whose value must contain Expect. It is
	// the identity check that needs no tool installed: a connection string
	// naming a different host is a different instance, and matching it never
	// discloses the value.
	Var string `json:"var,omitempty"`
	// Expect is the literal that must be present, matched case-insensitively.
	Expect string `json:"expect"`
	// Note names what a match proves, for the report.
	Note string `json:"note,omitempty"`
}

// Describe names the identity check in a report without disclosing anything
// beyond what the manifest already declares in plain text.
func (i Identity) Describe() string {
	if i.Note != "" {
		return i.Note
	}
	if i.Var != "" {
		return i.Var + " contains " + i.Expect
	}
	if len(i.Command) > 0 {
		return i.Command[0] + " reports " + i.Expect
	}
	return i.Expect
}

// Manifest is one project's declared authentication surface.
type Manifest struct {
	// Project is the project directory name this manifest describes.
	Project string `json:"project"`
	// Services are reported in manifest order, so the author controls what a
	// reader sees first.
	Services []Service `json:"services"`
	// Path is where the manifest was loaded from. It is not serialized.
	Path string `json:"-"`
}

// ManifestPath returns the manifest location for a project directory under a
// CFO home's data directory.
func ManifestPath(dataDir, project string) string {
	return filepath.Join(dataDir, ManifestDirName, ProjectName(project), ManifestFileName)
}

// ProjectName reduces a project path to the directory name manifests are
// keyed by, so `projects/clock-in`, an absolute path, and a trailing separator
// all name the same project.
func ProjectName(project string) string {
	cleaned := strings.TrimRight(filepath.ToSlash(strings.TrimSpace(project)), "/")
	if cleaned == "" {
		return ""
	}
	return filepath.Base(filepath.FromSlash(cleaned))
}

// LoadManifest reads and validates one project's manifest.
func LoadManifest(dataDir, project string) (Manifest, error) {
	path := ManifestPath(dataDir, project)
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, err
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("auth: %s: %w", path, err)
	}
	manifest.Path = path
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("auth: %s: %w", path, err)
	}
	return manifest, nil
}

// Validate refuses a manifest that would report a misleading status: a
// nameless service, an unknown method, a duplicate name, or an environment
// name that cannot be exported into a pane shell.
func (m Manifest) Validate() error {
	seen := map[string]bool{}
	for index, service := range m.Services {
		if strings.TrimSpace(service.Name) == "" {
			return fmt.Errorf("service %d has no name", index)
		}
		if seen[service.Name] {
			return fmt.Errorf("service %q is declared twice", service.Name)
		}
		seen[service.Name] = true
		switch service.Method {
		case MethodEnv, MethodCLI, MethodOAuth:
		default:
			return fmt.Errorf("service %q has unknown method %q", service.Name, service.Method)
		}
		for _, name := range service.Env {
			if !ValidEnvName(name) {
				return fmt.Errorf("service %q declares invalid environment name %q", service.Name, name)
			}
		}
		if service.Method == MethodEnv && len(service.Env) == 0 {
			return fmt.Errorf("service %q uses method env but declares no environment variables", service.Name)
		}
		if err := validateAliases(service); err != nil {
			return err
		}
		if err := validateIdentity(service); err != nil {
			return err
		}
	}
	return nil
}

// validateAliases refuses an alias that names something the service does not
// declare, because it would silently never be consulted, and an alias target
// that could not be a credential name.
func validateAliases(service Service) error {
	declared := map[string]bool{}
	for _, name := range service.Env {
		declared[name] = true
	}
	for name, aliases := range service.Aliases {
		if !declared[name] {
			return fmt.Errorf("service %q aliases %q, which it does not declare in env", service.Name, name)
		}
		for _, alias := range aliases {
			if !ValidEnvName(alias) {
				return fmt.Errorf("service %q declares invalid alias %q for %q", service.Name, alias, name)
			}
		}
	}
	return nil
}

// validateIdentity refuses an identity check that cannot establish anything:
// no expectation, no way to look, or two ways at once.
func validateIdentity(service Service) error {
	identity := service.Identity
	if identity == nil {
		return nil
	}
	if strings.TrimSpace(identity.Expect) == "" {
		return fmt.Errorf("service %q declares an identity check with no expect value", service.Name)
	}
	hasCommand := len(identity.Command) > 0
	hasVar := strings.TrimSpace(identity.Var) != ""
	if hasCommand == hasVar {
		return fmt.Errorf("service %q must declare exactly one of identity.command or identity.var", service.Name)
	}
	if hasVar {
		declared := false
		for _, name := range service.Env {
			if name == identity.Var {
				declared = true
			}
		}
		if !declared {
			return fmt.Errorf("service %q identity checks %q, which it does not declare in env", service.Name, identity.Var)
		}
	}
	return nil
}

// ValidProjectName reports whether a project name is safe as a credential
// scope. The scope becomes a directory name in the file store and part of a
// vault target, so it is restricted to characters that cannot traverse or
// collide.
func ValidProjectName(project string) bool {
	if project == "" {
		return false
	}
	for _, character := range project {
		switch {
		case character == '-' || character == '_':
		case character >= 'A' && character <= 'Z':
		case character >= 'a' && character <= 'z':
		case character >= '0' && character <= '9':
		default:
			return false
		}
	}
	return true
}

// EnvNames returns every environment variable the manifest declares, sorted
// and deduplicated.
func (m Manifest) EnvNames() []string {
	seen := map[string]bool{}
	var names []string
	for _, service := range m.Services {
		for _, name := range service.Env {
			if !seen[name] {
				seen[name] = true
				names = append(names, name)
			}
		}
	}
	sort.Strings(names)
	return names
}

// ValidEnvName reports whether a name is exportable as an environment
// variable. It matches the harness package's pane-shell rule so a manifest
// can never declare a name that would break the launch prefix.
func ValidEnvName(name string) bool {
	if name == "" {
		return false
	}
	for index, character := range name {
		switch {
		case character == '_':
		case character >= 'A' && character <= 'Z':
		case character >= 'a' && character <= 'z':
		case index > 0 && character >= '0' && character <= '9':
		default:
			return false
		}
	}
	return true
}
