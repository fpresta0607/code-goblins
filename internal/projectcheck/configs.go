package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/fsx"
	"github.com/fpresta0607/code-goblins/internal/services"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

// configs assesses the project's configs: that git ignores its env files,
// and that the worktree and services manifests the home keeps for it agree
// with the repository.
func (c *checker) configs(ctx context.Context) error {
	if err := c.envIgnored(ctx); err != nil {
		return err
	}
	c.worktreeManifest()
	c.servicesManifest(ctx)
	return nil
}

// envIgnored checks that git ignores every env file of the project but the
// examples it commits on purpose.
func (c *checker) envIgnored(ctx context.Context) error {
	var files, examples []string
	for _, name := range c.repo.envFiles() {
		if isExample(path.Base(name)) {
			examples = append(examples, name)
			continue
		}
		files = append(files, name)
	}
	ignored, err := c.repo.ignored(ctx, files)
	if err != nil {
		return err
	}
	indexed, err := c.repo.indexed(ctx, files)
	if err != nil {
		return err
	}
	var kept, exposed, credentials []string
	for _, name := range files {
		switch {
		case c.repo.tracked[name]:
			exposed = append(exposed, name+" (tracked at "+c.repo.at()+")")
		case indexed[name]:
			exposed = append(exposed, name+" (tracked in the checkout's index)")
		case !ignored[name]:
			exposed = append(exposed, name+" (git check-ignore: not ignored)")
		default:
			kept = append(kept, name)
			continue
		}
		if names := c.credentialNames(ctx, name); len(names) > 0 {
			credentials = append(credentials, name+" holds values shaped like credentials: "+strings.Join(names, ", "))
		}
	}
	looked := fmt.Sprintf("looked at every path %s tracks and at the folder's root and two folders down", c.repo.at())
	if len(exposed) > 0 {
		severity, says := High, "git does not ignore an env file of the project, so the next commit of everything publishes it"
		evidence := strings.Join(exposed, ", ")
		if len(credentials) > 0 {
			severity, says = Critical, "git does not ignore an env file that holds credentials, so the next commit of everything publishes them"
			evidence += ". " + strings.Join(credentials, ". ")
		}
		c.add(AreaConfigs, "env-file-not-ignored", severity, says, evidence,
			"add each file to .gitignore, take a tracked one out of git with git rm --cached, and rotate every credential a tracked one held")
	}
	evidence := "the project holds no env file beside its examples"
	if len(kept) > 0 {
		evidence = "git check-ignore names " + strings.Join(kept, ", ")
	}
	if len(examples) > 0 {
		evidence += ". Examples, which are committed on purpose: " + strings.Join(examples, ", ")
	}
	c.add(AreaConfigs, "env-files-ignored", OK,
		fmt.Sprintf("git ignores %d of the project's %d env files", len(kept), len(files)),
		evidence+". "+looked, "")
	return nil
}

// credentialNames returns the variables of an env file whose values are
// shaped like credentials. It returns names only: a value never leaves here.
func (c *checker) credentialNames(ctx context.Context, name string) []string {
	data, err := fsx.ReadFile(filepath.Join(c.Checkout, filepath.FromSlash(name)))
	if err != nil {
		var ok bool
		if data, ok = c.repo.read(ctx, name); !ok {
			return nil
		}
	}
	values, err := auth.ParseEnv(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	shaped := map[string]bool{}
	for variable, value := range values {
		// An equals sign reads as an assignment to SecretShape, which is
		// asked about names elsewhere; inside a value it is only padding or
		// a query string.
		if auth.SecretShape(strings.ReplaceAll(value, "=", "")) != "" {
			shaped[variable] = true
		}
	}
	return sortedKeys(shaped)
}

// worktreeManifest checks that what worktree.json shares and installs is
// in the repository.
func (c *checker) worktreeManifest() {
	file := worktree.ManifestPath(c.DataDir, c.project)
	manifest, err := worktree.Resolve(c.DataDir, c.project)
	if err != nil {
		c.add(AreaConfigs, "worktree-invalid", High,
			"the loader refuses the project's worktree manifest, which refuses the project's next spawn",
			err.Error(), "correct what the loader names in "+file)
		return
	}
	if manifest.Path == "" {
		c.add(AreaConfigs, "worktree-defaults", OK,
			"the project declares no worktree manifest, so a spawn shares the default env files and installs by lockfile",
			"no file at "+file+". Of the default env files the checkout holds "+strings.Join(c.held(manifest.Link), ", "), "")
		return
	}
	clean := true
	if manifest.Project != "" && manifest.Project != c.project {
		clean = false
		c.add(AreaConfigs, "worktree-names-another-project", Medium,
			"the worktree manifest names a project other than the checkout it is filed under",
			fmt.Sprintf("%s says project %q and is filed for the checkout folder %q", file, manifest.Project, c.project),
			fmt.Sprintf("set project to %q", c.project))
	}
	var links []string
	if !manifest.LinkDefaulted {
		links = append(links, manifest.Link...)
	}
	var absent []string
	for _, name := range append(links, manifest.Dependencies.Paths...) {
		if !c.repo.has(name) {
			absent = append(absent, name+" is not in "+c.Checkout)
		}
	}
	if len(absent) > 0 {
		clean = false
		c.add(AreaConfigs, "worktree-link-missing", Medium,
			"the worktree manifest shares something the checkout does not hold, which a spawn passes over without a word",
			strings.Join(absent, ", "), "create it in the checkout or take it out of "+file)
	}
	var faults []string
	for _, line := range manifest.Dependencies.Install {
		faults = append(faults, c.commandFaults(strings.Fields(line))...)
	}
	if len(faults) > 0 {
		clean = false
		c.add(AreaConfigs, "worktree-install-missing", High,
			"an install command of the worktree manifest cannot run as written, so a goblin's first step fails",
			strings.Join(faults, ", "), "name the install the repository really uses in "+file)
	}
	if clean {
		shared := "it shares nothing into a worktree"
		if entries := append(links, manifest.Dependencies.Paths...); len(entries) > 0 {
			shared = "it shares " + strings.Join(entries, ", ")
		}
		if manifest.LinkDefaulted {
			shared = "it names no link, so it shares the default env files the checkout holds, " + strings.Join(c.held(manifest.Link), ", ")
		}
		c.add(AreaConfigs, "worktree-agrees", OK,
			"the worktree manifest agrees with the repository",
			fmt.Sprintf("%s: %s. Its %d install commands name programs and files that are there, read at %s", file, shared, len(manifest.Dependencies.Install), c.repo.at()), "")
	}
}

// held returns which of names the checkout holds, or the word none.
func (c *checker) held(names []string) []string {
	var held []string
	for _, name := range names {
		if c.repo.has(name) {
			held = append(held, name)
		}
	}
	if len(held) == 0 {
		return []string{"none"}
	}
	return held
}

// servicesManifest checks that the compose file, services, env file and
// check services.json names are in the repository.
func (c *checker) servicesManifest(ctx context.Context) {
	file := services.ManifestPath(c.DataDir, c.project)
	manifest, err := services.LoadManifest(c.DataDir, c.project)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		c.add(AreaConfigs, "services-none", OK, "the project declares no local services", "no file at "+file, "")
		return
	case err != nil:
		c.add(AreaConfigs, "services-invalid", High,
			"the loader refuses the project's services manifest, so cfo services up cannot start its stack",
			err.Error(), "correct what the loader names in "+file)
		return
	}
	compose, where, ok := c.folderFile(ctx, manifest.Compose)
	if !ok {
		c.add(AreaConfigs, "services-compose-missing", High,
			"the services manifest names a compose file the project does not have, so cfo services up cannot start its stack",
			fmt.Sprintf("%s names %s, which is neither in %s nor tracked at %s", file, manifest.Compose, c.Checkout, c.repo.at()),
			"name the project's real compose file in "+file)
		return
	}
	clean := true
	var declared struct {
		Services map[string]yaml.Node `yaml:"services"`
	}
	if err := yaml.Unmarshal(compose, &declared); err != nil {
		clean = false
		c.add(AreaConfigs, "services-not-in-compose", High,
			"the compose file the services manifest names does not parse, so its services cannot be told",
			fmt.Sprintf("%s (%s): %v", manifest.Compose, where, err), "correct the compose file")
	} else {
		var unknown []string
		for _, name := range manifest.Services {
			if _, ok := declared.Services[name]; !ok {
				unknown = append(unknown, name)
			}
		}
		if len(unknown) > 0 {
			clean = false
			c.add(AreaConfigs, "services-not-in-compose", High,
				"the services manifest names a service its compose file does not have, so cfo services up fails",
				fmt.Sprintf("%s is not a service of %s (%s), which has %s", strings.Join(unknown, ", "), manifest.Compose, where, strings.Join(sortedKeys(declared.Services), ", ")),
				"name services the compose file has in "+file)
		}
	}
	if manifest.EnvFile != "" && !c.repo.has(manifest.EnvFile) {
		clean = false
		c.add(AreaConfigs, "services-env-file-missing", Medium,
			"the services manifest starts the stack from an env file the checkout does not hold",
			fmt.Sprintf("%s names %s, which is not in %s", file, manifest.EnvFile, c.Checkout),
			"create the env file from the project's example or name the one the stack uses")
	}
	switch {
	case len(manifest.Check) == 0 && manifest.EnvFile != "":
		clean = false
		c.add(AreaConfigs, "services-no-check", Medium,
			"nothing checks the env file a local stack starts from, so one that names production starts against production",
			fmt.Sprintf("%s declares env_file %s and no check", file, manifest.EnvFile),
			"declare a check in "+file+" that refuses an env file reaching production")
	case len(manifest.Check) > 0:
		if faults := c.commandFaults(manifest.Check); len(faults) > 0 {
			clean = false
			c.add(AreaConfigs, "services-check-missing", High,
				"the check of the services manifest cannot run as written, which refuses every start of the stack",
				strings.Join(faults, ", "), "name a check that exists in "+file)
		}
	}
	if clean {
		c.add(AreaConfigs, "services-agree", OK,
			"the services manifest agrees with the repository",
			fmt.Sprintf("%s: compose file %s (%s) has %s, and its env file and check are there", file, manifest.Compose, where, strings.Join(manifest.Services, ", ")), "")
	}
}

// folderFile reads a file a command run in the checkout would read: the
// folder's own copy, or the default branch's where the folder has none.
func (c *checker) folderFile(ctx context.Context, name string) (data []byte, where string, ok bool) {
	if data, err := fsx.ReadFile(filepath.Join(c.Checkout, filepath.FromSlash(name))); err == nil {
		return data, "in " + c.Checkout, true
	}
	if data, ok := c.repo.read(ctx, path.Clean(filepath.ToSlash(name))); ok {
		return data, "at " + c.repo.at(), true
	}
	return nil, "", false
}
