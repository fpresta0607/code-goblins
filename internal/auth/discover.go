package auth

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/execx"
)

// envFileNames are the local secret files a project keeps outside git. They
// are read to adopt what is already there, never written.
var envFileNames = []string{".env", ".env.local", ".env.development"}

// discoverDepth bounds how far into a project the scan walks. Real .env files
// live at the root or one level down in a workspace package; walking a whole
// node_modules tree to find more is waste, not thoroughness.
const discoverDepth = 2

// Adopted records one credential that was found already present somewhere and
// registered in the store, so the Overlord is never asked for something the
// machine already has.
type Adopted struct {
	Name   string
	Origin string
}

// Discover registers credentials the machine already holds: values in a
// project's local .env files, and the GitHub token gh already owns. It only
// ever adopts names the manifest declares, and never overwrites a credential
// the store already has, so an adopted value cannot silently replace a
// deliberate one.
func Discover(ctx context.Context, store Store, runner execx.Runner, manifest Manifest, projectDir string) ([]Adopted, error) {
	if store == nil {
		return nil, fmt.Errorf("auth: no credential store configured")
	}
	wanted := map[string]bool{}
	for _, name := range manifest.EnvNames() {
		wanted[name] = true
	}
	if len(wanted) == 0 {
		return nil, nil
	}

	var adopted []Adopted
	adopt := func(name, value, origin string) error {
		if !wanted[name] || strings.TrimSpace(value) == "" {
			return nil
		}
		existing, found, err := store.Get(name)
		if err != nil {
			return err
		}
		if found && existing != "" {
			return nil
		}
		if err := store.Set(name, value); err != nil {
			return err
		}
		adopted = append(adopted, Adopted{Name: name, Origin: origin})
		return nil
	}

	for _, path := range envFiles(projectDir) {
		values, err := ParseEnvFile(path)
		if err != nil {
			continue
		}
		names := make([]string, 0, len(values))
		for name := range values {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if err := adopt(name, values[name], path); err != nil {
				return adopted, err
			}
		}
	}

	if wanted["GITHUB_TOKEN"] && runner != nil {
		result, err := runner.Run(ctx, execx.Request{Name: "gh", Args: []string{"auth", "token"}})
		if err == nil && result.ExitCode == 0 {
			token := strings.TrimSpace(string(result.Stdout))
			if err := adopt("GITHUB_TOKEN", token, "gh auth token"); err != nil {
				return adopted, err
			}
		}
	}
	return adopted, nil
}

// envFiles lists the local secret files under a project, bounded in depth and
// skipping dependency and version-control directories.
func envFiles(projectDir string) []string {
	if projectDir == "" {
		return nil
	}
	var found []string
	root := filepath.Clean(projectDir)
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		depth := len(strings.Split(filepath.ToSlash(relative), "/"))
		if entry.IsDir() {
			switch entry.Name() {
			case "node_modules", ".git", "dist", "build", "vendor", ".venv":
				return filepath.SkipDir
			}
			if relative != "." && depth > discoverDepth {
				return filepath.SkipDir
			}
			return nil
		}
		for _, name := range envFileNames {
			if entry.Name() == name {
				found = append(found, path)
			}
		}
		return nil
	})
	sort.Strings(found)
	return found
}

// ParseEnvFile reads KEY=VALUE lines. It accepts the shapes real .env files
// use - comments, blank lines, `export ` prefixes, and quoted values - and
// ignores anything else rather than guessing.
func ParseEnvFile(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	values := map[string]string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		name, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		name = strings.TrimSpace(name)
		if !ValidEnvName(name) {
			continue
		}
		values[name] = unquote(strings.TrimSpace(value))
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return values, nil
}

func unquote(value string) string {
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}
