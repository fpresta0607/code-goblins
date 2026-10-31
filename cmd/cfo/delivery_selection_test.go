package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestSpawnSelectsDeliveryFromProjectPostureAndChangeSurface(t *testing.T) {
	for _, test := range []struct{ surface, risk, mode string }{
		{"internal", "low", "direct-PR"},
		{"product", "low", "no-mistakes"},
		{"mixed", "low", "no-mistakes"},
		{"unknown", "low", "no-mistakes"},
		{"internal", "credentials", "no-mistakes"},
	} {
		t.Run(test.surface+"/"+test.risk, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			runtime := testCommandRuntimeForHome(h)
			request := captureSpawn(&runtime, "spawned task")
			path := project.Path(h.Data, "demo")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(`{"project":"demo","delivery":{"posture":"no-mistakes-prod-only"},"verification":{"fast":[["git","status"]]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			brief := filepath.Join(t.TempDir(), "brief.md")
			if err := os.WriteFile(brief, []byte("## Delivery\nsurface: "+test.surface+"\nrisk: "+test.risk+"\nreason: change the approved output\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			var stdout, stderr bytes.Buffer
			exit := runSpawn([]string{"task", "--project", filepath.Join(t.TempDir(), "demo"), "--brief", brief, "--harness", "codex"}, &stdout, &stderr, runtime)

			// Assert
			if exit != 0 || request.Mode != test.mode || request.Delivery == nil || request.Delivery.Posture != "no-mistakes-prod-only" || len(request.Delivery.Verification.Fast) != 1 || !request.Delivery.RequiresCI {
				t.Fatalf("exit=%d request=%+v stderr=%s", exit, request, &stderr)
			}
		})
	}
}

func TestBriefResolvesDeliveryWithTheSameIntakePolicy(t *testing.T) {
	// Arrange
	h := testHome(t)
	t.Setenv("CFO_HOME", h.Root)
	t.Setenv("CFO_STATE_OVERRIDE", h.State)
	path := project.Path(h.Data, "demo")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"project":"demo","delivery":{"posture":"no-mistakes-prod-only"},"verification":{"fast":[["git","status"]]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer

	// Act
	exit := runBrief([]string{"task", "--project", filepath.Join(t.TempDir(), "demo"), "--surface", "internal", "--risk", "low", "--reason", "change the approved output"}, &stdout, &stderr, testCommandRuntimeForHome(h))

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, &stderr)
	}
	data, err := os.ReadFile(filepath.Join(h.Data, "task", "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := project.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := project.SelectDelivery(manifest, string(data), "", "")
	if err != nil || selection.Mode != "direct-PR" {
		t.Fatalf("selection=%+v error=%v", selection, err)
	}
}

func TestVerifyRunsTheFrozenChecksAfterProjectConfigurationChanges(t *testing.T) {
	// Arrange
	runtime, directory := measuredTask(t, `"verification":{"fast":[["git","-c","alias.check=!exit 1","check"]]}`)
	h, err := runtime.resolveHome()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := state.ReadTaskMeta(h.State, "t1")
	if err != nil {
		t.Fatal(err)
	}
	meta.Mode = "direct-PR"
	selection := project.DeliverySelection{Mode: "direct-PR", Posture: "direct-PR", Surface: "internal", Risk: "low", Reason: "approved output change", Verification: project.Verification{Fast: []project.Command{{"git", "--version"}}}, RequiresCI: true}
	data, hash, err := selection.Encode()
	if err != nil {
		t.Fatal(err)
	}
	meta.DeliveryHash = hash
	if err := os.WriteFile(filepath.Join(directory, "delivery.json"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := state.WriteTaskMeta(h.State, meta); err != nil {
		t.Fatal(err)
	}

	// Act
	var stdout, stderr bytes.Buffer
	exit := runVerify([]string{"t1"}, &stdout, &stderr, runtime)

	// Assert
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, &stderr)
	}
	results := savedResults(t, filepath.Join(directory, "verification-fast.json"))
	if len(results) != 1 || len(results[0].Command) != 2 || results[0].Command[1] != "--version" || results[0].Status != "passed" {
		t.Fatalf("verification ran another policy: %+v", results)
	}
}
