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
	{name: "CARGO_HOME", dir: "cargo"},
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

// CacheLine summarizes the redirects for the one-line spawn output. It names
// the variables and the root, never a credential: nothing here is a secret,
// which is why these values travel in the launch environment rather than in
// the restricted file the credentials use.
func CacheLine(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	sort.Strings(names)
	line := "caches: "
	for index, name := range names {
		if index > 0 {
			line += ", "
		}
		line += name
	}
	return line + " -> " + filepath.Dir(env[names[0]])
}
