package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/spawn"
)

// checkoutsRoot builds a temp projects root holding one git checkout per name
// and returns a runtime that reads it as the machine's projects root.
func checkoutsRoot(t *testing.T, runtime commandRuntime, names ...string) (string, commandRuntime) {
	t.Helper()
	root := t.TempDir()
	for _, name := range names {
		if err := os.MkdirAll(filepath.Join(root, name, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runtime.projectsRoot = func() (string, error) { return root, nil }
	return root, runtime
}

func TestSpawnDispatchesABareNameIntoTheSameCheckoutAsItsPath(t *testing.T) {
	root, deps := checkoutsRoot(t, testCommandRuntime(t), "PrecisionDocs-AI", "SIQshift")
	var got []string
	deps.spawn = func(_ context.Context, _ home.Home, request spawn.Request) (spawn.Result, error) {
		got = append(got, request.Project)
		return spawn.Result{Output: "spawned"}, nil
	}

	want := filepath.Join(root, "PrecisionDocs-AI")
	for _, project := range []string{"precisiondocs", want} {
		var stdout, stderr bytes.Buffer
		if exit := runWithRuntime([]string{"spawn", "g30", "--project", project, "--brief", briefFile(t), "--harness", "claude"}, &stdout, &stderr, deps); exit != 0 {
			t.Fatalf("--project %s: exit = %d, stderr=%s", project, exit, stderr.String())
		}
	}
	if len(got) != 2 || got[0] != want || got[1] != want {
		t.Errorf("dispatched into %q, want %q both times", got, want)
	}
}

func TestSpawnRefusesABareNameItCannotPlace(t *testing.T) {
	root, withRoot := checkoutsRoot(t, testCommandRuntime(t), "PrecisionDocs-AI", "SIQshift")
	for label, tc := range map[string]struct {
		runtime commandRuntime
		wants   []string
	}{
		"no projects root": {testCommandRuntime(t), []string{"clock-in", "cfo install --projects-root"}},
		"unknown name":     {withRoot, []string{"clock-in", root, "PrecisionDocs-AI", "SIQshift"}},
	} {
		tc.runtime.spawn = func(context.Context, home.Home, spawn.Request) (spawn.Result, error) {
			t.Errorf("%s: the spawn ran", label)
			return spawn.Result{}, nil
		}
		var stdout, stderr bytes.Buffer
		if exit := runWithRuntime([]string{"spawn", "g31", "--project", "clock-in", "--brief", briefFile(t), "--harness", "claude"}, &stdout, &stderr, tc.runtime); exit != 1 {
			t.Errorf("%s: exit = %d, want 1", label, exit)
		}
		for _, want := range tc.wants {
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("%s: stderr does not name %q: %s", label, want, stderr.String())
			}
		}
	}
}

func TestBriefNamesTheResolvedCheckoutAndItsScope(t *testing.T) {
	root, deps := checkoutsRoot(t, commandRuntime{}, "PrecisionDocs-AI")
	want := filepath.Join(root, "PrecisionDocs-AI")

	for id, project := range map[string]string{"t-name": "precisiondocs", "t-path": want} {
		t.Setenv("CFO_HOME", t.TempDir())
		var stdout, stderr bytes.Buffer
		if exit := runBrief([]string{id, "--project", project}, &stdout, &stderr, deps); exit != 0 {
			t.Fatalf("--project %s: exit = %d, stderr=%s", project, exit, stderr.String())
		}
		body, err := os.ReadFile(strings.TrimSpace(stdout.String()))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range []string{want, filepath.Join("data", "projects", "PrecisionDocs-AI", "auth.json")} {
			if !strings.Contains(string(body), line) {
				t.Errorf("--project %s: brief does not name %q:\n%s", project, line, body)
			}
		}
	}
}

func TestBriefRefusesABareNameWithoutAProjectsRoot(t *testing.T) {
	cfoHome := t.TempDir()
	t.Setenv("CFO_HOME", cfoHome)

	var stdout, stderr bytes.Buffer
	if exit := runBrief([]string{"t-unset", "--project", "precisiondocs"}, &stdout, &stderr, commandRuntime{}); exit != 1 {
		t.Fatalf("exit = %d, want 1", exit)
	}
	if !strings.Contains(stderr.String(), "cfo install --projects-root") {
		t.Errorf("stderr does not say how to set the projects root: %s", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(cfoHome, "data", "t-unset")); err == nil {
		t.Error("a refused brief left its directory behind")
	}
}

// The preflight names its scope when the manifest is missing, which is the
// cheapest place to read which scope a name and a path each reached.
func TestAuthPreflightReachesTheSameScopeByNameAndByPath(t *testing.T) {
	t.Setenv("CFO_HOME", t.TempDir())
	root, deps := checkoutsRoot(t, commandRuntime{}, "PrecisionDocs-AI")

	for _, project := range []string{"precisiondocs", filepath.Join(root, "PrecisionDocs-AI")} {
		code, _, stderr := runCLIWithRuntime(t, deps, project, "--check")
		if code != 1 || !strings.Contains(stderr, "no manifest for PrecisionDocs-AI") {
			t.Errorf("cfo auth %s --check = %d, want the PrecisionDocs-AI scope: %s", project, code, stderr)
		}
	}

	code, _, stderr := runCLIWithRuntime(t, deps, "clock-in", "--check")
	if code != 1 || !strings.Contains(stderr, root) || !strings.Contains(stderr, "PrecisionDocs-AI") {
		t.Errorf("cfo auth clock-in --check = %d, want a refusal naming the root and its candidates: %s", code, stderr)
	}
}

func TestAuthStoreByNameLandsInTheCheckoutsScope(t *testing.T) {
	useFileStore(t)
	_, deps := checkoutsRoot(t, commandRuntime{}, "PrecisionDocs-AI", "shop-api", "shop-web")

	code, stdout, stderr := runCLIWithRuntime(t, deps, "store", "--project", "precisiondocs", "DATABASE_URL", "postgres://precisiondocs")
	if code != 0 || !strings.Contains(stdout, "stored PrecisionDocs-AI/DATABASE_URL") {
		t.Errorf("store by name = %d, want the checkout's scope: %s%s", code, stdout, stderr)
	}

	// A scope with no checkout on this machine is still a scope.
	code, stdout, stderr = runCLIWithRuntime(t, deps, "store", "--project", "clock-in", "DATABASE_URL", "postgres://clock-in")
	if code != 0 || !strings.Contains(stdout, "stored clock-in/DATABASE_URL") {
		t.Errorf("store into a scope with no checkout = %d: %s%s", code, stdout, stderr)
	}

	code, _, stderr = runCLIWithRuntime(t, deps, "store", "--project", "shop", "DATABASE_URL", "postgres://shop")
	if code == 0 || !strings.Contains(stderr, "shop-api") || !strings.Contains(stderr, "shop-web") {
		t.Errorf("store into an ambiguous name = %d, want a refusal naming both checkouts: %s", code, stderr)
	}
}

func TestDoctorReportsTheProjectsRoot(t *testing.T) {
	root, withRoot := checkoutsRoot(t, commandRuntime{})
	gone := filepath.Join(root, "gone")

	for label, tc := range map[string]struct {
		runtime commandRuntime
		want    string
	}{
		"recorded": {withRoot, "projects root: " + root + " ("},
		"unset":    {commandRuntime{}, "projects root: not set, so --project takes a path only; run `cfo install --projects-root <dir>`"},
		"missing":  {commandRuntime{projectsRoot: func() (string, error) { return gone, nil }}, "projects root: " + gone + " is not a directory"},
	} {
		var stdout bytes.Buffer
		reportProjectsRoot(&stdout, tc.runtime)
		if !strings.Contains(stdout.String(), tc.want) {
			t.Errorf("%s: doctor printed %q, want %q", label, stdout.String(), tc.want)
		}
	}
}
