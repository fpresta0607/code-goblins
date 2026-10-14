package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"

	"github.com/fpresta0607/code-goblins/internal/cirunner"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

const usage = `usage: cg-ci-runner service
       cg-ci-runner status
       cg-ci-runner check
       cg-ci-runner install [--dry-run] --app-id <id> --installation-id <id> --repo <owner/name> --key <file> --user <account>
       cg-ci-runner uninstall [--dry-run]`

// machine is what the commands act on: the service's folder, this program,
// and the machine's commands, each of which a test replaces.
type machine struct {
	dir        string
	program    string
	isElevated func() bool
	commands   execx.Runner
	ask        func(ctx context.Context, request cirunner.Request, reply func(cirunner.Reply)) error
	download   func(ctx context.Context, url, path string) error
}

// thisMachine is the machine this program runs on.
func thisMachine() (machine, error) {
	dir, err := serviceFolder()
	if err != nil {
		return machine{}, err
	}
	program, _ := os.Executable()
	return machine{
		dir:        dir,
		program:    program,
		isElevated: func() bool { return windows.GetCurrentProcessToken().IsElevated() },
		commands:   execx.OSRunner{},
		ask: func(ctx context.Context, request cirunner.Request, reply func(cirunner.Reply)) error {
			return cirunner.Ask(ctx, cirunner.PipeName, request, reply)
		},
		download: func(ctx context.Context, url, path string) error {
			return cirunner.DownloadFile(ctx, &http.Client{Timeout: 30 * time.Minute}, url, path)
		},
	}, nil
}

// run runs the command args name, and returns its exit code: 2 for usage.
func run(args []string, stdout, stderr io.Writer, m machine) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "service":
		if err := svc.Run(cirunner.ServiceName, handler{dir: m.dir}); err != nil {
			fmt.Fprintf(stderr, "cg-ci-runner: run as a service: %v\n", err)
			return 1
		}
		return 0
	case "status":
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := m.ask(ctx, cirunner.Request{Kind: cirunner.AskStatus}, func(reply cirunner.Reply) { fmt.Fprintln(stdout, reply.Line) }); err != nil {
			fmt.Fprintf(stderr, "cg-ci-runner: %v\n", err)
			return 1
		}
		return 0
	case "install":
		return m.install(args[1:], stdout, stderr)
	case "uninstall":
		return m.uninstall(args[1:], stdout, stderr)
	case "check":
		return m.check(stdout, stderr)
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
}

// install installs the runner as its flags say, or with --dry-run says each
// step and runs nothing, which needs no elevation.
func (m machine) install(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	isDryRun := flags.Bool("dry-run", false, "say each step and run nothing")
	appID := flags.Int64("app-id", 0, "the GitHub App's id")
	installationID := flags.Int64("installation-id", 0, "the App's installation id")
	repo := flags.String("repo", "", "the one repository, as owner/name")
	keyFile := flags.String("key", "", "the App's private key file")
	user := flags.String("user", "", "the fleet's Windows account, which may open the service's pipe")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	owner, name, isRepo := strings.Cut(*repo, "/")
	sid, userErr := accountSID(*user)
	var problems []string
	if *appID <= 0 || *installationID <= 0 {
		problems = append(problems, "--app-id and --installation-id name the App and its installation")
	}
	if !isRepo || owner == "" || name == "" || strings.Contains(name, "/") {
		problems = append(problems, "--repo names the one repository as owner/name")
	}
	if *keyFile == "" {
		problems = append(problems, "--key names the App's private key file")
	}
	if userErr != nil {
		problems = append(problems, userErr.Error())
	}
	if len(problems) > 0 {
		fmt.Fprintf(stderr, "cg-ci-runner: %s\n%s\n", strings.Join(problems, ", and "), usage)
		return 2
	}
	if !*isDryRun && !m.isElevated() {
		fmt.Fprintln(stderr, "cg-ci-runner: the install makes a VM and a service, so it runs only from an elevated PowerShell, and --dry-run shows it without")
		return 1
	}
	steps := cirunner.InstallSteps(cirunner.InstallOptions{
		AppID:          *appID,
		InstallationID: *installationID,
		Owner:          owner,
		Repo:           name,
		KeyFile:        *keyFile,
		User:           sid,
		Dir:            m.dir,
		Program:        m.program,
		Commands:       m.commands,
		Download:       m.download,
		Ask:            m.ask,
		Sleep:          cirunner.Sleep,
		Now:            time.Now,
	})
	return finish(cirunner.RunSteps(context.Background(), steps, *isDryRun, stdout), "installed", *isDryRun, stdout, stderr)
}

// uninstall removes the runner, or with --dry-run says each step.
func (m machine) uninstall(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	flags.SetOutput(stderr)
	isDryRun := flags.Bool("dry-run", false, "say each step and run nothing")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if !*isDryRun && !m.isElevated() {
		fmt.Fprintln(stderr, "cg-ci-runner: the uninstall removes a VM and a service, so it runs only from an elevated PowerShell, and --dry-run shows it without")
		return 1
	}
	steps := cirunner.UninstallSteps(cirunner.UninstallOptions{Dir: m.dir, Commands: m.commands, DeleteOwnRunners: cirunner.DeleteOwnRunnersIn})
	return finish(cirunner.RunSteps(context.Background(), steps, *isDryRun, stdout), "uninstalled", *isDryRun, stdout, stderr)
}

// check says what of the install is not as it left it, and exits 1 when
// anything is not.
func (m machine) check(stdout, stderr io.Writer) int {
	if !m.isElevated() {
		fmt.Fprintln(stderr, "cg-ci-runner: the check reads the App's key, which only Administrators may open, so it runs only from an elevated PowerShell")
		return 1
	}
	config, key, err := cirunner.ReadConfig(m.dir)
	if err != nil {
		fmt.Fprintf(stderr, "cg-ci-runner: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	findings := cirunner.Check(ctx, cirunner.CheckOptions{
		Dir:          m.dir,
		Repo:         config.Repo,
		Commands:     m.commands,
		Installation: cirunner.NewGitHub(config, key).Installation,
		Ask:          m.ask,
	})
	exit := 0
	for _, finding := range findings {
		if finding.Problem == "" {
			fmt.Fprintf(stdout, "passed: %s\n", finding.Checked)
			continue
		}
		exit = 1
		fmt.Fprintf(stdout, "failed: %s: %s\n", finding.Checked, finding.Problem)
	}
	return exit
}

// accountSID is the SID of account, a Windows account's name or a SID.
func accountSID(account string) (string, error) {
	if account == "" {
		return "", errors.New("--user names the fleet's Windows account")
	}
	if sid, err := windows.StringToSid(account); err == nil {
		return sid.String(), nil
	}
	sid, _, _, err := windows.LookupSID("", account)
	if err != nil {
		return "", fmt.Errorf("the account %q is not one this machine knows: %v", account, err)
	}
	return sid.String(), nil
}

// finish says how steps ended and is their exit code.
func finish(err error, done string, isDryRun bool, stdout, stderr io.Writer) int {
	switch {
	case err != nil:
		fmt.Fprintf(stderr, "cg-ci-runner: %v\n", err)
		return 1
	case isDryRun:
		fmt.Fprintln(stdout, "cg-ci-runner: a dry run, so nothing was done")
	default:
		fmt.Fprintf(stdout, "cg-ci-runner: %s\n", done)
	}
	return 0
}
