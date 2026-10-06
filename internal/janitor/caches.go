package janitor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fpresta0607/code-goblins/internal/disk"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/fleetconfig"
)

// cacheTool is one cache in the home's caches folder, the variable that points
// its tool there, and the tool's own prune, which removes what the tool no
// longer needs rather than deleting the cache blind.
type cacheTool struct {
	dir      string
	variable string
	prune    []string
}

// cacheTools are the caches the janitor can trim. Playwright's browsers have
// no prune of their own, so they are measured and never deleted.
var cacheTools = []cacheTool{
	{dir: "uv", variable: "UV_CACHE_DIR", prune: []string{"uv", "cache", "prune"}},
	{dir: "pnpm", variable: "npm_config_store_dir", prune: []string{"pnpm", "store", "prune"}},
	{dir: "npm", variable: "npm_config_cache", prune: []string{"npm", "cache", "verify"}},
	{dir: "go-build", variable: "GOCACHE", prune: []string{"go", "clean", "-cache"}},
	{dir: "go-mod", variable: "GOMODCACHE", prune: []string{"go", "clean", "-modcache"}},
}

// cacheTimeout bounds one tool's prune.
const cacheTimeout = 10 * time.Minute

// trimCaches brings the home's caches folder back under its cap: over it, each
// tool prunes its own cache, the one used longest ago first, until the folder
// is under the cap again or every tool has pruned.
func (cfg Config) trimCaches(ctx context.Context, record *Record) {
	root := cfg.Home.Caches()
	limit := int64(fleetconfig.Bytes(cfg.Settings.CachesCapGB))
	total := Size(root)
	if total <= limit {
		return
	}
	type used struct {
		tool cacheTool
		last time.Time
	}
	var order []used
	for _, tool := range cacheTools {
		if last, ok := lastUse(filepath.Join(root, tool.dir)); ok {
			order = append(order, used{tool, last})
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i].last.Before(order[j].last) })
	for _, entry := range order {
		if total <= limit {
			break
		}
		dir := filepath.Join(root, entry.tool.dir)
		before := Size(dir)
		bounded, cancel := context.WithTimeout(ctx, cacheTimeout)
		result, err := cfg.Commands.Run(bounded, execx.Request{
			Name:     entry.tool.prune[0],
			Args:     entry.tool.prune[1:],
			Env:      append(os.Environ(), entry.tool.variable+"="+dir),
			KillTree: true,
		})
		cancel()
		if err != nil || result.ExitCode != 0 {
			record.Notes = append(record.Notes, fmt.Sprintf("%s could not prune %s: %v %s", strings.Join(entry.tool.prune, " "), dir, err, strings.TrimSpace(string(result.Stderr))))
			continue
		}
		freed := before - Size(dir)
		total -= freed
		if freed > 0 {
			record.Removed = append(record.Removed, Item{Kind: "cache", Path: dir, Bytes: freed, Detail: strings.Join(entry.tool.prune, " ")})
		}
	}
	if total > limit {
		record.Notes = append(record.Notes, fmt.Sprintf("the caches hold %.1f GB, over their %.0f GB cap, after every tool's own prune; nothing is deleted blind", disk.GB(uint64(total)), cfg.Settings.CachesCapGB))
	}
}

// lastUse is the latest modification time among a cache's own entries, which
// is when its tool last wrote it.
func lastUse(dir string) (time.Time, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return time.Time{}, false
	}
	var last time.Time
	for _, entry := range entries {
		if info, err := entry.Info(); err == nil && info.ModTime().After(last) {
			last = info.ModTime()
		}
	}
	return last, true
}
