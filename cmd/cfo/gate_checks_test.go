package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/verify"
)

// webPolicy has a check besides Go's: a change under web/ or dist/ runs npm's
// build and tests in web, after npm ci unless web/node_modules holds an
// install made from the same lockfile and git's version, and the build must
// leave dist as committed. CI runs the browser tests.
const webPolicy = `{
	"version": 1,
	"outside": [{"paths": ["README.md"], "why": "documentation no Go check reads"}],
	"checks": [{
		"name": "web",
		"paths": ["web/**", "dist/**"],
		"dir": "web",
		"install": {"command": ["npm", "ci"], "inputs": ["web/package-lock.json"], "versions": [["git", "--version"]], "output": "web/node_modules"},
		"commands": [["npm", "run", "build"], ["npm", "test"]],
		"unchanged": ["dist"],
		"left": [{"check": "npm run e2e", "why": "CI runs the browser tests"}]
	}]
}`

// webBase is what the default branch holds besides the module: webPolicy,
// the web project's lockfile and source, its committed build, and an ignore
// rule for what npm ci installs.
var webBase = map[string]string{
	"config/verify.json":    webPolicy,
	".gitignore":            "node_modules/\n",
	"web/package-lock.json": "{}\n",
	"web/app.ts":            "export const app = 1\n",
	"dist/app.js":           "const app = 1\n",
}

// unchangedDist is the command that lists what differs from the commit in
// dist, which webPolicy's check must leave as committed.
const unchangedDist = "git status --porcelain --untracked-files=all -- dist"

// npmStandIn is a runtime on a machine with memory to spare whose npm is not
// a process: each npm command is recorded with the folder it ran in, npm ci
// exits installExit and makes web/node_modules when it passes, npm run build
// writes build into dist/app.js, and npm test passes. Every other command
// runs for real, so the status of dist is git's own.
func npmStandIn(installExit int, build string) (commandRuntime, *[]string) {
	runtime := defaultCommandRuntime()
	runtime.availableMemory = plenty
	ran := &[]string{}
	runtime.gateRun = func(command []string, dir string, env []string, stdout, stderr io.Writer) (int, error) {
		line := strings.Join(command, " ")
		*ran = append(*ran, line+" in "+filepath.Base(dir))
		switch line {
		case "npm ci":
			if installExit != 0 {
				return installExit, nil
			}
			return 0, os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755)
		case "npm run build":
			return 0, os.WriteFile(filepath.Join(dir, "..", "dist", "app.js"), []byte(build), 0o644)
		case "npm test":
			return 0, nil
		}
		return runGateCommand(command, dir, env, stdout, stderr)
	}
	return runtime, ran
}

// A change to a file a check besides Go's reads runs the check: its install,
// then its commands in its folder, then the status of what it must leave as
// committed. The plan and the report say why it ran and what CI runs of it
// instead, and a second run in the same checkout does not install again.
func TestGateTestRunsACheckBesidesGosAndInstallsOnlyWhenItsInputsChanged(t *testing.T) {
	// Arrange
	dir := testStepModule(t, webBase, map[string]string{"web/app.ts": "export const app = 2\n"})
	t.Chdir(dir)
	runtime, ran := npmStandIn(0, "const app = 1\n")

	// Act
	var first, firstErr bytes.Buffer
	firstExit := gateTestWith(runtime, &first, &firstErr)
	firstRan := slices.Clone(*ran)
	report, _ := lastReport(t)
	*ran = nil
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	var second, secondErr bytes.Buffer
	secondExit := gateTestWith(runtime, &second, &secondErr)

	// Assert
	if firstExit != 0 || secondExit != 0 {
		t.Fatalf("exits = %d, %d; want 0 for both\nfirst: %s%s\nsecond: %s%s", firstExit, secondExit, first.String(), firstErr.String(), second.String(), secondErr.String())
	}
	root := filepath.Base(dir)
	wantFirst := []string{"npm ci in web", "npm run build in web", "npm test in web", unchangedDist + " in " + root}
	if !slices.Equal(firstRan, wantFirst) {
		t.Errorf("the first run ran %q; want %q", firstRan, wantFirst)
	}
	if want := wantFirst[1:]; !slices.Equal(*ran, want) {
		t.Errorf("the second run ran %q; want %q, with no install", *ran, want)
	}
	for _, want := range []string{
		"checks besides Go's:\n- web (web/app.ts changed)\n  left to CI: npm run e2e (CI runs the browser tests)\n",
		"cfo gate test: passed at level affected",
	} {
		if !strings.Contains(first.String(), want) {
			t.Errorf("the first run's output %q lacks %q", first.String(), want)
		}
	}
	if want := "- web (web/app.ts changed); web/node_modules holds the install made from its inputs, so npm ci does not run\n"; !strings.Contains(second.String(), want) {
		t.Errorf("the second run's output %q lacks %q", second.String(), want)
	}
	if want := []verify.Selection{{Check: "web", Why: "web/app.ts changed"}}; !slices.Equal(report.Selected, want) {
		t.Errorf("the report selected %+v; want %+v", report.Selected, want)
	}
	if want := []verify.Left{{Check: "npm run e2e", Why: "CI runs the browser tests"}}; !slices.Equal(report.Left, want) {
		t.Errorf("the report left %+v; want %+v", report.Left, want)
	}
	var checks []string
	for _, check := range report.Checks {
		checks = append(checks, strings.Join(check.Command, " ")+" in "+check.Dir+": "+check.Status)
	}
	wantChecks := []string{"npm ci in web: passed", "npm run build in web: passed", "npm test in web: passed", unchangedDist + " in .: passed"}
	if report.Status != "passed" || !slices.Equal(checks, wantChecks) {
		t.Errorf("the report says %s with checks %q; want passed with %q", report.Status, checks, wantChecks)
	}
}

// A check's commands that leave a file it must keep as committed different
// fail the run, which names the files, as CI does when the embedded build is
// not the one the lockfile builds.
func TestGateTestFailsACheckWhoseCommandsChangeWhatItMustLeaveAsCommitted(t *testing.T) {
	// Arrange
	dir := testStepModule(t, webBase, map[string]string{"web/app.ts": "export const app = 2\n"})
	t.Chdir(dir)
	runtime, _ := npmStandIn(0, "const app = 2\n")

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTestWith(runtime, &stdout, &stderr)

	// Assert
	if exit != 1 {
		t.Fatalf("exit = %d, want 1; stdout=%s stderr=%s", exit, stdout.String(), stderr.String())
	}
	for _, want := range []string{"cfo gate test: dist differ from the commit once the web check's commands ran", "dist/app.js"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr %q lacks %q", stderr.String(), want)
		}
	}
	report, _ := lastReport(t)
	if last := report.Checks[len(report.Checks)-1]; report.Status != "failed" || strings.Join(last.Command, " ") != unchangedDist || last.Status != "failed" {
		t.Errorf("the report says %s with its last check %+v; want failed, on the status of dist", report.Status, last)
	}
}

// An install that fails fails the run before the check's commands, and is
// not taken for one in place: the next run installs again.
func TestGateTestInstallsAgainAfterAnInstallThatFailed(t *testing.T) {
	// Arrange
	dir := testStepModule(t, webBase, map[string]string{"web/app.ts": "export const app = 2\n"})
	t.Chdir(dir)
	failing, failed := npmStandIn(1, "const app = 1\n")
	passing, passed := npmStandIn(0, "const app = 1\n")

	// Act
	var first, firstErr bytes.Buffer
	firstExit := gateTestWith(failing, &first, &firstErr)
	report, _ := lastReport(t)
	t.Setenv("CFO_VERIFY_DIR", t.TempDir())
	var second, secondErr bytes.Buffer
	secondExit := gateTestWith(passing, &second, &secondErr)

	// Assert
	if firstExit != 1 || !strings.Contains(firstErr.String(), "cfo gate test: npm ci: exit 1") || !slices.Equal(*failed, []string{"npm ci in web"}) {
		t.Errorf("a failed install = exit %d, stderr %q, ran %q; want exit 1, the install named, nothing run after it", firstExit, firstErr.String(), *failed)
	}
	var statuses []string
	for _, check := range report.Checks {
		statuses = append(statuses, check.Status)
	}
	if want := []string{"failed", "not_run", "not_run", "not_run"}; !slices.Equal(statuses, want) {
		t.Errorf("the failed run's checks are %q; want %q", statuses, want)
	}
	if secondExit != 0 || len(*passed) == 0 || (*passed)[0] != "npm ci in web" {
		t.Errorf("the run after it = exit %d, ran %q; want exit 0, installing first\n%s%s", secondExit, *passed, second.String(), secondErr.String())
	}
}

// The plan lists a check's install, its commands in its folder and the status
// it reads, and runs none of them.
func TestGateTestPlanListsWhatACheckBesidesGosWouldRun(t *testing.T) {
	// Arrange
	dir := testStepModule(t, webBase, map[string]string{"web/app.ts": "export const app = 2\n"})
	t.Chdir(dir)
	runtime, ran := npmStandIn(0, "const app = 1\n")

	// Act
	var stdout, stderr bytes.Buffer
	exit := gateTestWith(runtime, &stdout, &stderr, "--plan")

	// Assert
	if exit != 0 || len(*ran) != 0 {
		t.Fatalf("exit = %d, ran %q; want 0 with nothing run; stdout=%s stderr=%s", exit, *ran, stdout.String(), stderr.String())
	}
	want := "would run: npm ci (in web)\nwould run: npm run build (in web)\nwould run: npm test (in web)\nwould run: " + unchangedDist + "\n"
	if !strings.HasSuffix(stdout.String(), want) {
		t.Errorf("stdout %q does not end with %q", stdout.String(), want)
	}
	if !strings.Contains(stdout.String(), "cfo gate test: no Go package changed since") || strings.Contains(stdout.String(), "nothing to test here") {
		t.Errorf("stdout %q; want it to say no Go package changed without saying there is nothing to test", stdout.String())
	}
}
