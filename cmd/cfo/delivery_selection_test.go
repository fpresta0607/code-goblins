package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestSpawnSelectsDeliveryFromProjectPostureAndChangeSurface(t *testing.T) {
	for _, test := range []struct{ posture, surface, risk, mode string }{
		{"no-mistakes-prod-only", "internal", "low", "direct-PR"},
		{"no-mistakes-prod-only", "product", "low", "no-mistakes"},
		{"no-mistakes-prod-only", "mixed", "low", "no-mistakes"},
		{"no-mistakes-prod-only", "unknown", "low", "no-mistakes"},
		{"no-mistakes-prod-only", "internal", "credentials", "no-mistakes"},
		{"direct-PR", "product", "low", "direct-PR"},
		{"local-only", "product", "low", "local-only"},
		{"direct-PR", "product", "authorization", "no-mistakes"},
		{"local-only", "product", "migration", "no-mistakes"},
	} {
		t.Run(test.posture+"/"+test.surface+"/"+test.risk, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			runtime := testCommandRuntimeForHome(h)
			request := captureSpawn(&runtime, "spawned task")
			path := project.Path(h.Data, "demo")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"project":"demo","delivery":{"posture":%q},"verification":{"fast":[["git","status"]]}}`, test.posture)), 0o600); err != nil {
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
			if exit != 0 || request.Mode != test.mode || request.Delivery == nil || request.Delivery.Posture != test.posture || len(request.Delivery.Verification.Fast) != 1 || request.Delivery.RequiresCI != (test.mode != "local-only") {
				t.Fatalf("exit=%d request=%+v stderr=%s", exit, request, &stderr)
			}
		})
	}
}

func TestBriefResolvesDeliveryWithTheSameIntakePolicy(t *testing.T) {
	for _, test := range []struct{ posture, surface, mode string }{
		{"no-mistakes-prod-only", "internal", "direct-PR"},
		{"no-mistakes-prod-only", "product", "no-mistakes"},
		{"direct-PR", "product", "direct-PR"},
		{"local-only", "product", "local-only"},
	} {
		t.Run(test.posture+"/"+test.surface, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			t.Setenv("CFO_HOME", h.Root)
			t.Setenv("CFO_STATE_OVERRIDE", h.State)
			path := project.Path(h.Data, "demo")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"project":"demo","delivery":{"posture":%q},"verification":{"fast":[["git","status"]]}}`, test.posture)), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runBrief([]string{"task", "--project", filepath.Join(t.TempDir(), "demo"), "--surface", test.surface, "--risk", "low", "--reason", "change the approved output"}, &stdout, &stderr, testCommandRuntimeForHome(h))

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
			if err != nil || selection.Mode != test.mode {
				t.Fatalf("selection=%+v error=%v; want %s", selection, err, test.mode)
			}
		})
	}
}

func TestVerifyRunsTheFrozenChecksAfterProjectConfigurationChanges(t *testing.T) {
	for _, tier := range []struct {
		name    string
		command []string
	}{
		{"fast", []string{"git", "--version"}},
		{"full", []string{"git", "version"}},
		{"deep", []string{"git", "rev-parse", "--is-inside-work-tree"}},
	} {
		for _, livePolicy := range []string{"changed", "missing", "invalid"} {
			t.Run(tier.name+"/"+livePolicy, func(t *testing.T) {
				// Arrange
				runtime, directory := measuredTask(t, `"verification":{"fast":[["git","-c","alias.check=!exit 1","check"]],"full":[["git","-c","alias.check=!exit 1","check"]],"deep":[["git","-c","alias.check=!exit 1","check"]]}`)
				h, err := runtime.resolveHome()
				if err != nil {
					t.Fatal(err)
				}
				meta, err := state.ReadTaskMeta(h.State, "t1")
				if err != nil {
					t.Fatal(err)
				}
				meta.Mode = "direct-PR"
				selection := project.DeliverySelection{Mode: "direct-PR", Posture: "direct-PR", Surface: "internal", Risk: "low", Reason: "approved output change", Verification: project.Verification{
					Fast: []project.Command{{"git", "--version"}},
					Full: []project.Command{{"git", "version"}},
					Deep: []project.Command{{"git", "rev-parse", "--is-inside-work-tree"}},
				}, RequiresCI: true}
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
				path := project.Path(h.Data, "demo")
				switch livePolicy {
				case "missing":
					if err := os.Remove(path); err != nil {
						t.Fatal(err)
					}
				case "invalid":
					if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
						t.Fatal(err)
					}
				}

				// Act
				var stdout, stderr bytes.Buffer
				exit := runWithRuntime([]string{"verify", "t1", "--tier", tier.name}, &stdout, &stderr, runtime)

				// Assert
				if exit != 0 {
					t.Fatalf("exit=%d stderr=%s", exit, &stderr)
				}
				results := savedResults(t, filepath.Join(directory, "verification-"+tier.name+".json"))
				if len(results) != 1 || !slices.Equal(results[0].Command, tier.command) || results[0].Status != "passed" || results[0].Output == "" || results[0].Execution == "" {
					t.Fatalf("verification ran another policy: %+v", results)
				}
			})
		}
	}
}

func TestVerifyRejectsAnInvalidFrozenSelection(t *testing.T) {
	for _, tier := range []string{"fast", "full", "deep"} {
		for _, problem := range []struct{ name, message string }{
			{"changed receipt", "frozen selection changed"},
			{"different mode", "task mode differs from its frozen selection"},
			{"invalid JSON", "unexpected end of JSON input"},
			{"missing receipt", "delivery.json"},
		} {
			t.Run(tier+"/"+problem.name, func(t *testing.T) {
				// Arrange
				runtime, directory := measuredTask(t, `"verification":{"fast":[["git","--version"]],"full":[["git","--version"]],"deep":[["git","--version"]]}`)
				h, err := runtime.resolveHome()
				if err != nil {
					t.Fatal(err)
				}
				meta, err := state.ReadTaskMeta(h.State, "t1")
				if err != nil {
					t.Fatal(err)
				}
				meta.Mode = "direct-PR"
				selection := project.DeliverySelection{Mode: meta.Mode, Verification: project.Verification{Fast: []project.Command{{"git", "--version"}}}}
				data, hash, err := selection.Encode()
				if err != nil {
					t.Fatal(err)
				}
				meta.DeliveryHash = hash
				switch problem.name {
				case "changed receipt":
					data = "{}"
				case "different mode":
					meta.Mode = "local-only"
				case "invalid JSON":
					data = "{"
					meta.DeliveryHash = fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
				}
				if problem.name != "missing receipt" {
					if err := os.WriteFile(filepath.Join(directory, "delivery.json"), []byte(data), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := state.WriteTaskMeta(h.State, meta); err != nil {
					t.Fatal(err)
				}

				// Act
				var stdout, stderr bytes.Buffer
				exit := runWithRuntime([]string{"verify", "t1", "--tier", tier}, &stdout, &stderr, runtime)

				// Assert
				if exit != 1 || !strings.Contains(stderr.String(), problem.message) {
					t.Fatalf("exit=%d stderr=%s; invalid receipt must refuse verification", exit, &stderr)
				}
				if _, err := os.Stat(filepath.Join(directory, "verification-"+tier+".json")); !os.IsNotExist(err) {
					t.Fatalf("invalid receipt reached execution: %v", err)
				}
			})
		}
	}
}
