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
	// Inherited marks a location the operator's own environment already set,
	// which CacheEnv leaves alone rather than overriding.
	Inherited bool
}

// CacheAudit lists every cache variable and where that ecosystem actually
// builds, including the ones inherited rather than set. It exists because
// CacheEnv returns only what the pane receives, and an audit that showed only
// that would be silent about exactly the variable an operator tuned: absent
// and inherited would read the same.
//
// It is deliberately not what the launch path uses. CacheEnv still decides
// what a pane inherits from the CFO, so nothing here can hand a pane back a
// location the CFO never set.
func CacheAudit(home string) []CacheRedirect {
	if home == "" {
		return nil
	}
	set := CacheEnv(home)
	audit := make([]CacheRedirect, 0, len(cacheVars))
	for _, cache := range cacheVars {
		if path, redirected := set[cache.name]; redirected {
			audit = append(audit, CacheRedirect{Name: cache.name, Path: path})
			continue
		}
		audit = append(audit, CacheRedirect{Name: cache.name, Path: os.Getenv(cache.name), Inherited: true})
	}
	sort.Slice(audit, func(i, j int) bool { return audit[i].Name < audit[j].Name })
	return audit
}
