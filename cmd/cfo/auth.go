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
)

const authUsage = `usage: cfo auth <project> [--check|--fix] [--env]
       cfo auth store <NAME> [value]   (omit value to read it from stdin)
       cfo auth list
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
		return runAuthList(stdout, stderr)
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
	showEnv := flags.Bool("env", false, "print the environment a goblin's pane would inherit (values redacted)")
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
	manifest, err := auth.LoadManifest(h.Data, project)
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(stderr, "cfo auth: no manifest for %s\n", auth.ProjectName(project))
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
	checker := auth.Checker{Store: store, Runner: runner, Browser: auth.ChromeBrowser{Runner: runner}}

	ctx := context.Background()
	report := auth.Report{}
	if *fix {
		adopted, err := auth.Discover(ctx, store, runner, manifest, project)
		if err != nil {
			fmt.Fprintf(stderr, "cfo auth: adopt existing credentials: %v\n", err)
		}
		for _, item := range adopted {
			fmt.Fprintf(stdout, "adopted %s from %s\n", item.Name, item.Origin)
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
		env, err := auth.InjectEnv(store, manifest, report)
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
	if len(args) == 0 || len(args) > 2 {
		fmt.Fprint(stderr, authUsage)
		return 2
	}
	name := args[0]
	if !auth.ValidEnvName(name) {
		fmt.Fprintf(stderr, "cfo auth store: %q is not a valid environment variable name\n", name)
		return 2
	}
	value := ""
	if len(args) == 2 {
		value = args[1]
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
	if err := store.Set(name, value); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	fmt.Fprintf(stdout, "stored %s (%s) in %s\n", name, auth.Redact(value), store.Describe())
	return 0
}

func runAuthList(stdout, stderr io.Writer) int {
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
	fmt.Fprintf(stdout, "store %s\n", store.Describe())
	if len(keys) == 0 {
		fmt.Fprintln(stdout, "no credentials stored")
		return 0
	}
	for _, key := range keys {
		fmt.Fprintln(stdout, key)
	}
	return 0
}
