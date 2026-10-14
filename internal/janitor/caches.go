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
	"github.com/fpresta0607/code-goblins/internal/verify"
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
// is under the cap again or every tool has pruned. A prune can delete a
// whole cache, as go clean -cache does, so each starts only once the machine's
// heavy line is free, and runs in a turn of that line (HeavyLine.Wrap), which
// no run that waits its turn builds beside. The trim stops at a line that is
// not free and leaves the rest to the next sweep.
func (cfg Config) trimCaches(ctx context.Context, record *Record) {
	root := cfg.Home.Caches()
	limit := int64(fleetconfig.Bytes(cfg.Settings.CachesCapGB))
	total, isWhole := sizeOf(root)
	if !isWhole {
		record.Notes = append(record.Notes, fmt.Sprintf("the caches could not all be read, so the %.1f GB they hold against their %.0f GB cap is only what could be counted", disk.GB(uint64(total)), cfg.Settings.CachesCapGB))
	}
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
		if err := cfg.lineFree(); err != nil {
			record.Notes = append(record.Notes, fmt.Sprintf("the caches hold %.1f GB, over their %.0f GB cap, and are not trimmed further, as the machine's heavy line is not free: %v", disk.GB(uint64(total)), cfg.Settings.CachesCapGB, err))
			return
		}
		dir := filepath.Join(root, entry.tool.dir)
		before := Size(dir)
		command := cfg.Line.Wrap("the janitor, pruning the "+entry.tool.dir+" cache", entry.tool.prune)
		bounded, cancel := context.WithTimeout(ctx, cacheTimeout)
		result, err := cfg.Commands.Run(bounded, execx.Request{
			Name:     command[0],
			Args:     command[1:],
			Env:      append(outsideATurn(os.Environ()), entry.tool.variable+"="+dir),
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

// outsideATurn is environment without the variable that names a turn of the
// machine's heavy line to a command run in it, so a prune's own turn is taken
// whatever the sweep's process was started inside.
func outsideATurn(environment []string) []string {
	var kept []string
	for _, entry := range environment {
		if name, _, _ := strings.Cut(entry, "="); !strings.EqualFold(name, verify.TurnVariable) {
			kept = append(kept, entry)
		}
	}
	return kept
}
