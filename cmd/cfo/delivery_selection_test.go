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

	"github.com/fpresta0607/code-goblins/internal/fleet"
	"github.com/fpresta0607/code-goblins/internal/project"
	"github.com/fpresta0607/code-goblins/internal/state"
)

func TestSpawnSelectsDeliveryFromProjectPostureAndChangeSurface(t *testing.T) {
	for _, test := range []struct{ posture, asked, surface, risk, mode string }{
		{"no-mistakes-prod-only", "direct-PR", "internal", "low", "direct-PR"},
		{"no-mistakes-prod-only", "direct-PR", "product", "low", "no-mistakes"},
		{"no-mistakes-prod-only", "direct-PR", "mixed", "low", "no-mistakes"},
		{"no-mistakes-prod-only", "direct-PR", "unknown", "low", "no-mistakes"},
		{"no-mistakes-prod-only", "direct-PR", "internal", "credentials", "no-mistakes"},
		{"direct-PR", "direct-PR", "product", "low", "direct-PR"},
		{"local-only", "local-only", "product", "low", "local-only"},
		{"direct-PR", "direct-PR", "product", "authorization", "no-mistakes"},
		{"local-only", "local-only", "product", "migration", "no-mistakes"},
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
			if err := os.WriteFile(brief, []byte(strings.ReplaceAll(completedCommandBrief, "mode: no-mistakes", "mode: "+test.asked+"\nsurface: "+test.surface+"\nrisk: "+test.risk+"\nreason: change the approved output")), 0o600); err != nil {
				t.Fatal(err)
			}

			// Act
			var stdout, stderr bytes.Buffer
			exit := runSpawn([]string{"task", "--project", filepath.Join(t.TempDir(), "demo"), "--brief", brief, "--harness", "codex"}, &stdout, &stderr, runtime)

			// Assert
			if exit != 0 || request.Mode != test.mode || request.Delivery == nil || request.Delivery.Posture != test.posture || len(request.Delivery.Verification.Fast) != 1 {
				t.Fatalf("exit=%d request=%+v stderr=%s", exit, request, &stderr)
			}
		})
	}
}

func TestSpawnKeepsAnUnauthorizedNarrowerModeStrictAndSaysWhy(t *testing.T) {
	for _, test := range []struct{ name, posture, brief, mode string }{
		{"unconfigured project", "", strings.ReplaceAll(completedCommandBrief, "mode: no-mistakes", "mode: local-only"), "local-only"},
		{"unconfigured project", "", strings.ReplaceAll(completedCommandBrief, "mode: no-mistakes", "mode: direct-PR"), "direct-PR"},
		{"queued row", "direct-PR", fleet.QueuedBriefText(fleet.QueuedTask{Row: fleet.BacklogRow{ID: "task", Repo: "demo", Title: "Show the demo", Mode: "direct-PR"}}, nil), "direct-PR"},
		{"pre-upgrade root brief", "local-only", "## Task\n\n> Fix the report.\n\n## Acceptance criteria\n\nThe report is fixed.\n\n## Constraints\n\nChange only what this root needs.\n\n## Authentication\n\ncredentials: none\nmcp: none\n\n## Delivery\n\nkind: ship\nmode: direct-PR\nready: true\n", "direct-PR"},
	} {
		t.Run(test.name+"/"+test.mode, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			runtime := testCommandRuntimeForHome(h)
			request := captureSpawn(&runtime, "spawned task")
			if test.posture != "" {
				path := project.Path(h.Data, "demo")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"project":"demo","delivery":{"posture":%q},"verification":{"fast":[["git","status"]]}}`, test.posture)), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			brief := filepath.Join(t.TempDir(), "brief.md")
			if err := os.WriteFile(brief, []byte(test.brief), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runSpawn([]string{"task", "--project", filepath.Join(t.TempDir(), "demo"), "--brief", brief, "--harness", "codex", "--mode", test.mode}, &stdout, &stderr, runtime)

			// Assert
			if exit != 0 || request.Mode != "no-mistakes" || request.Delivery == nil || request.Delivery.Mode != "no-mistakes" {
				t.Fatalf("exit=%d request=%+v stderr=%s; want the task started strict", exit, request, &stderr)
			}
			if notice := stderr.String(); !strings.Contains(notice, "--mode "+test.mode+" was not selected") || !strings.Contains(notice, "requested "+test.mode+" is not authorized") {
				t.Fatalf("stderr=%q; want the reason the narrower mode was not taken", notice)
			}
		})
	}
}

// Issue 1925: the brief's Delivery mode is the one readiness validated, so a
// spawn that asks for another starts nothing and names both.
func TestSpawnRefusesAModeThatDiffersFromTheBriefsDeliveryMode(t *testing.T) {
	for _, test := range []struct{ name, brief, asked, named string }{
		{"narrower than the brief", completedCommandBrief, "direct-PR", "no-mistakes"},
		{"stricter than the brief", strings.ReplaceAll(completedCommandBrief, "mode: no-mistakes", "mode: local-only"), "no-mistakes", "local-only"},
		{"the mode a line of the task text names", strings.Replace(completedCommandBrief, "do the work\n", "do the work\nmode: direct-PR\n", 1), "direct-PR", "no-mistakes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := testHome(t)
			runtime := testCommandRuntimeForHome(h)
			request := captureSpawn(&runtime, "spawned task")
			path := project.Path(h.Data, "demo")
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(`{"project":"demo","delivery":{"posture":"direct-PR"},"verification":{"fast":[["git","status"]]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if test.brief == completedCommandBrief && test.named != "no-mistakes" || !strings.Contains(test.brief, "mode: "+test.named) {
				t.Fatalf("the brief names no Delivery mode %s, so this test proves nothing: %q", test.named, test.brief)
			}
			brief := filepath.Join(t.TempDir(), "brief.md")
			if err := os.WriteFile(brief, []byte(test.brief), 0o600); err != nil {
				t.Fatal(err)
			}
			var stdout, stderr bytes.Buffer

			// Act
			exit := runSpawn([]string{"task", "--project", filepath.Join(t.TempDir(), "demo"), "--brief", brief, "--harness", "codex", "--mode", test.asked, "--delivery-exception", "the Overlord asked for it"}, &stdout, &stderr, runtime)

			// Assert
			if exit != 1 || request.ID != "" {
				t.Fatalf("exit=%d request=%+v stderr=%s; want the spawn refused before anything starts", exit, request, &stderr)
			}
			if refusal := stderr.String(); !strings.Contains(refusal, "--mode "+test.asked) || !strings.Contains(refusal, "Delivery mode "+test.named) {
				t.Fatalf("stderr=%q; want the refusal to name --mode %s and the brief's Delivery mode %s", refusal, test.asked, test.named)
			}
		})
	}
}

func TestBriefKeepsAnUnauthorizedNarrowerModeStrictAndSaysWhy(t *testing.T) {
	// Arrange
	h := testHome(t)
	t.Setenv("CFO_HOME", h.Root)
	t.Setenv("CFO_STATE_OVERRIDE", h.State)
	var stdout, stderr bytes.Buffer

	// Act
	exit := runBrief([]string{"task", "--project", filepath.Join(t.TempDir(), "demo"), "--mode", "local-only"}, &stdout, &stderr, testCommandRuntimeForHome(h))

	// Assert
	path := filepath.Join(h.Data, "task", "brief.md")
	if exit != 0 || strings.TrimSpace(stdout.String()) != path {
		t.Fatalf("exit=%d stdout=%q stderr=%s; want the brief written", exit, &stdout, &stderr)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, delivery, _ := strings.Cut(string(data), "\n## Delivery\n")
	if !slices.Contains(strings.Split(delivery, "\n"), "mode: no-mistakes") {
		t.Fatalf("delivery section = %q; want strict delivery recorded", delivery)
	}
	if notice := stderr.String(); !strings.Contains(notice, "--mode local-only was not selected") || !strings.Contains(notice, "missing or uncertain delivery configuration") {
		t.Fatalf("stderr=%q; want the reason local-only was not taken", notice)
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
			if selection := project.SelectDelivery(manifest, string(data), "", ""); selection.Mode != test.mode {
				t.Fatalf("selection=%+v; want %s", selection, test.mode)
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
				}}
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
