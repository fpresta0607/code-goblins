package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/fpresta0607/code-goblins/internal/cirunner"
	"github.com/fpresta0607/code-goblins/internal/execx"
)

// ranNothing is a machine's commands that count what they are asked to run.
type ranNothing struct{ ran int }

func (r *ranNothing) Run(context.Context, execx.Request) (execx.Result, error) {
	r.ran++
	return execx.Result{}, nil
}

// testMachine is a machine whose every seam records what it was asked to do,
// elevated as isElevated says.
func testMachine(t *testing.T, isElevated bool) (machine, *ranNothing, *[]string) {
	t.Helper()
	commands := &ranNothing{}
	var asked []string
	return machine{
		dir:        t.TempDir(),
		program:    `C:\Program Files\Code Goblins\ci-runner\cg-ci-runner.exe`,
		isElevated: func() bool { return isElevated },
		commands:   commands,
		ask: func(_ context.Context, request cirunner.Request, reply func(cirunner.Reply)) error {
			asked = append(asked, "ask "+request.Kind)
			reply(cirunner.Reply{Kind: cirunner.ReplyStatus, Line: "no job has run since the service started"})
			return nil
		},
		download: func(_ context.Context, url, _ string) error {
			asked = append(asked, "download "+url)
			return nil
		},
	}, commands, &asked
}

// testUser is the SID of the user the test runs as, one user's account.
var testUser = func() string {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		panic(err)
	}
	return user.User.Sid.String()
}()

// installArgs are an install's flags for the user that runs the test, by SID.
func installArgs(extra ...string) []string {
	return append([]string{"install", "--app-id", "1", "--installation-id", "2", "--repo", "fpresta0607/PrecisionDocs-AI", "--key", `C:\keys\app.pem`, "--user", testUser}, extra...)
}

// A dry run needs no elevation, says every step, and runs, fetches and asks
// nothing.
func TestInstallDryRunShowsEveryStepUnelevated(t *testing.T) {
	// Arrange
	m, commands, asked := testMachine(t, false)
	var stdout, stderr bytes.Buffer

	// Act
	exit := run(installArgs("--dry-run"), &stdout, &stderr, m)

	// Assert
	said := stdout.String()
	if exit != 0 || !strings.Contains(said, "step 1 of 12: check this machine\n") || !strings.Contains(said, "step 12 of 12: readiness check\n") || !strings.HasSuffix(said, "cg-ci-runner: a dry run, so nothing was done\n") {
		t.Errorf("exit %d, stdout %q, stderr %q; want every step said and nothing done", exit, said, stderr.String())
	}
	if !strings.Contains(said, "CG_CI_PROGRAM="+m.program) || !strings.Contains(said, "whose pipe "+testUser+" may open") {
		t.Errorf("stdout %q; want the program and the user named", said)
	}
	if commands.ran != 0 || len(*asked) != 0 {
		t.Errorf("ran %d command(s) and did %q; want nothing", commands.ran, *asked)
	}
}

// What the install cannot use is refused with why, and runs nothing.
func TestInstallRefusesWhatItCannotUse(t *testing.T) {
	for name, row := range map[string]struct {
		args []string
		want string
	}{
		"no App":            {args: []string{"install", "--dry-run", "--repo", "o/r", "--key", "k", "--user", "S-1-5-21-1"}, want: "--app-id and --installation-id name the App"},
		"a repo by name":    {args: []string{"install", "--dry-run", "--app-id", "1", "--installation-id", "2", "--repo", "PrecisionDocs-AI", "--key", "k", "--user", "S-1-5-21-1"}, want: "--repo names the one repository as owner/name"},
		"no key":            {args: []string{"install", "--dry-run", "--app-id", "1", "--installation-id", "2", "--repo", "o/r", "--user", "S-1-5-21-1"}, want: "--key names the App's private key file"},
		"no user":           {args: []string{"install", "--dry-run", "--app-id", "1", "--installation-id", "2", "--repo", "o/r", "--key", "k"}, want: "--user names the fleet's Windows account"},
		"an unknown user":   {args: installArgs("--dry-run", "--user", "no-such-account-cg-ci-runner"), want: `the account "no-such-account-cg-ci-runner" is not one this machine knows`},
		"a group":           {args: installArgs("--dry-run", "--user", "S-1-5-32-545"), want: `the account "S-1-5-32-545" is no one user's account`},
		"everyone":          {args: installArgs("--dry-run", "--user", "Everyone"), want: `the account "Everyone" is no one user's account`},
		"a word past flags": {args: installArgs("now"), want: "usage: cg-ci-runner service"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			m, commands, asked := testMachine(t, true)
			var stdout, stderr bytes.Buffer

			// Act
			exit := run(row.args, &stdout, &stderr, m)

			// Assert
			if exit != 2 || !strings.Contains(stderr.String(), row.want) || commands.ran != 0 || len(*asked) != 0 {
				t.Errorf("exit %d, stderr %q, ran %d, did %q; want 2, %q and nothing run", exit, stderr.String(), commands.ran, *asked, row.want)
			}
		})
	}
}

// The install, the uninstall and the check change or read what only an
// administrator may, so unelevated they refuse and run nothing.
func TestCommandsRefuseUnelevated(t *testing.T) {
	for name, args := range map[string][]string{
		"install":   installArgs(),
		"uninstall": {"uninstall"},
		"check":     {"check"},
	} {
		t.Run(name, func(t *testing.T) {
			// Arrange
			m, commands, asked := testMachine(t, false)
			var stdout, stderr bytes.Buffer

			// Act
			exit := run(args, &stdout, &stderr, m)

			// Assert
			if exit != 1 || !strings.Contains(stderr.String(), "runs only from an elevated PowerShell") || commands.ran != 0 || len(*asked) != 0 {
				t.Errorf("exit %d, stderr %q, ran %d, did %q; want 1, the elevation named and nothing run", exit, stderr.String(), commands.ran, *asked)
			}
		})
	}
}

// The uninstall's dry run says its steps unelevated and runs nothing.
func TestUninstallDryRunShowsEveryStepUnelevated(t *testing.T) {
	// Arrange
	m, commands, asked := testMachine(t, false)
	var stdout, stderr bytes.Buffer

	// Act
	exit := run([]string{"uninstall", "--dry-run"}, &stdout, &stderr, m)

	// Assert
	if exit != 0 || !strings.Contains(stdout.String(), "step 4 of 4: remove the service's folder") || commands.ran != 0 || len(*asked) != 0 {
		t.Errorf("exit %d, stdout %q, ran %d, did %q; want every step said and nothing run", exit, stdout.String(), commands.ran, *asked)
	}
}

// Status says the service's line.
func TestStatusSaysTheServicesLine(t *testing.T) {
	// Arrange
	m, _, _ := testMachine(t, false)
	var stdout, stderr bytes.Buffer

	// Act
	exit := run([]string{"status"}, &stdout, &stderr, m)

	// Assert
	if exit != 0 || stdout.String() != "no job has run since the service started\n" {
		t.Errorf("exit %d, stdout %q; want the service's line", exit, stdout.String())
	}
}

// A command it does not know, or none, is its usage.
func TestRunSaysItsUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"start"}} {
		// Arrange
		m, _, _ := testMachine(t, true)
		var stdout, stderr bytes.Buffer

		// Act
		exit := run(args, &stdout, &stderr, m)

		// Assert
		if exit != 2 || stderr.String() != usage+"\n" {
			t.Errorf("run %q = %d, %q; want 2 and the usage", args, exit, stderr.String())
		}
	}
}
