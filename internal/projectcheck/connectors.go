package projectcheck

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
)

// connectors assesses the services and connectors the project declares in
// auth.json against what its repository names: a declared one nothing uses,
// and a credential the code or an MCP connector reads that no service
// declares. It reads names only and probes nothing: `cfo auth <project>
// --check` is what asks each service whether it answers.
func (c *checker) connectors(ctx context.Context) error {
	file := auth.ManifestPath(c.DataDir, c.project)
	manifest, err := auth.LoadManifest(c.DataDir, c.project)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.add(AreaConnectors, "auth-missing", Medium,
			"the project declares no services, so a spawn carries no credential into a goblin's terminal and preflights nothing",
			"no file at "+file, "write "+file+" naming each service the project needs")
	case err != nil:
		c.add(AreaConnectors, "auth-invalid", High,
			"the loader refuses the project's auth manifest, so cfo auth and every spawn's preflight fail",
			err.Error(), "correct what the loader names in "+file)
		return nil
	}
	needs, sources, err := c.credentialNeeds(ctx)
	if err != nil {
		return err
	}
	declared := manifest.CredentialChains()
	var undeclared []string
	for _, name := range sortedKeys(needs) {
		if _, ok := declared[name]; !ok {
			undeclared = append(undeclared, name+" ("+needs[name]+")")
		}
	}
	if len(undeclared) > 0 {
		c.add(AreaConnectors, "connector-undeclared", Medium,
			fmt.Sprintf("the repository reads %d credentials no service declares, so no goblin's terminal carries them and no preflight checks them", len(undeclared)),
			strings.Join(undeclared, ", ")+". Read at "+c.repo.at(),
			"declare each in a service of "+file+", or say in the project's instructions why the code needs none of them from the fleet")
	}

	var names []string
	for _, service := range manifest.Services {
		names = append(names, serviceNames(service)...)
	}
	seen, err := c.repo.namesIn(ctx, names)
	if err != nil {
		return err
	}
	var unused, unjudged []string
	for _, service := range manifest.Services {
		own := serviceNames(service)
		if len(own) == 0 {
			unjudged = append(unjudged, service.Name)
			continue
		}
		used := false
		for _, name := range own {
			used = used || seen[strings.ToUpper(name)]
		}
		if !used {
			unused = append(unused, service.Name+": "+strings.Join(own, ", ")+" is in no file tracked at "+c.repo.at()+" but documents and tests")
		}
	}
	if len(unused) > 0 {
		c.add(AreaConnectors, "connector-unused", Low,
			fmt.Sprintf("%d declared services are used by nothing in the repository, so their credentials ride in every goblin's terminal for no reader", len(unused)),
			strings.Join(unused, ". "),
			"take each out of "+file+", or keep it and say in its note what outside the repository uses it")
	}
	evidence := fmt.Sprintf("git grep at %s for each declared name in any letter case, outside documents and tests. Credential names came from %s", c.repo.at(), sources)
	if len(unjudged) > 0 {
		evidence += ". Not judged, since they declare no variable to look for: " + strings.Join(unjudged, ", ")
	}
	c.add(AreaConnectors, "connectors-examined", OK,
		fmt.Sprintf("examined %d services and %d credential names the repository reads", len(manifest.Services), len(needs)), evidence, "")
	return nil
}

// serviceNames are the variables a service is read under: those it declares
// and their aliases.
func serviceNames(service auth.Service) []string {
	names := append([]string{}, service.Env...)
	for _, declared := range service.Env {
		names = append(names, service.Aliases[declared]...)
	}
	return names
}

// namesIn returns which of names, upper-cased, a file tracked at the default
// branch holds as a whole word in any letter case. Documents and tests do not
// count: a name only a guide or a fixture mentions is one no code reads.
func (r *repository) namesIn(ctx context.Context, names []string) (map[string]bool, error) {
	found := map[string]bool{}
	if len(names) == 0 {
		return found, nil
	}
	args := []string{"grep", "-I", "-i", "-w", "-o", "-F"}
	for _, name := range names {
		args = append(args, "-e", name)
	}
	// Exit 1 is git saying nothing matched.
	out, code, err := r.git(ctx, append(args, r.ref, "--", ".")...)
	if err != nil || (code != 0 && code != 1) {
		return nil, fmt.Errorf("projectcheck: git grep did not answer in %s", r.dir)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimPrefix(strings.TrimRight(line, "\r"), r.ref+":")
		// A name has no colon in it, so the last one ends the path.
		if cut := strings.LastIndex(line, ":"); cut > 0 && !isTestOrDocument(line[:cut]) {
			found[strings.ToUpper(line[cut+1:])] = true
		}
	}
	return found, nil
}

// environmentRead matches the ways source code reads an environment
// variable by name, in the extended syntax git grep takes. The name is what
// each match ends with.
const environmentRead = `(os\.environ(\.get)?[[(] *["'][A-Za-z_][A-Za-z0-9_]*` +
	`|os\.getenv\( *["'][A-Za-z_][A-Za-z0-9_]*` +
	`|os\.(Getenv|LookupEnv)\("[A-Za-z_][A-Za-z0-9_]*` +
	`|process\.env\.[A-Za-z_][A-Za-z0-9_]*` +
	`|process\.env\[["'][A-Za-z_][A-Za-z0-9_]*` +
	`|import\.meta\.env\.[A-Za-z_][A-Za-z0-9_]*` +
	`|\$env:[A-Za-z_][A-Za-z0-9_]*)`

var (
	// grepLine splits a line of git grep -n into its path, line and match.
	grepLine = regexp.MustCompile(`^(.+?):(\d+):(.*)$`)
	// trailingName is the variable an environment read ends with.
	trailingName = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*$`)
	// reference is a variable an MCP entry reads into a header or its
	// environment.
	reference = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
)

// credentialNeeds returns the credentials the repository reads, each with
// the first place that reads it, and a sentence saying where it looked:
// environment reads in source code that is neither a test nor a document,
// the names of the env examples it commits, and the variables its MCP
// connectors authenticate with. A harness's own billing key is left out,
// since no manifest may inject one.
func (c *checker) credentialNeeds(ctx context.Context) (map[string]string, string, error) {
	needs := map[string]string{}
	add := func(name, where string) {
		if _, seen := needs[name]; !seen && !auth.IsHarnessBillingKey(name) {
			needs[name] = where
		}
	}
	out, code, err := c.repo.git(ctx, "grep", "-I", "-n", "-o", "-E", "-e", environmentRead, c.repo.ref, "--", ".")
	if err != nil || (code != 0 && code != 1) {
		return nil, "", fmt.Errorf("projectcheck: git grep did not answer in %s", c.repo.dir)
	}
	sources := 0
	read := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		parts := grepLine.FindStringSubmatch(strings.TrimPrefix(strings.TrimRight(line, "\r"), c.repo.ref+":"))
		if parts == nil || isTestOrDocument(parts[1]) {
			continue
		}
		if !read[parts[1]] {
			read[parts[1]] = true
			sources++
		}
		if name := trailingName.FindString(parts[3]); credentialName(name) {
			add(name, parts[1]+":"+parts[2])
		}
	}
	examples := 0
	for _, name := range sortedKeys(c.repo.tracked) {
		if !isEnvFile(path.Base(name)) || !isExample(path.Base(name)) {
			continue
		}
		examples++
		data, _ := c.repo.read(ctx, name)
		values, err := auth.ParseEnv(bytes.NewReader(data))
		if err != nil {
			continue
		}
		for _, variable := range sortedKeys(values) {
			if credentialName(variable) {
				add(variable, name)
			}
		}
	}
	servers := 0
	if data, _, ok := c.folderFile(ctx, ".mcp.json"); ok {
		var config struct {
			Servers map[string]struct {
				Token   string            `json:"bearerTokenEnvVar"`
				Headers map[string]string `json:"headers"`
				Env     map[string]string `json:"env"`
			} `json:"mcpServers"`
		}
		if json.Unmarshal(data, &config) == nil {
			for _, server := range sortedKeys(config.Servers) {
				servers++
				entry := config.Servers[server]
				where := ".mcp.json server " + server
				if auth.ValidEnvName(entry.Token) {
					add(entry.Token, where)
				}
				for _, values := range []map[string]string{entry.Headers, entry.Env} {
					for _, key := range sortedKeys(values) {
						for _, match := range reference.FindAllStringSubmatch(values[key], -1) {
							add(match[1], where)
						}
					}
				}
			}
		}
	}
	return needs, fmt.Sprintf("%d source files that read the environment, %d env examples and %d MCP connectors", sources, examples, servers), nil
}

// credentialParts are the words that end the name of a variable holding a
// credential.
var credentialParts = map[string]bool{"KEY": true, "TOKEN": true, "SECRET": true, "PASSWORD": true, "DSN": true, "CREDENTIALS": true}

// credentialName reports whether a variable is named like a credential: its
// last word is one of credentialParts, or it carries an API key or a secret
// key under a suffix, as OPENROUTER_API_KEY_CHAT does.
func credentialName(name string) bool {
	upper := strings.ToUpper(name)
	parts := strings.Split(upper, "_")
	if credentialParts[parts[len(parts)-1]] && len(parts) > 1 {
		return true
	}
	return strings.Contains(upper, "_API_KEY_") || strings.Contains(upper, "_SECRET_KEY_")
}

// isTestOrDocument reports whether a tracked path is a test or a document,
// neither of which says what the running code needs.
func isTestOrDocument(name string) bool {
	base := path.Base(name)
	for _, folder := range strings.Split(path.Dir(name), "/") {
		switch folder {
		case "test", "tests", "testdata", "__tests__", "e2e", "docs", "fixtures":
			return true
		}
	}
	switch {
	case strings.HasSuffix(base, ".md"), strings.HasSuffix(base, ".mdx"), strings.HasSuffix(base, ".rst"):
		return true
	case strings.HasSuffix(base, "_test.go"), strings.HasPrefix(base, "test_"), base == "conftest.py":
		return true
	case strings.Contains(base, ".test."), strings.Contains(base, ".spec."):
		return true
	}
	return false
}
