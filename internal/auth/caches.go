package auth

import (
	"os"
	"path/filepath"
	"sort"
)

// CacheDirName is the shared package-cache root under the CFO home. One root
// with a subdirectory per ecosystem, because these locations are a property of
// the machine rather than of any project: a per-project manifest would have to
// repeat the same absolute path in every project that uses the ecosystem, and
// the first one to disagree would fragment the store it exists to share.
const CacheDirName = "caches"

// cacheVars are the environment redirects a goblin's pane inherits, in the
// shape PLAYWRIGHT_BROWSERS_PATH established: a pure redirect to a large
// content-addressed artifact with no absolute path baked into what it
// produces. That is why nothing here materializes an environment. A .venv or a
// node_modules shared between worktrees bakes absolute paths and compiled
// native artifacts, and fails as flaky tests rather than as an honest error,
// so a worktree always re-materializes its own against the shared store.
//
// CARGO_HOME is deliberately not here and must not be added: cargo has no
// cache-only variable, so redirecting it relocates config.toml and
// credentials.toml and bin/ as well, and a goblin would lose the operator's
// registry and linker configuration and fail a private fetch as an auth error
// far from its cause.
var cacheVars = []struct {
	name string
	dir  string
}{
	{name: "UV_CACHE_DIR", dir: "uv"},
	// pnpm reads its store location from the npm config environment rather
	// than from a variable of its own.
	{name: "npm_config_store_dir", dir: "pnpm"},
	{name: "PLAYWRIGHT_BROWSERS_PATH", dir: "playwright"},
	{name: "GOMODCACHE", dir: "go-mod"},
}

// CacheEnv returns the shared cache redirects for a CFO home. A variable the
// CFO's own environment already sets is left alone and reported as inherited:
// an operator who has already pointed an ecosystem at a tuned location has
// made the decision this function exists to make, and overriding it would
// strand whatever is already warm there.
//
// Nothing is created here. Every one of these tools creates its cache root on
// first write, so a directory made in advance would only be an empty one on
// every machine that never runs that ecosystem.
func CacheEnv(home string) map[string]string {
	if home == "" {
		return nil
	}
	root := filepath.Join(home, CacheDirName)
	env := map[string]string{}
	for _, cache := range cacheVars {
		if existing, set := os.LookupEnv(cache.name); set && existing != "" {
			continue
		}
		env[cache.name] = filepath.Join(root, cache.dir)
	}
	return env
}

// CacheRedirect is one ecosystem's cache location as an audit sees it.
type CacheRedirect struct {
	Name string
	Path string
	// Source names who decided this location, so an audit can tell the three
	// apart instead of reporting them all as one number.
	Source string
}

// Where a cache location came from, in the precedence the pane and the
// dependency install both apply.
const (
	// CacheSourceProject is a location the project's own worktree manifest
	// declares. It wins everywhere: provisioning merges the project's env
	// over the shared caches, and the launch treats a name the project
	// already set as taken.
	CacheSourceProject = "project"
	// CacheSourceCFO is the shared root under the CFO home, which is what
	// CacheEnv sets for everything nothing else claimed.
	CacheSourceCFO = "cfo"
	// CacheSourceInherited is a location the operator's own environment
	// already set, which CacheEnv leaves alone rather than overriding.
	CacheSourceInherited = "inherited"
)

// CacheAudit lists every cache variable and where that ecosystem actually
// builds, including the ones inherited or declared rather than set. It exists
// because CacheEnv returns only what the CFO itself sets, and an audit that
// showed only that would be silent about exactly the variables somebody
// tuned: absent, inherited, and project-declared would all read the same.
//
// projectEnv is the project's own worktree manifest env block, which is why
// this takes an argument the launch path does not. `cfo auth <project> --env`
// answers "where does THIS project's goblin build", and a project that
// declares a location overrides the shared root in both real consumers - the
// dependency install and the pane - so an audit that reported the machine
// value there would name a directory nothing uses.
//
// It is deliberately not what the launch path uses. CacheEnv still decides
// what a pane inherits from the CFO, so nothing here can hand a pane back a
// location the CFO never set.
func CacheAudit(home string, projectEnv map[string]string) []CacheRedirect {
	if home == "" {
		return nil
	}
	set := CacheEnv(home)
	audit := make([]CacheRedirect, 0, len(cacheVars))
	for _, cache := range cacheVars {
		if declared := projectEnv[cache.name]; declared != "" {
			audit = append(audit, CacheRedirect{Name: cache.name, Path: declared, Source: CacheSourceProject})
			continue
		}
		if path, redirected := set[cache.name]; redirected {
			audit = append(audit, CacheRedirect{Name: cache.name, Path: path, Source: CacheSourceCFO})
			continue
		}
		audit = append(audit, CacheRedirect{Name: cache.name, Path: os.Getenv(cache.name), Source: CacheSourceInherited})
	}
	sort.Slice(audit, func(i, j int) bool { return audit[i].Name < audit[j].Name })
	return audit
}
