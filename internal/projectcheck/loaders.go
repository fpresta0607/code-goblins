package projectcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// loadKind is what kind of thing loads an env file, which decides whether a
// test run reads what the file holds.
type loadKind int

const (
	// byTest is a test or its setup, so a test run loads the file.
	byTest loadKind = iota
	// byApplication is the application's own code, which a test that starts
	// the application runs.
	byApplication
	// byScript is a script, a package script or a shell file, which runs when
	// someone runs it.
	byScript
	// byStack is the local stack, a compose file or an image's build.
	byStack
	// byFramework is a framework whose development server and build load env
	// files by its own rule.
	byFramework
)

// envLoader is one thing of the repository that loads an env file.
type envLoader struct {
	kind loadKind
	// by names it: a tracked file, a package script, or a framework.
	by string
	// names are the env files its line names. One that names none loads
	// .env, which is what the libraries do.
	names []string
	// isFramework marks a loader that loads env files by a framework's own
	// rule and names none.
	isFramework bool
}

// loads reports whether the loader loads an env file.
func (l envLoader) loads(file string) bool {
	base := path.Base(file)
	switch {
	case l.isFramework:
		return base == ".env" || base == ".env.local" || strings.HasPrefix(base, ".env.development") || strings.HasPrefix(base, ".env.production")
	case len(l.names) == 0:
		return base == ".env"
	}
	return slices.Contains(l.names, base)
}

// loadIdiom matches the ways a file loads an env file, in the extended
// syntax git grep takes and the one Go's own expressions share: the dotenv
// libraries of each language, the flag node and docker take, the key a
// settings class, a compose file and pytest name it under, and the helpers
// of Next.js.
const loadIdiom = `[Dd]otenv|--env-file|env_file|loadEnvConfig|loadEnvFile|read_env\(|from decouple|import decouple|next/jest`

var (
	loadIdiomIn = regexp.MustCompile(loadIdiom)
	// envFileNamed is an env file a line names. What stands before it rules
	// out a variable read through an object, as in process.env.name.
	envFileNamed = regexp.MustCompile(`(?:^|[^A-Za-z0-9_.)\]])(\.env(?:\.[a-z0-9_-]+)*)`)
)

// frameworks are the packages whose development server and build load the
// env files beside their package file by their own rule.
var frameworks = []string{"next", "vite", "astro", "nuxt", "@sveltejs/kit", "@remix-run/dev", "react-scripts", "gatsby"}

// dependencyLists are the files that list what a project depends on, where
// the name of a loader is a dependency and not a load.
var dependencyLists = map[string]bool{
	"package.json": true, "package-lock.json": true, "pnpm-lock.yaml": true, "yarn.lock": true, "bun.lockb": true,
	"pyproject.toml": true, "uv.lock": true, "poetry.lock": true, "Pipfile": true, "Pipfile.lock": true, "setup.py": true, "setup.cfg": true,
	"go.mod": true, "go.sum": true, "Cargo.toml": true, "Cargo.lock": true, "Gemfile": true, "Gemfile.lock": true,
}

// scriptFolders are the folders a project keeps its scripts in.
var scriptFolders = map[string]bool{"scripts": true, "script": true, "bin": true, "tools": true, "ops": true}

// shellExtensions end the name of a file a shell runs.
var shellExtensions = map[string]bool{".sh": true, ".ps1": true, ".cmd": true, ".bat": true}

// loaderKind says what kind of loader a tracked file is, and whether it is
// one at all: a file that is neither source code nor a file a stack or a
// shell reads loads nothing by naming a loader.
func loaderKind(name string) (kind loadKind, ok bool) {
	base := path.Base(name)
	_, sectioned := testSetupSections[base]
	inScripts := false
	for _, folder := range strings.Split(path.Dir(name), "/") {
		inScripts = inScripts || scriptFolders[folder]
	}
	extension := strings.ToLower(path.Ext(base))
	switch {
	case isTestSetup(name) || sectioned || (isTestOrDocument(name) && !isDocument(name)):
		return byTest, true
	case strings.HasPrefix(base, "docker-compose") || strings.HasPrefix(base, "compose.") || strings.HasPrefix(base, "Dockerfile"):
		return byStack, true
	case inScripts || shellExtensions[extension] || base == "Makefile" || base == "justfile":
		return byScript, true
	case sourceExtensions[extension]:
		return byApplication, true
	}
	return 0, false
}

// envLoaders reads what of the repository loads an env file: the tracked
// files that hold a load, the package scripts that do, and the frameworks
// the package files depend on. A document, and the name of a loader in a
// list of dependencies, load nothing.
func (c *checker) envLoaders(ctx context.Context) ([]envLoader, error) {
	// Exit 1 is git saying nothing matched.
	out, code, err := c.repo.git(ctx, "grep", "-I", "-n", "-E", "-e", loadIdiom, c.repo.ref, "--", ".")
	if err != nil || (code != 0 && code != 1) {
		return nil, fmt.Errorf("projectcheck: git grep did not answer in %s", c.repo.dir)
	}
	var loaders []envLoader
	at := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		parts := grepLine.FindStringSubmatch(strings.TrimPrefix(strings.TrimRight(line, "\r"), c.repo.ref+":"))
		if parts == nil {
			continue
		}
		name, text := parts[1], parts[3]
		base := path.Base(name)
		// A list of dependencies names a loader without loading. Only the key
		// pytest reads there names an env file to load.
		if isDocument(name) || suppliedByGitHub(name) || isEnvFile(base) || (dependencyLists[base] || strings.HasPrefix(base, "requirements")) && !strings.Contains(text, "env_file") {
			continue
		}
		kind, ok := loaderKind(name)
		if !ok {
			continue
		}
		index, seen := at[name]
		if !seen {
			index = len(loaders)
			at[name] = index
			loaders = append(loaders, envLoader{kind: kind, by: name})
		}
		loaders[index].names = append(loaders[index].names, envFilesNamed(text)...)
	}
	for _, name := range sortedKeys(c.repo.tracked) {
		if path.Base(name) != "package.json" || strings.Count(name, "/") > 2 {
			continue
		}
		data, _ := c.repo.read(ctx, name)
		var manifest struct {
			Scripts      map[string]string `json:"scripts"`
			Dependencies map[string]string `json:"dependencies"`
			Development  map[string]string `json:"devDependencies"`
		}
		if json.Unmarshal(data, &manifest) != nil {
			continue
		}
		for _, script := range sortedKeys(manifest.Scripts) {
			if text := manifest.Scripts[script]; loadIdiomIn.MatchString(text) {
				loaders = append(loaders, envLoader{kind: byScript, by: name + " script " + script, names: envFilesNamed(text)})
			}
		}
		for _, framework := range frameworks {
			_, runs := manifest.Dependencies[framework]
			_, builds := manifest.Development[framework]
			if runs || builds {
				loaders = append(loaders, envLoader{kind: byFramework, by: "the development server and the build of " + framework + ", which " + name + " depends on", isFramework: true})
				break
			}
		}
	}
	return loaders, nil
}

// envFilesNamed returns the env files a line names, by their file names.
func envFilesNamed(text string) []string {
	var names []string
	for _, match := range envFileNamed.FindAllStringSubmatch(text, -1) {
		names = append(names, match[1])
	}
	return names
}

// whatLoads says what loads an env file, for a line's evidence, and whether
// a test run loads it: through a test or its setup, or through the
// application's own code, which a test that starts the application runs.
func whatLoads(loaders []envLoader, file string) (says string, byATest, byTheApplication bool) {
	named := map[loadKind][]string{}
	for _, loader := range loaders {
		if loader.loads(file) {
			named[loader.kind] = append(named[loader.kind], loader.by)
		}
	}
	var parts []string
	for _, kind := range []struct {
		kind  loadKind
		says  string
		count string
	}{
		{byTest, "a test or the test setup", ""},
		{byApplication, "the application's own code", ""},
		{byScript, "", "script"},
		{byStack, "the local stack", ""},
		{byFramework, "", ""},
	} {
		names := named[kind.kind]
		switch {
		case len(names) == 0:
		case kind.kind == byFramework:
			parts = append(parts, strings.Join(names, ", "))
		case kind.count != "":
			parts = append(parts, count(len(names), kind.count)+" ("+some(names)+")")
		default:
			parts = append(parts, kind.says+" ("+some(names)+")")
		}
	}
	if len(parts) == 0 {
		return "nothing this check knows loads it", false, false
	}
	return "loaded by " + strings.Join(parts, ", by "), len(named[byTest]) > 0, len(named[byApplication]) > 0
}

// some joins the first three of names and counts the rest.
func some(names []string) string {
	if len(names) <= 3 {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:3], ", ") + fmt.Sprintf(" and %d more", len(names)-3)
}
