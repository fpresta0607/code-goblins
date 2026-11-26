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

func TestResumeChecksThatItsRecordedEngineCanStillRun(t *testing.T) {
	catalogs := map[harness.Kind][]supervisor.EngineModel{
		harness.Claude: {{ID: "claude-opus-5-5"}},
		harness.Codex:  {{ID: "gpt-6-astra"}},
	}
	exhausted := func(provider string) quota.Report {
		return quota.Report{Providers: map[string]quota.Provider{provider: {Known: true, Scopes: map[string]quota.Scope{
			"all_models": {Name: "all_models", Known: true, PercentRemaining: 0, Runway: "exhausted_now"},
		}}}}
	}
	for _, test := range []struct {
		name         string
		model        string
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
		{name: "unreadable catalog and quota never block", model: "claude-fable-5-2", routeLane: "build", skipped: "quota-axi failed", catalogErr: errors.New("catalog unreadable"), wantSession: "session-1"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Arrange
			h := primaryHomeFixture(t)
			writeTestFile(t, filepath.Join(h.Data, "routing.json"), movedRoutingJSON)
			brief := filepath.Join(t.TempDir(), "brief.md")
			writeTestFile(t, brief, "# Brief task\n\n## Task\n\nAdd a field.\n")
			meta := state.TaskMeta{ID: "task", SpawnGen: "generation-1", Backend: "native", Harness: "claude", Model: test.model, Effort: "xhigh", RouteLane: test.routeLane, Brief: brief, Worktree: t.TempDir()}
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
					return spawn.SwitchResult{}, err
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
