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
	// also its key in the credential store, so there is exactly one name to
	// know per credential.
	Env []string `json:"env,omitempty"`
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
	}
	return nil
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
