package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/spawn"
)

const approvedAuthority = "the Overlord approved direct-PR for this fix on 2026-11-26"

// exceptionHome is a home whose project demo runs no-mistakes by posture and
// defines fast checks, so only an exception narrows a task's delivery, with
// goblins brief and spawn resolving it.
func exceptionHome(t *testing.T) (home.Home, string) {
	t.Helper()
	h := testHome(t)
	t.Setenv("GOBLINS_HOME", h.Root)
	t.Setenv("GOBLINS_STATE_OVERRIDE", h.State)
	if err := os.MkdirAll(h.State, 0o700); err != nil {
		t.Fatal(err)
	}
	writeDemoChecks(t, h, `[["git","status"]]`)
	return h, filepath.Join(t.TempDir(), "demo")
}

func writeDemoChecks(t *testing.T, h home.Home, fast string) {
	t.Helper()
	path := project.Path(h.Data, "demo")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"project":"demo","delivery":{"posture":"no-mistakes"},"verification":{"fast":`+fast+`}}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readyBrief writes task's brief with goblins brief and the given flags,
// fills its placeholders and finalizes it, and returns its path and what
// readiness said.
func readyBrief(t *testing.T, h home.Home, checkout string, flags ...string) (string, string) {
	t.Helper()
	runtime := testCommandRuntimeForHome(h)
	var scaffold, failure bytes.Buffer
	if exit := runBrief(append([]string{"task", "--project", checkout}, flags...), &scaffold, &failure, runtime); exit != 0 {
		t.Fatalf("brief exit=%d stderr=%s", exit, &failure)
	}
	path := strings.TrimSpace(scaffold.String())
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	brief := string(data)
	for placeholder, authored := range map[string]string{
		"{TASK - what to build or learn, in one clear paragraph}":    "Fix the report total.",
		"{ACCEPTANCE - concrete, verifiable outcomes}":               "The total is right.",
		"{CONSTRAINTS - things not to touch, boundaries, non-goals}": "Change only the report.",
	} {
		brief = strings.ReplaceAll(brief, placeholder, authored)
	}
	if err := os.WriteFile(path, []byte(brief), 0o600); err != nil {
		t.Fatal(err)
	}
	var ready bytes.Buffer
	if exit := runWithRuntime([]string{"brief", "ready", "task"}, &bytes.Buffer{}, &ready, runtime); exit != 0 {
		t.Fatalf("brief ready exit=%d stderr=%s", exit, &ready)
	}
	return path, ready.String()
}

// spawnTask runs goblins spawn for task in a fresh runtime, as a new process
// does, and returns the start it asked for and what it said.
func spawnTask(t *testing.T, h home.Home, checkout, brief string, flags ...string) (int, string, *spawn.Request) {
	t.Helper()
	runtime := testCommandRuntimeForHome(h)
	request := captureSpawn(&runtime, "spawned task")
	var stdout, stderr bytes.Buffer
	exit := runSpawn(append([]string{"task", "--project", checkout, "--brief", brief, "--harness", "codex"}, flags...), &stdout, &stderr, runtime)
	return exit, stderr.String(), request
}

// Issue 2146: the exception the registered primary CEO gave goblins brief is
// kept beside the brief, so the supervisor's automatic start, which brings no
// exception and does not run under the CEO, selects the approved mode after a
// restart, and an explicit spawn racing it selects the very same delivery.
func TestTheDeliveryExceptionApprovedAtBriefReachesEveryLaterStart(t *testing.T) {
	// Arrange
	h, checkout := exceptionHome(t)
	registeredPrimary(t, h, "ceo")
	brief, readiness := readyBrief(t, h, checkout, "--mode", "direct-PR", "--delivery-exception", approvedAuthority)
	if readiness != "" {
		t.Fatalf("brief ready said %q; want no notice for the approved mode", readiness)
	}
	registration := filepath.Join(h.State, "primary.json")
	primary, err := os.ReadFile(registration)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(registration); err != nil {
		t.Fatal(err)
	}

	// Act
	automaticExit, automaticNotice, automatic := spawnTask(t, h, checkout, brief, "--mode", "direct-PR")
	if err := os.WriteFile(registration, primary, 0o600); err != nil {
		t.Fatal(err)
	}
	explicitExit, explicitNotice, explicit := spawnTask(t, h, checkout, brief, "--delivery-exception", approvedAuthority)

	// Assert
	if automaticExit != 0 || automatic.Mode != "direct-PR" || automatic.Delivery == nil || automatic.Delivery.Reason != "explicit CEO-authorized exception: "+approvedAuthority {
		t.Fatalf("automatic start exit=%d selection=%+v stderr=%s; want the approved direct-PR", automaticExit, automatic.Delivery, automaticNotice)
	}
	if explicitExit != 0 || explicit.Mode != automatic.Mode || !reflect.DeepEqual(explicit.Delivery, automatic.Delivery) {
		t.Fatalf("explicit start exit=%d selection=%+v stderr=%s; want the automatic start's %+v", explicitExit, explicit.Delivery, explicitNotice, automatic.Delivery)
	}
}

// An exception approves nothing unless the registered primary CEO gives it:
// goblins brief and goblins spawn refuse one from any other caller and keep
// nothing, so the task's start still selects no-mistakes.
func TestADeliveryExceptionFromAnyoneButThePrimaryCEOIsRefusedAndKeptNowhere(t *testing.T) {
	for _, caller := range []struct{ name, variable, value string }{
		{"an unregistered caller", "", ""},
		{"a goblin", harness.RoleVariable, harness.RoleGoblin},
		{"a gate agent", gateAgentVariable, "1"},
	} {
		t.Run(caller.name, func(t *testing.T) {
			// Arrange
			h, checkout := exceptionHome(t)
			if caller.variable != "" {
				registeredPrimary(t, h, "ceo")
				t.Setenv(caller.variable, caller.value)
			}
			brief := filepath.Join(t.TempDir(), "brief.md")
			if err := os.WriteFile(brief, []byte(strings.ReplaceAll(completedCommandBrief, "mode: no-mistakes", "mode: direct-PR")), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer

			// Act
			briefExit := runBrief([]string{"task", "--project", checkout, "--mode", "direct-PR", "--delivery-exception", approvedAuthority}, &stdout, &stderr, testCommandRuntimeForHome(h))
			spawnExit, spawnRefusal, spawned := spawnTask(t, h, checkout, brief, "--delivery-exception", approvedAuthority)
			_, strictNotice, strict := spawnTask(t, h, checkout, brief, "--mode", "direct-PR")

			// Assert
			if briefExit != 1 || !strings.Contains(stderr.String(), "honoured only from the registered primary CEO") {
				t.Fatalf("brief exit=%d stderr=%s; want the exception refused", briefExit, &stderr)
			}
			if _, err := os.Stat(filepath.Join(h.Data, "task", "brief.md")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("brief written after its exception was refused: %v", err)
			}
			if spawnExit != 1 || spawned.ID != "" || !strings.Contains(spawnRefusal, "honoured only from the registered primary CEO") {
				t.Fatalf("spawn exit=%d mode=%q stderr=%s; want the exception refused before anything starts", spawnExit, spawned.ID, spawnRefusal)
			}
			if _, err := os.Stat(project.DeliveryExceptionPath(h.Data, "task")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a refused exception was kept: %v", err)
			}
			if strict.Mode != "no-mistakes" || !strings.Contains(strictNotice, "not authorized") {
				t.Fatalf("start without the exception mode=%q stderr=%s; want no-mistakes", strict.Mode, strictNotice)
			}
		})
	}
}

// An exception is approved under the project's checks of that moment: once
// they change, the one kept approves nothing, spawn says why, and
// the task starts no-mistakes until the primary CEO approves it again.
func TestAKeptDeliveryExceptionApprovesNothingOnceTheProjectsChecksChange(t *testing.T) {
	// Arrange
	h, checkout := exceptionHome(t)
	registeredPrimary(t, h, "ceo")
	runtime := testCommandRuntimeForHome(h)
	var scaffold, failure bytes.Buffer
	if exit := runBrief([]string{"task", "--project", checkout, "--mode", "direct-PR", "--delivery-exception", approvedAuthority}, &scaffold, &failure, runtime); exit != 0 {
		t.Fatalf("brief exit=%d stderr=%s", exit, &failure)
	}
	brief := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(brief, []byte(strings.ReplaceAll(completedCommandBrief, "mode: no-mistakes", "mode: direct-PR")), 0o600); err != nil {
		t.Fatal(err)
	}
	if exit, notice, before := spawnTask(t, h, checkout, brief); exit != 0 || before.Mode != "direct-PR" {
		t.Fatalf("premise: exit=%d mode=%q stderr=%s; want the kept exception to approve direct-PR under its checks", exit, before.Mode, notice)
	}
	writeDemoChecks(t, h, `[["git","status","--short"]]`)

	// Act
	exit, notice, after := spawnTask(t, h, checkout, brief)

	// Assert
	if exit != 0 || after.Mode != "no-mistakes" || !strings.Contains(notice, "checks changed since the delivery exception kept for task was approved") {
		t.Fatalf("exit=%d mode=%q stderr=%s; want no-mistakes and why", exit, after.Mode, notice)
	}
}
