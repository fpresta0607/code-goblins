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
				pattern := filepath.Join(root, "subagents", "*", ".claude", "agents", "cfo-"+role+".md")
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
		})
	}
}
