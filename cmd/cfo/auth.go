package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/fpresta0607/code-goblins/internal/auth"
	"github.com/fpresta0607/code-goblins/internal/execx"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/worktree"
)

const authUsage = `usage: cfo auth <project> [--check|--fix] [--env]
       cfo auth store [--project <p>] <NAME> [value]   (omit value to read it from stdin)
       cfo auth list [--project <p>]
       cfo auth copy <NAME> --to <project> [--from <project>]   (copy a stored value into a project scope; the source is left in place)

Credentials are namespaced on (project, NAME). Omitting --project stores or
lists the shared scope, which is where every credential stored before
namespacing already lives.
`

func runAuth(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, authUsage)
		return 2
	}
	switch args[0] {
	case "store":
		return runAuthStore(args[1:], stdout, stderr)
	case "list":
		return runAuthList(args[1:], stdout, stderr)
	case "copy":
		return runAuthCopy(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		fmt.Fprint(stdout, authUsage)
		return 0
	}
	return runAuthPreflight(args, stdout, stderr)
}

func runAuthPreflight(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("auth", flag.ContinueOnError)
	flags.SetOutput(stderr)
	check := flags.Bool("check", false, "probe every service and report without changing anything (default)")
	fix := flags.Bool("fix", false, "probe, then repair what can be repaired without a human")
	showEnv := flags.Bool("env", false, "print the environment a goblin's pane would inherit (credential values redacted, cache locations in full)")
	positional, err := parseAuthArgs(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 {
		fmt.Fprint(stderr, authUsage)
		return 2
	}
	if *check && *fix {
		fmt.Fprintln(stderr, "cfo auth: --check and --fix are mutually exclusive")
		return 2
	}

	h, err := home.Resolve()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	project := positional[0]
	scope := auth.ProjectName(project)
	manifest, err := auth.LoadManifest(h.Data, project)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(stderr, "cfo auth: no manifest for %s\n", scope)
		fmt.Fprintf(stderr, "create %s listing the services this project needs\n", auth.ManifestPath(h.Data, project))
		return 1
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	store, err := auth.OpenStore()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	runner := execx.OSRunner{}
	checker := auth.Checker{Store: store, Runner: runner, Browser: auth.ChromeBrowser{Runner: runner}, Project: scope}

	ctx := context.Background()
	report := auth.Report{}
	if *fix {
		adopted, unscanned, err := auth.Discover(ctx, store, runner, manifest, project)
		if err != nil {
			fmt.Fprintf(stderr, "cfo auth: adopt existing credentials: %v\n", err)
		}
		if line := auth.IgnoreScanFailedLine(unscanned.IgnoreUnknown); line != "" {
			fmt.Fprintf(stderr, "cfo auth: %s\n", line)
		}
		if line := auth.WorktreeSharedLine(unscanned.WorktreeShared); line != "" {
			fmt.Fprintf(stderr, "cfo auth: %s\n", line)
		}
		if line := auth.LinkCheckFailedLine(unscanned.LinkCheckFailed); line != "" {
			fmt.Fprintf(stderr, "cfo auth: %s\n", line)
		}
		// A credential stored before namespacing lands in the scope that now
		// looks for it, so --fix repairs the state of the migration as well
		// as the state of the services.
		migrated, unreadable, err := auth.Migrate(store, h.Data, project, manifest)
		if err != nil {
			fmt.Fprintf(stderr, "cfo auth: migrate stored credentials: %v\n", err)
		}
		if line := auth.MigrationPausedLine(unreadable); line != "" {
			fmt.Fprintf(stderr, "cfo auth: %s\n", line)
		}
		for _, item := range append(adopted, migrated...) {
			verb := "adopted"
			if item.Refreshed {
				verb = "refreshed"
			}
			fmt.Fprintf(stdout, "%s %s from %s\n", verb, item.Key, item.Origin)
		}
		report, err = checker.Fix(ctx, manifest)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	} else {
		report, err = checker.Check(ctx, manifest)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
	}

	if err := auth.WriteTable(stdout, report); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if *showEnv {
		env, err := auth.InjectEnv(store, scope, manifest, report)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		names := make([]string, 0, len(env))
		for name := range env {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Fprintf(stdout, "\npane environment (%d variables)\n", len(names))
		for _, name := range names {
			// Redacted: this is an audit of what a goblin receives, not a
			// way to read credentials back out of the store.
			fmt.Fprintf(stdout, "%s=%s\n", name, auth.Redact(env[name]))
		}
		// The audit is project-scoped like the rest of this command: a
		// location this project declares overrides the shared root for both
		// real consumers, so reporting the machine value here would name a
		// directory nothing uses. A project with no worktree manifest simply
		// declares nothing.
		worktreeManifest, err := worktree.Resolve(h.Data, project)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		audit := auth.CacheAudit(h.Root, worktreeManifest.Env)
		counts := map[string]int{}
		for _, redirect := range audit {
			counts[redirect.Source]++
		}
		fmt.Fprintf(stdout, "\nshared caches (%d set, %d declared by %s, %d inherited)\n",
			counts[auth.CacheSourceCFO], counts[auth.CacheSourceProject], scope, counts[auth.CacheSourceInherited])
		for _, redirect := range audit {
			// Printed in full: a cache location is a path on this machine and
			// not a credential, and an operator auditing where a goblin builds
			// has to be able to read it. Every source is named, or the audit
			// would be silent about exactly the variable somebody tuned.
			marker := ""
			if redirect.Source != auth.CacheSourceCFO {
				marker = " (" + redirect.Source + ")"
			}
			fmt.Fprintf(stdout, "%s=%s%s\n", redirect.Name, redirect.Path, marker)
		}
	}
	if err := auth.WriteLoginRequest(stdout, report); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !report.OK() {
		return 1
	}
	return 0
}

// parseAuthArgs accepts flags before or after the project, matching the way
// every other cfo command reads its arguments.
func parseAuthArgs(flags *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for len(args) > 0 {
		if err := flags.Parse(args); err != nil {
			return nil, err
		}
		args = flags.Args()
		if len(args) > 0 {
			positional = append(positional, args[0])
			args = args[1:]
		}
	}
	return positional, nil
}

func runAuthStore(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("auth store", flag.ContinueOnError)
	flags.SetOutput(stderr)
	project := flags.String("project", "", "store in this project's scope instead of the shared scope")
	positional, err := parseAuthArgs(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) == 0 || len(positional) > 2 {
		fmt.Fprint(stderr, authUsage)
		return 2
	}
	key, ok := credentialKey(*project, positional[0], stderr)
	if !ok {
		return 2
	}
	value := ""
	if len(positional) == 2 {
		value = positional[1]
	} else {
		// Reading from stdin keeps the secret out of shell history, which is
		// why it is the documented path.
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		value = strings.TrimRight(string(data), "\r\n")
	}
	if strings.TrimSpace(value) == "" {
		fmt.Fprintln(stderr, "cfo auth store: refusing to store an empty value")
		return 2
	}
	store, err := auth.OpenStore()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := store.Set(key, value); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "stored %s (%s) in %s\n", key, auth.Redact(value), store.Describe())
	if key.IsShared() {
		fmt.Fprintln(stdout, "shared scope: only services a manifest declares shared will read this")
	}
	return 0
}

// credentialKey builds and validates one store key from the operator's
// arguments, refusing rather than sanitizing so a typo cannot land a
// credential in a scope nobody will look in.
func credentialKey(project, name string, stderr io.Writer) (auth.Key, bool) {
	key := auth.Shared(name)
	if project != "" {
		scope, ok := credentialScope(project)
		if !ok {
			fmt.Fprintf(stderr, "cfo auth: %q is not a usable project scope\n", project)
			return auth.Key{}, false
		}
		key = auth.Key{Project: scope, Name: name}
	}
	if !auth.ValidEnvName(name) {
		fmt.Fprintf(stderr, "cfo auth: %q is not a valid environment variable name\n", name)
		return auth.Key{}, false
	}
	return key, true
}

// credentialScope reduces a project name or checkout path to its scope. A path
// that walks upward is refused rather than reduced: `../escaped` would
// otherwise quietly become the scope `escaped`, and the operator would store a
// credential where nothing will ever look for it.
func credentialScope(project string) (string, bool) {
	separator := func(r rune) bool { return r == '/' || r == '\\' }
	for _, segment := range strings.FieldsFunc(project, separator) {
		// A relative segment is what walks; a name that merely contains dots
		// is one the spawn path already stores credentials under.
		if segment == ".." || segment == "." {
			return "", false
		}
	}
	scope := auth.ProjectName(project)
	return scope, auth.ValidProjectName(scope)
}

func runAuthList(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("auth list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	project := flags.String("project", "", "list only this project's scope")
	positional, err := parseAuthArgs(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) > 0 {
		fmt.Fprint(stderr, authUsage)
		return 2
	}
	store, err := auth.OpenStore()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	keys, err := store.Keys()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	scope := ""
	if *project != "" {
		valid := false
		if scope, valid = credentialScope(*project); !valid {
			fmt.Fprintf(stderr, "cfo auth list: %q is not a usable project scope\n", *project)
			return 2
		}
	}
	fmt.Fprintf(stdout, "store %s\n", store.Describe())
	listed := 0
	for _, key := range keys {
		if scope != "" && key.Project != scope {
			continue
		}
		fmt.Fprintln(stdout, key)
		listed++
	}
	if listed == 0 {
		fmt.Fprintln(stdout, "no credentials stored")
	}
	return 0
}

func runAuthCopy(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("auth copy", flag.ContinueOnError)
	flags.SetOutput(stderr)
	to := flags.String("to", "", "project scope to copy the shared value into")
	from := flags.String("from", "", "project scope to copy from (default: the shared scope)")
	positional, err := parseAuthArgs(flags, args)
	if err != nil {
		return 2
	}
	if len(positional) != 1 || *to == "" {
		fmt.Fprint(stderr, authUsage)
		return 2
	}
	source, ok := credentialKey(*from, positional[0], stderr)
	if !ok {
		return 2
	}
	target, ok := credentialKey(*to, positional[0], stderr)
	if !ok {
		return 2
	}
	if source == target {
		fmt.Fprintln(stderr, "cfo auth copy: source and target are the same scope")
		return 2
	}
	store, err := auth.OpenStore()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	value, found, err := store.Get(source)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !found || value == "" {
		fmt.Fprintf(stderr, "cfo auth copy: %s is not stored\n", source)
		return 1
	}
	// The source is left in place. A shared value more than one project
	// could claim is not this command's to delete: which project owns it is
	// exactly the question nobody may guess at.
	if err := store.Set(target, value); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "copied %s to %s (%s); %s is unchanged\n", source, target, auth.Redact(value), source)
	return 0
}
