package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fpresta0607/code-goblins/internal/harness"
	"github.com/fpresta0607/code-goblins/internal/home"
	"github.com/fpresta0607/code-goblins/internal/pipeline"
	"github.com/fpresta0607/code-goblins/internal/quota"
	"github.com/fpresta0607/code-goblins/internal/spawn"
	"github.com/fpresta0607/code-goblins/internal/state"
	"github.com/fpresta0607/code-goblins/internal/supervisor"
)

// The lane table had moved the fleet to codex by the time two dependency
// waiters resumed on the claude engine they were spawned on (issue #2164).
const movedRoutingJSON = `{"rules":[],"lanes":{"build":{"harness":"codex","model":"gpt-6-astra","effort":"xhigh"}},"default_lane":"build"}`

// A table whose default lane has a fallback on another codex model, so a
// verifier of work done on the default lane's model has a lane of its own.
const verifierRoutingJSON = `{"rules":[],"lanes":{"build":{"harness":"codex","model":"gpt-6-astra","effort":"xhigh"},"check":{"harness":"codex","model":"gpt-6-terra","effort":"high"}},"default_lane":"build","fallbacks":{"build":["check"]}}`

func TestResumeChecksThatItsRecordedEngineCanStillRun(t *testing.T) {
	catalogs := map[harness.Kind][]supervisor.EngineModel{
		harness.Claude: {{ID: "claude-opus-5-5"}},
		harness.Codex:  {{ID: "gpt-6-astra"}, {ID: "gpt-6-terra"}, {ID: "gpt-6-unlisted", Hidden: true}},
		harness.Pi:     {{ID: "provider/configured-default"}},
	}
	exhausted := func(provider string) quota.Report {
		return quota.Report{Providers: map[string]quota.Provider{provider: {Known: true, Scopes: map[string]quota.Scope{
			"all_models": {Name: "all_models", Known: true, PercentRemaining: 0, Runway: "exhausted_now"},
		}}}}
	}
	for _, test := range []struct {
		name         string
		harness      string
		model        string
		parent       string
		routingJSON  string
		task         string
		switchErr    error
		routeLane    string
		report       quota.Report
		skipped      string
		catalogErr   error
		wantHarness  harness.Kind
		wantModel    string
		wantSession  string
		wantStatus   string
		wantRefusal  string
		isUnswitched bool
	}{
		{name: "a model gone from its harness's catalog resumes on the table's lane", model: "claude-fable-5-2", routeLane: "build", skipped: "no reading", wantHarness: harness.Codex, wantModel: "gpt-6-astra", wantStatus: "resume-rerouted: claude claude-fable-5-2 can no longer run (claude's model catalog does not offer claude-fable-5-2), so it resumes on codex gpt-6-astra xhigh in lane build"},
		{name: "an exhausted allowance resumes on the table's lane", model: "claude-opus-5-5", routeLane: "build", report: exhausted("claude"), wantHarness: harness.Codex, wantModel: "gpt-6-astra", wantStatus: "resume-rerouted: claude claude-opus-5-5 can no longer run (claude all_models exhausted_now, reset time unknown), so it resumes on codex gpt-6-astra xhigh in lane build"},
		{name: "no usable lane refuses with the reason", model: "claude-fable-5-2", routeLane: "build", report: exhausted("codex"), wantRefusal: "claude claude-fable-5-2 can no longer run (claude's model catalog does not offer claude-fable-5-2), and no usable lane: build: codex all_models exhausted_now, reset time unknown", isUnswitched: true},
		{name: "a harness chosen by hand is not rerouted", model: "claude-fable-5-2", routeLane: "explicit", skipped: "no reading", wantRefusal: "claude claude-fable-5-2 can no longer run (claude's model catalog does not offer claude-fable-5-2), and its harness was chosen by hand; give it another with goblins switch task", isUnswitched: true},
		{name: "a usable engine resumes on its conversation", model: "claude-opus-5-5", routeLane: "build", skipped: "no reading", wantSession: "session-1"},
		{name: "a pi model its catalog cannot judge resumes on its conversation", harness: "pi", model: "your-open-model", routeLane: "build", skipped: "no reading", wantSession: "session-1"},
		{name: "a hidden codex model still offered resumes on its conversation", harness: "codex", model: "gpt-6-unlisted", routeLane: "build", skipped: "no reading", wantSession: "session-1"},
		{name: "a relaunch refused for room leaves no choice and no rerouted line", model: "claude-fable-5-2", routeLane: "build", skipped: "no reading", switchErr: errors.New("no room to resume"), wantHarness: harness.Codex, wantModel: "gpt-6-astra", wantRefusal: "no room to resume"},
		{name: "a verifier is rerouted away from its parent's model", model: "claude-opus-5-5", routeLane: "build", parent: "parent", routingJSON: verifierRoutingJSON, task: "Verify the new field.", report: exhausted("claude"), wantHarness: harness.Codex, wantModel: "gpt-6-terra", wantStatus: "so it resumes on codex gpt-6-terra high in lane check"},
		{name: "a verifier whose parent's model cannot be read is refused", model: "claude-opus-5-5", routeLane: "build", parent: "missing", routingJSON: verifierRoutingJSON, task: "Verify the new field.", report: exhausted("claude"), wantRefusal: "claude claude-opus-5-5 can no longer run (claude all_models exhausted_now, reset time unknown), and verification requires its parent's recorded model", isUnswitched: true},
		{name: "unreadable catalog and quota never block", model: "claude-fable-5-2", routeLane: "build", skipped: "quota-axi failed", catalogErr: errors.New("catalog unreadable"), wantSession: "session-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := primaryHomeFixture(t)
			routingJSON, task, recordedHarness := movedRoutingJSON, "Add a field.", "claude"
			if test.routingJSON != "" {
				routingJSON = test.routingJSON
			}
			if test.task != "" {
				task = test.task
			}
			if test.harness != "" {
				recordedHarness = test.harness
			}
			writeTestFile(t, filepath.Join(h.Data, "routing.json"), routingJSON)
			if err := state.WriteTaskMeta(h.State, state.TaskMeta{ID: "parent", SpawnGen: "generation-0", Backend: "native", Harness: "codex", Model: "gpt-6-astra", Effort: "xhigh"}); err != nil {
				t.Fatal(err)
			}
			brief := filepath.Join(t.TempDir(), "brief.md")
			writeTestFile(t, brief, "# Brief task\n\n## Task\n\n"+task+"\n")
			meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Backend: "native", Harness: recordedHarness, Model: test.model, Effort: "xhigh", RouteLane: test.routeLane, Brief: brief, Worktree: t.TempDir(), Parent: test.parent}
			prior := state.Lifecycle{Session: "session-1", Started: time.Now().Add(-time.Hour), Handoff: "saved handoff", HandoffSaved: true}
			var request spawn.SwitchRequest
			isSwitched := false
			admittedHarness := ""
			runtime := commandRuntime{
				switchTask: func(_ context.Context, _ home.Home, selected spawn.SwitchRequest) (spawn.SwitchResult, error) {
					request, isSwitched = selected, true
					// The relaunch's admission prices the engine it resumes on.
					var err error
					admittedHarness, _, err = supervisor.ResumeEngine(h.State, meta)
					return spawn.SwitchResult{}, errors.Join(err, test.switchErr)
				},
				quota: func(context.Context) (quota.Report, string) { return test.report, test.skipped },
				harnessModels: func(kind harness.Kind) ([]supervisor.EngineModel, error) {
					return catalogs[kind], test.catalogErr
				},
			}

			// Act
			err := resumeTask(t.Context(), h, runtime, &resumeRunner{}, pipeline.Reader{}, meta, &prior, nil)

			// Assert
			if test.wantRefusal != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantRefusal) {
					t.Fatalf("resume = %v, want refusal %q", err, test.wantRefusal)
				}
			} else if err != nil {
				t.Fatalf("resume = %v", err)
			}
			if isSwitched == test.isUnswitched {
				t.Fatalf("switched = %v; a resume that cannot run must start no terminal", isSwitched)
			}
			if !test.isUnswitched && (request.Harness != test.wantHarness || request.Model != test.wantModel || request.ResumeSession != test.wantSession) {
				t.Fatalf("resume request = %+v, want harness %q model %q session %q", request, test.wantHarness, test.wantModel, test.wantSession)
			}
			if wantAdmitted := string(test.wantHarness); !test.isUnswitched && wantAdmitted != "" && admittedHarness != wantAdmitted {
				t.Fatalf("admission priced %q, want the rerouted %q", admittedHarness, wantAdmitted)
			}
			status, readErr := os.ReadFile(state.StatusPath(h.State, meta.ID))
			if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
				t.Fatal(readErr)
			}
			if test.wantStatus == "" && strings.Contains(string(status), "resume-rerouted") || test.wantStatus != "" && !strings.Contains(string(status), test.wantStatus) {
				t.Fatalf("status log = %q, want %q", status, test.wantStatus)
			}
			if _, choiceErr := state.ReadEngineChoice(h.State, meta.ID); !errors.Is(choiceErr, os.ErrNotExist) {
				t.Fatalf("engine choice after resume = %v, want none left behind", choiceErr)
			}
		})
	}
}

// The production runtime reads Codex's own model cache under the user's home,
// in the format Codex writes it, keeping a model Codex hides from its picker.
func TestDefaultRuntimeReadsTheHarnessModelCatalogUnderTheUserHome(t *testing.T) {
	// Arrange
	root := t.TempDir()
	t.Setenv("USERPROFILE", root)
	t.Setenv("HOME", root)
	writeTestFile(t, filepath.Join(root, ".codex", "models_cache.json"), `{"models":[{"slug":"gpt-6-astra","display_name":"GPT-6 Astra","visibility":"list"},{"slug":"gpt-6-unlisted","visibility":"hide"}]}`)
	runtime := defaultCommandRuntime()

	// Act
	models, err := runtime.harnessModels(harness.Codex)

	// Assert
	if err != nil || len(models) != 2 || models[0].ID != "gpt-6-astra" || models[0].Hidden || models[1].ID != "gpt-6-unlisted" || !models[1].Hidden {
		t.Fatalf("codex catalog = %+v, %v; want gpt-6-astra listed and gpt-6-unlisted hidden", models, err)
	}
}
