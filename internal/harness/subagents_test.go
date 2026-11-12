package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fpresta0607/code-goblins/internal/routing"
)

func TestBuildGivesNativeSubagentsTheirRoutedModels(t *testing.T) {
	for _, kind := range []Kind{Claude, Codex} {
		t.Run(string(kind), func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			adapter, err := DefaultRegistry().Get(kind)
			if err != nil {
				t.Fatal(err)
			}
			spec := LaunchSpec{BriefPath: filepath.Join(root, "brief.md"), TaskTmp: root, Scratch: root, Model: "parent-model", Subagents: map[string]routing.ExecutionLane{
				"search": {Model: "small-model", Effort: "medium"},
				"review": {Model: "strong-model", Effort: "xhigh"},
				"verify": {Model: "different-model", Effort: "high"},
			}}
			// Act
			launch, err := adapter.Build(spec)
			// Assert
			if err != nil {
				t.Fatal(err)
			}
			args := strings.Join(launch.Args, " ")
			if kind == Claude && !strings.Contains(args, "--add-dir") || kind == Codex && !strings.Contains(args, "agents.default_subagent_model=small-model") {
				t.Fatalf("native role configuration missing: %v", launch.Args)
			}
			for role, lane := range spec.Subagents {
				if kind == Codex {
					if !strings.Contains(launch.SubagentInstruction, lane.Model) || !strings.Contains(launch.SubagentInstruction, lane.Effort) || !strings.Contains(launch.SubagentInstruction, "fork_turns none") {
						t.Fatalf("role %s lacks explicit model and fresh context: %s", role, launch.SubagentInstruction)
					}
					continue
				}
				pattern := filepath.Join(root, "subagents", "*", ".claude", "agents", "ceo-"+role+".md")
				files, err := filepath.Glob(pattern)
				if err != nil || len(files) != 1 {
					t.Fatalf("%s: files=%v err=%v", role, files, err)
				}
				data, err := os.ReadFile(files[0])
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(data), lane.Model) || !strings.Contains(string(data), lane.Effort) || strings.Contains(string(data), "parent-model") {
					t.Fatalf("%s role = %s", role, data)
				}
			}
			if strings.Contains(launch.SubagentInstruction, "implementation slice") {
				t.Fatalf("a goblin with no implement lane was told to hand off slices: %s", launch.SubagentInstruction)
			}
		})
	}
}

func TestBuildHandsImplementationSlicesToTheImplementRolesModelInAFreshContext(t *testing.T) {
	trio := map[string]routing.ExecutionLane{
		"search": {Model: "small-model", Effort: "medium"},
		"review": {Model: "strong-model", Effort: "xhigh"},
		"verify": {Model: "different-model", Effort: "high"},
	}
	for _, kind := range []Kind{Claude, Codex} {
		for _, test := range []struct {
			name      string
			subagents map[string]routing.ExecutionLane
		}{
			{"beside the native helpers", trio},
			{"with no native helpers", nil},
		} {
			t.Run(string(kind)+" "+test.name, func(t *testing.T) {
				// Arrange
				root := t.TempDir()
				adapter, err := DefaultRegistry().Get(kind)
				if err != nil {
					t.Fatal(err)
				}
				spec := LaunchSpec{BriefPath: filepath.Join(root, "brief.md"), TaskTmp: root, Scratch: root, Model: "lead-model", Subagents: test.subagents, Implement: routing.ExecutionLane{Name: "light", Model: "light-model", Effort: "high"}}

				// Act
				launch, err := adapter.Build(spec)

				// Assert
				if err != nil {
					t.Fatal(err)
				}
				instruction := launch.SubagentInstruction
				if !strings.Contains(instruction, "never stages, commits or pushes: read its git diff and run those tests yourself before you commit") {
					t.Errorf("the lead is not told to read the slice's diff before any commit: %s", instruction)
				}
				if isHelped := strings.Contains(instruction, "Verification must be independent"); isHelped != (test.subagents != nil) {
					t.Errorf("native helper instruction present = %v, want %v: %s", isHelped, test.subagents != nil, instruction)
				}
				if kind == Codex {
					if !strings.Contains(instruction, "spawn_agent with model light-model, reasoning_effort high and fork_turns none") {
						t.Errorf("codex worker lacks the implement model and a fresh context: %s", instruction)
					}
					return
				}
				if !strings.Contains(instruction, "Hand each bounded implementation slice to ceo-implement") || !strings.Contains(strings.Join(launch.Args, " "), "--add-dir") {
					t.Errorf("claude lead is not given ceo-implement: args=%v instruction=%s", launch.Args, instruction)
				}
				files, err := filepath.Glob(filepath.Join(root, "subagents", "*", ".claude", "agents", "ceo-implement.md"))
				if err != nil || len(files) != 1 {
					t.Fatalf("ceo-implement files=%v err=%v", files, err)
				}
				data, err := os.ReadFile(files[0])
				if err != nil {
					t.Fatal(err)
				}
				if agent := string(data); !strings.Contains(agent, `model: "light-model"`) || !strings.Contains(agent, `effort: "high"`) || !strings.Contains(agent, "never stage, commit or push") || strings.Contains(agent, "lead-model") {
					t.Errorf("ceo-implement = %s, want the implement model and no commit", agent)
				}
			})
		}
	}
}

func TestBuildRefusesAnImplementLaneWithoutASupportedEffort(t *testing.T) {
	for _, kind := range []Kind{Claude, Codex} {
		t.Run(string(kind), func(t *testing.T) {
			// Arrange
			root := t.TempDir()
			adapter, err := DefaultRegistry().Get(kind)
			if err != nil {
				t.Fatal(err)
			}
			spec := LaunchSpec{BriefPath: filepath.Join(root, "brief.md"), TaskTmp: root, Scratch: root, Model: "lead-model", Implement: routing.ExecutionLane{Name: "light", Model: "light-model", Effort: "ludicrous"}}

			// Act
			_, err = adapter.Build(spec)

			// Assert
			if err == nil || !strings.Contains(err.Error(), "subagent implement requires a model and supported effort") {
				t.Errorf("err = %v, want the implement lane refused", err)
			}
		})
	}
}
