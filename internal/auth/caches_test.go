package auth

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// paneCacheEnvironment is this process's environment as a goblin's pane
// carries it: the cache redirects under root, and no npm or pnpm setting of
// the shell running the test, which may itself be a pane an older cfo set up.
func paneCacheEnvironment(t *testing.T, root string) []string {
	t.Helper()
	for _, cache := range cacheVars {
		clearEnv(t, cache.name)
	}
	var environment []string
	for _, entry := range os.Environ() {
		name := strings.ToLower(strings.SplitN(entry, "=", 2)[0])
		if strings.HasPrefix(name, "npm_config_") || strings.HasPrefix(name, "pnpm_config_") {
			continue
		}
		environment = append(environment, entry)
	}
	for name, value := range CacheEnv(root) {
		environment = append(environment, name+"="+value)
	}
	return environment
}

func runInPane(t *testing.T, environment []string, program string, args ...string) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, program, args...)
	command.Dir = t.TempDir()
	command.Env = environment
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("%s %s: %v\n%s", program, strings.Join(args, " "), err, stderr.String())
	}
	return strings.TrimSpace(stdout.String()), stderr.String()
}

// npm refuses an npm_config_ variable it does not know in its next major, so a
// cache redirect npm reads but does not know would fail every npm and npx
// command a goblin runs, where npm 11 only warns.
func TestNpmKnowsEveryCacheVariableAPaneCarries(t *testing.T) {
	npm, err := exec.LookPath("npm")
	if err != nil {
		t.Skip("a machine with no npm on PATH runs no npm command in a pane")
	}
	environment := paneCacheEnvironment(t, filepath.Join(t.TempDir(), "caches"))

	_, stderr := runInPane(t, environment, npm, "--version")

	if strings.Contains(stderr, "Unknown env config") {
		t.Errorf("npm warned about the pane's cache environment:\n%s", stderr)
	}
}

func TestPnpmStoreResolvesInsideTheSharedCaches(t *testing.T) {
	pnpm, err := exec.LookPath("pnpm")
	if err != nil {
		t.Skip("a machine with no pnpm on PATH has no pnpm store to share")
	}
	root := filepath.Join(t.TempDir(), "caches")
	environment := paneCacheEnvironment(t, root)
	version, _ := runInPane(t, environment, pnpm, "--version")
	if major, err := strconv.Atoi(strings.SplitN(version, ".", 2)[0]); err != nil || major < 11 {
		t.Skipf("pnpm %s reads its store only from npm_config_ variables, which npm refuses", version)
	}

	store, _ := runInPane(t, environment, pnpm, "store", "path")

	if want := filepath.Join(root, "pnpm"); !strings.EqualFold(filepath.Dir(store), want) {
		t.Errorf("pnpm store path = %q, want a store under %q", store, want)
	}
}
