package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// Providers this report can name precisely, because the checkout commits a
// manifest that says so.
const (
	ProviderVercel   = "vercel"
	ProviderFly      = "fly"
	ProviderSupabase = "supabase"
)

// hostingServices are the credential-manifest service names that denote a
// place work is deployed to, rather than a service the app merely talks to.
// It is an explicit list because the distinction is not derivable: a project
// declares postgres and openrouter the same way it declares fly and ionos,
// and only a human knows which of them is a destination.
//
// A service outside this list still surfaces when its own note says it is a
// deploy target, which is how a provider nobody has met yet still appears.
var hostingServices = map[string]bool{
	"vercel": true, "fly": true, "railway": true, "render": true,
	"netlify": true, "heroku": true, "supabase": true, "supabase-mgmt": true,
	"ionos": true, "hetzner": true, "digitalocean": true, "linode": true,
	"cloudflare": true, "neon": true, "aws": true, "gcp": true, "azure": true,
}

// ReadProject reads one checkout's deploy targets and local stack commands
// from the manifests the project itself commits, plus the project's
// credential manifest in the CFO's data directory.
//
// Nothing here is inferred from a project's name or shape. Every fact the
// report prints carries the file it came from, so a wrong answer is traceable
// to the manifest that declared it rather than to this command.
// It returns a warning naming any manifest it found but could not read. A
// malformed credential manifest would otherwise drop a project's deploy
// targets silently, and a project that looks like it deploys nowhere is worse
// than one that says its manifest is broken.
func ReadProject(dataDir, name, path string) (Project, string) {
	project := Project{Name: name, Path: path}
	project.Targets, project.Stacks = readManifests(path)
	credentials, warning := readCredentialTargets(dataDir, name)
	project.Targets = append(project.Targets, credentials...)
	sort.SliceStable(project.Targets, func(i, j int) bool {
		return project.Targets[i].Provider < project.Targets[j].Provider
	})
	project.Targets = mergeTargets(project.Targets)
	project.Local = readLocal(dataDir, name, path)
	return project, warning
}

// readManifests reads the deploy and stack facts a checkout commits.
func readManifests(path string) ([]Target, []StackName) {
	var targets []Target
	var stacks []StackName

	if source := filepath.Join(path, ".vercel", "project.json"); exists(source) {
		var row struct {
			ProjectName string `json:"projectName"`
			OrgID       string `json:"orgId"`
		}
		if readJSON(source, &row) == nil && row.ProjectName != "" {
			detail := "project " + row.ProjectName
			if row.OrgID != "" {
				detail += ", scope " + row.OrgID
			}
			targets = append(targets, Target{Provider: ProviderVercel, Detail: detail, Source: source})
		}
	}

	if source := filepath.Join(path, "fly.toml"); exists(source) {
		app := tomlValue(source, "app")
		region := tomlValue(source, "primary_region")
		if app != "" {
			detail := "app " + app
			if region != "" {
				detail += ", primary region " + region
			}
			targets = append(targets, Target{Provider: ProviderFly, Detail: detail, Source: source})
		}
	}

	if source := filepath.Join(path, "supabase", "config.toml"); exists(source) {
		if ref := tomlValue(source, "project_id"); ref != "" {
			targets = append(targets, Target{Provider: ProviderSupabase, Detail: "project ref " + ref, Source: source})
			// The Supabase CLI names the local stack after this same value,
			// which makes it the project's own claim on that stack wherever
			// the CLI was run from.
			stacks = append(stacks, StackName{Name: ref, Source: source})
		}
	}

	for _, candidate := range []string{"docker-compose.yml", "docker-compose.yaml"} {
		source := filepath.Join(path, candidate)
		if !exists(source) {
			continue
		}
		if name := composeName(source); name != "" {
			stacks = append(stacks, StackName{Name: name, Source: source})
		}
		break
	}
	return targets, stacks
}

// readCredentialTargets surfaces the deploy notes the credential manifests
// already carry. They hold what no checkout manifest does - which VPS, which
// Vercel scope by name, which account owns the token - and leaving them there
// is what makes every deploy question a rediscovery.
func readCredentialTargets(dataDir, name string) ([]Target, string) {
	source := auth.ManifestPath(dataDir, name)
	manifest, err := auth.LoadManifest(dataDir, name)
	if err != nil {
		if os.IsNotExist(err) {
			// A project with no credential manifest simply has none.
			return nil, ""
		}
		return nil, "DEPLOY TARGETS UNREADABLE for " + name + ": " + err.Error() + " - fix the manifest, or this project reads as deploying nowhere"
	}
	var targets []Target
	for _, service := range manifest.Services {
		if !hostingServices[strings.ToLower(service.Name)] && !strings.Contains(strings.ToLower(service.Note), "deploy") {
			continue
		}
		targets = append(targets, Target{Provider: strings.ToLower(service.Name), Source: source, Note: service.Note})
	}
	return targets, ""
}

// mergeTargets folds a credential note into the checkout manifest's fact for
// the same provider, so Vercel is one row carrying both the project id and
// the note about how it must be deployed, not two rows telling half a story
// each.
func mergeTargets(targets []Target) []Target {
	merged := make([]Target, 0, len(targets))
	for _, target := range targets {
		index := -1
		for position, existing := range merged {
			if existing.Provider == target.Provider {
				index = position
				break
			}
		}
		if index < 0 {
			merged = append(merged, target)
			continue
		}
		if merged[index].Detail == "" {
			merged[index].Detail = target.Detail
			merged[index].Source = target.Source
		}
		if merged[index].Note == "" {
			merged[index].Note = target.Note
		}
	}
	return merged
}

// readLocal answers "how do I test this locally" once, so the CFO stops
// reading the repository for it.
//
// A project that declares the commands in its worktree manifest is taken at
// its word. Everything else is derived from what the checkout actually holds,
// and says so: a derived answer is a good guess from real evidence, and it
// must never be presented as the project's own declaration.
func readLocal(dataDir, name, path string) Local {
	if manifest, err := worktree.Resolve(dataDir, name); err == nil && len(manifest.Local.Up) > 0 {
		source := manifest.Path
		if source == "" {
			source = worktree.ManifestPath(dataDir, name)
		}
		return Local{Up: manifest.Local.Up, Down: manifest.Local.Down, Source: source, Declared: true}
	}

	if source := filepath.Join(path, "supabase", "config.toml"); exists(source) {
		return Local{
			Up:     []string{"npx supabase start"},
			Down:   []string{"npx supabase stop"},
			Source: source,
		}
	}
	for _, candidate := range []string{"docker-compose.yml", "docker-compose.yaml", "docker-compose.dev.yml"} {
		source := filepath.Join(path, candidate)
		if !exists(source) {
			continue
		}
		return Local{
			Up:     []string{"docker compose -f " + candidate + " up -d"},
			Down:   []string{"docker compose -f " + candidate + " down"},
			Source: source,
		}
	}
	if source := filepath.Join(path, "package.json"); exists(source) {
		var row struct {
			Scripts map[string]string `json:"scripts"`
		}
		if readJSON(source, &row) == nil {
			for _, script := range []string{"dev", "start", "serve"} {
				if _, ok := row.Scripts[script]; ok {
					return Local{
						Up:     []string{"npm run " + script},
						Down:   []string{"stop the process (cfo runtime names its pid)"},
						Source: source,
					}
				}
			}
		}
	}
	return Local{}
}

// composeName reads a compose file's top-level `name:`, the only line in it
// that names the stack. A compose file without one takes its project name
// from whatever directory it was run in, which is not a claim the project
// made, so nothing is returned for it.
//
// ponytail: one line, not a YAML parse. A top-level key is column zero, which
// is the whole rule; add a parser if a project ever needs more of this file.
func composeName(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimRight(line, "\r")
		if strings.HasPrefix(trimmed, " ") || strings.HasPrefix(trimmed, "\t") {
			continue
		}
		if value, ok := strings.CutPrefix(trimmed, "name:"); ok {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}

// tomlValue reads one top-level `key = "value"` out of a TOML file.
//
// ponytail: no TOML parser for three scalars at the top of two files. A key
// inside a [table] is skipped, so a later `app` under some section cannot be
// mistaken for fly.toml's own.
func tomlValue(path, key string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
		if strings.HasPrefix(trimmed, "[") {
			// A table has begun; every later key belongs to it.
			return ""
		}
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		name, value, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimSpace(name) != key {
			continue
		}
		value = strings.TrimSpace(value)
		if index := strings.Index(value, " #"); index >= 0 {
			value = strings.TrimSpace(value[:index])
		}
		return strings.Trim(value, `"'`)
	}
	return ""
}

func readJSON(path string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("runtime: %s: %w", path, err)
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
