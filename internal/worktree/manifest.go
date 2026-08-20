package worktree

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

// ManifestFileName is one project's worktree provisioning declaration under
// the CFO home's data directory, beside its auth manifest.
const ManifestFileName = "worktree.json"

// Dependency strategies a project can declare.
const (
	// StrategyInstall re-materializes dependencies per worktree with the
	// project's own installer against the shared per-user package cache
	// (pnpm store, uv cache, npm cache). Correct by construction: a goblin's
	// `pnpm add` mutates only its own worktree.
	StrategyInstall = "install"
	// StrategyLink junctions the declared dependency directories from the
	// primary checkout. Zero seconds and zero disk, but a package-manager run
	// inside one worktree mutates every worktree's dependencies at once.
	StrategyLink = "link"
	// StrategyNone provisions no dependencies at all.
	StrategyNone = "none"
)

// Dependencies is one project's declared dependency provisioning.
type Dependencies struct {
	// Strategy is install (the default), link, or none.
	Strategy string `json:"strategy,omitempty"`
	// Install overrides the detected install commands for strategy install,
	// for projects whose bootstrap is not the lockfile default. Each entry is
	// one command line run in order in the worktree, so a project whose
	// lockfile command does not materialize its environment (a uv.lock without
	// a [project] table) can declare ["uv venv", "uv pip install -r ..."].
	Install []string `json:"install,omitempty"`
	// Paths lists the dependency directories strategy link junctions from
	// the primary checkout.
	Paths []string `json:"paths,omitempty"`
}

// Manifest is one project's declared worktree environment.
type Manifest struct {
	// Project is the project directory name this manifest describes.
	Project string `json:"project"`
	// Link names primary-checkout config files or directories a worktree
	// shares by hardlink (files) or junction (directories): identical by
	// definition, same repo, same machine. When the manifest is absent the
	// defaults are .env, .env.local, and .env.docker.local. .mcp.json is
	// never linked - goblins receive its token-authenticated subset,
	// materialized fresh (see mcp.go).
	Link []string `json:"link,omitempty"`
	Dependencies Dependencies `json:"dependencies,omitempty"`
	// Env carries environment redirects into the goblin's pane, for large
	// read-only caches with no baked absolute paths (PLAYWRIGHT_BROWSERS_PATH
	// and friends).
	Env map[string]string `json:"env,omitempty"`
	// Path is where the manifest was loaded from. It is not serialized.
	Path string `json:"-"`
}

// defaultLink is the config-file set an undeclared project shares.
var defaultLink = []string{".env", ".env.local", ".env.docker.local"}

// ManifestPath returns the worktree manifest location for a project directory
// under a CFO home's data directory.
func ManifestPath(dataDir, project string) string {
	return filepath.Join(dataDir, "projects", auth.ProjectName(project), ManifestFileName)
}

// Resolve reads one project's worktree manifest and applies the defaults for
// anything it does not declare. A missing manifest is not an error: the
// defaults (share existing .env files, install against the shared package
// cache) are the right treatment for a project that never asked for anything.
func Resolve(dataDir, project string) (Manifest, error) {
	manifest := Manifest{}
	path := ManifestPath(dataDir, project)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(data, &manifest); err != nil {
			return Manifest{}, fmt.Errorf("worktree: %s: %w", path, err)
		}
		manifest.Path = path
	case errors.Is(err, os.ErrNotExist):
	default:
		return Manifest{}, fmt.Errorf("worktree: read manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, fmt.Errorf("worktree: %s: %w", path, err)
	}
	if manifest.Link == nil {
		manifest.Link = defaultLink
	}
	if manifest.Dependencies.Strategy == "" {
		manifest.Dependencies.Strategy = StrategyInstall
	}
	return manifest, nil
}

// Validate refuses a manifest that would provision a misleading or unsafe
// environment: an unknown strategy, a link path that escapes the checkout, or
// an environment variable without a name.
func (m Manifest) Validate() error {
	switch m.Dependencies.Strategy {
	case "", StrategyInstall, StrategyLink, StrategyNone:
	default:
		return fmt.Errorf("unknown dependency strategy %q (install, link, or none)", m.Dependencies.Strategy)
	}
	for _, path := range m.Link {
		if err := validRelativePath("link", path); err != nil {
			return err
		}
	}
	for _, path := range m.Dependencies.Paths {
		if err := validRelativePath("dependency path", path); err != nil {
			return err
		}
	}
	for name := range m.Env {
		if strings.TrimSpace(name) == "" {
			return errors.New("env redirect with an empty name")
		}
	}
	return nil
}

// validRelativePath refuses absolute paths, parent escapes, and nested paths:
// provisioning links root-level entries only, because root level is where a
// project's environment lives and where Return's junction scan looks.
func validRelativePath(kind, path string) error {
	cleaned := filepath.Clean(filepath.FromSlash(strings.TrimSpace(path)))
	if cleaned == "." || cleaned == ".." || filepath.IsAbs(cleaned) || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s %q must be a root-level name inside the checkout", kind, path)
	}
	if filepath.Base(cleaned) != cleaned {
		return fmt.Errorf("%s %q must be a root-level name inside the checkout", kind, path)
	}
	return nil
}
